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

.PHONY: build test lint run verify-ldflags dist verify-dist

# Build every command under ./cmd into $(BINDIR).
build:
	@for b in $(BIN_NAMES); do \
		go build -ldflags "$(LDFLAGS)" -o $(BINDIR)/$$b ./cmd/$$b || exit 1; \
	done

test:
	go test ./... -race -count=1

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
dist:
	@rm -rf "$(DISTDIR)"
	@mkdir -p "$(DISTDIR)"
	@for arch in $(DIST_ARCHES); do \
		stage="$(DISTDIR)/darwin-$$arch"; \
		mkdir -p "$$stage"; \
		for b in $(BIN_NAMES); do \
			GOOS=darwin GOARCH=$$arch go build -ldflags "$(LDFLAGS)" -o "$$stage/$$b" ./cmd/$$b || exit 1; \
		done; \
		COPYFILE_DISABLE=1 tar --create --gzip --file "$(DISTDIR)/fleetdeck-$(VERSION)-darwin-$$arch.tar.gz" \
			--directory "$$stage" $(BIN_NAMES) || exit 1; \
	done
	@$(MAKE) --no-print-directory verify-dist

# verify-dist proves the archives in $(DISTDIR) are the ones this VERSION built and
# that an unpacked binary knows its own version. `dist` runs it, so a local build
# catches a broken release before CI does; the release workflow also runs it as a step
# of its own, so the gate between building and publishing is visible in the workflow
# rather than implied by a target it happens to call.
verify-dist:
	@scripts/verify-dist.sh "$(DISTDIR)" "$(VERSION)" "$(DIST_ARCHES)" "$(BIN_NAMES)" "$(LDFLAGS)"

run: build
	@if [ ! -x $(BINDIR)/fleetdeck ]; then \
		echo "run: $(BINDIR)/fleetdeck was not built" >&2; \
		exit 1; \
	fi
	$(BINDIR)/fleetdeck
