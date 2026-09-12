# The Stalled badge: field notes

These are facts from fixing a badge that lit on sessions which were working (2026-09-12, Claude Code 2.1.263). Each fact says how it was established: **measured** (observed on the live fleet or reconstructed from real transcripts), **read** (in code or a protocol document), or **decided** (a choice, with its reason).

Read this before changing `createStalledTracker` in `web/js/header.js`, before touching `BLOCKED_SETTLE_MS`, and before adding any rule that reads `state` or `tempo` to decide whether a session has stopped.

The protocol side of Waiting/Stalled lives in [`docs/protocol/daemon-control-socket.md`](../protocol/daemon-control-socket.md) section 5, and is deliberately not repeated here. That document classifies what arrives on the wire. This page is about the other half, which that document itself puts out of its own scope: what the panel is entitled to show a person, given that the wire's answer is sometimes wrong.

## 1. `state` sticks, and the panel already knows better

**Measured.** Sampling the live panel's own `/api/snapshot` every ten seconds on 2026-09-12 caught three of six fleet sessions holding `state: "blocked"` with `needs: ""` while they were plainly working: 22%, 25% and 37% of samples each. Across every one of those samples, `silentFor` — the age of the last write to the session's transcript — never passed 59 seconds, and its median sat between 5.5 and 18.6 seconds. The sessions were writing to their transcripts the whole time the flag said they had stopped.

That is the shape the operator reported: a row reading `blocked 17s`, with a Stalled badge beside it. Two fields, on one row, contradicting each other — and the badge believed the wrong one.

**Read.** `state` and `tempo` come from the daemon and are set by a mechanism the session does not control; the protocol document says as much, and says why neither can distinguish "waiting on a person" from "waiting on my own subagents". What it does not say, because it is not a property of the protocol, is that `state` also *lags*: it is observed to stay at `blocked` well after the session has resumed. A flag that lags is not merely ambiguous — it is wrong, in a direction no amount of reading the other flag corrects.

**Read.** `silentFor` is not another flag. It is `time.Now().Sub(fi.ModTime())` on the session's transcript (`cmd/fleetdeck/collect.go`, `transcriptState`), attached to `state.SessionView` by the collector. Every append to the transcript moves it, including the writes a subagent makes into the same file. It is the one value on the row that is a measurement rather than a report.

## 2. Both flags lie, each in its own direction — so neither may be dropped

