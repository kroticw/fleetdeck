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
#             content  frame, on a fleet with something in it: the stand builds
#                    scripts/standdaemon from this checkout and hands its socket to
#                    the window, so the board has a card in every stage, the
#                    sessions panel lists more long-named sessions than it shows,
#                    one waiting and one stopped, and the orchestrator's terminal
#                    has long lines and a status line. The panel's snapshot has to
#                    list the daemon's sessions and the stopped card before the
#                    screenshot is taken. The done column is taller than the
#                    window: the board's own box must not scroll down beside the
#                    sessions glass, and no bar down it or a column may be wider
#                    than 8 px. Whether its last column comes out from under the
#                    sessions panel is printed as the page measures it, not
#                    gated. The sessions surface's boxes must lie on one ground:
#                    the glass, or the opaque panel's square body. And the frame
#                    has to keep its properties as scripts/standcheck reads them
#                    off the window's own measurements: every capsule in the row
#                    and over neither panel, the board meeting both panels as
#                    they are laid out and its last column out from under the
#                    sessions panel, the selected tab a capsule, the window's
#                    buttons concentric in the orchestrator panel's corner with
#                    the header's row on their line.
#
# FLEETDECK_STAND_OPEN=session, for content: the board opens the first session
# the panel lists, as a sheet over the dimmed board
# (cmd/fleetdeck-window/standsettings.go, web/js/main.js). The band the window is
# dragged by has to survive it: the board says what stands in the way of its own
# top (web/js/standreport.js, topBandReport) and the height it reports must be
# more than nothing, and the window's own measurement of the band -- its height,
# and whether a press in the middle of it lands on it -- is held to the same by
# scripts/standcheck. v0.12.0 reported no band the moment a session opened and
# could not be moved at all, and no stand had ever opened one (T-079).
#
# FLEETDECK_STAND_FULLSCREEN=on, for content: the window goes into full screen
# once its surfaces have loaded, comes out of it after a while, and goes in and
# out a second time (cmd/fleetdeck-window/standfullscreen.go). The stand takes
# the frame each time in full screen (window-fullscreen-1.png,
# window-fullscreen-2.png), with the pointer at the top of the screen on a CI
# runner (window-fullscreen-revealed-1.png, -2.png) and after (window.png), and
# standcheck holds the frame to its properties before, in and after each time --
# in full screen at rest, that nothing of the title bar keeps the window's top or
# shows over the capsules; what the pointer brings out over them is said, not
# failed. v0.10.1's capsules lay over the sessions panel in full screen, which no
# stand had entered.
#
# FLEETDECK_STAND_FOLD=orchestrator, sessions or both, for content: the window
# opens with those panels folded, whatever the stand's defaults say, and keeps no
# widths (cmd/fleetdeck-window/standsettings.go); its log has to say it opened so.
# standcheck holds the folded frame to the same properties, among them that no
# capsule lies under the window's buttons, and each folded strip to nothing
# wider than it or past its edge and an unfold control a press reaches.
#
# FLEETDECK_STAND_APPEARANCE, when set, has to reach the window: its log has to say
# it is drawn in NSAppearanceNameDarkAqua for dark, NSAppearanceNameAqua for light.
#
# FLEETDECK_STAND_CAPSULES, when set with FLEETDECK_STAND_APPEARANCE, is the
# material the capsules have to be drawn in, glass or opaque: the last word the
# window's log says of them (cmd/fleetdeck-window/capsules_darwin.c) has to name
# it and the app's appearance. It checks that wiring, not that the capsules can
# be read; the screenshot is for that.
#
# FLEETDECK_STAND_SYSTEM, when set, is the system's mode the stand was set to,
# dark or light: the window's log has to say the system's appearance is that, as
# AppKit drew the app before its theme was given (window_darwin.go). `defaults`
# says what was written, and a screenshot of glass menus says nothing.
#
# The stand is the documented one: its own HOME with a configuration naming <port>,
# and FLEETDECK_STAND_SOCKET naming the stand's socket -- nothing listens on it but
# standdaemon, for content -- so the panel never looks for the fleet daemon. "panel" is what the window's page script reports for a
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
	echo "usage: $0 <app> <port> <out-dir> <exec|open> [page|frame|content]" >&2
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
	page | frame | content) ;;
	*)
		echo "unknown expectation: $expect" >&2
		exit 2
		;;
