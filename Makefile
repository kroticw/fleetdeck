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
# cross-arch release path without its own C cross-toolchain setup. `dist-app` below is
# that setup: it ships the window inside the release app, built for both architectures
# with clang told the target.
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

# SIGN_IDENTITY is the codesign identity `dist-app` seals the release app with: a
# "Developer ID Application: ..." name, or empty for an ad-hoc seal. Empty by default
# and set from the outside -- the release workflow's environment, or a command line --
# for three reasons. A machine that happens to hold a certificate must not quietly
# start signing every build with it. CI's check job runs `make test`, which runs the
# real `make dist-app`, on a runner that has no certificate and never will. And the
# identity names a person and a team, which is the kind of thing that belongs in a
# secret rather than in a file everyone can read.
#
# EXPECT_SEAL is what the gate then demands of the zip, and it follows from
# SIGN_IDENTITY rather than being a second switch someone can set to "adhoc" to make a
# red gate go green. `notarize-app` overrides it on its own command line, which is the
# one place a stronger demand is legitimate.
SIGN_IDENTITY ?=
ifeq ($(strip $(SIGN_IDENTITY)),)
EXPECT_SEAL := adhoc
else
EXPECT_SEAL := developer-id
endif

.PHONY: build test test-web lint run verify-ldflags dist verify-dist dist-app verify-dist-app notarize-app dist-dmg verify-dist-dmg notarize-dmg dmg-background dmg-layout window-app install icon

# Build every command under ./cmd into $(BINDIR) -- fleetdeck-window only on darwin,
# see BUILD_BIN_NAMES above.
build:
	@for b in $(BUILD_BIN_NAMES); do \
		go build -ldflags "$(LDFLAGS)" -o $(BINDIR)/$$b ./cmd/$$b || exit 1; \
	done

# test runs every Go test and, through go test, the board's Python tests as well
# (plugin/templates/board_scripts_test.go), so it needs python3 and fails in words
# without it: a skipped Python run would read exactly like a passing one.
test:
	go test ./... -race -count=1

# The frontend's own tests. Kept out of `make test` deliberately: node is not a
# build requirement of this project, so a contributor without node still gets a
# complete check of everything else. CI runs both.
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

# dist-app builds the release app: fleetdeck-$(VERSION)-macos.zip in $(DISTDIR), holding
# the fleetdeck.app `window-app` builds, made into one a person can download, drag to
# Applications and open -- every command in it, both architectures in every binary, the
# tag as its version, and a seal over the whole bundle -- Developer ID when
# SIGN_IDENTITY says so, ad hoc otherwise. What each of those is for, and what is left
# out on purpose, is in scripts/build-dist-app.sh.
#
# It takes every command there is (BIN_NAMES), not DIST_BIN_NAMES: the window is the
# app, and the exclusion that keeps it out of `dist` is about a plain GOARCH switch
# turning cgo off, which this target does not do -- it gives clang the target
# architecture instead.
#
# It runs after `dist` into the same directory and removes only zips of its own
# making, so `make dist dist-app` leaves the whole release there. macOS only: the
# window needs cgo against WebKit, and the bundle needs lipo, codesign and ditto.
dist-app:
	@scripts/build-dist-app.sh "$(DISTDIR)" "$(VERSION)" "$(DIST_ARCHES)" "$(BIN_NAMES)" "$(LDFLAGS)" "$(SIGN_IDENTITY)"
	@$(MAKE) --no-print-directory verify-dist-app

# verify-dist-app interrogates the zip dist-app wrote, the way it reaches a person:
# unpacked, then looked at from the outside. The release workflow runs it as a step of
# its own, for the same reason it runs verify-dist.
verify-dist-app:
	@scripts/verify-dist-app.sh "$(DISTDIR)" "$(VERSION)" "$(DIST_ARCHES)" "$(BIN_NAMES)" "$(LDFLAGS)" "$(EXPECT_SEAL)"

