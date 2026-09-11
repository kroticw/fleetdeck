# App, panel and update: field notes

This page is for whoever works next on the fleetdeck app, the panel it starts, or the Update button. It covers what the code relies on and the things that are easy to get wrong. It was written on 2026-09-11, when the app became the panel's owner (#97), the panel came to live exactly as long as the app (#108), and the Update button arrived (#111). The code comments hold the details; this page is the map, with the numbers that were measured and the mistakes that were made.

For the live terminal, the window's web view, and the terminal bridge, see [live-terminal.md](live-terminal.md).

## Who owns the panel

The panel lives exactly as long as the app. That rule was set by the operator on 2026-09-11. Before it, the panel outlived the window by design, and a panel left behind by an earlier window is how a new window came to show an old build.

The panel keeps the rule itself, so no way of the app ending is missed:

- The window starts the panel directly, as its own child, with `--owner-pid` set to the window's PID (`panelArgs` in `cmd/fleetdeck-window/owner.go`). It is never started through a shell. `TestAStartedPanelIsTheStartersOwnChild` fails if a process appears in between.
- At start, the panel checks that `getppid()` is that PID and refuses otherwise (`checkOwner` in `cmd/fleetdeck/owner.go`).
- It then asks the kernel to say when that process exits: kqueue `EVFILT_PROC` with `NOTE_EXIT` on macOS, a pidfd polled for `POLLIN` on Linux. Nothing is polled on a schedule. The exit gets the same graceful shutdown as SIGTERM, bounded by five seconds.
- A watch that cannot be set up counts as the window gone, and `ESRCH` at registration means the owner has already exited. A panel that cannot tell whether its window lives must not risk outliving it.
- The red button only hides the window (`installCloseToHide`). The process runs on, and so does the panel.
- The owner PID is in the panel's build fingerprint (`/api/snapshot`, `build.owner`). A panel started any other way reports none.

A window that finds a panel already answering (`Keeper.replaceable` in `internal/supervisor/keeper.go`):

- **The panel's window is gone:** the panel is stopped, and the window starts its own.
- **Another window is still open:** its panel is used as it is.
- **Started from a terminal, or not a fleetdeck panel:** used as it is, never stopped.
- **From an app bundle, reporting no owner:** replaced too. This rule is temporary and marked so in the code. Such a panel comes from an app older than the owner rule. The rule is to be removed once every fleetdeck panel answering anywhere reports its owner. Left in past that, it would stop a debugging run started by hand from inside a bundle.

Measured on real processes with the real app on 2026-09-11. The panel was gone 44 ms after the window got SIGTERM and 82 ms after SIGKILL. A panel started from a terminal survived the window.

## The PID reuse trap

A PID is a number the kernel hands out again once the process that had it is gone. Anything that reads a PID now and acts on it later can act on a different process. How each place here deals with that:

- **The panel and its window.** The panel does not trust the number it was given; it checks that the number is its parent. A process's parent PID cannot belong to a stranger: when the parent dies, the child is handed to launchd and `getppid()` becomes 1. So a panel whose owner has already died refuses to start instead of watching whatever took the number.

  Between the check and the kernel watch there is a gap, and the window can end inside it. Either way the answer is the same:

  - **The window ended before the watch was set up.** On macOS, registering `EVFILT_PROC` for it fails with `ESRCH`. On Linux, `pidfd_open` fails with `ESRCH` once the process is reaped. If it is not yet reaped, the pidfd opens and is readable at once. The panel reads all of these as "the owner is gone" and shuts down.
  - **The window ended after the watch was set up.** The watch fires.

  Only one case is left. The window dies inside the gap, and its PID is handed to a new process before the registration, which then watches that stranger. On Linux the pidfd rules even this out, because it is bound to the process it was opened for, not to the number. On macOS it would take the PID being reused within microseconds. That is not guarded against and not expected. A second `getppid()` check after registering would close it.
- **Stopping a panel.** `StopHolder` asks the kernel who listens on the port (`lsof -nP -t -iTCP:<port> -sTCP:LISTEN`) at that moment, and only after the holder answered as a fleetdeck panel. It never uses a PID remembered from earlier, and never matches a process by name. Matching by name is how the old rebuild script stopped panels, and it would stop the wrong one the day two installs coexist.
- **Whether a panel's window lives.** The keeper uses `kill -0` on the owner the panel reports. If that PID has been handed to some other process, a panel whose window is gone looks like one with a live window, and it is used for the moments until its own watch shuts it down. The mistake costs seconds, not a wrong stop.
- **Test stand-ins.** They watch their test process with `kill -0` every 100 ms. In tests only, the worst case is a stand-in that lives longer.