esac

mkdir -p "$out"
stand=$(mktemp -d)
mkdir -p "$stand/home/.config/fleetdeck"
socket="$stand/no-daemon-here.sock"
daemon=
case $expect in
	frame)
		# An empty board is enough: the page reports its layout whatever the board holds.
		mkdir -p "$stand/board/cards"
		printf 'server:\n  port: %s\nfleets:\n  - name: stand\n    board:\n      path: "%s"\n' "$port" "$stand/board" >"$stand/home/.config/fleetdeck/config.yaml"
		url="http://127.0.0.1:$port/?fleet=stand"
		;;
	content)
		# The orchestrator pinned is standdaemon's orchestratorShort, the session
		# whose attach shows a terminal.
		mkdir -p "$stand/board"
		printf 'server:\n  port: %s\nfleets:\n  - name: stand\n    board:\n      path: "%s"\n    orchestrator:\n      session: 0c7e1a2b\n' "$port" "$stand/board" >"$stand/home/.config/fleetdeck/config.yaml"
		url="http://127.0.0.1:$port/?fleet=stand"
		(cd "$(dirname "$0")/.." && go build -o "$stand/standdaemon" ./scripts/standdaemon)
		(cd "$(dirname "$0")/.." && go build -o "$stand/standcheck" ./scripts/standcheck)
		(cd "$(dirname "$0")/.." && go build -o "$stand/standpointer" ./scripts/standpointer)
		socket="$stand/daemon.sock"
		"$stand/standdaemon" -socket "$socket" -home "$stand/home" -board "$stand/board" >"$out/standdaemon.log" 2>&1 &
		daemon=$!
		for _ in $(seq 20); do
			[ -S "$socket" ] && break
			sleep 0.5
		done
		if [ ! -S "$socket" ]; then
			echo "standdaemon did not listen on $socket:"
			cat "$out/standdaemon.log"
			exit 1
		fi
		;;
	*)
		printf 'server:\n  port: %s\n' "$port" >"$stand/home/.config/fleetdeck/config.yaml"
		url="http://127.0.0.1:$port/"
		;;
esac
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
	if [ "$expect" != page ]; then
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
# For content, the panel has to have read the stand's daemon, board and job store
# before the screenshot: an empty panel in it would look like the defect it is
# there to show.
content_shown=yes
if [ "$expect" = content ]; then
	content_shown=no
	for _ in $(seq 30); do
		if curl --silent --fail --output "$out/snapshot.json" "http://127.0.0.1:$port/api/snapshot?fleet=stand" &&
			[ "$(plutil -extract sessions raw -o - "$out/snapshot.json" 2>/dev/null || echo 0)" -ge 8 ] &&
			[ "$(plutil -extract stoppedCards raw -o - "$out/snapshot.json" 2>/dev/null || echo 0)" -ge 1 ]; then
			content_shown=yes
			break
		fi
		sleep 1
	done
fi
# terminal_at_rest: the orchestrator surface's last report says neither bar down
# its terminal is in sight -- no native bar under xterm's viewport, and xterm's
# own bar faded out (web/js/standreport.js). It sets native and own.
terminal_at_rest() {
	terminal=$(grep 'the orchestrator surface reports its scrolling' "$out/window.log" | grep 'viewportScrollbarWidth' | tail -n 1)
	native=$(printf '%s\n' "$terminal" | sed -n 's/.*"viewportScrollbarWidth":\([0-9]*\).*/\1/p')
	own=$(printf '%s\n' "$terminal" | sed -n 's/.*"ownBarOpacity":\([0-9.]*\).*/\1/p')
	[ "$native" = 0 ] && [ "$own" = 0 ]
}
# For content the screenshot is of the terminal at rest: xterm's bar shows while
# the terminal writes its first screen and fades after.
if [ "$expect" = content ]; then
	for _ in $(seq 15); do
		terminal_at_rest && break
		sleep 1
	done