**Read.** Dropping `state` in favour of `tempo` alone was tried once, in commit `96ba6d4` (#62), and rejected. The reason is in the fixtures: `internal/daemon/testdata/list_sessions.json`'s record `e4fa5037` is an attested hour-long stall carrying `tempo: "active"` with `state: "blocked"`. A rule reading `tempo` alone calls that session healthy for the whole hour.

**Decided.** So the disjunction `state == "blocked" || tempo == "blocked"` stays exactly as it is, in the daemon type and in both JS restatements of it. What changed is that the disjunction stopped being *sufficient*: a flag-only stall now also has to be silent.

This is the property to preserve if this code is touched again. There are two failures available here and they are not symmetric — a badge on a working session teaches a person to stop reading badges, and a missing badge on a stalled one hides someone who is waiting. A change that fixes either one by discarding a signal will reintroduce the other, and this project has now done that once in each direction.

## 3. The threshold is measured against silence, not against the flag's age

**Read.** `createStalledTracker` used to hold, per session, the moment a flag-only stall was first seen, and count from there. Its cleanup loop did drop the timestamp once the flag cleared — so the mechanism was not wrong about what it measured. It was measuring the wrong thing: the age of a lagging flag. Once the counter had run out during a real stall, the sticky flag kept the condition true through everything that followed, and the badge stayed lit on a session that had long since resumed.

**Decided.** The threshold now applies to `silentFor`. "Has been silent continuously for T" is simply `silentFor >= T`, which needs no clock of its own. That removes the accumulator rather than teaching it to reset: a single write to the transcript *is* the reset, and there is no state left that could survive a session coming back to life.

**Decided.** `silentFor == 0` means "no transcript to stat", not "silent for zero time" — `transcriptState` returns zero on a failed stat, and `silentLabel` in `web/js/sessions.js` renders it as unknown for the same reason. So an unmeasured `silentFor` falls back to the old flag-age clock, which is still held per session for exactly this case. Reading that zero as "not silent enough" would permanently hide a session that stalled before writing anything.

## 4. Ten minutes, and where the number comes from

**Measured.** Every transcript on this machine over the thirty days to 2026-09-12 — 103 files — was split into the gaps between consecutive writes, and each gap classified by the record that closed it: an assistant message or a tool result means the session was working through that gap, a message from a person means it was stopped and waiting. That gives 98 665 gaps in which a live session was silent while working:

| | |
| --- | --- |
| p50 | 2.3 s |
| p90 | 14.0 s |
| p99 | 54.7 s |
| p99.9 | 333 s (5.5 m) |
| p99.99 | 1 070 s (17.8 m) |
| longest | 2 096 s (34.9 m), once in thirty days |

Two contaminations had to come out of that tail first, and both are worth knowing about before anyone repeats this measurement:

- A gap closed by an `AskUserQuestion` tool result is **not** work. The session was stopped, waiting for a person to answer, and the answer arriving is what closed the gap. These made up most of the original tail and belong on the other side of the split.
- Seventeen "gaps" ran from one hour to 462 hours, every one of them a tool result arriving after the session was resumed with `--resume`. A live session does not wait four days for a `WebSearch`. They are listed individually rather than averaged away.

**Measured.** The same pass counted 2 339 gaps in which a session really was stopped waiting for a person: 305 of them ran past ten minutes, 157 past thirty, 125 past an hour.

**Decided: ten minutes.** It sits at roughly twice the p99.9 knee of live silence. Forty-two of the 98 665 working gaps reach it — about 1.4 a day across the whole fleet — and each of those still needs a stuck blocked flag beside it before anything lights up. Fifteen minutes was measured too and rejected: it removes about one false badge a day and costs a further five minutes of delay on each of the roughly ten real stalls a day. The attested hour-long case clears any threshold in this range by a wide margin, so it plays no part in choosing between them.

The number is unchanged from what `BLOCKED_SETTLE_MS` held before, which is a coincidence worth stating plainly: the previous value was chosen against a different quantity (a 2.5-minute floor on how long the flag itself persisted) and happened to land in the same place. If this constant is revisited, the table above is the thing to re-measure, not the flag's persistence.

## 5. Where the rule lives, and where it must not

**Read.** `daemon.Session.Stalled()` in `internal/daemon/types.go` is a restatement of the protocol document's rule and nothing more. It is unchanged by this work, and `internal/daemon/testdata/list_sessions.json`'s expectation that `e4fa5037` is stalled is unchanged with it. `Session` has no `SilentFor` field and must not grow one: silence is measured by the collector from a file on disk, which is not something the wire carries or the daemon knows.

**Read.** `SilentFor` lives on `state.SessionView`, one layer up, where the collector attaches it. That is also the layer at which the panel decides what to show.

**Decided.** So the silence rule lives in the panel, in `createStalledTracker`, and nowhere else. Both consumers — the header's counter and the session row's badge — already share that one function, which is why the whole change fits in `web/js/header.js` and why the two can no longer disagree. Section 5 of the protocol document draws the same line from its own side: presentation obligations are explicitly out of its scope and must not be pushed back into `Waiting`/`Stalled`.

**A trap left in place.** `web/js/sessions.js` defines a local `isStalled` that nothing calls; it is the per-instant flag rule, kept as documentation beside the comment explaining why the row must *not* use it. It is dead code that looks live. Anyone reaching for it should read this page first.

## 6. What was not measured

The live sampling window that caught the false-positive side was minutes long, not days, and it caught no genuine hour-long stall — that side rests on the thirty-day transcript reconstruction and on the `e4fa5037` capture, not on a stall watched live from start to finish. A longer live window is the thing to run if this is ever in doubt: sample `/api/snapshot`, record `state`, `tempo`, `needs` and `silentFor` per session, and replay the recording through the rule rather than trusting a screenshot.
