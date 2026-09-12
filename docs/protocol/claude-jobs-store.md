# The job store on disk: `~/.claude/jobs`

This is the second thing fleetdeck reads from Claude Code, beside the daemon control socket. Unlike the socket, whose behaviour was measured and written down in [daemon-control-socket.md](daemon-control-socket.md), this is a **file format owned by someone else**, read from outside, with no contract of any kind behind it. This document exists so that the dependency is a thing the project knows it has rather than a surprise the day it breaks.

Measured against Claude Code CLI 2.1.263 on 2026-09-12.

## 1. Why it is read at all

The control socket cannot answer "which sessions are stopped".

Section 4 of the socket document says it outright: there is no `live` and no `resumable` field on the wire, and "a session's mere presence in a `list` reply with no `dying` flag means it is alive". The reply is the running sessions and nothing else. A session that someone stopped — `claude stop`, or `delete_session permanent=false` through the claude-agents MCP — simply stops being an element of the reply. It carries no flag on its way out and leaves no trace behind it.

Asked over the socket alone, "stopped" and "never existed" are the same answer. The panel drew that answer literally: the row disappeared, and the board card naming that session was marked orphaned — the panel saying *this card lost its session* about a session sitting on disk with its whole history, one resume away. The operator's own words for it: «она должна там оставаться, просто с пометкой».

The job store is where the session that dropped out of the reply still is.

## 2. The layout

```text
~/.claude/jobs/
  pins.json          — the agents view's pin set. Not a session; skipped.
  <short>/           — one directory per session, named by its short id
    state.json       — the record fleetdeck reads
    timeline.jsonl   — the session's own event log. Not read.
    tmp/             — the session's scratch directory. Not read.
```

The directory name is the short id, and that is the id fleetdeck uses — not the `daemonShort` field inside the file. The directory name is what the daemon lists a session under, what a board card names a session with, and what everything else addresses a session by; a file disagreeing with the directory it sits in must not be able to make the panel answer about a different session.

A directory holding no `state.json` is **not** a session. Two exist on the machine this was written on, each holding nothing but an empty `tmp/` — sessions that never got far enough to have a record. `claude agents --json --all` does not list them either.

## 3. The fields fleetdeck reads

Every one of them is optional. The panel reports what it found and never invents a value for what it did not.

| Field | Used for |
| --- | --- |
| `sessionId` | the transcript UUID; the session's identity |
| `resumeSessionId` | what the daemon actually resumes by — equal to `sessionId` on an ordinary session, different on one resumed from another |
| `name` | the row's display name |
| `cwd` | the saved working directory. Its existence is the resumability test, see below |
| `state` | the last state the session recorded. Shown as the last state, never as a reading of now |
| `detail` | the last thing the session said about itself |
| `intent` | the last prompt submitted into it |
| `backend`, `cliVersion` | how it ran |
| `createdAt`, `updatedAt` | RFC 3339 strings; `updatedAt` orders the stopped rows, most recent first |

The file carries more than this — `respawnFlags`, `worktreePath`, `linkScanPath`, `bridgeSessionId`, `providerEnv` and others. fleetdeck reads none of them. The read is deliberately shallow: identity, a name, a working directory, and the last words, none of it interpreted beyond what the panel prints.

`tempo`, `needs` and anything else that is a reading of a *running* process is deliberately **not** carried over, even where the file holds it. See section 7.

## 4. The resumability rule

A session that is not running is in one of two states, and they are not the same thing:

- **stopped** — it can be brought back in place, with its full history;
- **dead** — it cannot.

The rule, copied from claude-agents-mcp's own (`internal/agents/resume.go`, `Resumable` and `cwdMissing`), which is what the agents view answers with:

> resumable = there is an id to resume by (`resumeSessionId`, else `sessionId`) **and** the saved `cwd` still exists as a directory.

Two details of it are easy to get wrong and both are load-bearing:

- **It is the working directory that is tested, not the job directory.** Every stopped session has a job directory — that is where its record is. On the machine this was written on, eleven sessions had a record and were not resumable, all of them because the directory they ran in had been renamed or deleted (a removed `.claude/worktrees/…` in most cases). Testing the job directory instead would have called all eleven resumable and offered a resume that crashes on startup.
- **An empty `cwd` is not a missing one.** The daemon falls back to a default, so such a record still resumes. Treating empty as missing would move a resumable session into the group that cannot come back.

## 5. Why fleetdeck walks the store itself

The agents view's own list comes from `claude agents --json --all`, which walks this same directory from inside the CLI. fleetdeck could shell out to it and inherit its answer exactly. It does not, for two reasons:

- The panel polls every two seconds (`daemon.poll_interval`). That is forty thousand child processes a day to learn about thirty rows.
- It would make the panel stop showing stopped sessions on a machine where the CLI is not on `PATH` — which is a thing the panel is otherwise indifferent to.

The two answers agree. Checked on 2026-09-12: 33 short ids from the store, 33 rows from `claude agents --json --all`, identical sets, identical `sessionId` and `name` for every one of them. The single row the CLI has and the store does not is one with `id: null` — a transcript the CLI recovered with no job record behind it, which fleetdeck deliberately does not invent: with no short id there is nothing for the panel to address it by and no card could name it.

That agreement is a test, not a claim: `internal/jobs/store_real_test.go` reads the real store on the machine running it and compares it against the real CLI, and fails on a disagreement. It skips where there is nothing real to check (no store, no CLI), which is honest — a skip says the check did not happen, unlike a green test that checked a fixture it wrote itself.

## 6. What breaks if the format changes, and how it is noticed

Nothing here is a contract. Claude Code writes these files for its own agents view and is free to move, rename or restructure them in any release.

**How it degrades.** A store that cannot be read fills `Snapshot.JobsError` and nothing else: the live sessions still arrive and the panel still works, minus the stopped ones, and it says so on screen rather than showing an empty group — "nothing is stopped" and "we cannot tell" look identical otherwise, and only one of them is good news. A single record that cannot be parsed still produces a row carrying its short id, marked as not resumable, because a session disappearing from the panel is the exact failure this whole mechanism exists to end.

**How it is noticed.** `internal/jobs/store_real_test.go` is the tripwire. A renamed directory, a renamed `state.json`, a renamed `sessionId` or `name` field all show up there as a disagreement with the CLI, on the machine of whoever runs the tests. It is the only test in the project that reads a path nothing in the project wrote, and that is the point of it: every other test in `internal/jobs` builds the store it then reads, so it can only prove that the package agrees with itself.

**What must stay true if the rule is touched.** The resumability answer has to match the agents view's, because the agents view is what actually performs the resume. A panel that says "stopped, resumable" over a session `resume_session` then refuses is worse than the panel that dropped the row.

## 7. What is deliberately not taken from the store

A record holds the last values of `state`, `tempo` and `detail` — frozen at the moment the session went away. Only `detail` reaches the panel unchanged. `tempo` is dropped, and `state` is carried in a field of its own (`SessionView.LastState`) rather than in the one the daemon's own state travels in.

That is not tidiness. `state: "blocked"` means *stalled* to every rule that reads it — in `internal/daemon`, in the notification rules, and again in the browser. A session that happened to stop while blocked would be counted as stalled on every poll, forever, by a counter whose whole purpose is to be short and actionable, with nobody able to unstick it. Keeping the frozen value out of the field those rules read makes that impossible rather than something every future caller has to remember.

The same reasoning covers the rest: a session that is not running is not waiting for anyone's answer, is not making progress, and is not silent. It stopped. That is a thing that already happened, not a thing going wrong.