fi
# opens_a_session: the stand was told to take the frame with a session open.
opens_a_session() {
	case ",${FLEETDECK_STAND_OPEN:-}," in
		*,session,*) return 0 ;;
		*) return 1 ;;
	esac
}
# A session opens as a sheet over the dimmed board, and the sheet is what the
# band is measured against: the gates below run once the board has said it is
# open, not before.
sheet_open=yes
if [ "$expect" = content ] && opens_a_session; then
	sheet_open=no
	for _ in $(seq 20); do
		if grep -q 'the board reports its top band: .*"sheetOpen":\["session-panel"\]' "$out/window.log" 2>/dev/null; then
			sheet_open=yes
			break
		fi
		sleep 1
	done
fi
# measure_from <line> <true|false>: the number of the first line from <line> on
# where the window measures its frame in full screen (true) or out of it
# (false); nothing before there is one.
measure_from() {
	n=$(tail -n "+$1" "$out/window.log" | grep -n "fleetdeck-window: the frame measures .*\"fullScreen\":$2" | head -n 1 | cut -d: -f1)
	if [ -n "$n" ]; then
		echo $(($1 + n - 1))
	fi
}
# For a stand in full screen, its frame each time it is in full screen, then the
# frame after: the window goes in once its surfaces have loaded, comes out on
# its own, and goes in and out a second time.
fullscreen=no
trips=0
if [ "$expect" = content ] && [ "${FLEETDECK_STAND_FULLSCREEN:-}" = on ]; then
	from=1
	while [ "$trips" -lt 2 ]; do
		went_in=
		for _ in $(seq 45); do
			went_in=$(measure_from "$from" true)
			[ -n "$went_in" ] && break
			sleep 1
		done
		[ -n "$went_in" ] || break
		sleep 3
		capture_screen "$out/window-fullscreen-$((trips + 1)).png"
		# The pointer at the top of the screen, as a person's brings the title bar
		# out over the content, once the window has settled with the menu bar
		# hidden; the window measures its frame meanwhile (revealOnStand).
		# Only on a CI runner: the pointer on a person's machine is theirs, and
		# standpointer refuses off a runner too.
		rest=
		for _ in $(seq 10); do
			rest=$(measure_from "$went_in" 'true,"menuBarVisible":false')
			[ -n "$rest" ] && break
			sleep 1
		done
		if [ -n "$rest" ] && [ "${GITHUB_ACTIONS:-}" = true ]; then
			"$stand/standpointer" -to top || echo "--- full screen $((trips + 1)): the pointer did not reach the top of the screen"
			sleep 1
			capture_screen "$out/window-fullscreen-revealed-$((trips + 1)).png"
			sleep 2
			"$stand/standpointer" -to away || true
		fi
		echo "--- full screen $((trips + 1)): settled at log line ${rest:-never}"
		came_out=
		for _ in $(seq 45); do
			came_out=$(measure_from "$went_in" false)
			[ -n "$came_out" ] && break
			sleep 1
		done
		[ -n "$came_out" ] || break
		trips=$((trips + 1))
		from=$came_out
	done
	[ "$trips" -eq 2 ] && fullscreen=yes
	echo "--- full screen: in and out $trips times of 2"
fi
# The screenshot comes after every page said so: for frame, it is the frame's.
sleep 3
capture_screen "$out/window.png"
alive=no
if kill -0 "$window" 2>/dev/null; then
	alive=yes
fi
curl --silent --show-error --output /dev/null --write-out 'panel answered HTTP %{http_code}\n' "$url" || true
kill "$window" 2>/dev/null || true
[ -n "$daemon" ] && kill "$daemon" 2>/dev/null || true
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

