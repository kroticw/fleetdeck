#!/bin/sh
#
# ci-window-stand.sh starts the window of one app bundle on a stand and says how far
# it got: whether the window was loaded and started, whether the panel it started
# looked for no daemon, and whether the panel's page reported itself loaded. CI's
# window-on-oldest-macos runs it on a runner and nowhere else: it opens a window, and
# on a person's machine a window needs a moment they agree to
# (docs/engineering/window-and-panel.md, "Test stands on a machine with a live fleet").
#
# Usage: ci-window-stand.sh <app> <port> <out-dir> <how> [<expect>]
#
#   <how>     exec   the window binary started directly, the way a terminal starts it
#             open   the bundle opened through LaunchServices, the way Finder opens it
#
#   <expect>  page   (the default) the panel's page says "panel"
#             frame  the glass frame comes up too: the stand configures a fleet, the
#                    window opens that fleet's page, and the orchestrator's and the
#                    sessions' surfaces say "panel" as well (surfacePageSays in
#                    cmd/fleetdeck-window/glasswindow.go). Only a fleet's page reports
#                    the layout the frame follows; / is the start page, which does not.
#                    A window from before the frame never says it.
#
# The stand is the documented one: its own HOME with a configuration naming <port>,
# and FLEETDECK_STAND_SOCKET naming a socket nothing listens on, so the panel never
# looks for the fleet daemon. "panel" is what the window's page script reports for a
# page that loaded with its styles and its scripts (pagePanel in
# cmd/fleetdeck-window/owner.go); a window process that merely stays up is not that.
#
# Everything it saw goes to <out-dir>. It exits 0 only when every page it expects
# said "panel", the window was still running when asked, and the panel's log says it
# looks for no daemon.
#
# Its screenshot is the whole screen, so it is taken on GitHub Actions only
# (scripts/stand-capture.sh): on a person's Mac it would be everything they have open.
set -eu

# shellcheck source=scripts/stand-capture.sh
. "$(dirname "$0")/stand-capture.sh"

if [ "$#" -ne 4 ] && [ "$#" -ne 5 ]; then
	echo "usage: $0 <app> <port> <out-dir> <exec|open> [page|frame]" >&2
	exit 2
fi

# Absolute: open starts the window by its full path, and that is what the process
# is found by.
app=$(cd "$1" && pwd)
port=$2
out=$3
how=$4
expect=${5:-page}
case $expect in
	page | frame) ;;
	*)
		echo "unknown expectation: $expect" >&2
		exit 2
		;;
esac

mkdir -p "$out"
stand=$(mktemp -d)
mkdir -p "$stand/home/.config/fleetdeck"
if [ "$expect" = frame ]; then
	# An empty board is enough: the page reports its layout whatever the board holds.
	mkdir -p "$stand/board/cards"
	printf 'server:\n  port: %s\nfleets:\n  - name: stand\n    board:\n      path: "%s"\n' "$port" "$stand/board" >"$stand/home/.config/fleetdeck/config.yaml"
	url="http://127.0.0.1:$port/?fleet=stand"
else
	printf 'server:\n  port: %s\n' "$port" >"$stand/home/.config/fleetdeck/config.yaml"
	url="http://127.0.0.1:$port/"
fi
socket="$stand/no-daemon-here.sock"
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

# missing names the pages that have not said "panel" yet.
loaded=no
missing="every page (the window went before it was looked at)"
for _ in $(seq 60); do
	kill -0 "$window" 2>/dev/null || break
	missing=
	grep -q "the panel's page says \"panel\"" "$out/window.log" 2>/dev/null || missing="the board"
	if [ "$expect" = frame ]; then
		grep -q "the orchestrator surface's page says \"panel\"" "$out/window.log" 2>/dev/null ||
			missing="${missing:+$missing, }the orchestrator surface"
		grep -q "the sessions surface's page says \"panel\"" "$out/window.log" 2>/dev/null ||
			missing="${missing:+$missing, }the sessions surface"
	fi
	if [ -z "$missing" ]; then
		loaded=yes
		break
	fi
	sleep 1
done
# The screenshot comes after every page said so: for frame, it is the frame's.
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
# What the system did with the panel the window started, when its page never
# came: the process launch is what a panel that logs nothing in its start's time
# leaves to look at. GitHub Actions only: elsewhere it is a person's system log.
if [ "$loaded" = no ] && [ "${GITHUB_ACTIONS:-}" = true ]; then
	panel_pid=$(sed -n 's/.*panel starting (pid \([0-9]*\).*/\1/p' "$out/window.log" | head -n 1)
	started_at=$(sed -n 's/^\([0-9/]* [0-9:]*\)\.[0-9]* fleetdeck-window: panel starting (pid.*/\1/p' "$out/window.log" | head -n 1 | tr / -)
	if [ -n "$panel_pid" ] && [ -n "$started_at" ]; then
		log show --style compact --info --debug --start "$started_at" \
			--predicate "processID == $panel_pid OR ((process IN {\"runningboardd\", \"amfid\", \"syspolicyd\", \"tccd\", \"launchservicesd\", \"kernel\"}) AND (eventMessage CONTAINS \"$panel_pid\" OR eventMessage CONTAINS[c] \"fleetdeck\"))" \
			>"$out/system-around-panel.log" 2>&1 || true
		echo "--- system log around the panel (pid $panel_pid, from $started_at): $(wc -l <"$out/system-around-panel.log") lines in $out/system-around-panel.log"
	fi
fi

echo "--- $app ($how, $expect): every page said panel: $loaded${missing:+ (not yet: $missing)}, window still running: $alive, panel looks for no daemon: $discovery"
[ "$loaded" = yes ] && [ "$alive" = yes ] && [ "$discovery" = yes ]
