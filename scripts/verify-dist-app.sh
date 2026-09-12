#!/bin/sh
#
# verify-dist-app.sh interrogates the zip `make dist-app` has just written, and fails if
# it is not an app a release may publish. It looks at the app the way a person gets it:
# unpacked from the zip, from the outside, and trusting nothing build-dist-app.sh says
# about what it did.
#
# Everything it asks about the app itself lives in scripts/dist-app-checks.sh, which
# scripts/verify-dist-dmg.sh sources too: the app inside the disk image and the app
# inside the zip are checked by one piece of code, not by two that resemble each other.
# What stays here is everything about the zip -- that there is exactly one of it, and
# that it holds exactly the app and nothing else.
#
# Usage: verify-dist-app.sh <dist-dir> <version> <arches> <binaries> <ldflags> <expect-seal>
#
# <expect-seal> is the seal this zip is required to carry, and the gate demands
# exactly it:
#
#   adhoc         no certificate was involved. What a developer's machine and CI's
#                 check job build, and what nobody may publish.
#   developer-id  signed with a Developer ID, hardened runtime on, secure
#                 timestamp, and the entitlements this repository keeps.
#   notarized     the above, plus Apple's ticket stapled into the bundle and
#                 Gatekeeper actually accepting the app.
#
# It is told rather than deduced. A gate that read the bundle and agreed with
# whatever it found would pass an unsigned release with a shrug -- and an
# unsigned release is exactly the one that must never reach a person.
set -eu

if [ "$#" -ne 6 ]; then
	echo "usage: $0 <dist-dir> <version> <arches> <binaries> <ldflags> <expect-seal>" >&2
	exit 2
fi

dist_dir=$1
version=$2
arches=$3
binaries=$4
ldflags=$5
expect_seal=$6

work=
# shellcheck disable=SC2034  # read by scripts/dist-app-checks.sh
src=cmd/fleetdeck-window

fail() {
	echo "verify-dist-app: $*" >&2
	[ -z "$work" ] || rm -rf "$work"
	exit 1
}

# shellcheck source=scripts/dist-app-checks.sh
. "$(dirname "$0")/dist-app-checks.sh"

[ -d "$dist_dir" ] || fail "$dist_dir is not a directory"
[ -n "$version" ] || fail "version is empty"
case $expect_seal in
	adhoc | developer-id | notarized) ;;
	*) fail "unknown seal '$expect_seal': expected adhoc, developer-id or notarized" ;;
esac
[ -n "$ldflags" ] || fail "ldflags is empty: nothing would stamp a version into the binaries"

zip_name="fleetdeck-$version-macos.zip"
zip="$dist_dir/$zip_name"

# The publish step uploads dist/*.zip by glob: a zip left from another version would be
# published under this tag, named after a version nobody released.
found=$(for f in "$dist_dir"/*.zip; do [ -e "$f" ] && basename "$f"; done)
[ "$found" = "$zip_name" ] || fail "$dist_dir must hold exactly $zip_name, found: $(echo "$found" | tr '\n' ' ')"

# Exactly the app, nothing else: no AppleDouble "._" companions, no __MACOSX, no
# staging directory.
expected_members=$(
	{
		printf '%s\n' fleetdeck.app/ fleetdeck.app/Contents/ fleetdeck.app/Contents/Info.plist
		printf '%s\n' fleetdeck.app/Contents/MacOS/
		for b in $binaries; do echo "fleetdeck.app/Contents/MacOS/$b"; done
		printf '%s\n' fleetdeck.app/Contents/Resources/ fleetdeck.app/Contents/Resources/icon.icns
		printf '%s\n' fleetdeck.app/Contents/_CodeSignature/ fleetdeck.app/Contents/_CodeSignature/CodeResources
		# The notarization ticket, which stapler writes into the bundle. Measured
		# on a notarized app on this machine: Contents/CodeResources, beside
		# _CodeSignature rather than inside it, magic "s8ch". A notarized release
		# without it would ask Apple over the network at every first launch
		# instead of carrying its own answer -- and would fail on a Mac offline.
		# An `if`, not a `[ ... ] &&`: the last command of this group decides the
		# command substitution's status, and a false test there would end the
		# script under set -e with every check below unrun.
		if [ "$expect_seal" = notarized ]; then
			printf '%s\n' fleetdeck.app/Contents/CodeResources
		fi
	} | sort
)
members=$(unzip -Z1 "$zip" | sort)
if [ "$members" != "$expected_members" ]; then
	echo "verify-dist-app: $zip does not hold exactly the app" >&2
	echo "  expected: $(echo "$expected_members" | tr '\n' ' ')" >&2
	echo "  found:    $(echo "$members" | tr '\n' ' ')" >&2
	exit 1
fi

work=$(mktemp -d)
# No EXIT trap, on purpose. Under that bash 3.2, a syntax error with an EXIT trap set
# ends the script with the trap's status -- 0 after a successful rm -- so a gate with
# a syntax error in it passes (measured: status 2 without the trap, 0 with it, and
# still 0 with a trap that tries to keep $?). The scratch directory is removed by
# fail and at the end instead -- a command that fails under set -e leaves it in
# $TMPDIR, the cheaper failure of the two -- and only a signal needs a trap.
trap 'rm -rf "$work"; exit 1' INT TERM HUP
ditto -x -k "$zip" "$work"
app="$work/fleetdeck.app"

app_is_the_release "$app" "$version" "$arches" "$binaries" "$ldflags"
app_carries_the_seal "$app" "$binaries" "$expect_seal"

want_archs=$(for arch in $arches; do lipo_arch "$arch"; done | sort | tr '\n' ' ')
rm -rf "$work"
# This line is the proof the gate reached its end; the Go test asserts it.
echo "verify-dist-app: $zip ok (contents, version $version in plist and binaries, ${want_archs% }, $expect_seal)"