# For content, the sessions list's scrollbar as the sessions surface measured
# it (web/js/standreport.js): the classic bar macOS draws for a mouse is 15 px,
# the islands ask for 6.
scrollbar=yes
if [ "$expect" = content ]; then
	width=$(sed -n 's/.*the sessions surface reports its scrolling: .*"scrollbarWidth":\([0-9]*\).*/\1/p' "$out/window.log" | tail -n 1)
	echo "--- the sessions list's scrollbar: ${width:-not reported} px"
	if [ -z "$width" ] || [ "$width" -gt 8 ]; then
		scrollbar=no
	fi
	# And down the orchestrator's terminal, at rest: no native bar under xterm's
	# viewport, and xterm's own bar out of sight.
	rest=yes
	terminal_at_rest || rest=no
	echo "--- the orchestrator's terminal at rest: native bar ${native:-not reported} px, xterm's own bar opacity ${own:-not reported}"
	if [ "$rest" = no ]; then
		scrollbar=no
	fi
fi

# For content with a session open, the band the window is dragged by, as the
# board measures its own top (web/js/standreport.js, topBandReport). The window's
# own word on the band -- its height, and whether a press in the middle of it
# lands on it -- is in the frame report, which scripts/standcheck holds to below;
# this says what took the band when there is none. v0.12.0 reported 0 here, and
# the window could not be moved at all while a session was open (T-079).
band=unset
if [ "$expect" = content ] && opens_a_session; then
	band=yes
	said=$(grep 'the board reports its top band' "$out/window.log" | tail -n 1)
	height=$(printf '%s\n' "$said" | sed -n 's/.*"height":\([0-9]*\).*/\1/p')
	echo "--- the band with a session open: ${height:-not reported} pt"
	echo "--- the board's own word on its top: ${said:-none}"
	if [ "$sheet_open" = no ]; then
		echo "--- no session was ever open: the band below proves nothing"
		band=no
	fi
	if [ -z "$height" ] || [ "$height" -le 0 ]; then
		band=no
	fi
fi

# For content, the frame's properties as scripts/standcheck reads them off the
# window's measurements of itself and the pages' of themselves.
frame=yes
if [ "$expect" = content ]; then
	trips=0
	if [ "${FLEETDECK_STAND_FULLSCREEN:-}" = on ]; then
		trips=2
	fi
	# What the stand opened (FLEETDECK_STAND_OPEN) is what standcheck expects to
	# find reported open: the new card form, the fleet menu's list.
	"$stand/standcheck" -log "$out/window.log" -fullscreen-trips "$trips" -open "${FLEETDECK_STAND_OPEN:-}" >"$out/standcheck.txt" 2>&1 || frame=no
	echo "--- the frame's properties (scripts/standcheck)"
	cat "$out/standcheck.txt"
fi

# The panels folded as the stand asked, as the window says it opened them.
folded=yes
if [ -n "${FLEETDECK_STAND_FOLD:-}" ]; then
	grep -q "fleetdeck-window: on this stand: .*, panels folded \"$FLEETDECK_STAND_FOLD\"" "$out/window.log" || folded=no
fi

# For content, the board down its height, as the board measured it
# (web/js/standreport.js): the stand's done column is taller than the window
# (scripts/standdaemon), and v0.10.1's board then scrolled down its whole
# height, with a classic 15 px bar at the sessions glass's edge that read as a
# second island behind it. In the window the board's own box does not scroll
# down, and no bar down it or a column is wider than the islands' thin one.
# Whether the last column comes out from under the sessions panel is printed
# as the page says it, and not gated: the page measures against the inset it
# was sent, not against the panel the window drew.
# whole says whether its argument is a whole number of no sign, the only form a
# width or a count the pages report may take to be compared.
whole() {
	case $1 in
		'' | *[!0-9]*) return 1 ;;
	esac
}

