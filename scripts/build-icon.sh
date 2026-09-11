#!/usr/bin/env bash
# Rebuilds cmd/fleetdeck-window/icon.icns from cmd/fleetdeck-window/icon-source.svg.
#
# This is a human tool, not a build step: `make window-app`, `make dist` and CI
# never call it. The .icns it produces is committed to the repository as a build
# artifact, because the tool this script needs (rsvg-convert) is a Homebrew
# package present on this machine, not something GitHub's macOS or Ubuntu
# runners carry, and a build step that silently degrades to a blurry or missing
# icon on CI would be worse than one that simply does not regenerate anything
# there. Run this by hand — `make icon` — after editing icon-source.svg, and
# commit the resulting icon.icns alongside the source change.
set -euo pipefail

cd "$(dirname "$0")/.."

SRC="cmd/fleetdeck-window/icon-source.svg"
OUT="cmd/fleetdeck-window/icon.icns"

if ! command -v rsvg-convert >/dev/null 2>&1; then
	echo "build-icon: rsvg-convert not found on PATH — install it: brew install librsvg" >&2
	exit 1
fi

if ! command -v iconutil >/dev/null 2>&1; then
	echo "build-icon: iconutil not found on PATH — this script only runs on macOS" >&2
	exit 1
fi

WORKDIR=$(mktemp -d)
trap 'rm -rf "$WORKDIR"' EXIT

ICONSET="$WORKDIR/icon.iconset"
mkdir -p "$ICONSET"

# The full set macOS expects: five base sizes, each also at @2x. A partial set
# gives a blurry icon in whichever context (Dock, Finder list, Finder icon
# view, Cmd+Tab) picks a size this iconset does not have.
render() {
	local name=$1 pixels=$2
	rsvg-convert --width "$pixels" --height "$pixels" "$SRC" --output "$ICONSET/$name"
}

render icon_16x16.png      16
render icon_16x16@2x.png   32
render icon_32x32.png      32
render icon_32x32@2x.png   64
render icon_128x128.png    128
render icon_128x128@2x.png 256
render icon_256x256.png    256
render icon_256x256@2x.png 512
render icon_512x512.png    512
render icon_512x512@2x.png 1024

iconutil --convert icns "$ICONSET" --output "$OUT"

echo "build-icon: wrote $OUT from $(ls "$ICONSET" | wc -l | tr -d ' ') source images"
