# Getting started

This page walks through installing fleetdeck, opening the panel, creating a first board card, and connecting it to a running Claude Code session. The last section, about the `session` field, describes the design as it exists today and is the part worth reading carefully.

## Installing the app from a release

Every release on the [releases page](https://github.com/kroticw/fleetdeck/releases) carries `fleetdeck-<version>-macos.dmg`: a disk image holding the fleetdeck app, one download for Apple silicon and Intel Macs, with the release's version in Finder's Get Info. It needs Claude Code on the same Mac, since the app starts sessions with `claude`, and git and python3 for the board — see "What the machine needs" under [First launch](#first-launch-choosing-the-workspace).

From v0.3.0 the app is signed with an Apple Developer ID certificate and notarized by Apple, and the disk image carries a signature and a ticket of its own. macOS opens both with a double click and says nothing: there is no warning to dismiss and nothing to allow in System Settings.

There are two steps:

1. Download the `.dmg` and double-click it. A window opens with the app on the left and a shortcut to Applications on the right.
2. Drag the app onto that shortcut.

Then open fleetdeck from Applications, and the panel's setup page takes over — see [First launch](#first-launch-choosing-the-workspace).

The drag is not a formality, and the window is laid out to make it the obvious move. An app opened straight out of its download runs from a temporary copy macOS makes at a random path, and first-run setup would record that path in Claude Code's settings as the statusline command — a path that stops existing the moment the app quits.

The release also carries `fleetdeck-<version>-macos.zip`, holding the same app. That is what an installed fleetdeck downloads for itself when you press Update, and it is there for anyone who would rather unpack an archive — in which case dragging the app into Applications before opening it is yours to remember.

**Releases before v0.3.0 are not signed**, and macOS will not open one until you make an exception for it. If you have an older one, download the current release instead; that is quicker than the exception and leaves nothing behind in Privacy & Security. Should you want the older one anyway: open it from Applications, and when macOS says it could not verify the app is free of malware, press **Done** — not **Move to Trash**, which is the highlighted button. Then open System Settings, choose **Privacy & Security**, scroll to **Security**, and press the **Open Anyway** button on the line about fleetdeck. It appears only after the refusal and, according to [Apple](https://support.apple.com/guide/mac-help/mh40616/mac), stays for about an hour. The warning comes back with a button that opens the app; Apple's instructions say macOS then asks for your login password.

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

## First launch: choosing the workspace

The first time the panel starts on a machine with no configuration file, it shows a setup page instead of the board. The page asks for one folder, the workspace, and proposes `~/fleetdeck`. Inside the app a Choose… button opens the system's folder chooser, and the workspace is made in a `fleetdeck` folder inside the chosen one. Before anything is written, the page says what will be written outside that folder. Create makes two directories in the workspace:

- `board` — an empty board made from the board template this repository carries (`plugin/templates/board`): a `cards` directory with no cards in it, `README.md`, `archive/AGENTS-ARCHIVE.md`, and the card validator `scripts/validate_cards.py` with its tests. It is a git repository of its own, on `master`, with no commit yet: a signed commit would ask for a passphrase, and the panel has nobody to ask.
- `docs` — an empty directory, which the Docs tab reads.

Outside the workspace it then does what `fleetdeck init` does (below): it writes the configuration file naming both directories, wires Claude Code's statusline, and lets Claude Code sessions write in the workspace. Every step's outcome is shown, and a refused step is shown with its reason. As soon as the configuration and the board exist, the page becomes the panel, the app does not restart it, and the page goes on to the orchestrator (below) with those outcomes still on screen. When the folder cannot be made, the page stays, and another folder can be chosen.

A panel that has a configuration file never shows this page. A board that already exists elsewhere, with its own git history, is not moved and not changed: the configuration goes on naming it.

A new board has no cards, and that is not an error: what makes a directory a board is its `cards` subdirectory. A `board.path` whose directory has no `cards` subdirectory is still reported as the wrong directory.

**What the machine needs.** The board's history needs `git`, and the validator that agents run needs `python3`. On a Mac without the Command Line Tools neither exists: `/usr/bin/git` and `/usr/bin/python3` are stubs that offer to install the tools. The board is made anyway. Without git, the panel writes a card field and says it could not commit it. Installing the Command Line Tools (`xcode-select --install`) provides both.

## Appointing the orchestrator

The orchestrator is the Claude Code session the fleet is led from: it sets the other sessions their tasks, takes their work in and keeps the board. After the workspace, the same page asks which session that is — a new one, or one already running — or lets the choice be skipped.

Both are appointed the same way, so that two orchestrators appointed differently cannot come to behave differently:

1. The orchestrator's working order ([`orchestrator.md`](orchestrator.md), in the page's language) is written to `orchestrator.md` in the first documentation directory, or in the board when there is none, headed by where this panel keeps its board, documentation and configuration. The Docs tab shows it. It is rewritten each time the orchestrator is appointed. A file of that name that fleetdeck did not write is never replaced: the appointment stops and names it.
2. The session is sent one line telling it to read that file. The page shows the line and the file before anything is sent. It is a line and not the working order itself because a long, multi-line message can be left in a session's input box as an unsent paste instead of being submitted; and the file stays on disk for the orchestrator to read again once its context has been compacted.
3. The session is pinned to the orchestrator column (`orchestrator.session`), only after the line went in.

**A new session** is started with `claude --bg --name orchestrator` in the workspace, waited for until the daemon lists it (up to a minute), and sent the same line. Its first message is that line: it has no history of its own.

**An existing session** is chosen from the running sessions, each shown with its directory, its state, how full its context is, and what it is busy with — the question it is waiting on, or its detail. Choosing one says, before its button, what appointing it means: the line goes onto the end of a conversation that has its own history and its own task, as if it had been typed there; nothing is erased or restarted; the working order takes up part of its context, and from then on it carries its task and the fleet together; a session that is working reads the line after its current step, and one waiting on a question should be answered first; and none of this can be taken back. A session that will not take the line within a few seconds — most often one with a question on its screen — is not pinned, and the page says so.

The wizard can be run again from the orchestrator column's head (**wizard…**), which opens the same page at this step. There it marks the current orchestrator and says what happens to it when another is chosen: it leaves the column, and the line it was sent stays in its history. The column's dropdown, beside it, only moves the pin and sends nothing.

## Running `fleetdeck init`

`fleetdeck init` does from a terminal what the setup page does. It prints one line per step, including the steps it refused and why:

- **The configuration file**, `~/.config/fleetdeck/config.yaml`, is written only when there is no file there, and only after the board it names exists: a configuration naming a board that could not be made would point the panel at nothing. An existing configuration is read and left exactly as it is, comments included — it is hand-written YAML, and rewriting it from a struct would drop every comment and every ordering its author chose. The output says `(kept)` when that happens.
- **The workspace**, on a machine with no configuration, is `--workspace <path>` or `~/fleetdeck`: the board and the docs described above, both recorded in the new configuration (`board.path` and `docs.paths`). `--board <path>` makes a board alone instead, with no docs; the two flags cannot be combined. With an existing configuration, the board it names is made from the template only when its directory is absent or empty. A directory that already holds files is left untouched.
- **The statusline** is wired by setting `statusLine` in `~/.claude/settings.json` — that one key, with every other setting left alone. Two things stop this step. A `statusLine` that already runs something else is refused unless you pass `--force`, since replacing a statusline you configured yourself is not this command's decision. A `fleetdeck-status` that is not next to the `fleetdeck` binary is refused outright: writing a path that does not work would break the status line of every Claude Code session on the machine.
- **The permissions**: the workspace — or the board alone, when there is no workspace — is added to `permissions.additionalDirectories` in `~/.claude/settings.json`. An agent keeps its card on the board, outside its own working directory, and without this entry every card write is a permission prompt. A directory that is already listed, or that lies inside a listed one, is left as it is; an entry may start with `~/`.

Earlier versions of `init` also wrote a launch agent that started the panel at login. The app starts the panel now, and an agent left in place would start a second one at every login. When `init` finds an agent an earlier `init` wrote, it says so and prints the two commands that remove it; it runs neither — see [The app and the panel](#the-app-and-the-panel).

A refused step does not stop the others, and the command exits non-zero when anything was skipped, naming what it found and what it would have written. Running `init` a second time changes no file: every step reports `(kept)`.

Two cases end in a refusal rather than an edit, both for the same reason — `init` does not rewrite a configuration file it did not create. A configuration that names no `board.path` is left for you to fill in; a `--board` or `--workspace` that puts the board somewhere other than the configured board is refused rather than silently ignored.

## Adding a second fleet

A fleet is a board, its documentation and its orchestrator. The first one is the workspace the first launch or `fleetdeck init` makes. Another one is added from the start page — **Start a fleet**, a name and a folder — or from a terminal:

```bash
fleetdeck init --fleet clining --workspace ~/clining-fleet
```

Both do the same thing: they make the workspace — a board and docs, as above — add the fleet to `fleets` in the configuration only once its board exists, and let Claude Code sessions write in the new folder. `--board <path>` makes a board alone instead. A name or a board another fleet already has is refused before anything is made; running the same command again keeps what the first run added.

Either way the panel serves the new fleet only after it is restarted. The panel reads the configuration when it starts and not again, and the page says so before the button and after it.

Restart the panel, and the new fleet is on the start page and in the header's menu, with how many of its sessions wait for an answer. Appoint its orchestrator from its own tab: the wizard opened from a fleet's orchestrator column writes that fleet's working order and pins that fleet's orchestrator. See [Several fleets](configuration.md#several-fleets) for what the fleets share and how a session comes to be in one.

## The start page

The application opens on the start page: the fleets, what waits in each, and the way to a new one. Choosing one opens the panel on it; the fleet you worked in last time is marked.

The panel itself is one fleet's, and its address says which: `http://127.0.0.1:7777/?fleet=clining`. The bare address, `http://127.0.0.1:7777/`, is the start page. In the panel, the fleet's name in the header opens a menu that switches fleets, goes back to the start page, and starts a new one; switching reloads the page, which closes any terminal that tab had open and touches nothing in the fleet being left.

A machine with no configuration file at all has no panel yet, so it has no start page either: it opens the setup page instead — see [First launch](#first-launch-choosing-the-workspace).

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

The app has an Update button in its header. It is there only in the app: a browser tab has nothing to run an update with.

What the button does depends on how the app got onto your Mac. An app built from a git checkout brings that checkout forward; an app installed from a release downloads the next release. A build that can do neither still has the button, and says which of those it is instead of leaving you to work it out from a button that is not there.

The app also asks GitHub once a day, when it starts, whether there is a newer version, and says so in the header when there is. It asks nothing else and at no other time, and when it cannot reach GitHub it says nothing — press the button to ask out loud and see why.

### An app built from a checkout

Pressing the button brings the checkout forward and hands over to the new build:

1. The checkout is fetched and fast-forwarded to its remote's `master`. A checkout on another branch, with uncommitted edits to tracked files, or with commits of its own is left exactly as it is, and the button says why.
2. The new app is built into `.fleetdeck-update/` beside the installed `fleetdeck.app` — never over it.
3. The new version's window opens, stops the running panel, starts its own from the new build, and checks that the panel answers with the build it expects. Only then does it put the new `fleetdeck.app` in place of the old one, in one step: there is no moment with nothing at that path. It starts its panel again from there, and the old window closes.

A step that takes longer than two seconds shows how long it has taken. A second press while an update runs does nothing, and a second window or a terminal cannot start another update of the same checkout alongside it. If you have typed a message and not sent it, the first press asks whether to update anyway: the page reloads into the new version at the end.

If the new version's panel does not start, or answers with another build, nothing is replaced: the installed app stays, its panel is started again, and the button says what went wrong. The version an update replaces stays in `.fleetdeck-update/` until the next update.

### An app installed from a release

Pressing the button downloads the newest release and installs it over this one:

1. GitHub's releases page is asked which release is newest. If it is the one you are running, or an older one, nothing is downloaded and the button says so.
2. The archive is downloaded beside the installed `fleetdeck.app` — never over it.
3. **It is checked before anything moves.** The app that came down must be whole, signed with an Apple Developer ID, signed by the same Apple team as the app you are running, and notarized by Apple. An app that fails any of those is deleted and not installed, and the button says which check it failed.
4. From there it is the same handover as above: the new version's window opens, takes the panel over, proves its panel answers, and only then puts itself in place of the old app.

The check in step 3 is the point of doing this inside the app at all. Without it, a program that downloads an archive and puts it over the running app is a way to hand you anything at all.

There are two things the button cannot do, and it says so rather than trying:

- **An app you built yourself** with `make window-app`, without a checkout written into it, is signed by nobody. There is no release that corresponds to it, so there is nothing to update it to — build it again from your checkout.
- **A binary run outside an app bundle** — `go run`, or the executable on its own — has no app to replace.

## Opening the panel

The panel listens on `http://127.0.0.1:7777` — the loopback interface only, on the port `server.port` sets (see [`configuration.md`](configuration.md)). Open the fleetdeck app to have it started for you, or run `fleetdeck` in a terminal.

A browser pointed at that address gets the start page, and `?fleet=<name>` on it gets that fleet's panel. The routes the page uses are callable directly too — `GET /api/snapshot` for the state of the whole fleet, `GET /ws` for a live stream of it, `GET /api/sessions/{id}/pty` for a live two-way terminal on one session (a WebSocket whose geometry is given as `?cols=&rows=`), `GET /api/docs` and `GET /api/docs/content` for the directories `docs.paths` names, `POST /api/sessions/{id}/image` to attach an image, and the routes that type into a session and move a card. Only pages the panel served itself may call them: a request carrying any other `Origin` is refused with 403, and a request body that is not `application/json` with 415. The terminal socket carries no request body, so it has a check of its own in place of the second one: its first message must be the panel's terminal token, which `GET /api/terminal-token` hands to the panel's own page and, carrying no CORS headers, to no page on any other origin. The token is new with every start of the panel, and the page reads it again for every terminal it opens.

An image is attached by pasting it: copy it, put the caret in an input box — the orchestrator column's or the session panel's — and press Cmd+V. The panel uploads it and puts the file's path in the box, leaving whatever you had already typed where it was. Pasting ordinary text works as it always did.

An image attached to a session is written to `~/.claude/fleetdeck/images/<session>/`, under a name the panel chooses, and the panel then sends the session that file's path. The directory is deliberately outside the repositories you work in, so nothing is left behind in a working tree — the trade is that a session's first read from there asks your permission. Answer it once per session (the prompt offers to allow the whole directory) and it does not come back; the question shows up in the panel's "waiting for you" counter and is answerable from there.

The orchestrator column shows the whole conversation with the pinned session: its answers, and what you wrote to it. A message typed while the session was thinking takes its place in the thread at the moment you pressed Enter, not the moment the session got round to reading it. Your own lines are signed "operator" and marked with a bar down the side; a message that arrived from another session is signed with the sender's name and the time; everything else is the session speaking.

The column is resized by dragging: put the pointer on the strip to its right — it lights up — then press and pull. The column follows the pointer, and letting go remembers the width. It stops at a floor, so there is always an edge left to take hold of. The fold button above the title puts the column away entirely, leaving a narrow strip with a button that brings it back at the width it had. The width is kept as a share of the window, so the three zones hold their proportions when the window changes. Both the width and the folded state are kept in the browser and survive a reload; in a private window, where storage is unavailable, everything still works but the choice does not outlive the page.

The type in a live terminal — the orchestrator column's and the session panel's screen tab — is resized from the keyboard while the terminal has the focus: Cmd+= (or Cmd+Shift+=) makes it bigger, Cmd+- smaller, Cmd+0 brings back the 12 px it starts at. It goes from 9 to 24 px, and each of the two places remembers its own size across a reload. A bigger type in the same pane leaves fewer columns, and the columns are the session's own: every change reshapes the session for everyone attached to it, a `claude attach` in Terminal.app included, just as dragging the column's edge does. So the new size is shown over the terminal for a moment — the type's and the session's, as in "14 px · session 69 × 25" — and at either end of the range the same note says that it is the limit.

The same three steps are buttons too: A−, the current size, and A+. In the orchestrator column they sit in the strip at its top, beside the fold button; on the screen tab, in the panel's header beside the tabs. The size between them is the button that brings back 12 px. Each does exactly what its key does, and the button that cannot do anything at the current size is greyed out: A− at 9 px, A+ at 24 px, the size at 12 px. Pressing one leaves the keyboard focus in the terminal. When the session panel is too narrow to hold them beside the tabs and the close button, its header leaves them out; the keys still work there.

What you send appears in the thread at once, before the session has read it, and in the same place the transcript's own copy will take — so nothing shifts when one replaces the other. If the send failed, the line goes off the screen, the text returns to the input box, and the reason appears in a line above it. The session panel does the same.

Next to the name in the header is the version of the running panel. An app from a release shows its release, such as `v0.2.0`. A build from a checkout shows `dev` and the commit it was built from, so it never passes for a release. An asterisk means the build came from a tree with changes not yet committed. Hover over it for the version, the full commit, when that commit was made, when the binary was built, and the path of the binary — which is how to tell which of several installs is the one answering on the port.

A page stays open while the panel under it is rebuilt and restarted, and it keeps running the code it arrived with. When the new panel serves a different interface, the page catches up. In the fleetdeck window it reloads by itself, and a session panel that was open opens again; the one thing that holds it back is text you have typed and not sent, and then a bar above the header says the page will reload once the text is sent, with a button to reload now. In a browser the bar appears instead, with a button to reload. A rebuild that leaves the interface as it was — the same commit again, or a change only in the server's own code — changes nothing on the page. If reloading brings back the same page, the window tries once more; after that, or straight away in a browser, the bar says the reload did not help and asks you to quit the window with Cmd+Q and open it again.

The window's View menu has Reload, on Cmd+R, for reloading the page by hand.

To check that it is up:

```bash
curl http://127.0.0.1:7777/api/snapshot
```

## Creating a first card

The panel starts a card: the **+ card** button in the row of tabs above the board takes a title and a zone, and writes a card in stage `new` at progress `0`, named from the title — a Russian title gets a file name in latin letters — and records it in the board's git history. That is all it writes; the rest of the card is written by whoever takes the task on. An existing card is never overwritten: a second card with the same title on the same day gets a `-2` file. When the card reaches the board but its commit does not happen, the panel says so and does not offer to create it again.

A card does not need the panel either: it is a markdown file with YAML frontmatter, following the convention described in [`board-convention.md`](board-convention.md). Copy the template card, fill in the frontmatter, and describe the task in the body.

## Linking a card to a session

A card is connected to a running session through one frontmatter field: `session`. The orchestrator fills this field in when it starts a session for a card, and from that point the card's `session` value is the identifier the panel uses to find that session's live state on the daemon. With several fleets, the same field is what puts the session in the fleet whose board the card is on.

## Why `session` is the only connecting field

fleetdeck's design keeps three sources of truth, and it never merges them and never lets one stand in for another:

- The daemon's control socket knows a session's life: whether it is running, waiting, or gone, and what its screen currently shows.
- The session transcript knows what a session did: its steps, its questions, its answers so far.
- The board files, kept in git, know what a task means: its stage, its progress, its log, and how it links to other work.

Exactly one field ties these together: `session`, written on a board card. Nothing else crosses between the three sources. In particular, a session's state is never copied into the card. It lives in the daemon and is read from there, every time it is needed, rather than written into the card once and left to go stale.

This matters because the moment session state gets copied into a card as a second record, the fleet has two answers to the same question — one live, in the daemon, and one frozen, on the card — and those two answers start to disagree the instant the session's real state changes. The card would then need a mechanism to keep itself in sync with the daemon, which is exactly the kind of second source of truth this design avoids by keeping the connection to a single field. A card is not a place to store what a session is doing. It is a place to store what a session is for, and the one field that says which session that is.