board_down=yes
if [ "$expect" = content ]; then
	board_said=$(sed -n 's/.*fleetdeck-window: the board reports its scrolling: \({.*}\)$/\1/p' "$out/window.log" | tail -n 1)
	board_field() { printf '%s\n' "$board_said" | sed -n "s/.*\"$1\":\"\{0,1\}\([^,\"}]*\).*/\1/p"; }
	taller=$(board_field contentTallerThanRoom)
	overflow_y=$(board_field overflowY)
	board_bar=$(board_field scrollbarWidth)
	column_bar=$(board_field columnScrollbarWidth)
	last_clear=$(board_field lastColumnClear)
	echo "--- the board down its height: taller than its room ${taller:-not reported}, overflow-y ${overflow_y:-not reported}, its bar ${board_bar:-not reported} px, a column's bar ${column_bar:-not reported} px, last column clear of the sessions panel as the page measures it (page-side, not gated) ${last_clear:-not reported}"
	[ "$taller" = true ] || board_down=no
	case $overflow_y in
		auto | scroll | "") board_down=no ;;
	esac
	# A width that is not a whole number fails the gate rather than the test
	# beside it: [ 14.5 -gt 8 ] is an error, and an error is not "wider".
	if ! whole "$board_bar" || [ "$board_bar" -gt 8 ] || ! whole "$column_bar" || [ "$column_bar" -gt 8 ]; then
		board_down=no
	fi
fi

# For content, a scrolled column across the panel's snapshots, as the board
# measured it (web/js/standreport.js, probeColumnScroll): the board scrolls its
# done column once and reports where it is after two more snapshots. The panel
# sends one a second, and the board draws its columns again for each; a column
# drawn back at its top could never be read to its end, and a screenshot of a
# still frame does not show it.
column_kept=yes
if [ "$expect" = content ]; then
	kept_said=$(sed -n 's/.*fleetdeck-window: the board reports its column scroll: \({.*}\)$/\1/p' "$out/window.log" | tail -n 1)
	kept_field() { printf '%s\n' "$kept_said" | sed -n "s/.*\"$1\":\([0-9]*\).*/\1/p"; }
	asked=$(kept_field asked)
	renders=$(kept_field renders)
	kept_top=$(kept_field scrollTop)
	echo "--- the done column scrolled to ${asked:-not reported} px: after ${renders:-no} snapshots drawn it is at ${kept_top:-not reported} px"
	if ! whole "$asked" || ! whole "$renders" || ! whole "$kept_top" || [ "$renders" -lt 2 ] || [ "$asked" -eq 0 ] || [ "$kept_top" != "$asked" ]; then
		column_kept=no
	fi
fi

# For content, the grounds the sessions list lies on, as the sessions surface
# computed them (web/js/standreport.js, groundsReport): one island. On glass no
# box paints a ground, an image, a shadow or a corner of its own; opaque, the
# body paints the panel and its edge, square, and nothing else paints.
grounds=yes
if [ "$expect" = content ]; then
	sed -n 's/.*fleetdeck-window: the sessions surface reports its grounds: \({.*}\)$/\1/p' "$out/window.log" | tail -n 1 >"$out/grounds.json"
	material=$(plutil -extract glass raw -o - "$out/grounds.json" 2>/dev/null || true)
	boxes=$(plutil -extract elements raw -o - "$out/grounds.json" 2>/dev/null || echo 0)
	second=
	i=0
	while [ "$i" -lt "$boxes" ]; do
		at() { plutil -extract "elements.$i.$1" raw -o - "$out/grounds.json" 2>/dev/null; }
		selector=$(at selector)
		ground=$(at background)
		image=$(at image)
		radius=$(at radius)
		shadow=$(at shadow)
		[ "$radius" = 0px ] || second="${second:+$second; }$selector rounds its corners $radius"
		if [ "$material" != opaque ] || [ "$selector" != body ]; then
			case $ground in
				"rgba(0, 0, 0, 0)" | transparent) ;;
				*) second="${second:+$second; }$selector paints $ground" ;;
			esac
			[ "$image" = none ] || second="${second:+$second; }$selector paints $image"
			[ "$shadow" = none ] || second="${second:+$second; }$selector casts $shadow"
		fi
		i=$((i + 1))
	done
	echo "--- the sessions island's grounds (${material:-not reported}, $boxes boxes): ${second:-one ground}"
	if [ "$boxes" -eq 0 ] || [ -z "$material" ] || [ -n "$second" ]; then
		grounds=no
	fi
