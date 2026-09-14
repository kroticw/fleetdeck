#!/bin/sh
#
# ci-window-stand.sh starts the window of one app bundle on a stand and says how far
# it got: whether the window was loaded and started, whether the panel it started
# looked for no daemon, and whether the panel's page reported itself loaded. CI's
# window-on-oldest-macos runs it on a runner and nowhere else: it opens a window, and
# on a person's machine a window needs a moment they agree to
# (docs/engineering/window-and-panel.md, "Test stands on a machine with a live fleet").
#
# Usage: ci-window-stand.sh <app> <port> <out-dir> <how>
#
#   <how>  exec  the window binary started directly, the way a terminal starts it
#          open  the bundle opened through LaunchServices, the way Finder opens it
#
# The stand is the documented one: its own HOME with a configuration naming <port>,
# and FLEETDECK_STAND_SOCKET naming a socket nothing listens on, so the panel never
# looks for the fleet daemon. "panel" is what the window's page script reports for a
# page that loaded with its styles and its scripts (pagePanel in
# cmd/fleetdeck-window/owner.go); a window process that merely stays up is not that.
#
# Everything it saw goes to <out-dir>. It exits 0 only when the page said "panel",
# the window was still running when asked, and the panel's log says it looks for no
# daemon.
#
# Its screenshot is the whole screen, so it is taken on GitHub Actions only
# (scripts/stand-capture.sh): on a person's Mac it would be everything they have open.
set -eu

# shellcheck source=scripts/stand-capture.sh
. "$(dirname "$0")/stand-capture.sh"

if [ "$#" -ne 4 ]; then
	echo "usage: $0 <app> <port> <out-dir> <exec|open>" >&2
	exit 2
fi

# Absolute: open starts the window by its full path, and that is what the process
# is found by.
app=$(cd "$1" && pwd)
port=$2
out=$3
how=$4

mkdir -p "$out"
stand=$(mktemp -d)
mkdir -p "$stand/home/.config/fleetdeck"
printf 'server:\n  port: %s\n' "$port" >"$stand/home/.config/fleetdeck/config.yaml"
socket="$stand/no-daemon-here.sock"
url="http://127.0.0.1:$port/"
window_bin="$app/Contents/MacOS/fleetdeck-window"

case $how in
	exec)
		HOME="$stand/home" FLEETDECK_STAND_SOCKET="$socket" "$window_bin" -url "$url" >"$out/window.log" 2>&1 &
		window=$!
		;;
	open)
		# -n: a new instance even if LaunchServices knows another bundle under the
		# same identifier. The window logs to standard error, which open connects
		# to a file only when told to.
		if ! open -n -g --env "HOME=$stand/home" --env "FLEETDECK_STAND_SOCKET=$socket" \
			--stdout "$out/window.out" --stderr "$out/window.log" "$app" --args -url "$url" >"$out/open.log" 2>&1; then
			echo "open refused $app:"
			cat "$out/open.log"
			exit 1
		fi
		window=
		for _ in $(seq 20); do
			window=$(pgrep -f "^$window_bin" | head -n 1 || true)
			[ -n "$window" ] && break
			sleep 1
		done
		if [ -z "$window" ]; then
			echo "open returned, and no window process from $window_bin is running"
			cat "$out/open.log" "$out/window.log" 2>/dev/null || true
			exit 1
		fi
		;;
	*)
		echo "unknown way to start the window: $how" >&2
		exit 2
		;;
esac

loaded=no
for _ in $(seq 60); do
	kill -0 "$window" 2>/dev/null || break
	if grep -q "the panel's page says \"panel\"" "$out/window.log" 2>/dev/null; then
		loaded=yes
		break
	fi
	sleep 1
done
sleep 3
capture_screen "$out/window.png"
alive=no
if kill -0 "$window" 2>/dev/null; then
	alive=yes
fi
curl --silent --show-error --output /dev/null --write-out 'panel answered HTTP %{http_code}\n' "$url" || true
kill "$window" 2>/dev/null || true
# A process open started is not this shell's child, so wait cannot reap it.
for _ in $(seq 10); do
	kill -0 "$window" 2>/dev/null || break
	sleep 1
done

cp "$stand/home/Library/Logs/fleetdeck.log" "$out/panel.log" 2>/dev/null || echo "(no panel log)" >"$out/panel.log"
discovery=no
if grep -q 'daemon discovery disabled' "$out/panel.log"; then
	discovery=yes
fi

echo "--- window log ($how)"
cat "$out/window.log" 2>/dev/null || echo "(none)"
echo "--- panel log"
cat "$out/panel.log"
echo "--- $app ($how): page said panel: $loaded, window still running: $alive, panel looks for no daemon: $discovery"
[ "$loaded" = yes ] && [ "$alive" = yes ] && [ "$discovery" = yes ]
