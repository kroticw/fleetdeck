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

`make window-app` builds the fleetdeck app, `bin/fleetdeck.app`: a native window around the panel, with the panel itself inside the bundle beside the window. The app starts the panel on its own — see [The app and the panel](#the-app-and-the-panel). Open it from Finder, or with `open bin/fleetdeck.app`.

A third way exists for the one case those two do not cover: something outside this repository that references a fixed path directly, rather than wherever a build happened to land — Claude Code's `statusLine.command` composed with another statusline tool, for one. `make install` builds both binaries fresh and writes them to `INSTALLDIR` (default `~/.local/bin`), replacing whatever already sits there under those two names and printing each binary's own sha256 so the replacement is verifiable rather than assumed.

## Running `fleetdeck init`

`fleetdeck init` sets up three things and prints one line per step, including the steps it refused and why:

- **The configuration file**, `~/.config/fleetdeck/config.yaml`, is written only when there is no file there. An existing configuration is read and left exactly as it is, comments included — it is hand-written YAML, and rewriting it from a struct would drop every comment and every ordering its author chose. The output says `(kept)` when that happens.
- **The board directory** comes from the configuration. On a machine with neither a configuration nor a `--board` flag the board is `~/fleetdeck/board`, and that path is what the newly created configuration records. A board directory that is absent or empty gets a `cards` subdirectory created under it with one example card written inside — the panel reads cards from `<board.path>/cards/`, not from the board directory itself, and an empty board is an error to the panel indistinguishable from a broken one. A directory that already holds files is left untouched, `cards` subdirectory included.
- **The statusline** is wired by setting `statusLine` in `~/.claude/settings.json` — that one key, with every other setting left alone. Two things stop this step. A `statusLine` that already runs something else is refused unless you pass `--force`, since replacing a statusline you configured yourself is not this command's decision. A `fleetdeck-status` that is not next to the `fleetdeck` binary is refused outright: writing a path that does not work would break the status line of every Claude Code session on the machine.

Earlier versions of `init` also wrote a launch agent that started the panel at login. The app starts the panel now, and an agent left in place would start a second one at every login. When `init` finds an agent an earlier `init` wrote, it says so and prints the two commands that remove it; it runs neither — see [The app and the panel](#the-app-and-the-panel).

A refused step does not stop the others, and the command exits non-zero when anything was skipped, naming what it found and what it would have written. Running `init` a second time changes no file: every step reports `(kept)`.

Two cases end in a refusal rather than an edit, both for the same reason — `init` does not rewrite a configuration file it did not create. A configuration that names no `board.path` is left for you to fill in; a `--board` pointing somewhere other than the configured board is refused rather than silently ignored.

## The app and the panel

The fleetdeck app starts the panel itself, and the panel lives exactly as long as the app. When the app opens, it looks at the panel's address. A panel that already answers there and was started from a terminal is shown as it is, and so is the panel of another fleetdeck window that is still open. A panel whose window is gone — one left behind by an older app that did not stop its panel, or by an app that ended some way its panel did not notice — is stopped, and the window says so while it starts its own. When nothing answers, the app starts the panel it carries, sends both of the panel's output streams to `~/Library/Logs/fleetdeck.log`, and opens it as soon as it answers.

While the app is open, a panel it started that stops is started again. One that stops within ten seconds of starting is not: a panel that dies at once dies at once again. The window then says the panel did not start, shows the last lines of its log, and has a button to start it again. The same page appears when the panel runs but does not answer at the address the app looks at within a third of a second — most often a `server.port` in the configuration other than 7777; the window's `--url` flag must then name the same port.

Closing the window with the red button only hides it: the app and its panel keep running, notifications keep coming, and clicking the app in the Dock brings the window back as it was. Quitting the app with Cmd+Q stops the panel with it, and so does the app ending any other way, a crash or `kill -9` included: the panel watches the app's process, notices at once that it has ended, and shuts down, which takes at most five seconds. Opening the app again starts a fresh panel. A panel started from a terminal does not belong to any app and runs until it is stopped. To stop it, stop the process that listens on its port:

```bash
kill $(lsof -t -iTCP:7777 -sTCP:LISTEN)
```

A panel that cannot take its port says what holds it: another fleetdeck panel, by its commit and the path of its binary; something that is not fleetdeck; or something that took the connection and did not answer within a second.

A launch agent written by an earlier `init` would start a second panel at every login, before the app does. Remove it with the two commands `init` prints:

```bash
launchctl bootout gui/$(id -u)/dev.fleetdeck.panel
rm ~/Library/LaunchAgents/dev.fleetdeck.panel.plist
```

`bootout` is the current spelling; `launchctl unload` still works but is listed under legacy subcommands in `man launchctl`.

## Updating the app

An app built with `make window-app` from a git checkout has an Update button in its header. It is there only in the app: a browser tab has nothing to run an update with.

Pressing it brings the checkout forward and hands over to the new build:

1. The checkout is fetched and fast-forwarded to its remote's `master`. A checkout on another branch, with uncommitted edits to tracked files, or with commits of its own is left exactly as it is, and the button says why.
2. The new app is built into `.fleetdeck-update/` beside the installed `fleetdeck.app` — never over it.
3. The new version's window opens, stops the running panel, starts its own from the new build, and checks that the panel answers with the build it expects. Only then does it put the new `fleetdeck.app` in place of the old one, in one step: there is no moment with nothing at that path. It starts its panel again from there, and the old window closes.

A step that takes longer than two seconds shows how long it has taken. A second press while an update runs does nothing, and a second window or a terminal cannot start another update of the same checkout alongside it. If you have typed a message and not sent it, the first press asks whether to update anyway: the page reloads into the new version at the end.

If the new version's panel does not start, or answers with another build, nothing is replaced: the installed app stays, its panel is started again, and the button says what went wrong. The version an update replaces stays in `.fleetdeck-update/` until the next update.

## Opening the panel

The panel listens on `http://127.0.0.1:7777` — the loopback interface only, on the port `server.port` sets (see [`configuration.md`](configuration.md)). Open the fleetdeck app to have it started for you, or run `fleetdeck` in a terminal.

A browser pointed at that address gets the panel. The routes the page uses are callable directly too — `GET /api/snapshot` for the state of the whole fleet, `GET /ws` for a live stream of it, `GET /api/sessions/{id}/pty` for a live two-way terminal on one session (a WebSocket whose geometry is given as `?cols=&rows=`), `GET /api/docs` and `GET /api/docs/content` for the directories `docs.paths` names, `POST /api/sessions/{id}/image` to attach an image, and the routes that type into a session and move a card. Only pages the panel served itself may call them: a request carrying any other `Origin` is refused with 403, and a request body that is not `application/json` with 415. The terminal socket carries no request body, so it has a check of its own in place of the second one: its first message must be the panel's terminal token, which `GET /api/terminal-token` hands to the panel's own page and, carrying no CORS headers, to no page on any other origin. The token is new with every start of the panel, and the page reads it again for every terminal it opens.

An image is attached by pasting it: copy it, put the caret in an input box — the orchestrator column's or the session panel's — and press Cmd+V. The panel uploads it and puts the file's path in the box, leaving whatever you had already typed where it was. Pasting ordinary text works as it always did.

An image attached to a session is written to `~/.claude/fleetdeck/images/<session>/`, under a name the panel chooses, and the panel then sends the session that file's path. The directory is deliberately outside the repositories you work in, so nothing is left behind in a working tree — the trade is that a session's first read from there asks your permission. Answer it once per session (the prompt offers to allow the whole directory) and it does not come back; the question shows up in the panel's "waiting for you" counter and is answerable from there.

The orchestrator column shows the whole conversation with the pinned session: its answers, and what you wrote to it. A message typed while the session was thinking takes its place in the thread at the moment you pressed Enter, not the moment the session got round to reading it. Your own lines are signed "operator" and marked with a bar down the side; a message that arrived from another session is signed with the sender's name and the time; everything else is the session speaking.

The column is resized by dragging: put the pointer on the strip to its right — it lights up — then press and pull. The column follows the pointer, and letting go remembers the width. It stops at a floor, so there is always an edge left to take hold of. The fold button above the title puts the column away entirely, leaving a narrow strip with a button that brings it back at the width it had. The width is kept as a share of the window, so the three zones hold their proportions when the window changes. Both the width and the folded state are kept in the browser and survive a reload; in a private window, where storage is unavailable, everything still works but the choice does not outlive the page.

The type in a live terminal — the orchestrator column's and the session panel's screen tab — is resized from the keyboard while the terminal has the focus: Cmd+= (or Cmd+Shift+=) makes it bigger, Cmd+- smaller, Cmd+0 brings back the 12 px it starts at. It goes from 9 to 24 px, and each of the two places remembers its own size across a reload. A bigger type in the same pane leaves fewer columns, and the columns are the session's own: every change reshapes the session for everyone attached to it, a `claude attach` in Terminal.app included, just as dragging the column's edge does. So the new size is shown over the terminal for a moment — the type's and the session's, as in "14 px · session 69 × 25" — and at either end of the range the same note says that it is the limit.

What you send appears in the thread at once, before the session has read it, and in the same place the transcript's own copy will take — so nothing shifts when one replaces the other. If the send failed, the line goes off the screen, the text returns to the input box, and the reason appears in a line above it. The session panel does the same.

Next to the name in the header is the commit the running panel was built from, with an asterisk when it was built from a tree with changes not yet committed. Hover over it for the full commit, when that commit was made, when the binary was built, and the path of the binary — which is how to tell which of several installs is the one answering on the port.

A page stays open while the panel under it is rebuilt and restarted, and it keeps running the code it arrived with. When the new panel serves a different interface, the page catches up. In the fleetdeck window it reloads by itself, and a session panel that was open opens again; the one thing that holds it back is text you have typed and not sent, and then a bar above the header says the page will reload once the text is sent, with a button to reload now. In a browser the bar appears instead, with a button to reload. A rebuild that leaves the interface as it was — the same commit again, or a change only in the server's own code — changes nothing on the page. If reloading brings back the same page, the window tries once more; after that, or straight away in a browser, the bar says the reload did not help and asks you to quit the window with Cmd+Q and open it again.

The window's View menu has Reload, on Cmd+R, for reloading the page by hand.

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
