# Getting started

This page walks through installing fleetdeck, opening the panel, creating a first board card, and connecting it to a running Claude Code session. The last section, about the `session` field, describes the design as it exists today and is the part worth reading carefully.

## Installing the binary

Build from source with `make build`, which puts two binaries in `bin/`:

- `fleetdeck` — the panel, and the `init` command described below;
- `fleetdeck-status` — the statusline reporter Claude Code runs for every session.

Keep the two together. `fleetdeck init` looks for `fleetdeck-status` in the directory it is run from and records that path in Claude Code's settings, so copying one binary somewhere without the other leaves `init` refusing to wire the statusline. `go install` works the same way as long as both packages are installed, since both land in the same `bin` directory:

```bash
go install github.com/kroticw/fleetdeck/cmd/fleetdeck@latest
go install github.com/kroticw/fleetdeck/cmd/fleetdeck-status@latest
```

A third way exists for the one case those two do not cover: something outside this repository that references a fixed path directly, rather than wherever a build happened to land — a launch agent's `ProgramArguments`, or Claude Code's `statusLine.command` composed with another statusline tool. `make install` builds both binaries fresh and writes them to `INSTALLDIR` (default `~/.local/bin`), replacing whatever already sits there under those two names and printing each binary's own sha256 so the replacement is verifiable rather than assumed.

## Running `fleetdeck init`

`fleetdeck init` sets up four things and prints one line per step, including the steps it refused and why:

- **The configuration file**, `~/.config/fleetdeck/config.yaml`, is written only when there is no file there. An existing configuration is read and left exactly as it is, comments included — it is hand-written YAML, and rewriting it from a struct would drop every comment and every ordering its author chose. The output says `(kept)` when that happens.
- **The board directory** comes from the configuration. On a machine with neither a configuration nor a `--board` flag the board is `~/fleetdeck/board`, and that path is what the newly created configuration records. A board directory that is absent or empty gets a `cards` subdirectory created under it with one example card written inside — the panel reads cards from `<board.path>/cards/`, not from the board directory itself, and an empty board is an error to the panel indistinguishable from a broken one. A directory that already holds files is left untouched, `cards` subdirectory included.
- **The statusline** is wired by setting `statusLine` in `~/.claude/settings.json` — that one key, with every other setting left alone. Two things stop this step. A `statusLine` that already runs something else is refused unless you pass `--force`, since replacing a statusline you configured yourself is not this command's decision. A `fleetdeck-status` that is not next to the `fleetdeck` binary is refused outright: writing a path that does not work would break the status line of every Claude Code session on the machine.
- **The launch agent** is written to `~/Library/LaunchAgents/dev.fleetdeck.panel.plist`, with its log in `~/Library/Logs/fleetdeck.log`. An agent file at that path that this command did not write is refused unless you pass `--force`.

A refused step does not stop the others, and the command exits non-zero when anything was skipped, naming what it found and what it would have written. Running `init` a second time changes no file: every step reports `(kept)`.

Two cases end in a refusal rather than an edit, both for the same reason — `init` does not rewrite a configuration file it did not create. A configuration that names no `board.path` is left for you to fill in; a `--board` pointing somewhere other than the configured board is refused rather than silently ignored.

## Loading the launch agent

`init` writes the agent and does not load it. Starting a program at every login is a change to your machine, and it is yours to make. The command is printed at the end of `init`'s output:

```bash
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/dev.fleetdeck.panel.plist
```

`gui/$(id -u)` is your own GUI domain — the agent runs as you, in your login session. `bootstrap` and `bootout` are the current subcommands; `launchctl load` and `unload` still work but are listed under legacy subcommands in `man launchctl`.

The agent runs the `fleetdeck` binary from wherever it was when `init` ran, starts it at login, restarts it if it exits, and sends both its output streams to `~/Library/Logs/fleetdeck.log`. To undo it:

```bash
launchctl bootout gui/$(id -u)/dev.fleetdeck.panel
```

After moving the binary, re-run `fleetdeck init` to record the new path, then bootout and bootstrap the agent again.