## launchd, its throttle, and the keeper

Until 2026-09-11 the panel was started at login by a launch agent that `fleetdeck init` wrote, and launchd kept it alive (`KeepAlive`). The app replaced that agent. A leftover agent would start a second panel at every login, so `init` now names one it finds and prints the two commands that remove it: `launchctl bootout gui/$(id -u)/dev.fleetdeck.panel`, then removing the plist. `init` never runs them. `bootout` is the current spelling; `unload` is listed as legacy in `man launchctl`.

That agent had `KeepAlive` set to plain `<true/>`: start the panel again whenever it exits, for whatever reason. On a taken port the panel names the holder and exits with status 1 (`log.Fatalf`). launchd then starts it again once `ThrottleInterval` has passed, it fails again the same way, and so on: every ten seconds, for as long as the port is taken, with another "cannot take its port" in the log each time. Nobody is told.

`KeepAlive` as a dictionary with `SuccessfulExit` does not cure that. Read from `man launchd.plist`, not measured here:

- **`SuccessfulExit` false** restarts the job only when it exits non-zero. A taken port is a non-zero exit, so the loop stays.
- **`SuccessfulExit` true** restarts it only when it exits with zero. The taken-port loop stops, but so does the restart after every real crash, which is what `KeepAlive` was for.
- **A panel that exits with zero on a taken port**, to break the loop under `false`, would make a panel that could not start look like one that stopped on purpose.

An exit status cannot say "the port is taken, and a person should look". So the keeper does not guess from exit codes. It judges by how long the panel stayed up.

The keeper does what launchd did, including launchd's throttle. launchd will not start a job again sooner than `ThrottleInterval` after its last start, 10 seconds by default. The keeper's `MinUptime` is the same 10 s (`launchdThrottle` in `cmd/fleetdeck-window/owner.go`). A panel that dies within it is not restarted: a panel that dies at once dies at once again. The window says so, shows the end of the panel's log, and offers a button to start it again.

How long a started panel has to answer: `panelStartTimeout` = 110 ms × 3. The 110 ms is the worst of ten measured starts. They were ten separately built binaries, each run for the first time, polled the way the keeper polls. A start straight after login, with a cold disk cache, was not measured.

## Why a taken 7777 is reported, not worked around

The panel answers on `127.0.0.1:7777`. Three things point at that port without asking anyone:

- the window (`defaultURL` in `cmd/fleetdeck-window/main.go`);
- the statusline reporter, `fleetdeck-status`, which posts every session's status to `http://127.0.0.1:7777/api/status` unless `FLEETDECK_ENDPOINT` is set;
- whatever the operator has open in a browser.

A panel that moved to another port would be invisible to all three. The statusline reports would go nowhere, and the context and state they carry would silently vanish from the panel. The old panel would keep running unseen. So a panel that cannot take its port does not look for another one. It names what holds the port and exits (`portHolder` in `cmd/fleetdeck/portholder.go`), asking the holder for one second at most. There are three answers: another fleetdeck panel, by commit and binary path; something that is not fleetdeck; or something that took the connection and did not answer.

## Handing the panel to a new version

The Update button (`internal/supervisor/update.go`, `cmd/fleetdeck-window/update.go`) builds the new app beside the installed one, and lets the new version put itself in place. The invariant, in the operator's words and in the code: at the canonical path there is always a working version, at any moment.

1. The old window takes the update lock. It is an flock outside the tree, so a crashed update never locks the next one out.
2. It fast-forwards the source tree. It refuses, in words, a tree on another branch, with edits, or with commits of its own.
3. It builds the app into `.fleetdeck-update/` beside the installed bundle.
4. It pauses its own keeper. A keeper left running would race the new window for the port; a test holds this.
5. It starts the new window from the build with `--handover <file> --canonical <installed bundle>`.
6. The new window reports `alive`, stops the old panel, and starts its own from the staged bundle. Its keeper starts only after the old panel is stopped, so the replacement rules above never come into play here.
7. It checks that the new panel answers with the build the window is, then reports `panel`.
8. It exchanges the staged and installed bundles in one system call: `renamex_np` with `RENAME_SWAP` on macOS, `renameat2` with `RENAME_EXCHANGE` on Linux. Then it reports `swapped`.
9. It restarts its panel from the canonical path, so the panel reports where it really runs from, and reports `done`. The old window quits.

