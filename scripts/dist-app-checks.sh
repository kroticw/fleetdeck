#!/bin/sh
#
# dist-app-checks.sh holds the questions both release gates ask of fleetdeck.app
# itself, so that the app inside the disk image is checked by the same code as
# the app inside the zip rather than by something that merely looks similar.
#
# It is sourced, never run. Two gates source it:
#
#   scripts/verify-dist-app.sh   the app unpacked from fleetdeck-<v>-macos.zip
#   scripts/verify-dist-dmg.sh   the app on the mounted fleetdeck-<v>-macos.dmg
#
# What stays in each gate rather than moving here: everything about the
# container. The zip's member list, read with unzip -Z1; the image's volume
# contents, window layout and its own signature. Those have nothing in common
# and sharing them would mean inventing a shape that fits neither.
#
# The sourcing script must define, before calling anything here:
#
#   fail()  ends the run with a message, prefixed with that gate's own name
#   work    a scratch directory this file may write into
#   src     cmd/fleetdeck-window, the tree the release app is compared against
#
# Two shapes below are not stylistic, and changing them back has already cost
# this repository a silently passing gate. /bin/sh on macOS is bash 3.2:
#
#   - lipo_arch is a function rather than a `case` inside $(...). That bash
#     cannot parse a case pattern's ")" inside a command substitution, and a
#     mutation pass found the gate stopping there, part way, with status 0 and
#     every check after it unrun.
#   - nothing here installs an EXIT trap. Under that bash a syntax error with
#     an EXIT trap set ends the script with the trap's status -- 0 after a
#     successful rm -- so a gate with a syntax error in it passes.
#
# See docs/engineering/release-app.md.

# work and src are the two variables the sourcing script is required to set;
# nothing reading this file on its own can see where they come from.
# shellcheck shell=sh
# shellcheck disable=SC2154

# lipo_arch is lipo's name for a Go architecture.
lipo_arch() {
	case $1 in
		amd64) echo x86_64 ;;
		*) echo "$1" ;;
	esac
}

# seal_of prints everything codesign knows about one piece of signed code.
# codesign --display writes to standard error, so every reader folds it in.
seal_of() { codesign --display --verbose=4 "$1" 2>&1; }

# entitlements_of writes one piece's entitlements to $2 as JSON. A piece signed
# with no entitlements at all has no blob to print, and that is the same thing as
# an empty list -- so it is normalised to one rather than left to compare as an
# empty file against a plist.
entitlements_of() {
	codesign --display --entitlements :- "$1" >"$work/entitlements.raw" 2>/dev/null || :
	if [ -s "$work/entitlements.raw" ]; then
		plutil -convert json -o "$2" "$work/entitlements.raw" ||
			fail "$1 carries an entitlements blob that is not a property list"
	else
		echo '{}' >"$2"
	fi
}

# The one thing the hardened runtime is: a set of restrictions a process runs
# under, recorded as a flag in the signature. Everything in entitlements.plist is
# a hole in it, which is why this compares that file byte for byte with what the
# bundle actually carries -- an entitlement added to a build and not to the file,
# or the other way round, is a difference nobody meant.
developer_id_seal() {
	_piece=$1
	_info=$(seal_of "$_piece")
	echo "$_info" | grep -q '^Authority=Developer ID Application:' ||
		fail "$_piece is not signed with a Developer ID Application certificate"
	echo "$_info" | grep -q '^Authority=Developer ID Certification Authority$' ||
		fail "$_piece: the signing certificate does not chain to Apple's Developer ID authority"
	echo "$_info" | grep -q '^Authority=Apple Root CA$' ||
		fail "$_piece: the signing certificate does not chain to the Apple root"
	echo "$_info" | grep -q '^Timestamp=' ||
		fail "$_piece was signed without a secure timestamp, and notarization rejects a submission without one"
	echo "$_info" | grep -q '^TeamIdentifier=[A-Z0-9]' ||
		fail "$_piece carries no team identifier"
	echo "$_info" | grep -qE '^CodeDirectory .*flags=0x[0-9a-f]+\([^)]*runtime' ||
		fail "$_piece was signed without the hardened runtime, which notarization requires"
	entitlements_of "$_piece" "$work/got-entitlements.json"
	plutil -convert json -o "$work/want-entitlements.json" "$src/entitlements.plist"
	cmp -s "$work/got-entitlements.json" "$work/want-entitlements.json" ||
		fail "$_piece carries entitlements this repository does not keep -- every one of them is a hole in the hardened runtime
  bundle: $(cat "$work/got-entitlements.json")
  $src/entitlements.plist: $(cat "$work/want-entitlements.json")"
}

