# Getting started

This page walks through installing fleetdeck, opening the panel, creating a first board card, and connecting it to a running Claude Code session. Most of the steps below describe intended behaviour that is not built yet; each such section is marked. The last section, about the `session` field, describes the design as it exists today and is the part worth reading carefully even before there is anything to install.

## Installing the binary

> **Not implemented yet.** This section describes the intended behaviour, not what the current build does. Nothing named here exists in the repository at the time of writing.

The intended path is a prebuilt binary downloaded from GitHub Releases, or `go install` against this module once it exposes a `cmd/fleetdeck` package. Neither exists yet. There is no release, and there is no `fleetdeck` binary to install.

## Running `fleetdeck init`

> **Not implemented yet.** This section describes the intended behaviour, not what the current build does. Nothing named here exists in the repository at the time of writing.

The intended `fleetdeck init` command would write a configuration file, install a launchd agent so the panel starts automatically, and wire the `fleetdeck-status` statusline reporter into Claude Code's settings. No `init` subcommand exists, because no `fleetdeck` binary exists for it to belong to.

## Loading the launch agent

> **Not implemented yet.** This section describes the intended behaviour, not what the current build does. Nothing named here exists in the repository at the time of writing.

The intended flow is to load a launchd agent with `launchctl load` so the panel keeps running across logins. There is no `.plist` file anywhere in this repository yet, and no code that installs one.

## Opening the panel

> **Not implemented yet.** This section describes the intended behaviour, not what the current build does. Nothing named here exists in the repository at the time of writing.

The intended address for the panel is `http://127.0.0.1:7777`, matching the `server.port` default described in [`configuration.md`](configuration.md). `internal/server` provides the HTTP and WebSocket surface that would answer at that address, but wiring it to a real daemon, board, and config is still someone else's task, and there is no `fleetdeck` binary to start it — so nothing is listening on that address today, and there is no web UI to load if something were.

## Creating a first card

Creating a card does not require the panel: a card is a markdown file with YAML frontmatter, following the convention described in [`board-convention.md`](board-convention.md). Copy the template card, fill in the frontmatter, and describe the task in the body.

## Linking a card to a session

A card is connected to a running session through one frontmatter field: `session`. The orchestrator fills this field in when it starts a session for a card, and from that point the card's `session` value is the identifier the panel (once it exists) will use to find that session's live state on the daemon.

## Why `session` is the only connecting field

fleetdeck's design keeps three sources of truth, and it never merges them and never lets one stand in for another:

- The daemon's control socket knows a session's life: whether it is running, waiting, or gone, and what its screen currently shows.
- The session transcript knows what a session did: its steps, its questions, its answers so far.
- The board files, kept in git, know what a task means: its stage, its progress, its log, and how it links to other work.

Exactly one field ties these together: `session`, written on a board card. Nothing else crosses between the three sources. In particular, a session's state is never copied into the card. It lives in the daemon and is read from there, every time it is needed, rather than written into the card once and left to go stale.

This matters because the moment session state gets copied into a card as a second record, the fleet has two answers to the same question — one live, in the daemon, and one frozen, on the card — and those two answers start to disagree the instant the session's real state changes. The card would then need a mechanism to keep itself in sync with the daemon, which is exactly the kind of second source of truth this design avoids by keeping the connection to a single field. A card is not a place to store what a session is doing. It is a place to store what a session is for, and the one field that says which session that is.
