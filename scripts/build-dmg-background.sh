#!/usr/bin/env bash
# Rebuilds packaging/dmg/background.tiff from packaging/dmg/background-source.svg.
#
# This is a human tool, not a build step: `make dist-dmg` and CI never call it.
# The TIFF it produces is committed to the repository as a build artifact, for
# the same reason cmd/fleetdeck-window/icon.icns is: the tool it needs
# (rsvg-convert) is a Homebrew package present on this machine and not something
# GitHub's runners carry, and a release step that silently degraded to a missing
# background would be worse than one that simply does not regenerate anything
# there. Run it by hand — `make dmg-background` — after editing the SVG, and
# commit the resulting TIFF alongside the source change.
#
# A TIFF and not a PNG, and this is the whole reason the file is not simply the
# PNG rsvg-convert emits: Finder picks the background's resolution out of a
# multi-representation TIFF, so one file serves a Retina display at 2x and an
# external monitor at 1x. `tiffutil -cathidpicheck` is what pairs them, and it
# refuses the pair unless the second image is exactly twice the first — which is
# also a check that the two renders did not drift apart.
#
# After changing the picture's *size*, the window has to be laid out again:
# the window's height is the background's height plus the title bar, and that
# number lives in packaging/dmg/DS_Store. See scripts/build-dmg-layout.sh.
set -euo pipefail

cd "$(dirname "$0")/.."

SRC="packaging/dmg/background-source.svg"
OUT="packaging/dmg/background.tiff"

# The size the window is built around. Changing it means regenerating the layout
# too, and the two numbers are checked against each other by the layout script.
WIDTH=660
HEIGHT=360

if ! command -v rsvg-convert >/dev/null 2>&1; then
	echo "build-dmg-background: rsvg-convert not found on PATH — install it: brew install librsvg" >&2
	exit 1
fi

if ! command -v tiffutil >/dev/null 2>&1; then
	echo "build-dmg-background: tiffutil not found on PATH — this script only runs on macOS" >&2
	exit 1
fi

WORKDIR=$(mktemp -d)
trap 'rm -rf "$WORKDIR"' EXIT

rsvg-convert --width "$WIDTH" --height "$HEIGHT" "$SRC" --output "$WORKDIR/background.png"
rsvg-convert --width "$((WIDTH * 2))" --height "$((HEIGHT * 2))" "$SRC" --output "$WORKDIR/background@2x.png"

tiffutil -cathidpicheck "$WORKDIR/background.png" "$WORKDIR/background@2x.png" -out "$OUT"

echo "build-dmg-background: wrote $OUT (${WIDTH}x${HEIGHT}, and $((WIDTH * 2))x$((HEIGHT * 2)) for Retina)"
