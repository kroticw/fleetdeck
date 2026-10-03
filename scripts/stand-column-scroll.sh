# shellcheck shell=sh
# stand-column-scroll.sh is sourced by scripts/ci-window-stand.sh for the
# scrolled column's gate.
#
# The board scrolls its done column once and reports where that column is after
# it has drawn two more of the panel's snapshots (web/js/standreport.js,
# probeColumnScroll). That is two to five seconds after the page loads, and the
# stand's other waits do not cover it: run 34952423754 closed the window 5.1 s
# after its page loaded, before the report, and the gate failed on a column
# nobody had measured (T-074). So the stand waits for the report itself, and
# says when it never came apart from a column that did not keep its place.

COLUMN_SCROLL_SAID='fleetdeck-window: the board reports its column scroll: '

# wait_column_scroll <log> <seconds> <pid>: until <log> has the board's column
# scroll report, the window <pid> is gone, or <seconds> pass. 0 once heard.
wait_column_scroll() {
	_deadline=$(($(date +%s) + $2))
	while :; do
		grep -q "$COLUMN_SCROLL_SAID" "$1" 2>/dev/null && return 0
		kill -0 "$3" 2>/dev/null || break
		[ "$(date +%s)" -lt "$_deadline" ] || break
		sleep 0.2
	done
	# A report written as the window went is heard all the same.
	grep -q "$COLUMN_SCROLL_SAID" "$1" 2>/dev/null
}

# column_scroll_verdict <log> <seconds>: says where the done column is after the
# snapshots drawn, as the board's last report in <log> has it, and returns 0 only
# for a column scrolled and kept where it was scrolled to. <seconds> is how long
# the stand waited, for the line that says no report came.
column_scroll_verdict() {
	_said=$(sed -n "s/.*$COLUMN_SCROLL_SAID\({.*}\)\$/\1/p" "$1" | tail -n 1)
	if [ -z "$_said" ]; then
		echo "--- the done column: the board never reported its scrolled column within $2 s, so whether it keeps its place was not measured"
		return 1
	fi
	_asked=$(printf '%s\n' "$_said" | sed -n 's/.*"asked":\([0-9]*\).*/\1/p')
	_renders=$(printf '%s\n' "$_said" | sed -n 's/.*"renders":\([0-9]*\).*/\1/p')
	_top=$(printf '%s\n' "$_said" | sed -n 's/.*"scrollTop":\([0-9]*\).*/\1/p')
	echo "--- the done column scrolled to ${_asked:-not reported} px: after ${_renders:-no} snapshots drawn it is at ${_top:-not reported} px"
	case "$_asked$_renders$_top" in
		'' | *[!0-9]*) return 1 ;;
	esac
	[ -n "$_asked" ] && [ -n "$_renders" ] && [ -n "$_top" ] || return 1
	[ "$_renders" -ge 2 ] && [ "$_asked" -gt 0 ] && [ "$_top" = "$_asked" ]
}
