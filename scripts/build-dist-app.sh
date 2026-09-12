#!/bin/sh
#
# build-dist-app.sh builds the release app: fleetdeck.app, the bundle `make window-app`
# builds, made into something a person downloads, drags to Applications and opens.
# `make dist-app` runs it and then verifies what it wrote.
#
# What it adds to window-app's bundle, and why each is needed on a machine that did
# not build it:
#
# - Every command, not only the window and the panel. First-run setup wires Claude
#   Code's statusline to the fleetdeck-status beside the running panel and writes
#   nothing when it is absent, so a bundle without it installs a fleet with no
#   statusline.
# - Both architectures in every binary (lipo). One download that opens on any Mac,
#   instead of asking a person which processor they have.
# - The tag as the version, in the binaries and in Info.plist, where Finder's Get
#   Info reads it.
# - A seal over the whole bundle. The Go linker signs each arm64 binary on its
#   own, which is enough to run it, but it seals nothing else: the bundle's
#   signature then claims resources it does not cover, and codesign reports "code
#   has no resources but signature indicates they must be present". That is a
#   broken signature, and a downloaded copy fails Gatekeeper's signature check
#   before it ever gets to the question a person can answer.
#
#   The seal comes in two kinds, and which one this run makes is decided by the
#   sign-identity argument alone:
#
#     Developer ID, when an identity is given. The release kind: hardened
#     runtime, a secure timestamp, the measured entitlements, and an identity
#     that says who built this. It is what notarization needs, and notarization
#     is what lets a downloaded app open with a double click.
#
#     Ad hoc, when no identity is given. What a machine without a certificate
#     gets: a developer's, and CI's check job, which runs this very script
#     through `make test` on a runner that has no certificate and never will. It
#     seals the bundle and identifies nobody; Gatekeeper still refuses the app
#     until a person makes an exception. See docs/engineering/release-app.md for
#     what that looks like on the screen.
#
#   Nothing here notarizes. That is scripts/notarize-dist-app.sh, a step of its
#   own because it goes to Apple over the network and takes minutes, while this
#   script has to stay something a test can run offline.
#
# What it leaves out on purpose: the source tree, git, go and make that
# window-app writes into the window for its Update button. A release is built on
# a CI runner; a window that knew the runner's checkout would offer to update
# from a tree that exists on no machine the app is installed on. Without them the
# window shows no Update button, and a new version is a new download.
#
# The zip is written with ditto, which is what Finder's Archive Utility is built
# on, and without resource forks, extended attributes or ACLs, so it holds the
# bundle's files and nothing else.
#
# Usage: build-dist-app.sh <dist-dir> <version> <arches> <binaries> <ldflags> <sign-identity>
#
# <sign-identity> is a codesign identity -- "Developer ID Application: ..." -- or
# empty for an ad-hoc seal. It is an argument rather than something this script
# looks up, so that what a build signs with is decided by the caller and visible
# in the workflow, and so that a machine that happens to have a certificate in
# its keychain does not quietly start signing every test build with it.
set -eu

if [ "$#" -ne 6 ]; then
	echo "usage: $0 <dist-dir> <version> <arches> <binaries> <ldflags> <sign-identity>" >&2
	exit 2
fi

dist_dir=$1
version=$2
arches=$3
binaries=$4
ldflags=$5
sign_identity=$6

work=

fail() {
	echo "dist-app: $*" >&2
	[ -z "$work" ] || rm -rf "$work"
	exit 1
}

[ "$(go env GOOS)" = darwin ] ||
	fail "builds a macOS app bundle, which needs a macOS host: cgo against WebKit, lipo, codesign, ditto"
[ -n "$version" ] || fail "version is empty"
[ -n "$arches" ] || fail "no architectures to build"
# Asked before the four cross builds below rather than at the signing step after
# them: a release that cannot be signed should fail in a second, not in a minute.
if [ -n "$sign_identity" ]; then
	security find-identity -v -p codesigning 2>/dev/null | grep -qF "$sign_identity" ||
		fail "the keychain holds no codesigning identity matching the one this build was told to sign with"
