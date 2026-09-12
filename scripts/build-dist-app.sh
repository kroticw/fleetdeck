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
# - An ad-hoc seal over the whole bundle. The Go linker signs each arm64 binary on
#   its own, which is enough to run it, but it seals nothing else: the bundle's
#   signature then claims resources it does not cover, and codesign reports "code
#   has no resources but signature indicates they must be present". That is a
#   broken signature, and a downloaded copy fails Gatekeeper's signature check
#   before it ever gets to the question a person can answer. The seal is not a
#   Developer ID signature and does not make Gatekeeper trust the app -- see
#   docs/engineering/release-app.md for what a person sees with it and without it.
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
# Usage: build-dist-app.sh <dist-dir> <version> <arches> <binaries> <ldflags>
set -eu

if [ "$#" -ne 5 ]; then
	echo "usage: $0 <dist-dir> <version> <arches> <binaries> <ldflags>" >&2
	exit 2
fi

dist_dir=$1
version=$2
arches=$3
binaries=$4
ldflags=$5

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

# The helpers are sealed first, each under an identifier of its own; the bundle
# seal then records them as nested code, and its identifier is Info.plist's.
executable=$(plutil -extract CFBundleExecutable raw "$app/Contents/Info.plist")
for b in $binaries; do
	[ "$b" = "$executable" ] && continue
	codesign --force --sign - --identifier "dev.fleetdeck.$b" "$app/Contents/MacOS/$b"
done
codesign --force --sign - "$app"

# Only zips this target writes are removed: the tarballs `make dist` left in the
# same directory are the release's too.
mkdir -p "$dist_dir"
rm -f "$dist_dir"/fleetdeck-*-macos.zip
ditto -c -k --norsrc --noextattr --noacl --keepParent "$app" "$dist_dir/fleetdeck-$version-macos.zip"
rm -rf "$work"
echo "dist-app: $dist_dir/fleetdeck-$version-macos.zip"