fi

# The appearance the stand asked for, as AppKit reports the window drawn
# (window_darwin.go).
appearance=yes
case ${FLEETDECK_STAND_APPEARANCE:-} in
	dark) grep -q 'the window is drawn in NSAppearanceNameDarkAqua' "$out/window.log" || appearance=no ;;
	light) grep -q 'the window is drawn in NSAppearanceNameAqua ' "$out/window.log" || appearance=no ;;
esac

# The capsules' material and the appearance they are drawn in, as the window
# last said it: the app's, whatever the system's.
capsules=yes
capsules_said=
if [ -n "${FLEETDECK_STAND_CAPSULES:-}" ]; then
	case ${FLEETDECK_STAND_APPEARANCE:-} in
		dark) capsules_want="$FLEETDECK_STAND_CAPSULES, in NSAppearanceNameDarkAqua" ;;
		light) capsules_want="$FLEETDECK_STAND_CAPSULES, in NSAppearanceNameAqua" ;;
		*)
			echo "FLEETDECK_STAND_CAPSULES needs FLEETDECK_STAND_APPEARANCE dark or light" >&2
			exit 2
			;;
	esac
	capsules_said=$(sed -n 's/.*fleetdeck-window: the capsules are drawn in \(.*\)$/\1/p' "$out/window.log" | tail -n 1)
	[ "$capsules_said" = "$capsules_want" ] || capsules=no
fi

# The system's mode the window found, before it gave the app its theme.
system=yes
system_said=
if [ -n "${FLEETDECK_STAND_SYSTEM:-}" ]; then
	case $FLEETDECK_STAND_SYSTEM in
		dark) system_want=NSAppearanceNameDarkAqua ;;
		light) system_want=NSAppearanceNameAqua ;;
		*)
			echo "unknown system mode: $FLEETDECK_STAND_SYSTEM" >&2
			exit 2
			;;
	esac
	system_said=$(sed -n "s/.*fleetdeck-window: the system's appearance is \([A-Za-z]*\)$/\1/p" "$out/window.log" | head -n 1)
	[ "$system_said" = "$system_want" ] || system=no
fi

echo "--- $app ($how, $expect): every page said panel: $loaded${missing:+ (not yet: $missing)}, window still running: $alive, panel looks for no daemon: $discovery, content shown: $content_shown, scroll bars as the islands ask: $scrollbar, the board down its height as the islands ask: $board_down, a scrolled column kept across snapshots: $column_kept, the sessions island on one ground: $grounds, the band with a session open: $band, the frame's properties: $frame, full screen ${FLEETDECK_STAND_FULLSCREEN:-off}: $fullscreen, panels folded ${FLEETDECK_STAND_FOLD:-unset}: $folded, appearance ${FLEETDECK_STAND_APPEARANCE:-unset}: $appearance, capsules ${FLEETDECK_STAND_CAPSULES:-unset}: $capsules${capsules_said:+ ($capsules_said)}, system ${FLEETDECK_STAND_SYSTEM:-unset}: $system${system_said:+ ($system_said)}"
full_screen_ok=yes
if [ "$expect" = content ] && [ "${FLEETDECK_STAND_FULLSCREEN:-}" = on ] && [ "$fullscreen" = no ]; then
	full_screen_ok=no
fi
[ "$loaded" = yes ] && [ "$alive" = yes ] && [ "$discovery" = yes ] && [ "$content_shown" = yes ] && [ "$scrollbar" = yes ] && [ "$board_down" = yes ] && [ "$column_kept" = yes ] && [ "$grounds" = yes ] && [ "$band" != no ] && [ "$frame" = yes ] && [ "$full_screen_ok" = yes ] && [ "$folded" = yes ] && [ "$appearance" = yes ] && [ "$capsules" = yes ] && [ "$system" = yes ]
