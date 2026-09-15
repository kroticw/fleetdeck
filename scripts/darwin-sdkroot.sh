#!/bin/sh
# darwin-sdkroot.sh prints the macOS SDK a cgo build on darwin links against:
# SDKROOT when it is set, and otherwise the SDK of the developer directory
# xcode-select chose, which is the one its linker understands. Left to itself,
# clang took the Command Line Tools' MacOSX.sdk even with Xcode's linker
# selected, and on a macOS 27 machine that SDK was newer than the linker knew
# (docs/engineering/dev-app.md, "The SDK").
#
# It fails, printing nothing, when there is no SDK to name: a build against an
# empty SDKROOT is no build at all.

if [ -n "${SDKROOT:-}" ]; then
	printf '%s\n' "$SDKROOT"
	exit 0
fi

sdk=$(xcrun --sdk macosx --show-sdk-path 2>/dev/null)
status=$?
if [ "$status" -ne 0 ] || [ -z "$sdk" ]; then
	# Asked again for what it says, which the first answer kept out of the
	# path: an Xcode whose license nobody has accepted yet exits 69 and says so.
	why=$(xcrun --sdk macosx --show-sdk-path 2>&1 >/dev/null)
	echo "darwin-sdkroot: xcrun --sdk macosx --show-sdk-path named no SDK (exit $status): $why" >&2
	echo "darwin-sdkroot: do what xcrun says, install Xcode or the Command Line Tools, or set SDKROOT" >&2
	exit 1
fi
printf '%s\n' "$sdk"
