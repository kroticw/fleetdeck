#!/bin/sh
#
# verify-dist-dmg.sh interrogates the disk image `make dist-dmg` has just written,
# and fails if it is not an image a release may publish. It looks at it the way a
# person gets it: mounted, from the outside, trusting nothing build-dist-dmg.sh
# says about what it did.
#
# Everything it asks about the app inside comes from scripts/dist-app-checks.sh,
# the same file scripts/verify-dist-app.sh asks the zip's app with. What is here
# is what only an image has: that there is exactly one of it; that the volume
# holds the app, the Applications shortcut and the window's two files and nothing
# else; that the window layout published is the one committed to this repository;
# and that the image carries a seal of its own.
#
# What it deliberately does not claim: that the window looks right. Whether the
# icons land where the layout says and whether the background paints is Finder's
# business, and no command here can see a window. The check this gate can make
# honestly is that the bytes shipped are the bytes reviewed -- .DS_Store and the
# background compared against packaging/dmg/ -- and a person looks at the window
# once, when the layout changes.
#
# Usage: verify-dist-dmg.sh <dist-dir> <version> <arches> <binaries> <ldflags> <expect-seal>
#
# <expect-seal> is the seal both the image and the app inside it are required to
# carry -- adhoc, developer-id or notarized -- and it is told rather than
# deduced, for the reason spelled out in scripts/verify-dist-app.sh.
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
mounted=
# shellcheck disable=SC2034  # read by scripts/dist-app-checks.sh
src=cmd/fleetdeck-window
layout=packaging/dmg

