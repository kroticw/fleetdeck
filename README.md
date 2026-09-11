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

This repository is under active development. As of this writing:

- Two binaries build from source: `fleetdeck`, the panel, and `fleetdeck-status`, the statusline reporter (see "Packages" below).
- The panel runs and serves on `127.0.0.1:7777`: the web interface, the fleet snapshot behind it, a WebSocket stream of that snapshot, and the routes that type into a session and move a card.
- `fleetdeck init` exists. It writes the configuration file when there is none, creates the board directory with one example card when it is empty, and points Claude Code's `statusLine` at `fleetdeck-status`. A step that would overwrite something you configured yourself refuses and says how to proceed instead.
- The fleetdeck app builds with `make window-app` into `bin/fleetdeck.app`: a native window around the panel, carrying the panel inside the bundle. It starts the panel when none answers and starts it again when it stops; the panel it starts lives exactly as long as the app — hiding the window keeps it, quitting or a crash of the app stops it; the panel's log is `~/Library/Logs/fleetdeck.log`.
- Building from source works (`make build`), and so does a tagged release: pushing a `v*` tag runs `.github/workflows/release.yaml`, which tests, builds `darwin-arm64` and `darwin-amd64` archives with `make dist`, verifies them, and publishes them as a GitHub Release. See "Installation" below.

What exists and works today: the Go packages behind the panel — reading and writing board cards, loading configuration, talking to the Claude Code daemon's control socket, reading session transcripts, reading account usage limits, and sending macOS notifications — the `fleetdeck` panel binary that assembles them, the `fleetdeck-status` statusline reporter, and the Claude Code plugin under `plugin/`. Each is described under "Packages" below.

## Installation

Install by building from source, or from a GitHub Release: pushing a `v*` tag runs the `release` workflow (`.github/workflows/release.yaml`), which tests, builds `darwin-arm64` and `darwin-amd64` archives with `make dist`, verifies them, and publishes them.

`make build` produces both binaries in `bin/`: `fleetdeck`, the panel, and `fleetdeck-status`, the statusline reporter. Keep the two together — `init` looks for the reporter beside the panel binary and records that path in Claude Code's settings.

Then run `fleetdeck init`. It writes `~/.config/fleetdeck/config.yaml` if there is none, creates the board directory (`~/fleetdeck/board` by default, or `--board <path>`) with an example card if it is empty, and wires `fleetdeck-status` into `~/.claude/settings.json`. It prints one line per step, including the steps it refused and why: a statusline you configured yourself is left alone unless you pass `--force`, and one refused step does not stop the others. A launch agent an earlier `init` wrote is named, with the two commands that remove it. Then build the app with `make window-app` and open `bin/fleetdeck.app`: it starts the panel. See [`docs/en/getting-started.md`](docs/en/getting-started.md) for the whole flow.

## Limitations

- **macOS only.** Two pieces of this project shell out to macOS-specific tools with no fallback: reading the Anthropic OAuth token from the macOS Keychain (via the `security` command-line tool), and showing notification banners (via `osascript`). Neither has a cross-platform equivalent in this codebase today.
- **Local daemon only.** fleetdeck talks to the Claude Code daemon over its Unix domain control socket on the local machine. There is no remote or networked mode.
- **Reads files under the user's home directory.** Specifically: the macOS Keychain item that holds the Claude Code OAuth credentials, any markdown files under the configured board path together with the git repository that contains them, and session transcript files that Claude Code writes for each session.
- **A banner is not confirmed delivery.** `osascript`'s exit code is the only signal fleetdeck has, and a zero exit means the command ran, not that a banner appeared: macOS drops notifications silently when permission is denied or a Focus mode is on, and `osascript` still exits zero. Nothing here checks on-screen delivery, since that would mean reading an undocumented private database. A banner that never appears is therefore not an error fleetdeck can report — the panel's own counters, which do not depend on any of this, are what to trust for whether something is waiting.
- **The page allows inline styles, for one library.** The panel's Content-Security-Policy is `'self'` everywhere except `style-src`, which also carries `'unsafe-inline'`: the vendored xterm.js builds `<style>` elements at runtime and fills them with the terminal's measured cell size and theme colours, and that version has no nonce option. Without the keyword the terminal draws in a proportional font with no colour. `script-src` and `default-src` stay `'self'`, so what this allows is an injected appearance, not injected behaviour — see the comment on `contentSecurityPolicy` in `internal/server/static.go`.
- **Credentials touch exactly one endpoint.** fleetdeck reads the Anthropic OAuth token from the macOS Keychain via the `security` command-line tool, and sends it to exactly one host, `api.anthropic.com`. It never logs the token, never writes it to disk, and never passes it to a browser.

## Building and testing

Requires Go 1.27 (see `go.mod`) and, for linting, `golangci-lint` v2.13.2 on `PATH`.

```sh
make build    # every binary under cmd/* into bin/
make test     # go test ./... -race -count=1
make test-web # runs every *.test.js under web/ (the frontend; needs node, no npm)
make lint     # go vet, gofmt -l, golangci-lint run
```

`make test-web` runs the frontend's tests under node's own test runner. It is not part of `make test`, which stays Go-only so that a checkout without node still gets a complete Go check; CI runs both. There is no npm install and no dependency to fetch.