# notarize-app sends the signed zip to Apple, waits for the answer, staples the ticket
# to the bundle inside it and writes the zip again -- see scripts/notarize-dist-app.sh
# for why the order is exactly that. It is a target of its own, and not part of
# dist-app, because it goes over the network and takes minutes, while dist-app has to
# stay something `make test` can run offline on any machine.
#
# It then runs the gate again under its strongest demand: a stapled ticket and
# Gatekeeper actually accepting the app. That last check is the whole point of the
# task, so it runs before anything is published rather than after someone downloads it.
notarize-app:
	@scripts/notarize-dist-app.sh "$(DISTDIR)" "$(VERSION)"
	@$(MAKE) --no-print-directory verify-dist-app EXPECT_SEAL=notarized

# dist-dmg builds the release disk image: fleetdeck-$(VERSION)-macos.dmg in
# $(DISTDIR), holding the app, a shortcut to Applications and a laid-out window
# that shows a person what to do with them. It is what somebody downloads; the
# zip dist-app writes stays, because that is what an installed app fetches to
# update itself, by a name rather than by looking at what a release carries.
#
# It is built out of that zip and so runs after dist-app, not instead of it, and
# after notarize-app when there is one -- the app inside the image should be the
# stapled one. What each piece of the image is for is in
# scripts/build-dist-dmg.sh; the window's own two files are committed under
# packaging/dmg/ and regenerated by hand with the two targets below.
dist-dmg:
	@scripts/build-dist-dmg.sh "$(DISTDIR)" "$(VERSION)" "$(SIGN_IDENTITY)"
	@$(MAKE) --no-print-directory verify-dist-dmg

# verify-dist-dmg interrogates the image dist-dmg wrote, the way it reaches a
# person: mounted, then looked at from the outside. The release workflow runs it
# as a step of its own, for the same reason it runs verify-dist-app.
verify-dist-dmg:
	@scripts/verify-dist-dmg.sh "$(DISTDIR)" "$(VERSION)" "$(DIST_ARCHES)" "$(BIN_NAMES)" "$(LDFLAGS)" "$(EXPECT_SEAL)"

# notarize-dmg sends the signed image to Apple and staples the answer into it.
# The app inside already carries its own ticket; this one is for the image,
# because the image is what a person opens first and what Gatekeeper therefore
# asks about first. Then the gate again, under its strongest demand.
notarize-dmg:
	@scripts/notarize-dist-dmg.sh "$(DISTDIR)" "$(VERSION)"
	@$(MAKE) --no-print-directory verify-dist-dmg EXPECT_SEAL=notarized

# dmg-background and dmg-layout rebuild the two committed files the image's
# window is made of. Human tools, like `icon` below and for the same reason:
# dmg-background needs rsvg-convert and dmg-layout needs create-dmg and a Finder
# to drive, none of which a release runner has. Run them by hand after changing
# packaging/dmg/background-source.svg, look at the result, and commit it.
dmg-background:
	@scripts/build-dmg-background.sh

dmg-layout:
	@scripts/build-dmg-layout.sh

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
# DIST_BIN_NAMES above. A bundle built and run locally (this target does both) never
# picks up the com.apple.quarantine attribute Gatekeeper acts on, so it is not sealed:
# its signature is the linker's, over the window binary alone. A downloaded copy of
# it would be "damaged" to Gatekeeper; the bundle a release publishes is built by
# `dist-app` instead, which seals it.
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
	@mkdir -p "$(BINDIR)/fleetdeck.app/Contents/Resources"
	@cp cmd/fleetdeck-window/Info.plist "$(BINDIR)/fleetdeck.app/Contents/Info.plist"
	@cp cmd/fleetdeck-window/icon.icns "$(BINDIR)/fleetdeck.app/Contents/Resources/icon.icns"
	go build -ldflags "$(WINDOW_LDFLAGS)" -o "$(BINDIR)/fleetdeck.app/Contents/MacOS/fleetdeck-window" ./cmd/fleetdeck-window
	go build -ldflags "$(LDFLAGS)" -o "$(BINDIR)/fleetdeck.app/Contents/MacOS/fleetdeck" ./cmd/fleetdeck
	@echo "window-app: $(BINDIR)/fleetdeck.app (open it, or: open $(BINDIR)/fleetdeck.app)"

# icon rebuilds cmd/fleetdeck-window/icon.icns from icon-source.svg. A human tool,
# not part of window-app/dist/CI -- see scripts/build-icon.sh's own comment for why:
# it needs rsvg-convert, which this machine has and CI's runners do not.
icon:
	@scripts/build-icon.sh

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
