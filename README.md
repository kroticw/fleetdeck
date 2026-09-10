# fleetdeck

[Русская версия](README.ru.md)

fleetdeck is meant to be one local window for a fleet of Claude Code background sessions: their live state, a kanban board of the work they are doing, and the notes and docs around that work, all in one place instead of scattered across terminals and a separate notes app.

## Why fleetdeck exists

A fleet of background Claude Code sessions runs into the same three problems once it grows past two or three sessions:

- A session stops and waits for a person, and nobody notices in time. One recorded case: three sessions stood idle for two hours after the usage limit that had stopped them had already reset, because nothing surfaced the stop.
- Understanding what one session is doing means reading its terminal screen, where the text that matters is mixed in with spinners and redraw fragments left over from the terminal UI.
- The Obsidian board used to track the work is built for notes, not for a control panel. It has no idea which sessions are alive, waiting, or stuck.

fleetdeck's answer is a panel that watches the sessions and the board together, and a single field — `session` — that connects a board card to the session working on it, without ever copying session state into the card itself. See [`docs/en/getting-started.md`](docs/en/getting-started.md) for why that field is the only connection allowed.

## Status

This repository is under active development and does not yet do most of what is described below. As of this writing:

- There is no `fleetdeck` server binary. The only binary that exists is `fleetdeck-status`, a small statusline reporter (see "Packages" below).
- `internal/server` provides an HTTP and WebSocket surface for the panel, but wiring it to a real daemon, board, and config is still someone else's task, and there is no `fleetdeck` binary to start it — so nothing in this repository listens on a network port yet, and there is still no web UI.
- There is no `fleetdeck init` command, because there is no `fleetdeck` binary for such a command to belong to.
- There is no launchd agent and no `.plist` file anywhere in the repository.
- There is no release process and nothing published to install from.

What does exist and work today: the Go packages that will eventually sit behind that panel — reading and writing board cards, loading configuration, talking to the Claude Code daemon's control socket, reading session transcripts, reading account usage limits, and sending macOS notifications — plus the `fleetdeck-status` statusline reporter and the Claude Code plugin under `plugin/`. Each is described under "Packages" below.

There is no interface yet, so no screenshot appears in this document.

## Installation

> **Not implemented yet.** This section describes the intended behaviour, not what the current build does. Nothing named here exists in the repository at the time of writing.

The plan is to install a prebuilt binary from GitHub Releases, or build it with `go install`, then run `fleetdeck init` to write a configuration file, install a launchd agent so the panel starts on login, and wire the `fleetdeck-status` statusline reporter into Claude Code's settings. None of that exists yet: there is no release, no `fleetdeck` binary, and no `init` subcommand. For now, only `fleetdeck-status` can be built, from source, with `make build`.

## Limitations

- **macOS only.** Two pieces of this project shell out to macOS-specific tools with no fallback: reading the Anthropic OAuth token from the macOS Keychain (via the `security` command-line tool), and showing notification banners (via `osascript`). Neither has a cross-platform equivalent in this codebase today.
- **Local daemon only.** fleetdeck talks to the Claude Code daemon over its Unix domain control socket on the local machine. There is no remote or networked mode.
- **Reads files under the user's home directory.** Specifically: the macOS Keychain item that holds the Claude Code OAuth credentials, any markdown files under the configured board path together with the git repository that contains them, and session transcript files that Claude Code writes for each session.
- **A banner is not confirmed delivery.** `osascript`'s exit code is the only signal fleetdeck has, and a zero exit means the command ran, not that a banner appeared: macOS drops notifications silently when permission is denied or a Focus mode is on, and `osascript` still exits zero. Nothing here checks on-screen delivery, since that would mean reading an undocumented private database. A banner that never appears is therefore not an error fleetdeck can report — the panel's own counters, which do not depend on any of this, are what to trust for whether something is waiting.
- **Credentials touch exactly one endpoint.** fleetdeck reads the Anthropic OAuth token from the macOS Keychain via the `security` command-line tool, and sends it to exactly one host, `api.anthropic.com`. It never logs the token, never writes it to disk, and never passes it to a browser.

