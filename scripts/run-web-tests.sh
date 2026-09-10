#!/bin/sh
#
# run-web-tests.sh finds and runs every frontend test, then proves node
# actually executed every one it found -- not just the ones that happened to
# survive whatever ran before them.
#
# `make test-web` used to run node against one fixed directory. The 23 tests
# that lived anywhere else never ran, and the target -- and the CI job that
# called it -- reported success the whole time, because node --test cannot
# fail on a file it was never told about. Finding files with `find` (not a
# fixed path) closed that. This closes the complementary gap: a file `find`
# does list but whose test() call never actually registers at runtime (an
# exception mid-file that node's own exit code already catches, but also a
# test() call sitting behind a condition that never holds -- code that node
# never even reaches, and so never has a reason to fail on) still leaves the
# process exiting 0, because nothing is left standing to fail. Comparing test
# *names* declared in source against the names node's own output reports
# having run closes that regardless of which file or which mechanism causes
# it -- a plain count comparison was tried first and does not: a file that
# registers zero real tests still earns one synthetic "file passed" entry in
# node's summary, which can cancel out exactly one skipped test's absence
# and pass the count check while the test itself never ran.
#
# Usage: run-web-tests.sh [root]
# root defaults to "web"; a test harness can point it at a fixture tree.
set -eu

cd "$(dirname "$0")/.."

root=${1:-web}

files=$(find "$root" -name '*.test.js' | sort)
if [ -z "$files" ]; then
	echo "run-web-tests.sh: no *.test.js files found under $root -- this would pass vacuously" >&2
	exit 1
fi

declared=$(mktemp)
log=$(mktemp)
ran=$(mktemp)
trap 'rm -f "$declared" "$log" "$ran"' EXIT

# Declared test names, extracted the same way every file under web/**/*.test.js
# writes one: a call literally starting the line, with its name as a plain
# double-quoted string literal -- the only form this codebase's tests use
# (checked once when this script was written; a file using a different form
# for a name would simply not be counted here, the same failure shape this
# script exists to catch, so a future style change must update this pattern
# too). test.skip()/test.todo()/test.only() are matched as well, since node's
# own output names them the same way a plain test() would.
grep -hE '^test(\.(skip|todo|only))?\("' $files \
	| sed -E 's/^test(\.(skip|todo|only))?\("([^"]*)".*/\3/' \
	| sort >"$declared"

if ! node --test $files >"$log" 2>&1; then
	cat "$log"
	echo "run-web-tests.sh: node --test reported a failure" >&2
	exit 1
fi
cat "$log"

# Test names node's own default reporter actually printed a result line for
# -- pass or fail, it does not matter which; a failed run already exited
# above. ANSI colour codes (present when node thinks it is writing to a TTY)
# are stripped first. Lines naming a *.test.js file rather than a test are
# node's synthetic "this file produced no failures" entry for a file that
# registered zero real tests, and are excluded: keeping them would let a
# file whose every test() call sits behind a condition that never holds
# still report a passing name that never corresponds to anything declared.
sed 's/\x1b\[[0-9;]*m//g' "$log" \
	| grep -E '^[✔✖] ' \
	| sed -E 's/^[✔✖] (.*) \([0-9.]+m?s\)$/\1/' \
	| grep -v '\.test\.js$' \
	| sort >"$ran"

missing=$(comm -23 "$declared" "$ran")
if [ -n "$missing" ]; then
	echo "run-web-tests.sh: the following declared tests never appeared as run in node's own output:" >&2
	echo "$missing" | sed 's/^/  - /' >&2
	echo "run-web-tests.sh: a file likely failed to register them (a condition that never holds, a rename, a duplicate name) -- this must never pass quietly" >&2
	exit 1
fi

declared_count=$(wc -l <"$declared" | tr -d ' ')
echo "run-web-tests.sh: $declared_count declared tests, all present in node's own output"