# A mounted image outlives the process that mounted it. Every way out of this
# script goes through here or through the end, and both detach: a gate that left
# a volume behind would leave the next run to find a name already taken.
fail() {
	echo "verify-dist-dmg: $*" >&2
	[ -z "$mounted" ] || hdiutil detach "$mounted" -force >/dev/null 2>&1 || :
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

dmg_name="fleetdeck-$version-macos.dmg"
dmg="$dist_dir/$dmg_name"

# The publish step uploads dist/*.dmg by glob: an image left from another version
# would be published under this tag, named after a version nobody released.
found=$(for f in "$dist_dir"/*.dmg; do [ -e "$f" ] && basename "$f"; done)
[ "$found" = "$dmg_name" ] || fail "$dist_dir must hold exactly $dmg_name, found: $(echo "$found" | tr '\n' ' ')"

work=$(mktemp -d)
# No EXIT trap, on purpose -- see scripts/dist-app-checks.sh for what one costs
# under the bash 3.2 that is /bin/sh here.
trap 'rm -rf "$work"; exit 1' INT TERM HUP

# The image's own seal, asked before it is mounted: a person's Mac asks the same
# question at the same moment, before it has seen anything inside.
#
# Integrity first, identity second, for the reason dist-app-checks.sh spells out:
# codesign --display believes whatever a tampered container tells it.
codesign --verify --strict "$dmg" || fail "the image's signature does not verify"
image_seal=$(seal_of "$dmg")
case $expect_seal in
	adhoc)
		echo "$image_seal" | grep -q '^Signature=adhoc' ||
			fail "the image is sealed with something other than an ad-hoc signature, and this build was not told to expect that"
		;;
	developer-id | notarized)
		# The image is checked for a Developer ID and a secure timestamp, and
		# deliberately not for the hardened runtime or entitlements: those are
		# restrictions a process runs under, and a disk image never becomes one.
		echo "$image_seal" | grep -q '^Authority=Developer ID Application:' ||
			fail "the image is not signed with a Developer ID Application certificate"
		echo "$image_seal" | grep -q '^Authority=Developer ID Certification Authority$' ||
			fail "the image: the signing certificate does not chain to Apple's Developer ID authority"
		echo "$image_seal" | grep -q '^Authority=Apple Root CA$' ||
			fail "the image: the signing certificate does not chain to the Apple root"
		echo "$image_seal" | grep -q '^Timestamp=' ||
			fail "the image was signed without a secure timestamp, and notarization rejects a submission without one"
		echo "$image_seal" | grep -q '^TeamIdentifier=[A-Z0-9]' ||
			fail "the image carries no team identifier"
		;;
esac

if [ "$expect_seal" = notarized ]; then
	xcrun stapler validate "$dmg" >/dev/null 2>&1 ||
		fail "no notarization ticket is stapled to the image: a Mac with no network would refuse to open it"
	# The image is assessed as something a person opens, not as something that
	# executes -- that is what --type open is -- and against its own signature
	# rather than against a quarantine record it does not carry yet.
	assessment=$(spctl --assess --type open --context context:primary-signature -vv "$dmg" 2>&1) ||
		fail "Gatekeeper rejects the image, which is what a person would see on opening it:
$assessment"
	echo "$assessment" | grep -q 'source=Notarized Developer ID' ||
		fail "Gatekeeper accepts the image for some reason other than its notarization:
$assessment"
fi

# -mountrandom needs the directory it randomises inside to exist already;
# without it hdiutil fails with "no mountable file systems", which reads like a
# broken image and is not one. -nobrowse keeps the volume out of the Finder
# sidebar of whoever is running this.
mkdir -p "$work/mnt"
hdiutil attach -nobrowse -readonly -mountrandom "$work/mnt" -plist "$dmg" >"$work/attach.plist" 2>"$work/attach.err" ||
	fail "the image does not mount:
$(cat "$work/attach.err")"
mounted=$(/usr/libexec/PlistBuddy -c 'Print' "$work/attach.plist" 2>/dev/null |
	sed -n 's/^ *mount-point = //p' | head -1)
[ -n "$mounted" ] || fail "the image mounted without saying where"

# Exactly the four things, and nothing else: no stray .DS_Store from a build
# machine, no .fseventsd, no second copy of anything.
expected_entries=$(printf '%s\n' .background .DS_Store Applications fleetdeck.app | sort)
# shellcheck disable=SC2012  # the names on this volume are the four below, all plain ASCII
entries=$(ls -A "$mounted" | sort)
if [ "$entries" != "$expected_entries" ]; then
	echo "verify-dist-dmg: the volume does not hold exactly the app and its window" >&2
	echo "  expected: $(echo "$expected_entries" | tr '\n' ' ')" >&2
	echo "  found:    $(echo "$entries" | tr '\n' ' ')" >&2
	hdiutil detach "$mounted" -force >/dev/null 2>&1 || :
	rm -rf "$work"
	exit 1
fi

# A symlink, not a folder. A copied /Applications would be a 60 GB image, and a
# folder named Applications would be a place to drop the app that goes nowhere.
[ -L "$mounted/Applications" ] || fail "Applications on the volume is not a symbolic link"
[ "$(readlink "$mounted/Applications")" = /Applications ] ||
	fail "Applications on the volume points at $(readlink "$mounted/Applications"), not /Applications"

# The window published is the window in this repository. Byte for byte, because
# these two files are the whole layout and neither is readable as text: a
# .DS_Store nobody reviewed is a window nobody has seen.
cmp -s "$mounted/.DS_Store" "$layout/DS_Store" ||
	fail "the window layout on the image is not $layout/DS_Store"
cmp -s "$mounted/.background/background.tiff" "$layout/background.tiff" ||
	fail "the window background on the image is not $layout/background.tiff"
# shellcheck disable=SC2012  # as above
background_entries=$(ls -A "$mounted/.background" | sort)
[ "$background_entries" = background.tiff ] ||
	fail ".background holds more than the background picture: $(echo "$background_entries" | tr '\n' ' ')"

# Both window files carry Finder's invisible flag as well as a leading dot.
# Checked because it is the one property of the assembly that leaves no trace
# anywhere else: an image built without it looks right to whoever built it and
# shows a stray folder to somebody whose file manager goes by the flag.
for hidden in .background .DS_Store; do
	# stat's %Sf prints the flags by name, or "-" for none.
	case ",$(stat -f '%Sf' "$mounted/$hidden")," in
		*,hidden,*) ;;
		*) fail "$hidden on the volume does not carry Finder's invisible flag" ;;
	esac
done

# The app, asked the same questions the zip's app is asked, in the place a person
# would drag it from.
app="$mounted/fleetdeck.app"
[ -d "$app" ] || fail "the volume holds no fleetdeck.app"
app_is_the_release "$app" "$version" "$arches" "$binaries" "$ldflags"
app_carries_the_seal "$app" "$binaries" "$expect_seal"

# And it is the *same* app the zip carries, not merely another one built to the
# same version. This is the one claim the checks above cannot make: they would
# pass an image holding a second build, made from a different tree, stamped with
# the same tag and signed with the same certificate.
#
# Compared by code directory hash rather than file by file. That hash is what the
# signature is over, so two bundles sharing one are the same program with the
# same sealed resources -- and it costs one command instead of a recursive
# comparison of eighty megabytes. The notarization ticket stapling adds lands
# outside what the signature seals, so a stapled bundle and the bundle it was
# stapled from share it.
zip="$dist_dir/fleetdeck-$version-macos.zip"
[ -f "$zip" ] || fail "$zip is not beside the image: the release publishes both, and this gate holds them to being one app"
ditto -x -k "$zip" "$work/fromzip" || fail "could not unpack $zip"
image_cdhash=$(seal_of "$app" | sed -n 's/^CDHash=//p' | head -1)
zip_cdhash=$(seal_of "$work/fromzip/fleetdeck.app" | sed -n 's/^CDHash=//p' | head -1)
[ -n "$image_cdhash" ] || fail "the app on the image reports no code directory hash"
[ "$image_cdhash" = "$zip_cdhash" ] ||
	fail "the app on the image is not the app in the zip
  image: $image_cdhash
  zip:   $zip_cdhash"

hdiutil detach "$mounted" >/dev/null 2>&1 || fail "the image would not detach"
mounted=
rm -rf "$work"
# This line is the proof the gate reached its end; the Go test asserts it.
echo "verify-dist-dmg: $dmg ok (volume contents, committed window layout, version $version, $expect_seal)"