The handover was designed while the panel still outlived its window, and it came through the change to "the panel lives exactly as long as the app" (#108) with no step changed:

- **The new window never adopts the old panel.** It stops it, always, and starts its own. The old window's panel was going to go anyway, when the old window quits after `done`.
- **The new window's keeper starts only after the old panel is stopped.** So the keeper never meets a panel it would have to judge, and none of the replacement rules come into play.
- **The new window's panels are its own children,** started with its PID as their owner, like any window's.
- **A new window that fails and quits** takes its panel with it by the owner rule, on top of stopping it explicitly.

Before the swap, a failure stops the staged panel and reports `failed`. The old window then starts its panel again, from the installed bundle as it was.

The handover file carries one line per step. A line not yet ended by a newline is not read, so a half-written step is never taken for a whole one.

The swap is watched in a test (`internal/supervisor/swap_test.go`). A reader stats the canonical path in a loop while bundles are exchanged. In one Linux run it found the path missing in 0 of 1274 reads. The control is the same reader during two-step replacements, where it found the gap in 2137 of 4918 reads.

The handover timeout was measured on 2026-09-11 on the operator's machine, with the fleet running. It covered ten real handovers, each a new window from a freshly built bundle of its own, timed from the new window's start to `done`. In ms: 812, 762, 542, 495, 490, 489, 490, 485, 484, 483. The worst is 812 ms, and three times that, 2436 ms, is `handoverTimeout`. The first two were slower because each binary ran for the first time. A handover straight after login, with nothing of the binaries in the disk cache, was not measured.

The ten were measured before #108. The new window runs the same steps, plus the panel checking its parent and registering one kernel watch at start. Neither should move the number, but it was not taken again.

## Test stands on a machine with a live fleet

Everything above is tested on the operator's own machine, next to the fleet he works with. Four things matter there:

- **A panel finds the fleet daemon by uid, not by HOME.** A separate HOME and config do not keep a test panel off the real daemon. `-stand-socket <path>` does (#107): given a path nothing listens on, the panel never discovers the real daemon. Before #107, stand panels listed the real fleet's sessions. Opening one of them on a stand would have resized that session's real terminal for everyone attached.
- **A stand uses its own port** (7790 here), with its own HOME, config and board. It is never 7777.
- **A window is visible.** Anything that opens a window, takes a screenshot or steals focus happens on the operator's screen. It needs a moment he agrees to. Afterwards, check that nothing is left open before saying the screen is free.
- **Key presses into the window from another process** (the red button, Cmd+Q) need a permission prompt on the operator's screen. They were not scripted. To the panel, Cmd+Q and SIGTERM to the window are the same event.

## Cleaning up test processes on a shared machine

Several sessions run `go test` on the same machine at the same time. Every run of a package's tests is a binary with the same name: `supervisor.test`, `fleetdeck.test`.

On 2026-09-11, a script written to count stand-in panels left behind by tests matched every process named `supervisor.test`. It killed what it found with `kill -KILL`. Between 15:23 and 15:35 it may have killed a neighbouring session's test run. The session that wrote it found this itself, while reading its own script back. Its report to the orchestrator opened with the harm, before any explanation or fix.

What it taught:

- **A name tells nothing about whose a process is.** A test binary's name is shared by every session testing the same package. Nothing in `ps` output separates your run from a neighbour's.
- **Count by a sign only your process has.** Stand-ins can be told apart: a test binary run again with no arguments (how `StartPanel` and the tests start them), or a copy of it inside a `t.TempDir()` path. A `go test` run always carries `-test.*` flags, so it never matches. A stand panel from a rig is `…/T/Test<Name><digits>/<n>/…/fleetdeck`.
- **Print, do not kill.** The counting script prints PIDs and stops nothing. Stopping is done by hand, by exact PID, after looking at each: its parent (1 for an orphan), its age, its path. The costs are not symmetric. A stand-in left for a minute costs a little memory; a neighbour's killed run costs their work and your credibility.
- **Fix the cause, not the count.** Stand-ins were left because they run in a session of their own, and a test binary killed by its `-timeout` runs no `t.Cleanup`. Each stand-in now watches its test process and exits when it is gone (`watchOwner` in `internal/supervisor/process_test.go`). So does the shell that stands in for the window in `cmd/fleetdeck/owner_test.go`. When the update rig built its stand-ins' environment by hand, it skipped that watch and left eleven panels after one mutation run. Every stand-in now goes through `helperEnvFor`.
- **Prove the fix with both ends.** The fix was measured by killing a test binary by its timeout mid-test, once before and once after. Before, one panel was left; after, none. A zero from a run where nothing was killed proves nothing.

## First launch: the setup surface

A panel whose configuration file does not exist serves the setup page (`internal/server/setup.go`, `web/setup.html`) instead of the panel, and becomes the panel once a workspace is made. Facts from building it, each marked as measured on a stand, read in code, or decided:

- **Decided: only a missing file is a first launch.** A file that exists and does not parse is an error, as it always was. A page that offered setup over a broken configuration would be a way to overwrite somebody's file from a browser.
- **Decided: one listener, one handler, replaced once.** `serve` in `cmd/fleetdeck/main.go` binds, serves the setup surface through `switchHandler`, and after setup loads the new configuration and swaps the panel in. The window watching the port sees no gap and restarts nothing. **Measured:** on a stand the board was on screen 311 ms after Create was clicked, and `/` was answered 200 twice (the setup page, then the panel), never 304.
- **Read, not measured: the setup page carries no ETag, and `Cache-Control: no-store`.** The panel's own page at the same `/` is tagged with the build's web hash and `no-cache`. A stored setup page carrying a validator would be revalidated on the reload after setup, and a matching tag would bring back a 304 and the setup page. The control that was measured is a different one: a build that never swaps the panel in makes the stand's drive stop at the board. So the drive can see a handover that did not happen.
- **Decided after a mutation found it: the configuration is written after the board.** Written first, a board that could not be made left a configuration pointing at nothing, and `init` — which never rewrites a configuration it finds — then refused the corrected path. The page could not recover from its own failure.
- **Read: `os.Rename` on Unix refuses to replace an existing directory, even an empty one,** although `rename(2)` would. `internal/workspace` builds a board in a staging directory beside its place and removes an empty target first. `os.Remove` removes only an empty directory, so a file that arrived in between fails the call rather than being lost.
- **Read: `ServeMux` rejects `GET /` next to `/api/`** (neither is more specific than the other), and panics at registration. The setup surface registers `/` and refuses other methods itself.
- **Read: webview_go calls a bound function synchronously, on the main thread, from WebKit's message handler.** `chooseFolder` (`cmd/fleetdeck-window/choose_darwin.go`) runs `NSOpenPanel`'s `runModal` right there. **Not measured:** the chooser has never been opened. It shows a window on the operator's screen, which needs their go-ahead.
- **Measured: a board made on a machine with no git configuration commits under an identity git makes up** — the account's full name and `user@host.local` — and unsigned. That is what a person who has never configured git gets; the panel adds nothing of its own.
- **Stands.** A first-launch stand has no configuration to carry `server.port`, so it takes `-port`, and it needs a `HOME` of its own, since setup writes the configuration, Claude Code's settings and the board's git history under it. See [`docs/en/configuration.md`](../en/configuration.md). A panel built before this change has no `-port`; a "before" stand of it needs a configuration with only `server.port`, which makes it a configured panel, not a first launch.

## Measuring before trusting

The constants above come with the numbers they were derived from, in the code, next to them. A threshold without its measurement is a guess somebody will copy. Some habits that saved time here:

- **A control case for every measurement.** The swap watch counts gaps during two-step replacements as well. A reader that never saw a gap would otherwise look the same as a swap that never has one.
- **Mutation testing needs two numbers:** how many mutations survived, and how many new tests no mutation failed. The second catches a test that passes whatever the code does. When the harness cannot attribute a kill — a test whose own process the mutation signals, or a Go test of names in a JavaScript file — it is confirmed by hand, without the mutation and with it.
- **Reproduce the whole CI order before pushing:** `go mod verify`, `govulncheck`, `make lint`, `make test`, `make build`, `make verify-ldflags`, `make test-web`, and the docs parity check. Run Linux in a container too, once more with `lsof` installed, so the tests that need it run rather than skip.