# app_is_the_release checks that the bundle at $1 is the app this VERSION built:
# the tag in its Info.plist, a plist and icon otherwise identical to the source
# tree's, both architectures in every binary, every slice built for the
# architecture it claims and with exactly the release ldflags, and the panel
# inside answering with the tag when it is asked.
#
#   $1 the bundle
#   $2 version, v-prefixed
#   $3 architectures, space separated, as Go names them
#   $4 binaries, space separated
#   $5 the exact ldflags the release was built with
app_is_the_release() {
	_app=$1
	_version=$2
	_arches=$3
	_binaries=$4
	_ldflags=$5
	_plist="$_app/Contents/Info.plist"

	for _key in CFBundleShortVersionString CFBundleVersion; do
		_got=$(plutil -extract "$_key" raw "$_plist" 2>/dev/null || echo "(missing)")
		[ "$_got" = "${_version#v}" ] ||
			fail "Info.plist $_key is '$_got', not '${_version#v}': Finder's Get Info would show a version this release is not"
	done
	_executable=$(plutil -extract CFBundleExecutable raw "$_plist")
	[ -x "$_app/Contents/MacOS/$_executable" ] ||
		fail "Info.plist names $_executable as the app, and it is not in Contents/MacOS"

	# In everything but the version, the release app is the app `make window-app`
	# builds: the same plist keys and the same icon. Compared through plutil's
	# JSON form, with the version keys taken out of both, so a comment or key
	# order in the source does not count as a difference and a changed or added
	# key does.
	for _p in release:"$_plist" source:"$src/Info.plist"; do
		cp "${_p#*:}" "$work/${_p%%:*}.plist"
		plutil -remove CFBundleShortVersionString "$work/${_p%%:*}.plist" 2>/dev/null || true
		plutil -remove CFBundleVersion "$work/${_p%%:*}.plist" 2>/dev/null || true
		plutil -convert json -o "$work/${_p%%:*}.json" "$work/${_p%%:*}.plist"
	done
	cmp -s "$work/release.json" "$work/source.json" ||
		fail "the release Info.plist differs from $src/Info.plist in more than the version
  release: $(cat "$work/release.json")
  source:  $(cat "$work/source.json")"
	cmp -s "$_app/Contents/Resources/icon.icns" "$src/icon.icns" ||
		fail "the release icon is not $src/icon.icns"

	# Each slice is taken out of the universal binary and read on its own:
	# `go version -m` on a universal binary reads the first slice and says
	# nothing about the others. The ldflags must match exactly, which is also
	# what proves the window was built without the source tree `make window-app`
	# writes into it for its Update button.
	_want_archs=$(for _arch in $_arches; do lipo_arch "$_arch"; done | sort | tr '\n' ' ')
	for _b in $_binaries; do
		_bin="$_app/Contents/MacOS/$_b"
		[ -x "$_bin" ] || fail "$_b is not executable"
		_got_archs=$(lipo -archs "$_bin" | tr ' ' '\n' | sort | tr '\n' ' ')
		[ "$_got_archs" = "$_want_archs" ] || fail "$_b holds '$_got_archs', want '$_want_archs'"
		for _arch in $_arches; do
			lipo -thin "$(lipo_arch "$_arch")" -output "$work/$_b-$_arch" "$_bin"
			_info=$(go version -m "$work/$_b-$_arch")
			echo "$_info" | grep -qE "^[[:space:]]*build[[:space:]]+GOARCH=$_arch\$" ||
				fail "$_b: the $(lipo_arch "$_arch") slice is not built for darwin/$_arch"
			_got_ldflags=$(echo "$_info" | sed -n 's/^[[:space:]]*build[[:space:]]*-ldflags=//p')
			[ "$_got_ldflags" = "\"$_ldflags\"" ] ||
				fail "$_b ($_arch) was not built with exactly the release ldflags
  wanted: \"$_ldflags\"
  found:  ${_got_ldflags:-none}"
		done
	done

	# Asking the program is not the same as reading its metadata: the linker
	# accepts an -X target that does not exist without complaint. The panel is
	# the one command that answers a version question; the host runs its own
	# slice of it.
	_reported=$("$_app/Contents/MacOS/fleetdeck" version)
	[ "$_reported" = "$_version" ] || fail "the app's panel reports '$_reported', not '$_version'"
}

# app_carries_the_seal checks who sealed the bundle at $1, and how.
#
#   $1 the bundle
#   $2 binaries, space separated
#   $3 the seal demanded: adhoc, developer-id or notarized
#
# The order is the load-bearing part and it is not interchangeable. Integrity
# first: codesign --display on a bundle changed after signing still prints the
# original Developer ID, team and stapled ticket, so no fact about identity
# means anything until --verify has passed. Identity second: --verify passes an
# ad-hoc bundle with status 0, so integrity says nothing about origin. The
# system's own verdict last. Measured, both of them -- see
# docs/engineering/update-from-release.md.
app_carries_the_seal() {
	_app=$1
	_binaries=$2
	_expect=$3

	# Without a seal a downloaded copy is "damaged" to Gatekeeper rather than
	# merely unverified.
	codesign --verify --deep --strict "$_app" || fail "the bundle's signature does not verify"

	case $_expect in
		adhoc)
			seal_of "$_app" | grep -q '^Signature=adhoc' ||
				fail "the app is sealed with something other than an ad-hoc signature, and this build was not told to expect that"
			;;
		developer-id | notarized)
			_executable=$(plutil -extract CFBundleExecutable raw "$_app/Contents/Info.plist")
			developer_id_seal "$_app"
			for _b in $_binaries; do
				[ "$_b" = "$_executable" ] && continue
				developer_id_seal "$_app/Contents/MacOS/$_b"
			done
			;;
	esac

	[ "$_expect" = notarized ] || return 0

	xcrun stapler validate "$_app" >/dev/null 2>&1 ||
		fail "no notarization ticket is stapled to the bundle: a Mac with no network would refuse this app"
	# The question the whole release is for, asked of the app rather than of the
	# process that made it: does Gatekeeper let a person open this.
	_assessment=$(spctl --assess --type execute -vv "$_app" 2>&1) ||
		fail "Gatekeeper rejects the app, which is what a person would see on opening it:
$_assessment"
	echo "$_assessment" | grep -q 'source=Notarized Developer ID' ||
		fail "Gatekeeper accepts the app for some reason other than its notarization:
$_assessment"
}