## Opening the panel

The panel listens on `http://127.0.0.1:7777` — the loopback interface only, on the port `server.port` sets (see [`configuration.md`](configuration.md)). Load the launch agent to have it running at login, or run `fleetdeck` in a terminal.

A browser pointed at that address gets the panel. The routes the page uses are callable directly too — `GET /api/snapshot` for the state of the whole fleet, `GET /ws` for a live stream of it, `GET /api/docs` and `GET /api/docs/content` for the directories `docs.paths` names, `POST /api/sessions/{id}/image` to attach an image, and the routes that type into a session and move a card. Only pages the panel served itself may call them: a request carrying any other `Origin` is refused with 403, and a request body that is not `application/json` with 415.

An image is attached by pasting it: copy it, put the caret in an input box — the orchestrator column's or the session panel's — and press Cmd+V. The panel uploads it and puts the file's path in the box, leaving whatever you had already typed where it was. Pasting ordinary text works as it always did.

An image attached to a session is written to `~/.claude/fleetdeck/images/<session>/`, under a name the panel chooses, and the panel then sends the session that file's path. The directory is deliberately outside the repositories you work in, so nothing is left behind in a working tree — the trade is that a session's first read from there asks your permission. Answer it once per session (the prompt offers to allow the whole directory) and it does not come back; the question shows up in the panel's "waiting for you" counter and is answerable from there.

The orchestrator column shows the whole conversation with the pinned session: its answers, and what you wrote to it. A message typed while the session was thinking takes its place in the thread at the moment you pressed Enter, not the moment the session got round to reading it. Your own lines are signed "operator" and marked with a bar down the side; a message that arrived from another session is signed with the sender's name and the time; everything else is the session speaking.

The column is resized by dragging: put the pointer on the strip to its right — it lights up — then press and pull. The column follows the pointer, and letting go remembers the width. It stops at a floor, so there is always an edge left to take hold of. The fold button above the title puts the column away entirely, leaving a narrow strip with a button that brings it back at the width it had. The width is kept as a share of the window, so the three zones hold their proportions when the window changes. Both the width and the folded state are kept in the browser and survive a reload; in a private window, where storage is unavailable, everything still works but the choice does not outlive the page.

What you send appears in the thread at once, before the session has read it, and in the same place the transcript's own copy will take — so nothing shifts when one replaces the other. If the send failed, the line goes off the screen, the text returns to the input box, and the reason appears in a line above it. The session panel does the same.

To check that it is up:

```bash
curl http://127.0.0.1:7777/api/snapshot
```

## Creating a first card

Creating a card does not require the panel: a card is a markdown file with YAML frontmatter, following the convention described in [`board-convention.md`](board-convention.md). Copy the template card, fill in the frontmatter, and describe the task in the body.

## Linking a card to a session

A card is connected to a running session through one frontmatter field: `session`. The orchestrator fills this field in when it starts a session for a card, and from that point the card's `session` value is the identifier the panel uses to find that session's live state on the daemon.

## Why `session` is the only connecting field

fleetdeck's design keeps three sources of truth, and it never merges them and never lets one stand in for another:

- The daemon's control socket knows a session's life: whether it is running, waiting, or gone, and what its screen currently shows.
- The session transcript knows what a session did: its steps, its questions, its answers so far.
- The board files, kept in git, know what a task means: its stage, its progress, its log, and how it links to other work.

Exactly one field ties these together: `session`, written on a board card. Nothing else crosses between the three sources. In particular, a session's state is never copied into the card. It lives in the daemon and is read from there, every time it is needed, rather than written into the card once and left to go stale.

This matters because the moment session state gets copied into a card as a second record, the fleet has two answers to the same question — one live, in the daemon, and one frozen, on the card — and those two answers start to disagree the instant the session's real state changes. The card would then need a mechanism to keep itself in sync with the daemon, which is exactly the kind of second source of truth this design avoids by keeping the connection to a single field. A card is not a place to store what a session is doing. It is a place to store what a session is for, and the one field that says which session that is.
