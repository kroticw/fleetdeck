# Orchestrator wizard: field notes

These are facts from building the orchestrator step of the first-run wizard (#118, 2026-09-11, Claude Code 2.1.263): the step that makes a session the fleet's orchestrator, a new one or one already running. Each fact says how it was established: **measured** (observed on a live system), **read** (in a binary, in code, or in another tool's source), or **decided** (a choice, with its reason). The code comments hold the details; this page holds what cost something to learn.

Read this before changing `internal/orchestrator`, `cmd/fleetdeck/orchestrator.go`, the orchestrator step in `web/js/setup.js`, or anything that sends text into a session. Read section 1 and section 5 before writing any test or stand that could start a Claude Code session, even by accident.

The page is named after the code it is about, like [live-terminal.md](live-terminal.md) and [window-and-panel.md](window-and-panel.md), not after the day, like [session-findings-2026-09-11.md](session-findings-2026-09-11.md). Almost every fact here is something a person changing the wizard needs at that moment. The two that reach further — the daemon socket that sorts first, and the rule in section 5 — are found where they were paid for, and the README points to them.

## 1. The daemon socket that sorts first wins

This is the most expensive fact on the page.

- **Read:** the panel finds the daemon by globbing `/tmp/cc-daemon-<uid>/*/control.sock` and takes the **first live socket in lexicographic order** (`daemon.SocketPath`, `resolveSocketCandidate` in `internal/daemon/client.go`). The comment there says the choice among several live daemons is arbitrary. A daemon's directory name is 8 random hex digits.
- **Measured: a second daemon is easy to make.** `claude --bg` run with a `HOME` that is not the user's own does not reach the user's daemon. It starts a daemon of its own: `claude daemon run --origin transient --spawned-by {"label":"claude --bg","cwd":…}`, in a new directory under the same `/tmp/cc-daemon-<uid>/`. A test run did this twice on 2026-09-11 (section 5). Its directories `581b074b` and `8b3f8fbe` both sorted ahead of the real `b9184055`.
- **What it looks like.** Any panel or fleet tool that resolves the socket while such a daemon is alive looks into it: an empty fleet, or one stray session. It reads as "the daemon lost its sessions", or as a refused key, never as "we are looking at the wrong daemon". That afternoon, waking the orchestrator failed with "didn't present the daemon control key" while the stray daemons were alive, and went through at the first try once they were gone. That is consistent with a client reaching the wrong daemon; it does not prove it.
- **Measured: after such a daemon exits,** its `control.sock` is gone and its directory stays, holding `pty/`, `rv/` and `spare/`. Discovery then skips it, since the glob needs `control.sock`, but nothing removes the directory.
- **How to check.** `ls /tmp/cc-daemon-$(id -u)/*/control.sock` should print one path. `pgrep -fl 'daemon run --origin transient'` should print nothing. More than one socket means find out which daemon is whose before trusting what any panel shows.
- **Open:** choosing the daemon by something other than name order — skipping transient daemons, for one — would take this trap away for everyone. That belongs to the daemon client, not to the wizard, and is not done.

## 2. One line, and a file

- **Read** in the claude-agents MCP server's source (`internal/agents/submit.go`): the daemon's `reply` acknowledgement means only that the text reached the session's prompt. A long or multi-line body can be collapsed into an unsent `[Pasted text]` that still needs an Enter, and that tool presses Enter after such replies itself. **Not measured here:** the collapse was not reproduced; it was designed around.
- **Decided:** the working order travels as a file, and the session is sent one line telling it to read that file (`orchestrator.Message`). One line has nothing to collapse. The file stays on disk, so the orchestrator can read it again after its context has been compacted, and a person sees it in the Docs tab. It is written to the first documentation directory as `orchestrator.md`, headed by where the panel keeps its board, docs and configuration. Its first line is a marker; a file of that name without the marker, or one that cannot be read, is never replaced.
- **Measured on two real sessions:** the one line arrived as a user turn both times, and both sessions called `Read` on the brief next.
- **Decided: both paths send the same line.** A new session is started and then sent exactly what an existing one is sent. Tests compare the messages as sent, not as built. Two paths that loaded the knowledge differently would drift, and the drift would show up only as two orchestrators behaving differently.

## 3. Appending does not rewrite, and the proof

- **Measured:** a real session did one task ("remember the word КАШАЛОТ"), then was appointed. Its transcript before the appointment, 488,242 bytes, is a **byte-for-byte prefix** of its transcript afterwards (sha256 of both prefixes `f806acb27b843c89…`). Asked nothing further, it said it had read the brief and still remembered the word. Context was added, not replaced, and nothing earlier was touched.
- **The technique carries over** to any claim of "we erased nothing". Copy the file before, act, then compare `after[:len(before)] == before`. Equal lengths or equal line counts are not enough, and neither is "it still looks right". Transcripts live at `~/.claude/projects/<cwd with every non-alphanumeric character as ->/<session id>.jsonl`.
- **Measured: what the session did with the line** is in the same file after the prefix: the user record with the line, the `Read` tool call with the brief's path, and the answer.

## 4. A line sent to a working session waits for its turn

- **Measured:** a session was given a task ("write the numbers 1 to 40"), and 2 s later it was sent the wizard's line. The transcript records the queue:

  | Time | Record |
  | --- | --- |
  | 57.464 | `queue-operation enqueue` — the line, while the turn was running |
  | 58.130 | `turn_duration` — the turn ended |
  | 58.135 | `queue-operation dequeue` — the line was taken |

  The count was complete. The line was not merged into it and did not interrupt it. One case, with 0.7 s of overlap — but it is what the wizard's warning promises, and it held.
- **Measured: a new session takes a reply at once.** `claude --bg`, the session listed by the daemon, the line delivered and the pin written: all within one second. The appointer still retries `ESTARTING`, `ENOREPLY` and `ERESPAWNING`, for up to a minute for a new session and 5 s for an existing one.
- **Measured: what `claude --bg` prints.** `backgrounded · \e[36m<short>\e[39m · <name>`, then hint lines that repeat the short (`claude attach <short>`). `orchestrator.ParseShort` reads the id only on the `backgrounded` line and only after stripping colours. A test pins both cases.
- **Measured: no trust dialog** appeared for `claude --bg` in two new folders on this machine: the first prompt was answered.
- **Measured: a hand-written configuration without an `orchestrator:` section** cannot be pinned. `config.SetField` edits an existing line and never adds a section. The line had already been sent, and the step says exactly that ("the session has its message, but it is not pinned"). A configuration written by setup has the section. The column's dropdown writes through the same `SetField` and has the same gap.

## 5. A stand must be unable to start a session, not merely not start one

### What a socket does not cover

`-stand-socket` keeps a panel off the real daemon (see [window-and-panel.md](window-and-panel.md#test-stands-on-a-machine-with-a-live-fleet)). A panel that can start sessions has a second way to reach the fleet: `claude --bg`. Whatever socket the panel reads, a real claude either reaches the user's daemon (with the real `HOME`) or starts a daemon of its own (with another `HOME`, section 1). Neither is the stand's.

### What the code does

**Decided:** on a stand the panel never looks a claude up. It runs only the one named by `-stand-claude`, and without that flag it starts no sessions at all; the wizard's new-session button is off and says why. `-stand-claude` without `-stand-socket`, or given empty, is refused at startup, and a test asks the built binary itself, not only the function. Like `-stand-socket`, the guarantee is a code path that does not exist (`sessionStarter` in `cmd/fleetdeck/orchestrator.go`), not a check that could itself go wrong.

### How it failed anyway

The guard was in the code. The test that exercised the wizard end to end, `TestAPanelAppointsBothWaysThroughItsDaemonAndConfiguration`, replaced `HOME` but not `PATH`. The mutation pass then did what an absent-minded edit would do: it removed the guard (`if o.standSocket != "" {` → `if false {`). The test looked claude up, found `/opt/homebrew/bin/claude`, and ran `claude --bg`. It did so twice, at 20:12 and 20:21: two transient daemons, whose sockets sorted ahead of the real one (section 1), and a real session named "оркестратор" — the name of the operator's real orchestrator. They were found by a live-session listing, not by any test, and stopped by exact PID, each checked against its command line, at 20:25.

### The fix, and its proof

`TestMain` in `cmd/fleetdeck/noclaude_test.go` puts a `claude` that refuses (exit 97, "the real claude is hidden from these tests") first on `PATH` for every test in the package, and empties the places looked at outside `PATH`. A test that means to run a claude gives its own. The proof is the same mutant, run in a copy of the tree:

- **Before the fix:** 2 daemons and 1 session on the machine.
- **After the fix:** the test fails with the decoy's words, the daemon directories are the same before and after, and the count of transient daemons is 0 before and 0 after.

`internal/orchestrator` needs no decoy. Nothing there calls `exec.LookPath`: `FindClaude` is handed its lookup, and every test hands it a fake.

### The general rule

**A tool that can touch something live will, sooner or later, touch it.** On this machine, on this one day, it happened three times:

- a cleanup script matched processes by name and may have killed a neighbouring session's test runs ([window-and-panel.md](window-and-panel.md#cleaning-up-test-processes-on-a-shared-machine));
- a stand attached to the real daemon, and attaching resizes the real sessions' terminals for everyone ([live-terminal.md](live-terminal.md#2-the-sessions-size-belongs-to-everyone));
- this wizard's tests started real sessions and daemons.

None of these was a tool meant to do harm. Each was able to, and something removed the one condition that stopped it. So:

1. **Make it unable, not unwilling.** In a stand or a test, the dangerous path should not exist: a socket of its own, a claude of its own, a decoy first on `PATH`, a code path that is not there. "The code checks first" is not enough, because the check is exactly what a mutation, a refactor or a typo removes.
2. **Prove the inability by removing the guard.** Run the mutant that deletes the check, in a copy of the tree, and count the live things — sessions, daemons, sockets, processes — before and after. A green test on intact code proves nothing about what the test does once the check is gone.
3. **Before a mutation pass, ask of each test what it does if the guard is gone.** A mutation run is an automated way of removing guards one at a time.
4. **Clean up by exact PID or id, checked against the command line — never by a name pattern.** A name is shared by everyone who runs the same binary.

## 6. Not verified

- **Permissions on another machine.** On this machine an existing session read the brief outside its own working directory without a permission prompt; that depends on this machine's settings. Setup adds the workspace to `permissions.additionalDirectories` in `~/.claude/settings.json`. Whether a session already running when that happens picks it up was not measured. If it does not, the orchestrator asks for permission to read, and the panel shows it as waiting.
- **A session with a dialog on its screen.** That it answers the reply with `ENOREPLY` is what the stand imitates. It was not measured on a real session with a real dialog open.
- **The app window as a whole.** The wizard was driven in a bare panel with headless Chrome, never inside the fleetdeck window.
- **The multi-line collapse** of section 2, which the design avoids rather than reproduces.
- **The orchestrator on a live fleet.** "Behaves like an orchestrator" was measured as: it read the brief, looked at the board and the docs, reported on its earlier task, and waited, on an empty board. The working order does not say how an orchestrator reaches other sessions (here, the claude-agents MCP server); on a machine without it, an orchestrator has only `claude --bg` and `claude attach`.
