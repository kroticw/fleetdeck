# Packages

What each package is for, and what it is deliberately not allowed to do. This is a map for whoever changes the code; the packages' own doc comments hold the detail.

Like the rest of `docs/engineering/`, this page is English only.

## The panel's own packages

- **`internal/board`** — reads and writes the fleet board: markdown cards with YAML frontmatter. The panel owns exactly two fields of a card, `stage` and `progress`, and can start a card from a title and a zone. Everything else belongs to the agents.
- **`internal/buildinfo`** — says which build of the panel is running: a hash of the embedded web interface, the binary's path, the commit it was built from, and when. The page compares that hash with the one its own document arrived with, which is how it learns the panel under it has been replaced.
- **`internal/config`** — loads and saves the YAML configuration described in [configuration.md](../en/configuration.md). A missing file is a set of defaults, not a failure. A malformed file is a failure.
- **`internal/daemon`** — a client for the daemon's Unix control socket: discovery, ownership and security checks on the socket and the control-key file, and the `ping`, `list`, `reply` and `attach` operations. Read [the protocol](../protocol/daemon-control-socket.md) before changing anything here.
- **`internal/fleet`** — resolves which fleet a page, a card or a session belongs to. A session's fleet is derived, never stored; see [multiple-fleets.md](multiple-fleets.md).
- **`internal/orchestrator`** — appoints the session a fleet is led from: it writes the orchestrator's working order where the session can read it, tells the session to read it, and pins the session to the panel's orchestrator column. A session is sent one line and a file, never the working order itself; see [orchestrator-wizard.md](orchestrator-wizard.md).
- **`internal/notify`** — shows macOS banners through `osascript`. The decision to notify belongs to the panel, not the browser: the panel knows the state, and a banner must not depend on whether a tab happens to be open.
- **`internal/server`** — exposes the snapshot over HTTP and WebSocket, and accepts the four writes the panel performs: text into a session, keys into a session, one field of one card, and a statusline reporter's report. It also starts a card, and for a panel with no configuration it serves the setup surface (`NewSetup`) instead of all of that. It performs no I/O of its own beyond the connection it is answering: every source it needs arrives as a function in a `Deps` struct, which `cmd/fleetdeck` fills in.
- **`internal/state`** — holds the snapshot type the panel renders from, and the rules for which state transitions are worth a banner. It performs no I/O either: every source reaches it as a plain value, so the rules are testable without a daemon, a board directory or a network.
- **`internal/transcript`** — reads session transcripts from the tail: locating a session's `.jsonl` file, a digest of its most recent steps, and an estimate of context-window occupancy for when the statusline reporter is not installed. It is the source for what a session *did*; it never reports what a session is doing now.
- **`internal/usage`** — reads account rate-limit windows from the Anthropic OAuth usage endpoint. See [limitations](../en/limitations.md) for where the token goes.
- **`internal/version`** — the build version, injected at link time by `make build` and `make verify-ldflags`. That package's own tests are how the injection is verified.
- **`internal/workspace`** — makes a workspace: `board`, a git repository laid out from the board template, and `docs`. A board appears whole or not at all, and a board directory that already holds files is never touched. See [workspace.md](workspace.md).
- **`plugin/templates`** — carries the board template (`plugin/templates/board`) into the binary, so a new board gets the same validator, README and archive without a checkout of this repository.

## `internal/supervisor`

This is the package that keeps the panel up to date and running without a terminal, and the one most worth reading before touching. Its parts:

- **Finding its tools.** It locates git, go and make by absolute path, and runs make in an environment that carries go to its recipes. An app started from the Dock has no PATH of its own.
- **Moving the source tree.** It fast-forwards the checkout, and refuses in words to touch a tree on another branch, with edits, or with commits of its own.
- **Starting the panel.** The panel becomes the window's own direct child, in a session of its own, so the window's signals do not reach it.
- **Taking a held port.** It stops a panel holding the port only when that panel answers as a fleetdeck panel. One update runs at a time.
- **The keeper**, which is what makes the app the panel's owner. It uses a panel already answering — one started from a terminal, or one belonging to another open window. It replaces a panel whose window is gone. It starts one when none answers, and starts it again when it dies, unless it died within ten seconds of starting. When it will not start, the keeper says why and shows the end of the panel's log.
- **Handing over on update.** An update builds the app to the side. The new version's window takes the panel over, stops whoever holds the port, and swaps its bundle into place in one system call once its panel answers with its own build.

No cgo, so it is tested on every CI leg. See [window-and-panel.md](window-and-panel.md) for the traps behind all of this.

## The binaries

- **`cmd/fleetdeck`** — the panel: it assembles every package above into one program, polls the daemon, watches the board, fires the banners the settings leave switched on, and serves the whole view on `127.0.0.1`.
- **`cmd/fleetdeck-window`** — the native window around the panel, and the app bundle's executable. See [window-and-panel.md](window-and-panel.md).
- **`cmd/fleetdeck-status`** — the statusline reporter. Claude Code runs it for every session: it prints the status line and, best effort, forwards the same data to the local panel over HTTP, which has no other way to get it.

## Dependencies

`internal/daemon` and `internal/transcript` are standard-library-only. `internal/config` and `internal/board` also depend on `gopkg.in/yaml.v3`, and `internal/board` additionally on `fsnotify`. `internal/server` is the one package that knows about a web server: it depends on `github.com/coder/websocket` on top of the standard library and `internal/board`, `internal/daemon` and `internal/state`.
