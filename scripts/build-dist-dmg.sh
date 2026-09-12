#!/bin/sh
#
# build-dist-dmg.sh writes fleetdeck-<version>-macos.dmg: the disk image a person
# downloads, opens, and drags the app out of into Applications.
#
# Why a disk image at all, when the release already carries a zip. A zip hands a
# person fleetdeck.app in their Downloads folder and then says nothing. Opened
# from there the app runs from a randomised read-only copy macOS makes of it
# (App Translocation), and the path it writes into Claude Code's settings is that
# copy's -- a path that stops existing the moment the app quits. The zip's answer
# to this is a sentence in the documentation asking a person to drag the app to
# Applications first, and a sentence in the documentation is a thing people skip.
# A disk image makes the same step the obvious one: the window that opens holds
# the app and a shortcut to Applications side by side, with an arrow between.
#
# The zip stays. It is what the installed app downloads to update itself
# (internal/supervisor/releasesource.go), by a URL it builds from a name rather
# than by looking at what a release happens to carry -- so an app already on
# somebody's Mac would not find a release that had dropped the zip, and would
# never update again. The two artefacts have two jobs: the image installs, the
# zip updates.
#
# The image is built from the zip rather than from a fresh build of the app, and
# that is the point: the app a person drags out of the image is byte for byte the
# app scripts/verify-dist-app.sh has already taken apart, and there is no second
# build to differ from the first.
#
# What goes in the image besides the app:
#
#   Applications          a symlink to /Applications, so the drag has somewhere
#                         to land without opening a second window.
#   .background/          the window's background picture, a HiDPI TIFF.
#   .DS_Store             the window itself: its size, icon view, icon size, and
#                         where the two icons sit. Finder writes this file; it
#                         cannot be computed, so it is generated once by hand
#                         (scripts/build-dmg-layout.sh, `make dmg-layout`) and
#                         committed under packaging/dmg/. Nothing here needs
#                         Finder, AppleScript or a window server, which is what
#                         lets this run on a CI runner.
#
# The volume name is fixed at "fleetdeck" and is not decoration: the background
# picture is recorded in that .DS_Store as an alias carrying the volume's name
# alongside the relative path, and an image built under another name is an image
# whose window may come up blank.
#
# Nothing here notarizes. That is scripts/notarize-dist-dmg.sh, a step of its own
# because it goes to Apple over the network and takes minutes, while this script
# has to stay something a test can run offline.
#
# Usage: build-dist-dmg.sh <dist-dir> <version> <sign-identity>
#
# <sign-identity> is a codesign identity -- "Developer ID Application: ..." -- or
# empty for an ad-hoc seal, exactly as in build-dist-app.sh and for the same
# reasons. The image carries a signature of its own, and it has to: Gatekeeper
# asks about the image when a person opens it, before it has ever seen the app
# inside. An unsigned image would put the dialog that the app's own signature
# exists to prevent one step earlier in the journey.
#
# The image is signed without --options runtime and without entitlements, unlike
# the app. Those describe restrictions a *process* runs under; a disk image is a
# container and never becomes a process.
set -eu

if [ "$#" -ne 3 ]; then
	echo "usage: $0 <dist-dir> <version> <sign-identity>" >&2
	exit 2
fi

dist_dir=$1
version=$2
sign_identity=$3

work=

fail() {
	echo "dist-dmg: $*" >&2
	[ -z "$work" ] || rm -rf "$work"
	exit 1
}

# No EXIT trap anywhere in this repository's release scripts: under the bash 3.2
# that is /bin/sh on macOS it turns a syntax error into status 0. See
# scripts/dist-app-checks.sh.
[ "$(uname -s)" = Darwin ] || fail "builds a macOS disk image, which needs hdiutil and codesign"

zip="$dist_dir/fleetdeck-$version-macos.zip"
[ -f "$zip" ] || fail "$zip is not there: build it with make dist-app first"

layout=packaging/dmg
for f in "$layout/DS_Store" "$layout/background.tiff"; do
	[ -f "$f" ] || fail "$f is missing: the window layout is committed, not computed (see make dmg-layout)"
done

# Asked before anything is built rather than at the signing step after, so a
# release that cannot be signed fails in a second.
if [ -n "$sign_identity" ]; then
	security find-identity -v -p codesigning 2>/dev/null | grep -qF "$sign_identity" ||
		fail "the keychain holds no codesigning identity matching the one this build was told to sign with"
fi

work=$(mktemp -d)
trap 'rm -rf "$work"; exit 1' INT TERM HUP

stage="$work/volume"
mkdir -p "$stage/.background"
ditto -x -k "$zip" "$work/unpacked" || fail "could not unpack $zip"
[ -d "$work/unpacked/fleetdeck.app" ] || fail "$zip holds no fleetdeck.app"
mv "$work/unpacked/fleetdeck.app" "$stage/fleetdeck.app"

cp "$layout/background.tiff" "$stage/.background/background.tiff"
cp "$layout/DS_Store" "$stage/.DS_Store"
ln -s /Applications "$stage/Applications"

# Both window files also carry Finder's invisible flag, not just a leading dot.
# The dot is what hides them from a person with default settings; the flag is
# what a file manager reading the flag rather than the name goes by. Neither
# hides them from somebody who has turned on "show all files", and nothing can --
# that setting means what it says.
chflags hidden "$stage/.background" "$stage/.DS_Store"

# Only images this target writes are removed, the way build-dist-app.sh removes
# only its own zips: the tarballs and the zip in the same directory are the
# release's too.
mkdir -p "$dist_dir"
rm -f "$dist_dir"/fleetdeck-*-macos.dmg
dmg="$dist_dir/fleetdeck-$version-macos.dmg"

# UDZO is the compressed read-only format; HFS+ because the committed .DS_Store
# and the Applications symlink are what the window is made of, and an image a
# person opens should not be writable anyway.
hdiutil create -format UDZO -fs HFS+ -volname fleetdeck -srcfolder "$stage" -ov "$dmg" >"$work/create.log" 2>&1 ||
	fail "hdiutil create failed:
$(cat "$work/create.log")"

if [ -n "$sign_identity" ]; then
	codesign --force --timestamp --sign "$sign_identity" "$dmg"
else
	codesign --force --sign - "$dmg"
fi

rm -rf "$work"
echo "dist-dmg: $dmg"
