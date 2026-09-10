#!/bin/sh
#
# verify-dist.sh interrogates the archives `make dist` has just written, and fails if
# they are not archives a release may publish.
#
# It exists because a release archive is wrong in ways a successful build never shows.
# -ldflags that did not reach the compiler produce a binary reporting "dev" from an
# archive named after the tag; an archive left over from an earlier version stays in
# dist/ and is uploaded under the new tag by the publish step's *.tar.gz glob; a stray
# file swept into the tarball is noticed only by whoever unpacks it. Each of those is
# discovered by the person who downloads the release unless something checks first, and
# a published release cannot be taken back. So this runs before publishing.
#
# Usage: verify-dist.sh <dist-dir> <version> <arches> <binaries> <ldflags>
# The lists are single space-separated arguments; the Makefile derives both from the
# repository rather than naming them here as well.
set -eu

if [ "$#" -ne 5 ]; then
	echo "usage: $0 <dist-dir> <version> <arches> <binaries> <ldflags>" >&2
	exit 2
fi

dist_dir=$1
version=$2
arches=$3
binaries=$4
ldflags=$5

fail() {
	echo "verify-dist: $*" >&2
	exit 1
}

[ -d "$dist_dir" ] || fail "$dist_dir is not a directory"
[ -n "$version" ] || fail "version is empty"
[ -n "$arches" ] || fail "no architectures to verify"
[ -n "$binaries" ] || fail "no binaries to verify"
[ -n "$ldflags" ] || fail "ldflags is empty: nothing would stamp a version into the binaries"

work=$(mktemp -d)
# shellcheck disable=SC2064 # $work is expanded now on purpose: it must not depend on
# the variable still holding this value when the trap fires.
trap "rm -rf '$work'" EXIT INT TERM HUP

expected_archives=$(
	for arch in $arches; do
		echo "fleetdeck-$version-darwin-$arch.tar.gz"
	done | sort
)

# The publish step uploads dist/*.tar.gz by glob, so "the archives this run built" and
# "the archives in this directory" have to be the same set. A single leftover from
# yesterday's version is published under today's tag, named after yesterday's.
found_archives=$(cd "$dist_dir" && ls -1 2>/dev/null | grep '\.tar\.gz$' | sort || true)
if [ "$found_archives" != "$expected_archives" ]; then
	echo "verify-dist: $dist_dir does not hold exactly the archives this build produced" >&2
	echo "  expected: $(echo "$expected_archives" | tr '\n' ' ')" >&2
	echo "  found:    $(echo "$found_archives" | tr '\n' ' ')" >&2
	exit 1
fi

expected_members=$(printf '%s\n' $binaries | sort)
host_os=$(go env GOOS)
host_arch=$(go env GOARCH)
executed=0

for arch in $arches; do
	archive="$dist_dir/fleetdeck-$version-darwin-$arch.tar.gz"

	# An archive must contain what it claims and nothing else: both commands, no
	# AppleDouble "._" companions, no .DS_Store, no staging directory swept in.
	members=$(tar --list --file "$archive" | sort)
	if [ "$members" != "$expected_members" ]; then
		echo "verify-dist: $archive does not contain exactly the release binaries" >&2
		echo "  expected: $(echo "$expected_members" | tr '\n' ' ')" >&2
		echo "  found:    $(echo "$members" | tr '\n' ' ')" >&2
		exit 1
	fi

	mkdir -p "$work/$arch"
	tar --extract --file "$archive" --directory "$work/$arch"

	for b in $binaries; do
		bin="$work/$arch/$b"
		[ -x "$bin" ] || fail "$archive: $b is not executable once unpacked"

		# `go version -m` reads the build metadata out of the binary without running
		# it, which is the only way to interrogate the archive built for the
		# architecture this host cannot execute. It proves two things the file name
		# alone asserts: that the binary really targets the architecture its archive
		# is named after, and that the -ldflags carrying the version reached the
		# compiler for this build and not only for the native one.
		info=$(go version -m "$bin")
		echo "$info" | grep -qE "^[[:space:]]*build[[:space:]]+GOARCH=$arch\$" ||
			fail "$archive: $b is not built for darwin/$arch: $(echo "$info" | grep GOARCH || echo 'no GOARCH recorded')"
		echo "$info" | grep -qF -- "-ldflags=\"$ldflags\"" ||
			fail "$archive: $b was not built with the release ldflags, so it cannot know its own version
  wanted: -ldflags=\"$ldflags\"
  found:  $(echo "$info" | grep -- -ldflags || echo 'no -ldflags recorded')"
	done

	# Reading metadata is not the same as asking the program. The linker accepts an
	# -X target naming a symbol that does not exist without a word of complaint, so a
	# binary can carry exactly the right -ldflags and still print "dev".
	#
	# Only the archive built for this host's own architecture is run, and that
	# condition is the architecture, not a probe of whether the foreign binary happens
	# to start: running an x86_64 binary on Apple Silicon needs Rosetta 2, which is
	# not part of the documented GitHub macOS runner image, and a check that quietly
	# does less in CI than it does on a laptop is not a check. The foreign archive is
	# covered by the metadata assertions above, which come from the same build
	# invocation and the same -ldflags as the one executed here.
	#
	# Only fleetdeck is asked: it is the one command in this repository that answers
	# a version question. fleetdeck-status reads a JSON status line on stdin and has
	# no subcommands at all, so running it here would prove nothing about the version
	# and would hang waiting for input.
	if [ "$host_os" = "darwin" ] && [ "$arch" = "$host_arch" ]; then
		for form in version --version; do
			reported=$("$work/$arch/fleetdeck" "$form")
			if [ "$reported" != "$version" ]; then
				fail "$archive: fleetdeck $form reports '$reported', not '$version' — this release would lie about its own version"
			fi
		done
		executed=1
		echo "verify-dist: $archive ok (contents, build metadata, and 'fleetdeck version' == $version)"
	else
		echo "verify-dist: $archive ok (contents and build metadata; not executed, darwin/$arch is not this host)"
	fi
done

# A run on macOS that executed nothing means the architecture list no longer contains
# this host's, and the strongest check in this script silently stopped happening.
if [ "$host_os" = "darwin" ] && [ "$executed" -eq 0 ]; then
	fail "no archive was executed: darwin/$host_arch is not among the architectures built ($arches)"
fi
