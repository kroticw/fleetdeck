# Several fleets: field notes

These are facts from building several fleets in one panel (#120, 2026-09-11, Claude Code 2.1.263, Chrome 153.0.8010.37): one panel serves several fleets, each with its own board, documentation and orchestrator, and each browser tab shows one of them. Each fact says how it was established: **measured** (observed on a live system or a stand), **read** (in code or a binary), or **decided** (a choice, with its reason). The code comments hold the details; this page holds what cost something to learn.

Read this before changing `internal/fleet`, the fleets part of `internal/config`, `internal/state/fleetview.go`, `internal/server/fleetdeps.go`, `cmd/fleetdeck/fleets.go` or `web/js/fleet.js`. Read section 1 before adding anything to the page that holds a connection open, whatever it is for.

The page is named after the code it is about, like [live-terminal.md](live-terminal.md), [window-and-panel.md](window-and-panel.md) and [orchestrator-wizard.md](orchestrator-wizard.md).

## 1. The back/forward cache keeps a left page alive, and its terminals with it

This is the one fact on this page that no reading of the code gives. Only a stand showed it.

**Decided:** switching fleets is a navigation. The tab loads the other fleet's address, and leaving a page was expected to close every connection the page held: the snapshot socket, the orchestrator column's live terminal, and an open session's screen. Nothing is sent to the left fleet's sessions and nothing stops them. The tab only stops looking.

**Why a connection left open is not cosmetic.** A live terminal is an attach to the daemon, and an attach sets the session's terminal size for everyone attached to it ([live-terminal.md](live-terminal.md#2-the-sessions-size-belongs-to-everyone)). An attach nobody sees still imposes its size on a session of the fleet just left, on the screen of whoever is looking at that session.

**Measured: the browser kept the left page alive.** On the stand, a tab on fleet A had two attaches open: A's orchestrator column and one of A's sessions on screen. It then switched to fleet B by a real mouse click. On the first build:

- the daemon still held both of A's attaches more than 25 seconds after the click;
- the panel still held three unix connections to the daemon;
- going back to A came in as navigation type `BackForwardCacheRestore`.

Chrome had put fleet A's page into its back/forward cache with its WebSockets open. A page in that cache is frozen, not unloaded: no script runs, and nothing the page would do on unload ever runs, so no connection is closed. The navigation happened exactly as designed. The browser simply kept the page it was leaving.

**Why no test saw it.** The unit tests run in node against a fake page. Node has no navigation and no back/forward cache, so a test that asserts "switching closes the terminals" passes by construction. The effect exists only in a real browser's page lifecycle, and it is visible only where the connection ends — at the daemon, as a count of open attaches. The page reports nothing, because a frozen page reports nothing.

**Fix, read in the code:**

- the snapshot socket (`web/js/store.js`, `releaseWhenHidden`) and every live terminal (`web/js/liveterminal.js`) close on `pagehide`, and they close without reconnecting;
- a page shown again from the cache (`pageshow` with `persisted`) reloads, so it never runs on with sockets it no longer holds.

`pagehide` fires both when a page is unloaded and when it goes into the cache, so the same code covers both.

**Measured after the fix,** on the same stand and the same click: A's attaches went from 2 to 0, 0 `reply` requests and 0 bytes reached A's sessions, and fleet B's orchestrator was on screen 74 ms after the click.

**Control:** the stand drive run on the build without the fix fails at "A's terminals released", with the daemon still holding `a0000001` and `a0000002`. So a green run on the fixed build shows the fix, not a drive that cannot see the problem.

**For whoever adds a connection to the page:** close it on `pagehide`, and check it on a stand that navigates away and counts at the far end — the daemon, or the panel's own connections — never in the page itself.

**Not verified:** the app window. It is a WebKit WebView, a different engine from Chrome, and whether and when it keeps a left page in a back/forward cache was not measured. `pagehide` closes the terminals there too, but a page coming back from its cache was seen only in Chrome.

## 2. Which session is in which fleet is derived, never stored

**Decided:** a fleet claims its orchestrator and the sessions its cards name in their `session` field. Nothing else puts a session in a fleet (`fleet.Claims`).

**Why not a stored list.** The daemon is one per user, and its sessions belong to nobody in particular. A list of each fleet's sessions would be a fourth source of truth next to the daemon, the transcripts and the board, and nothing would keep it current. An orchestrator starts sessions through MCP, past the panel. The first session it started would be missing from the list, so the list would be stale within the first minute of use. The card's `session` field already says which work a session is doing, and the orchestrator already fills it in.

**What follows:**

- a session no fleet claims is shown in every fleet, never in none;
- a session named by cards on two fleets' boards is in both;
- a fleet's own orchestrator is its own even before any card names it.

## 3. One port for every fleet

**Read:** every session's statusline runs `fleetdeck-status`, which reports to `http://127.0.0.1:7777/api/status` (`cmd/fleetdeck-status/main.go`). The address can be changed only through the `FLEETDECK_ENDPOINT` environment variable, and that variable is set for the Claude Code process, not for a session. A session does not know which fleet it is in, because that is derived by the panel (section 2). So no session can choose its own fleet's panel. A panel on any other port would never receive a single statusline report, and would show context usage estimated from transcripts instead.

**Decided: one panel serves every fleet,** and the fleet a tab shows is `?fleet=<name>` in its address. The server reads that one parameter for the snapshot, the WebSocket, card writes, documentation, the orchestrator pin and the wizard. Two tabs on two fleets work side by side, and there is no server-side "current fleet" that one tab could change under another. No parameter means the first fleet, so every address opened before there were several fleets shows what it always showed.

**Rejected: a fan-out.** The statusline could post to every fleet's panel. But each report is a POST with a 500 ms timeout (`cmd/fleetdeck-status/main.go`), made on every statusline refresh of every session. With N panels, every refresh becomes N requests. A panel that listens but does not answer — busy, or stopped in a debugger — costs up to 500 ms of that statusline's time on each refresh. The statusline reads no configuration either, so it would have to learn the list of ports somehow. One panel on one port needs none of this.

## 4. Editing the operator's configuration without rewriting it

The operator's `config.yaml` holds session labels and comments written by hand. Saving the configuration once re-marshalled the whole file and erased them.

**Decided:** the existing top-level keys stay the first fleet, unchanged. Further fleets go in a new `fleets:` list, and an optional `name:` names the first one. Two writers change the file:

- `config.AddFleet` appends an entry to the list, or opens the list at the end of the file;
- `config.SetFleetOrchestrator` rewrites one fleet's `orchestrator.session` line in place, keeping its comment, or adds the key to an entry that has none.

Both read the file as lines and touch only the lines of the one entry they were asked about. Neither ever falls back to `Save`. They follow the file's own indentation of list entries. A list written inline (`fleets: [{...}]`) is refused rather than rewritten.

**Decided: nothing is written that would not load.** `writeCheckedConfig` decodes the new bytes with `KnownFields(true)` and validates them before replacing the file atomically. A refused write leaves the file exactly as it was.

**How it is proven: by bytes.**

- `internal/config/save_shape_test.go` pins, byte for byte, what `Save` writes for a single-fleet configuration. Its golden text was captured from `Save` before the new keys existed, so adding them changed nothing in the file of an operator who does not use them.
- The writer tests in `internal/config/fleets_write_test.go` compare whole files before and after, including every refusal, which must leave the file byte-identical.

A test that parses the file and compares values would pass while every comment was gone. Only a byte comparison proves they survive.

**Read, still open:** the first fleet's orchestrator is pinned through `config.SetField`. That function edits an existing line and never adds a section, so a hand-written configuration without `orchestrator:` still cannot pin its first fleet ([orchestrator-wizard.md](orchestrator-wizard.md#4-a-line-sent-to-a-working-session-waits-for-its-turn)). A listed fleet's pin adds the key when it is missing.

## 5. The breaking change, and how to roll back

**Measured:** a fleetdeck binary older than #120 refuses to start on a configuration that has `fleets:` or `name:`. It stops with `unknown configuration key "fleets"` (or `"name"`), because the loader keeps `KnownFields(true)`. Loosening that to spare older binaries would let every misspelt key pass silently, which is exactly what the check exists to catch.

**Rolling back after adding a fleet:**

1. Delete the `fleets:` block from `~/.config/fleetdeck/config.yaml`. If you added a top-level `name:` line by hand, delete it too. Nothing in fleetdeck writes `name:` by itself.
2. What remains is the first fleet, in the shape the older binary reads. The writers never touched it (section 4).
3. The other fleets' boards and documentation stay on disk, untouched.

Keep a copy of the `fleets:` block before deleting it. After upgrading again, pasting it back restores every fleet exactly, orchestrator pins included, and it is the only record of which session led which fleet. Without the copy, `fleetdeck init --fleet NAME` with the same `--workspace` (or `--board`) as before adds a fleet back: a board that already holds files is kept, not touched. Its orchestrator then has to be appointed again.

## 6. One orchestrator per fleet, held in three places

A session leading two fleets would read two boards. It is refused in three places:

1. **At load.** `config.ValidateFleets` refuses a configuration in which one session is two fleets' orchestrator, or two fleets share a board.
2. **At the pin.** `vetPin` (`cmd/fleetdeck/fleets.go`) applies the pin to a copy of the configuration and validates the whole set before anything is written. The route answers `server.ErrOrchestratorTaken` with 409 and writes nothing. `TestPinningAnotherFleetsOrchestratorIsRefusedBeforeAnythingIsWritten` compares the file byte for byte and loads it again. A refusal that leaves half a change behind would be worse than no check.
3. **In the wizard.** `orchestrator.Appointer.Vet` asks about an existing session before the brief is written, so another fleet's orchestrator is never handed this fleet's working order.

- **Read, fixed in #120:** before `vetPin`, the first fleet's pin went through `SetField`, which only checks that the file still parses. It could write a configuration that the next start refuses.
- **Measured by mutation:** with one wizard shared by every fleet, fleet B's wizard pinned through the first fleet's key. A mutant doing exactly that survived every test until `TestAFleetsWizardPinsThatFleet`. Each fleet now has its own appointer, made once at start, because an appointer's lock is what keeps two appointments of the same fleet from racing.

## 7. The stand, and what the tests could not see

The stand: a fake daemon on its own socket, two fleets made by the branch's own `init`, a panel with `-stand-socket` and `-stand-claude` (a claude that starts nothing real — [orchestrator-wizard.md](orchestrator-wizard.md#5-a-stand-must-be-unable-to-start-a-session-not-merely-not-start-one)), and headless Chrome driven over the DevTools Protocol with real mouse events.

- **Measured: a check that was blind.** The documentation check waited for `.docs-empty`. The article's placeholder ("pick a document") has the same class, so the wait was satisfied before the list had loaded — and both fleets' docs folders were empty at that moment anyway. Three runs reported "no documents" on both sides and passed. The check was rewritten: each fleet gets a document only it has, and the check waits for entries in the list. On a build that asks for the list without the tab's fleet, fleet B's tab shows fleet A's document, so the check can now fail. Before trusting a wait, make sure nothing else on the page satisfies it. Equal answers from two places prove nothing when both are empty.
- **Read: the unit tests' fake DOM does not parse `innerHTML`.** A button built from markup, and its click handler, is invisible to the unit tests. Three mutants in such handlers survived every unit test: the pencil on an unclaimed session, the line that switches to another fleet, and a click on the current fleet's own entry. Each was built into a binary and killed by the stand drive.
- **Measured on CI: a test that starts a card commits it.** `CreateCard` commits to the board's git. A test that called it on a plain temporary board passed on the developer's machine, through the developer's global git identity and signing key, and failed on `ubuntu-latest` with `Author identity unknown`; the macOS runner guesses an identity and passed. Such a test calls `isolateHome` (`cmd/fleetdeck/setup_test.go`). To reproduce the runner locally, set `GIT_CONFIG_GLOBAL=/dev/null` and `GIT_CONFIG_NOSYSTEM=1`, and pass `user.useConfigOnly=true` through `GIT_CONFIG_COUNT`, `GIT_CONFIG_KEY_0` and `GIT_CONFIG_VALUE_0`. An empty `GIT_AUTHOR_NAME` is stricter than the runner: it overrides the identity a test sets in its own repository.
- **Mutation testing, with the second number.** 176 mutants over the new code, 175 killed. The one survivor is equivalent: the `break` that stops the search for `session` at the end of a fleet's `orchestrator` block finds the same line without it, because the schema has no second nested `session`. Before a last targeted pass, 7 of the 114 new tests had killed no mutant. One mutant aimed at the property of each was then killed by exactly that test. A test that kills nothing is not proven empty — no mutant may have aimed at it — but it is not proven useful either until one does.

## 8. Not verified

- **The back/forward cache in the app window.** Measured in Chrome only (section 1). The window's WebKit WebView was never driven.
- **Short-id reuse on a live daemon.** No real daemon was seen giving a freed short id to a new session. If one does, the new session is claimed by whatever card still names that id (section 2), and shown in that card's fleet.
- **A fleet added while the panel runs.** It is served only after a restart, and `init` says so. There is no hot reload of the fleet list.