`make verify-ldflags` builds and runs the one test that checks the release build's version-injection symbol path (`internal/version`) actually still resolves; it is not part of `make test` because it requires its own `-ldflags`, and CI runs it as a separate step.

For configuration, see [`docs/en/configuration.md`](docs/en/configuration.md).

## Packages

- `internal/buildinfo` — says which build of the panel is running: a hash of the embedded web interface, the path of the binary, the commit it was built from and when the binary was built. The page compares the web hash with the one its own document arrived with, which is how it learns that the panel under it has been replaced by one serving a different interface.
- `internal/board` — reads and writes the fleet board: markdown cards with YAML frontmatter. The panel owns exactly two fields, `stage` and `progress`; everything else belongs to the agents. Depends on `github.com/fsnotify/fsnotify` for watching the board directory and `gopkg.in/yaml.v3` for the frontmatter, in addition to the standard library.
- `internal/config` — loads and saves the YAML configuration file described in [`docs/en/configuration.md`](docs/en/configuration.md). A missing file is a set of defaults, not a failure; a malformed file is a failure.
- `internal/daemon` — a client for the daemon's Unix control socket: discovery, ownership/security checks on the socket and the control key file, and the `ping`, `list`, `reply`, and `attach` (screen read / key send) operations. The full wire protocol it implements is documented in [`docs/protocol/daemon-control-socket.md`](docs/protocol/daemon-control-socket.md) — read that first before changing anything in this package.
- `internal/notify` — shows macOS banners through `osascript`. The decision to notify belongs to the panel, not the browser: the panel knows the state, and a banner must not depend on whether a browser tab happens to be open.
- `internal/server` — exposes the panel's snapshot over HTTP and WebSocket and accepts the four writes the panel performs: text into a session, keys into a session, one field of one card, and a statusline reporter's report. It performs no I/O of its own beyond the connection it is answering: every source it needs arrives as a function in a `Deps` struct, and `cmd/fleetdeck` is what fills that struct in. Depends on `github.com/coder/websocket`, in addition to the standard library and `internal/board`, `internal/daemon`, and `internal/state`.
- `internal/supervisor` — keeps the panel up to date and running without a terminal: finds git, go and make by absolute path and runs make in an environment that carries go to its recipes (an app started from the Dock gets no PATH of its own), fast-forwards the source tree while refusing in words to touch a tree on another branch, with edits, or with commits of its own, starts the panel as the window's own direct child in a session of its own, so the window's signals do not reach it, stops a panel that holds the port only when it answers as a fleetdeck panel, and lets one update run at a time. Its keeper is what makes the app the panel's owner: it uses a panel already answering at the panel's URL — one started from a terminal, or one of another window still open — replaces a panel whose window is gone, starts one when none answers, starts it again when it dies — unless it dies within ten seconds of starting — and reports why, with the end of the panel's log, when it will not start. No cgo, so it is tested on every CI leg.
- `internal/state` — holds the snapshot type the panel would render from and the rules for deciding which state transitions are worth a banner. It performs no I/O of its own: every source reaches it as a plain value, so the rules are testable without a daemon, a board directory, or a network.
- `internal/transcript` — reads Claude Code session transcripts from the tail: locating a session's `.jsonl` file, a digest of its most recent conversational steps, and an estimate of context-window occupancy for when the statusline reporter is not installed. It is the source for what a session did; it never reports what a session is doing right now.
- `internal/usage` — reads account rate-limit windows from the Anthropic OAuth usage endpoint. The OAuth token is read from the macOS Keychain, sent only to that endpoint, never logged, and never stored.
- `internal/version` — the build version, injected at link time by `make build`/`make verify-ldflags`; see that package's own tests for how the injection is verified.
- `cmd/fleetdeck` — the panel itself: it assembles every package above into one running program, polls the daemon, watches the board, fires the notification banners the settings leave switched on, and serves the whole view on `127.0.0.1`.
- `cmd/fleetdeck-status` — the statusline reporter. Claude Code runs it for every session: it prints the status line and, best effort, forwards the same data to the local panel over HTTP, which has no other way to get it.

`internal/daemon` and `internal/transcript` are standard-library-only. `internal/config` and `internal/board` also depend on `gopkg.in/yaml.v3`, and `internal/board` additionally depends on `fsnotify`. `internal/server` is the one package that knows about a web server: besides the standard library and `internal/board`, `internal/daemon`, and `internal/state`, it depends on `github.com/coder/websocket`.

## Documentation

- [`docs/en/getting-started.md`](docs/en/getting-started.md) — installing the panel and connecting a board card to a session.
- [`docs/en/board-convention.md`](docs/en/board-convention.md) — the card file format, its frontmatter fields, and who is allowed to write what.
- [`docs/en/configuration.md`](docs/en/configuration.md) — every configuration key, its default, and what happens when it is set wrong.
- [`docs/engineering/live-terminal.md`](docs/engineering/live-terminal.md) — for whoever changes the live terminal, the window, or builds a test stand: measured facts about the terminal bridge, the session's shared size, kicks, Claude Code and xterm.js inside a terminal, WKWebView, and the traps each of them sets. Read it before changing `web/js/liveterminal.js`, `internal/server/pty.go` or `cmd/fleetdeck-window`.
