VERSION  ?= dev
# Where `make build` puts the binaries. Overridable so the release-binary version test
# (cmd/fleetdeck/version_test.go) can build into a scratch directory instead of
# clobbering whatever the operator has in ./bin.
BINDIR   ?= bin
# Recursively expanded (=, not :=) so a target-specific VERSION override (see
# verify-ldflags below) is still picked up when LDFLAGS is actually used in a recipe,
# rather than being frozen to the default at parse time.
LDFLAGS  = -X github.com/kroticw/fleetdeck/internal/version.value=$(VERSION)

# Every command in this repository, derived from the tree rather than listed. `build`,
# `dist` and the archive verification all work from this one list, so a third command
# added under cmd/ is built, packaged and checked without editing anything here — and
# there is no second list to forget it in.
# $(sort) because make 3.81, which is what macOS ships, returns $(wildcard) in
# directory order rather than sorted: without it the archive members come out in a
# different order depending on which machine built the release.
BIN_NAMES := $(sort $(notdir $(patsubst %/,%,$(wildcard cmd/*/))))

# DIST_BIN_NAMES is what `dist` and its verification actually package -- BIN_NAMES minus
# fleetdeck-window. `dist`'s DIST_ARCHES loop below cross-builds both architectures from
# one host via a plain GOARCH switch, which Go answers by quietly turning cgo off for
# whichever arch is not the host's own, and fleetdeck-window has no non-cgo fallback at
# all, so it fails to compile rather than link -- loud, not silent, but still a `dist`
# that cannot finish. This is a deliberate, single exception, not the "second list" the
# paragraph above warns about: that warning is about a *CLI* command someone adds and
# forgets to wire in, and this is a GUI window that was never meant to ride the same
# cross-arch release path without its own C cross-toolchain setup -- that belongs with
# codesigning the release build, when that is taken up on purpose.
DIST_BIN_NAMES := $(filter-out fleetdeck-window,$(BIN_NAMES))

# HOST_GOOS drives the same exclusion for `build`, but conditionally rather than always:
# a plain `go build ./cmd/fleetdeck-window` (same arch, no cross toolchain) really does
# work on darwin, cgo included -- see cmd/fleetdeck-window's own tests. On any other host
# it is the *package itself* that has no darwin build to offer, not a cgo/arch mismatch:
# `go build ./cmd/fleetdeck-window` on linux fails outright with "build constraints
# exclude all Go files", the same failure `make build` hit inside CI's ubuntu-latest leg
# (via cmd/fleetdeck's own version_test.go, which shells out to `make build`) before this
# was made conditional -- confirmed from that job's own log, not assumed from reading the
# tag. `go env GOOS` reports the host unless a cross build overrides it, which nothing
# here does.
HOST_GOOS := $(shell go env GOOS)
ifeq ($(HOST_GOOS),darwin)
BUILD_BIN_NAMES := $(BIN_NAMES)
else
BUILD_BIN_NAMES := $(DIST_BIN_NAMES)
endif

ifeq ($(strip $(BIN_NAMES)),)
$(error no command directories found under cmd/: there is nothing to build)
endif

# Where `make dist` stages and writes the release archives. Overridable so the archive
# test (cmd/fleetdeck/dist_test.go) can build into a scratch directory rather than
# emptying and rewriting whatever the operator has in ./dist.
DISTDIR  ?= dist

# The architectures a macOS release ships. Linux and Windows are out of scope for this
# project (spec section 1), so this is the whole list, not a default subset.
DIST_ARCHES ?= arm64 amd64

.PHONY: build test test-web lint run verify-ldflags dist verify-dist window-app install

# Build every command under ./cmd into $(BINDIR) -- fleetdeck-window only on darwin,
# see BUILD_BIN_NAMES above.
build:
	@for b in $(BUILD_BIN_NAMES); do \
		go build -ldflags "$(LDFLAGS)" -o $(BINDIR)/$$b ./cmd/$$b || exit 1; \
	done

test:
	go test ./... -race -count=1

# The frontend's own tests. Kept out of `make test` deliberately: that target is
# Go-only and node is not a build requirement of this project, so a contributor
# without node still gets a complete Go check. CI runs both.
#
# No npm and no dependencies: web/package.json exists only to tell node that the
# files under web/ are ES modules, and web/tests/ sits outside web/embed.go's
# go:embed patterns so nothing here reaches the binary.
#
# The test files are named one by one rather than by handing node the directory:
# `node --test web/tests/` is read as a module specifier by some node versions and
# fails with MODULE_NOT_FOUND before a single test runs, which is what CI hit on
# node 24 while node 26 walked the directory happily.
#
# They are found with `find` rather than spelled out as a fixed list of globs.
# A fixed list named only web/tests/, and the 23 tests under web/js/_tests/ went
# unrun for as long as they existed while this target — and the CI job that calls
# it — reported success: the tests were written, reviewed and merged, and nothing
# ever executed them. There are two test directories because they are excluded
# from the binary in two different ways (web/tests/ sits outside the go:embed
# patterns; web/js/_tests/ sits inside js/ but is skipped because the "all:"
# prefix is deliberately absent and plain directory walking ignores a leading
# "_"), and a third one would be added the same way. `find` cannot miss it.
#
# `find` finding the right files does not by itself prove node ran every test
# in them: a file whose test() call sits behind a condition that never holds
# still exits 0, the same way the missing-file bug above did, because nothing
# is left standing to fail. scripts/run-web-tests.sh runs node and then checks
# its own output for exactly that — see the script's own comment for why a
# plain pass/fail count was tried first and was not enough.
test-web:
	@scripts/run-web-tests.sh

lint:
	@command -v golangci-lint >/dev/null 2>&1 || { \
		echo "lint: golangci-lint not found on PATH — install it: https://golangci-lint.run/welcome/install/" >&2; \
		exit 1; \
	}
	go vet ./...
	@test -z "$$(gofmt -l .)" || { gofmt -l .; exit 1; }
	golangci-lint run ./...

# verify-ldflags proves the Makefile's own -ldflags -X symbol path actually reaches
# internal/version's `value` variable — the linker accepts an unknown -X target
# silently, so this is the only thing that would ever catch a module rename, a package
# move, or a variable rename breaking version injection in every release binary. See
# internal/version/version_ldflags_test.go.
verify-ldflags: VERSION := 9.9.9
verify-ldflags:
	go test -tags ldflagscheck -ldflags "$(LDFLAGS)" -count=1 -run TestLdflagsSymbolPathActuallyLands -v ./internal/version/...

# dist builds the release archives: one gzipped tarball per architecture, each holding
# every command, each binary stamped with $(VERSION) through the same LDFLAGS the
# ordinary build uses.
#
# The output directory is emptied first. The publish step uploads dist/*.tar.gz by
# glob, and an archive from an earlier version — a re-tag, a retry, a developer who
# built something else yesterday — would otherwise be published under the current tag
# while being named after a different one. That is the failure in this target with the
# highest cost, because unlike a build error it is discovered by whoever downloads it.
#
# COPYFILE_DISABLE=1 stops the copyfile-based tar variants Apple has shipped from
# writing an AppleDouble "._name" member beside every file carrying an extended
# attribute. The bsdtar currently at /usr/bin/tar does not do this — an archive built
# without it was listed and held only the two binaries — but which tar comes first on
# PATH is not something a release should depend on, and the verification below asserts
# the member list either way.
#
# The members are named explicitly rather than tarring the staging directory, so the
# archive can only ever contain the binaries this run built.
#
# The recipe empties DISTDIR wholesale, so it refuses to run against a directory that
# is plainly not its own: DISTDIR is overridable, and `make dist DISTDIR=.` must not be
# a way to delete a working tree.
dist:
	@case "$(DISTDIR)" in \
		""|.|./|..|../|/|/*/..*) \
			echo "dist: refusing to empty '$(DISTDIR)': DISTDIR must be a directory this target owns" >&2; \
			exit 1;; \
	esac
	@rm -rf "$(DISTDIR)"
	@mkdir -p "$(DISTDIR)"
	@for arch in $(DIST_ARCHES); do \
		stage="$(DISTDIR)/darwin-$$arch"; \
		mkdir -p "$$stage"; \
		for b in $(DIST_BIN_NAMES); do \
			GOOS=darwin GOARCH=$$arch go build -ldflags "$(LDFLAGS)" -o "$$stage/$$b" ./cmd/$$b || exit 1; \
		done; \
		COPYFILE_DISABLE=1 tar --create --gzip --file "$(DISTDIR)/fleetdeck-$(VERSION)-darwin-$$arch.tar.gz" \
			--directory "$$stage" $(DIST_BIN_NAMES) || exit 1; \
	done
	@$(MAKE) --no-print-directory verify-dist

# verify-dist proves the archives in $(DISTDIR) are the ones this VERSION built and
# that an unpacked binary knows its own version. `dist` runs it, so a local build
# catches a broken release before CI does; the release workflow also runs it as a step
# of its own, so the gate between building and publishing is visible in the workflow
# rather than implied by a target it happens to call.
verify-dist:
	@scripts/verify-dist.sh "$(DISTDIR)" "$(VERSION)" "$(DIST_ARCHES)" "$(DIST_BIN_NAMES)" "$(LDFLAGS)"

run: build
	@if [ ! -x $(BINDIR)/fleetdeck ]; then \
		echo "run: $(BINDIR)/fleetdeck was not built" >&2; \
		exit 1; \
	fi
	$(BINDIR)/fleetdeck

# window-app stages the fleetdeck app: an .app bundle holding cmd/fleetdeck-window and
# the panel it starts, side by side in Contents/MacOS -- the window looks for the panel
# beside itself, and starts it when nothing answers at the panel's URL (see
# cmd/fleetdeck-window's package doc). It is deliberately not part of `dist` -- see
# DIST_BIN_NAMES above -- and codesigning it is a separate, not-yet-taken-up task; a
# bundle built and run locally (this target does both) never picks up the
# com.apple.quarantine attribute Gatekeeper acts on, so none is needed for that case.
# If this bundle is ever downloaded instead of built locally, it will.
#
# The window is told, at build time, the tree it was built from and the tools
# that built it: its update button brings that tree forward and runs this very
# target again, from an app started from the Dock, where there is no PATH to
# find them by (see internal/supervisor). Single quotes around each value, so
# a path with a space in it stays one value.
WINDOW_LDFLAGS = $(LDFLAGS) \
	-X 'main.treeDir=$(CURDIR)' \
	-X 'main.gitPath=$(shell command -v git)' \
	-X 'main.goPath=$(shell go env GOROOT)/bin/go' \
	-X 'main.makePath=$(shell command -v make)'
window-app:
	@rm -rf "$(BINDIR)/fleetdeck.app"
	@mkdir -p "$(BINDIR)/fleetdeck.app/Contents/MacOS"
	@cp cmd/fleetdeck-window/Info.plist "$(BINDIR)/fleetdeck.app/Contents/Info.plist"
	go build -ldflags "$(WINDOW_LDFLAGS)" -o "$(BINDIR)/fleetdeck.app/Contents/MacOS/fleetdeck-window" ./cmd/fleetdeck-window
	go build -ldflags "$(LDFLAGS)" -o "$(BINDIR)/fleetdeck.app/Contents/MacOS/fleetdeck" ./cmd/fleetdeck
	@echo "window-app: $(BINDIR)/fleetdeck.app (open it, or: open $(BINDIR)/fleetdeck.app)"

# INSTALLDIR is where `make install` puts fleetdeck and fleetdeck-status: a
# fixed, stable path something outside this repository points at directly --
# Claude Code's statusLine.command, a person's own PATH --
# and which a rebuild must land on again at the same path, or whatever
# pointed there keeps running the binary from before. Defaults to the
# conventional ~/.local/bin; overridable because nothing about this project
# requires that particular directory, and a fresh machine may not have it on
# PATH yet at all.
INSTALLDIR ?= $(HOME)/.local/bin

# INSTALL_BIN_NAMES is deliberately its own list, not BIN_NAMES or
# DIST_BIN_NAMES: those answer "what does this repository build", this
# answers "what needs a stable path outside it". fleetdeck-window is built
# by window-app instead, as an .app bundle launched by opening it, never by
# a fixed path something else stores and reuses -- it has no business here.
# A third command under cmd/ that does need a stable path does not get one
# by accident of being added to cmd/; it gets one by being added to this
# list on purpose.
INSTALL_BIN_NAMES := fleetdeck fleetdeck-status

# install builds fleetdeck and fleetdeck-status fresh from this tree and
# writes them to INSTALLDIR, overwriting whatever is already there under
# those names. It never runs itself -- nothing else in this Makefile or in
# CI calls it -- because INSTALLDIR is the operator's own path, outside this
# repository, and it is their call when whatever Claude Code's
# statusLine.command, or a terminal, is currently running from that path
# changes.
#
# Each binary's own sha256 is printed right after it is written, not merely
# "done": a copy that silently failed, or landed somewhere the operator did
# not expect, reads identically to a real one in a bare "installed" message,
# and this project has already spent a day on exactly that gap once today
# (see the local rate-limits file's own trace mechanism, added for the same
# reason on a different path).
#
# Each binary is built to a throwaway name first and only then moved onto
# the real one, never straight onto INSTALLDIR/$$b: go build refuses to
# overwrite a target that is not recognizably one of its own prior outputs
# ("already exists and is not an object file"), which is exactly the shape
# a first install, or a stale file left by something else entirely, can
# take -- and the whole point of this target is to replace what is there
# unconditionally, not to succeed only when it already guessed right about
# what that was.
install:
	@mkdir -p "$(INSTALLDIR)"
	@for b in $(INSTALL_BIN_NAMES); do \
		go build -ldflags "$(LDFLAGS)" -o "$(INSTALLDIR)/.$$b.new" ./cmd/$$b || exit 1; \
		mv -f "$(INSTALLDIR)/.$$b.new" "$(INSTALLDIR)/$$b"; \
		echo "install: $(INSTALLDIR)/$$b  sha256=$$(shasum -a 256 "$(INSTALLDIR)/$$b" | cut -d' ' -f1)"; \
	done
