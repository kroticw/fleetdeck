#!/usr/bin/env bash
# Rebuilds packaging/dmg/DS_Store: the window a person sees when they open
# fleetdeck-<version>-macos.dmg.
#
# This is a human tool, not a build step, and the strongest case in this
# repository for committing a build artifact rather than making one.
#
# A .DS_Store cannot be computed. It is Finder's own record of a folder's window
# — its size and position, the icon view and icon size, where each icon sits, and
# which file is the background — written in a private binary format by Finder and
# by nothing else. The only supported way to produce one is to ask Finder,
# through AppleScript, on a machine with a window server. That is exactly what a
# release must not need: `make dist-dmg` runs on a GitHub runner, where driving
# Finder is the classic source of a build that passes four times and hangs the
# fifth.
#
# So the window is laid out once, here, by hand, on a machine with a desktop; the
# file it produces is committed; and the release step copies it in. The gate then
# compares what it published against what is committed, byte for byte
# (scripts/verify-dist-dmg.sh), which is the honest version of the claim: not
# "the window looks right", but "the window shipped is the window reviewed".
#
# It needs create-dmg (brew install create-dmg), which is a wrapper around
# hdiutil and that AppleScript. Nothing in the release path needs it.
#
# The app it lays the window out around is a stub, not the real one: a .DS_Store
# records icon positions by *file name*, and the stub is named fleetdeck.app like
# the real one. Building a 40 MB universal app to position an icon would be
# forty megabytes of nothing.
#
# Two numbers below are load-bearing and are checked here rather than left to
# drift:
#
#   the window is the background's height plus TITLE_BAR. Finder paints the
#   background into the window's content area, anchored top left, at its natural
#   size. A window shorter than the picture scrolls; a taller one shows a strip
#   of bare window under it.
#
#   the volume is named VOLUME_NAME, and the release build names it the same.
#   Finder records the background picture as an alias carrying the volume's name
#   beside the relative path, so an image built under another name can come up
#   with a blank window.
#
# Usage: build-dmg-layout.sh   (or: make dmg-layout)
#
# Afterwards, look at it. `make dmg-layout` prints the command that mounts the
# image it built; open it in Finder and see that the two icons sit where they
# should and the background paints. That look is the only check of the window
# there is or can be — so it happens here, once, when the layout changes.
set -euo pipefail

cd "$(dirname "$0")/.."

OUT="packaging/dmg/DS_Store"
BACKGROUND="packaging/dmg/background.tiff"

VOLUME_NAME=fleetdeck
TITLE_BAR=28
ICON_SIZE=128
# Where the two icons are centred, in the background picture's own coordinates.
APP_X=180
APP_Y=160
LINK_X=480
LINK_Y=160

if ! command -v create-dmg >/dev/null 2>&1; then
	echo "build-dmg-layout: create-dmg not found on PATH — install it: brew install create-dmg" >&2
	exit 1
fi
[ -f "$BACKGROUND" ] || {
	echo "build-dmg-layout: $BACKGROUND is missing — run make dmg-background first" >&2
	exit 1
}

# The window is sized from the picture rather than from a number typed twice.
WIDTH=$(sips -g pixelWidth "$BACKGROUND" | awk '/pixelWidth/ {print $2}')
HEIGHT=$(sips -g pixelHeight "$BACKGROUND" | awk '/pixelHeight/ {print $2}')
WINDOW_HEIGHT=$((HEIGHT + TITLE_BAR))

WORKDIR=$(mktemp -d /tmp/fleetdeck-dmg-layout.XXXXXX)
trap 'rm -rf "$WORKDIR"' EXIT

# The stub. create-dmg records the scratch image's own path inside the alias it
# writes, so this runs out of a short neutral directory rather than out of
# whoever's home directory happened to invoke it.
STUB="$WORKDIR/src/fleetdeck.app"
mkdir -p "$STUB/Contents/MacOS" "$STUB/Contents/Resources"
cp cmd/fleetdeck-window/Info.plist "$STUB/Contents/Info.plist"
cp cmd/fleetdeck-window/icon.icns "$STUB/Contents/Resources/icon.icns"
printf '#!/bin/sh\nexit 0\n' >"$STUB/Contents/MacOS/fleetdeck"
chmod +x "$STUB/Contents/MacOS/fleetdeck"

create-dmg \
	--volname "$VOLUME_NAME" \
	--background "$PWD/$BACKGROUND" \
	--window-pos 200 120 \
	--window-size "$WIDTH" "$WINDOW_HEIGHT" \
	--icon-size "$ICON_SIZE" \
	--icon fleetdeck.app "$APP_X" "$APP_Y" \
	--app-drop-link "$LINK_X" "$LINK_Y" \
	--hide-extension fleetdeck.app \
	--no-internet-enable \
	"$WORKDIR/layout.dmg" "$WORKDIR/src" >"$WORKDIR/create.log" 2>&1 || {
	echo "build-dmg-layout: create-dmg failed:" >&2
	cat "$WORKDIR/create.log" >&2
	exit 1
}

mkdir -p "$WORKDIR/mnt"
hdiutil attach -nobrowse -mountrandom "$WORKDIR/mnt" -plist "$WORKDIR/layout.dmg" >"$WORKDIR/attach.plist"
MOUNTED=$(/usr/libexec/PlistBuddy -c 'Print' "$WORKDIR/attach.plist" | sed -n 's/^ *mount-point = //p' | head -1)
cp "$MOUNTED/.DS_Store" "$OUT"
hdiutil detach "$MOUNTED" >/dev/null

echo "build-dmg-layout: wrote $OUT for a ${WIDTH}x${WINDOW_HEIGHT} window over a ${WIDTH}x${HEIGHT} background"
echo "build-dmg-layout: now look at it — build a release image and open it:"
echo "    make dist-app dist-dmg VERSION=v0.0.0 DISTDIR=/tmp/fleetdeck-dmg && open /tmp/fleetdeck-dmg/fleetdeck-v0.0.0-macos.dmg"
