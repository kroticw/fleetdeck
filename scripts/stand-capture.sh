# stand-capture.sh is sourced by scripts/ci-window-stand.sh for its screenshot.
#
# screencapture -x captures the whole screen. On a GitHub Actions runner the
# screen holds the stand's window and nothing else; on a person's Mac it holds
# everything they have open, which a stand has no business keeping. The window
# logs no window number to capture it alone by, so anywhere but GitHub Actions
# no screenshot is taken, and the stand says so.

# capture_screen <png>: the whole screen to <png> on GitHub Actions; anywhere
# else nothing, and a line saying why.
capture_screen() {
	if [ "${GITHUB_ACTIONS:-}" = true ]; then
		screencapture -x "$1" || echo "no screenshot, status $?"
	else
		echo "screenshot skipped: not on GitHub Actions, and here the capture would be this machine's whole screen"
	fi
}
