#!/bin/sh
#
# verify-dist-app.sh interrogates the zip `make dist-app` has just written, and fails if
# it is not an app a release may publish. It looks at the app the way a person gets it:
# unpacked from the zip, from the outside, and trusting nothing build-dist-app.sh says
# about what it did.
#
# Usage: verify-dist-app.sh <dist-dir> <version> <arches> <binaries> <ldflags>
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
	echo "verify-dist-app: $*" >&2
	[ -z "$work" ] || rm -rf "$work"
	exit 1
}

# lipo's name for a Go architecture. A function, not a case inside $(...): the bash
# 3.2 that is /bin/sh on macOS cannot parse a case pattern's ")" inside a command
# substitution. A mutation pass found this script stopping there, part way, with
# status 0 and every check below unrun (see docs/engineering/release-app.md).
lipo_arch() {
	case $1 in
		amd64) echo x86_64 ;;
		*) echo "$1" ;;
	esac
}

[ -d "$dist_dir" ] || fail "$dist_dir is not a directory"
[ -n "$version" ] || fail "version is empty"
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
plist="$app/Contents/Info.plist"

for key in CFBundleShortVersionString CFBundleVersion; do
	got=$(plutil -extract "$key" raw "$plist" 2>/dev/null || echo "(missing)")
	[ "$got" = "${version#v}" ] || fail "Info.plist $key is '$got', not '${version#v}': Finder's Get Info would show a version this release is not"
done
executable=$(plutil -extract CFBundleExecutable raw "$plist")
[ -x "$app/Contents/MacOS/$executable" ] || fail "Info.plist names $executable as the app, and it is not in Contents/MacOS"

# In everything but the version, the release app is the app `make window-app` builds:
# the same plist keys and the same icon. Compared through plutil's JSON form, with the
# version keys taken out of both, so a comment or key order in the source does not
# count as a difference and a changed or added key does.
src=cmd/fleetdeck-window
for p in release:"$plist" source:"$src/Info.plist"; do
	cp "${p#*:}" "$work/${p%%:*}.plist"
	plutil -remove CFBundleShortVersionString "$work/${p%%:*}.plist" 2>/dev/null || true
	plutil -remove CFBundleVersion "$work/${p%%:*}.plist" 2>/dev/null || true
	plutil -convert json -o "$work/${p%%:*}.json" "$work/${p%%:*}.plist"
done
cmp -s "$work/release.json" "$work/source.json" ||
	fail "the release Info.plist differs from $src/Info.plist in more than the version
  release: $(cat "$work/release.json")
  source:  $(cat "$work/source.json")"
cmp -s "$app/Contents/Resources/icon.icns" "$src/icon.icns" ||
	fail "the release icon is not $src/icon.icns"

# Each slice is taken out of the universal binary and read on its own: `go version -m`
# on a universal binary reads the first slice and says nothing about the others.
# The ldflags must match exactly, which is also what proves the window was built
# without the source tree `make window-app` writes into it for its Update button.
want_archs=$(for arch in $arches; do lipo_arch "$arch"; done | sort | tr '\n' ' ')
for b in $binaries; do
	bin="$app/Contents/MacOS/$b"
	[ -x "$bin" ] || fail "$b is not executable once unpacked"
	got_archs=$(lipo -archs "$bin" | tr ' ' '\n' | sort | tr '\n' ' ')
	[ "$got_archs" = "$want_archs" ] || fail "$b holds '$got_archs', want '$want_archs'"
	for arch in $arches; do
		lipo -thin "$(lipo_arch "$arch")" -output "$work/$b-$arch" "$bin"
		info=$(go version -m "$work/$b-$arch")
		echo "$info" | grep -qE "^[[:space:]]*build[[:space:]]+GOARCH=$arch\$" ||
			fail "$b: the $(lipo_arch "$arch") slice is not built for darwin/$arch"
		got_ldflags=$(echo "$info" | sed -n 's/^[[:space:]]*build[[:space:]]*-ldflags=//p')
		[ "$got_ldflags" = "\"$ldflags\"" ] ||
			fail "$b ($arch) was not built with exactly the release ldflags
  wanted: \"$ldflags\"
  found:  ${got_ldflags:-none}"
	done
done

# The seal over the whole bundle. Without it a downloaded copy is "damaged" to
# Gatekeeper rather than merely unverified.
codesign --verify --deep --strict "$app" || fail "the bundle's signature does not verify"

# Asking the program is not the same as reading its metadata: the linker accepts an
# -X target that does not exist without complaint. The panel is the one command that
# answers a version question; the host runs its own slice of it.
reported=$("$app/Contents/MacOS/fleetdeck" version)
[ "$reported" = "$version" ] || fail "the app's panel reports '$reported', not '$version'"

rm -rf "$work"
# This line is the proof the gate reached its end; the Go test asserts it.
echo "verify-dist-app: $zip ok (contents, version $version in plist and binaries, ${want_archs% }, sealed)"