## Building and testing

Requires Go 1.27 (see `go.mod`) and, for linting, `golangci-lint` v2.13.2 on `PATH`.

```sh
make build   # go build ./... (or every binary under cmd/*, once one exists)
make test    # go test ./... -race -count=1
make lint    # go vet, gofmt -l, golangci-lint run
```

`make verify-ldflags` builds and runs the one test that checks the release build's version-injection symbol path (`internal/version`) actually still resolves; it is not part of `make test` because it requires its own `-ldflags`, and CI runs it as a separate step.

For configuration, see [`docs/en/configuration.md`](docs/en/configuration.md).

## Packages

- `internal/board` — reads and writes the fleet board: markdown cards with YAML frontmatter. The panel owns exactly two fields, `stage` and `progress`; everything else belongs to the agents. Depends on `github.com/fsnotify/fsnotify` for watching the board directory and `gopkg.in/yaml.v3` for the frontmatter, in addition to the standard library.
- `internal/config` — loads and saves the YAML configuration file described in [`docs/en/configuration.md`](docs/en/configuration.md). A missing file is a set of defaults, not a failure; a malformed file is a failure.
- `internal/daemon` — a client for the daemon's Unix control socket: discovery, ownership/security checks on the socket and the control key file, and the `ping`, `list`, `reply`, and `attach` (screen read / key send) operations. The full wire protocol it implements is documented in [`docs/protocol/daemon-control-socket.md`](docs/protocol/daemon-control-socket.md) — read that first before changing anything in this package.
- `internal/notify` — shows macOS banners through `osascript`. The decision to notify belongs to the panel, not the browser: the panel knows the state, and a banner must not depend on whether a browser tab happens to be open.
- `internal/server` — exposes the panel's snapshot over HTTP and WebSocket and accepts the four writes the panel performs: text into a session, keys into a session, one field of one card, and a statusline reporter's report. It performs no I/O of its own beyond the connection it is answering — every source it needs arrives as a function in a `Deps` struct — which is what makes wiring it to the real daemon, board, and config still someone else's job. Depends on `github.com/coder/websocket`, in addition to the standard library and `internal/board`, `internal/daemon`, and `internal/state`.
- `internal/state` — holds the snapshot type the panel would render from and the rules for deciding which state transitions are worth a banner. It performs no I/O of its own: every source reaches it as a plain value, so the rules are testable without a daemon, a board directory, or a network.
- `internal/transcript` — reads Claude Code session transcripts from the tail: locating a session's `.jsonl` file, a digest of its most recent conversational steps, and an estimate of context-window occupancy for when the statusline reporter is not installed. It is the source for what a session did; it never reports what a session is doing right now.
- `internal/usage` — reads account rate-limit windows from the Anthropic OAuth usage endpoint. The OAuth token is read from the macOS Keychain, sent only to that endpoint, never logged, and never stored.
- `internal/version` — the build version, injected at link time by `make build`/`make verify-ldflags`; see that package's own tests for how the injection is verified.
- `cmd/fleetdeck-status` — the statusline reporter. Claude Code runs it for every session: it prints the status line and, best effort, forwards the same data to the local panel over HTTP, which has no other way to get it.

`internal/daemon` and `internal/transcript` are standard-library-only. `internal/config` and `internal/board` also depend on `gopkg.in/yaml.v3`, and `internal/board` additionally depends on `fsnotify`. `internal/server` is the one package that knows about a web server: besides the standard library and `internal/board`, `internal/daemon`, and `internal/state`, it depends on `github.com/coder/websocket`.

## Documentation

- [`docs/en/getting-started.md`](docs/en/getting-started.md) — installing the panel and connecting a board card to a session.
- [`docs/en/board-convention.md`](docs/en/board-convention.md) — the card file format, its frontmatter fields, and who is allowed to write what.
- [`docs/en/configuration.md`](docs/en/configuration.md) — every configuration key, its default, and what happens when it is set wrong.