fi

src=cmd/fleetdeck-window
work=$(mktemp -d)
# No EXIT trap: under the bash 3.2 that is /bin/sh on macOS it turns a syntax error
# into status 0 (see verify-dist-app.sh). Removed by fail and at the end instead; a
# command that fails under set -e leaves it behind in $TMPDIR, which is the cheaper
# failure of the two.
trap 'rm -rf "$work"; exit 1' INT TERM HUP

app="$work/fleetdeck.app"
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"
cp "$src/Info.plist" "$app/Contents/Info.plist"
cp "$src/icon.icns" "$app/Contents/Resources/icon.icns"

# Info.plist versions are numbers; the tag is v-prefixed.
plist_version=${version#v}
plutil -replace CFBundleShortVersionString -string "$plist_version" "$app/Contents/Info.plist"
plutil -replace CFBundleVersion -string "$plist_version" "$app/Contents/Info.plist"

# Every slice is built with cgo, the way `make window-app` builds on its host: the
# window cannot be built without it, and the other slices then differ from it in
# nothing but the architecture. Go names the architectures one way and clang
# another; clang is told the target through CC, since a plain GOARCH switch turns
# cgo off for any architecture but the host's.
for b in $binaries; do
	slices=
	for arch in $arches; do
		case $arch in
			arm64) clang_arch=arm64 ;;
			amd64) clang_arch=x86_64 ;;
			*) fail "no clang architecture known for GOARCH=$arch" ;;
		esac
		out="$work/slices/$arch/$b"
		CGO_ENABLED=1 GOOS=darwin GOARCH=$arch CC="clang -arch $clang_arch" \
			go build -ldflags "$ldflags" -o "$out" "./cmd/$b"
		slices="$slices $out"
	done
	# shellcheck disable=SC2086 # $slices is a list of paths without spaces, from $work.
	lipo -create -output "$app/Contents/MacOS/$b" $slices
done

# seal signs one piece of the bundle, in whichever of the two kinds this run
# makes (see the header). The flags only the Developer ID kind gets:
#
#   --options runtime turns on the hardened runtime. Notarization requires it,
#   and it is the thing the entitlements are exceptions to.
#   --timestamp asks Apple's timestamp server for a secure timestamp. Without one
#   notarization rejects the submission; it is also why signing needs a network.
#   codesign does this by default when it signs with a real identity -- measured:
#   only --timestamp=none turns it off, and a signature without it reports "Signed
#   Time=" where a timestamped one reports "Timestamp=". It is written out anyway,
#   because a release must not depend on a default staying what it is.
#   --entitlements is the measured list, and it is deliberately short: every
#   entry in it is a hole in the hardened runtime. See entitlements.plist.
seal() {
	_path=$1
	_identifier=$2
	set -- --force
	[ -z "$_identifier" ] || set -- "$@" --identifier "$_identifier"
	if [ -n "$sign_identity" ]; then
		set -- "$@" --timestamp --options runtime --entitlements "$src/entitlements.plist"
		set -- "$@" --sign "$sign_identity"
	else
		set -- "$@" --sign -
	fi
	codesign "$@" "$_path"
}

# The helpers are sealed first, each under an identifier of its own; the bundle
# seal then records them as nested code, and its identifier is Info.plist's.
executable=$(plutil -extract CFBundleExecutable raw "$app/Contents/Info.plist")
for b in $binaries; do
	[ "$b" = "$executable" ] && continue
	seal "$app/Contents/MacOS/$b" "dev.fleetdeck.$b"
done
seal "$app" ""

# Only zips this target writes are removed: the tarballs `make dist` left in the
# same directory are the release's too.
mkdir -p "$dist_dir"
rm -f "$dist_dir"/fleetdeck-*-macos.zip
ditto -c -k --norsrc --noextattr --noacl --keepParent "$app" "$dist_dir/fleetdeck-$version-macos.zip"
rm -rf "$work"
echo "dist-app: $dist_dir/fleetdeck-$version-macos.zip"
