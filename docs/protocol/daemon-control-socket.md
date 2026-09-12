# The daemon control socket protocol

This document describes the wire protocol implemented by `internal/daemon`. It replaces an earlier addendum that lived under a gitignored directory and was therefore invisible in a public checkout of this repository. Everything below reflects the daemon as it actually behaves, verified against a live socket, not the shape it was originally guessed to have.

No real path, session name, session id, or timestamp appears anywhere in this document. Every example below is illustrative.

## 1. Transport and framing

The socket is a Unix domain socket. One client connection carries one request and (for most operations) one response.

- The daemon reads bytes from the connection until the **first `\n`**. That line is the whole JSON request; anything after the newline is a payload tail (relevant only to `attach`, see below).
- A request sent without a trailing newline is never parsed: the daemon stays silent until its own idle timeout fires. This failure is invisible to the eye, so a correct client always appends the newline itself rather than relying on the caller to remember it.
- The request line is capped at **1 MB**. A larger line is rejected with `ETOOLARGE` (see the error table below) rather than being read into memory.
- The connection has a **30-second idle timeout** on the daemon side. An `attach` stream past its header is held regardless: one attached to an idle session stayed open through 80 seconds without a single byte in either direction (CLI 2.1.263), so a client holding a live terminal needs no keepalive.
- The daemon rejects a connection whose peer uid differs from its own, before parsing anything at all, with `EPEERUID`. This is a defence against another local user connecting to a socket that happens to be reachable; it is not a substitute for the client verifying the socket's on-disk ownership before it ever dials (see the client ownership checks in `internal/daemon/client.go`).

Responses are a single JSON object per line, except `attach`, which answers with one JSON header line and then streams raw PTY bytes with no further framing until the connection closes.

Nothing separates that header from the first screen bytes but its `\n`. Whatever a single read returns past the newline is already the start of the terminal stream — usually the session's whole first repaint — and a client that parses the header and throws away the rest of that read loses the screen, which the daemon does not send again until the session redraws something. Whether a read holds both depends on the reader's timing, not on the daemon, so a fast client never meets it in testing. Measured against CLI 2.1.263 on a live session: a read issued immediately after the request returned the 197-byte header alone in 8 attaches of 8, while a read issued 20 or 200 milliseconds later returned the header followed by 1470 to 1526 bytes of screen in the same read, also in 8 of 8. A buffered reader that keeps what it has read past the line (Go's `bufio.Reader`, which `internal/daemon` uses) handles this by construction; a client that reads raw chunks must split them itself.

An attach connection whose reader falls behind is closed without a marker. The daemon destroys the connection outright once more than 1 MiB is queued for it (`writableLength > 1048576` in the 2.1.263 binary — read there, not observed live): no `EKICKED:`, no reason, only a close, which to the client is indistinguishable from the session exiting. A client that can stop reading for a while — a stalled interface thread, a bridge whose far end is slow — should not report a bare close as the session having ended with more certainty than that.

## 2. Envelope

A request is:

```json
{"proto": <integer>, "op": "<name>", "...": "op-specific fields"}
```

The operation key is `op`. `proto` must be an integer equal to the daemon's current protocol number, compared strictly — a string `"1"` does not satisfy it. A mismatch is rejected with `EPROTO`, which also carries the daemon's own current protocol number so a client can renegotiate.

`ping` is the one exception: it is handled before the proto check and takes no `proto` field at all. A client is expected to `ping` once, cache the protocol number the daemon reports, and use that cached number on every subsequent request — never a value baked into the client at build time, since that would break the moment the daemon's protocol number changes.

## 3. Operations

### `ping` — liveness and protocol negotiation

No auth, no `proto` field required.

```text
request:  {"op": "ping"}
response: {"ok": true, "op": "ping", "version": "<daemon version>", "proto": <integer>}
```

A reply that omits `proto`, or carries a non-numeric value for it, is a malformed reply and must be treated as an error by the client — never silently cached as protocol 0. A client that accepts `Info{Proto: 0}` from such a reply will re-ping on every subsequent call (0 is indistinguishable from "not yet negotiated"), roughly doubling request volume against a poller.

### `list` — the session list

No auth.

```text
request:  {"proto": <n>, "op": "list"}
response: {"ok": true, "op": "list", "jobs": [<job record>, ...]}
```

A job that is being killed or retired carries an extra `"dying": true`.

An error response (`"ok": false`) may or may not carry a `code` field. When it does not, the client must report a clean "unknown error" rather than a code-shaped error string with nothing after the colon.

### `reply` — deliver text into a session

**Requires auth.**

```text
request:  {"proto": <n>, "op": "reply", "short": "<short id>", "text": "<text>", "auth": "<control key>"}
response: {"ok": true, "op": "reply"}
```

Failure codes: `EAUTH` (missing or wrong key), `ENOJOB` (no such session), `ENOREPLY` (session is not accepting replies), `ERESPAWNING` (retry shortly).

`reply` always delivers and submits the text; there is no way to place text in a session's prompt without sending it. `internal/daemon`'s `Client.SendText` reflects this at the type level — it takes no `submit` parameter — rather than accepting one whose only legal value is `true` and refusing at runtime otherwise.

### `attach` — the interactive screen and keyboard

Auth is **optional** for `attach`: the daemon allows an attach with no `auth` field at all, relying on the peer-uid check instead, but it rejects a *wrong* key outright. Send the key when the caller has one; omit the field entirely when it does not. This is why reading (see the screen-reading use below) keeps working even when no control key is available, while anything that writes into the session must not proceed without one.

```text
request: {"proto": <n>, "op": "attach", "short": "<short id>", "cols": <int>, "rows": <int>, "attachId": "<optional, caller-chosen>"}
response header line:
  {"ok": true, "op": "attach", "imarkNonce": "...", "decModes": {...}, "via": "...",
   "booting": false, "tempo": "...", "state": "...", "cached": false, "stale": false,
   "workerCliVersion": "..."}
then: raw terminal bytes, streamed until the connection is closed.
```

`cols` and `rows` are required, and they resize the session. An attach sent without them, or with zeros, is answered `{"ok": false, "error": "malformed request: Invalid input", "code": "EUNKNOWN"}` and yields no screen at all. The value that is sent reaches the session's real PTY, so it is not private to the attacher that asked for it — verified by reading the session's own tty device rather than the frames the daemon sends, since a frame is rendered per attacher and shows nothing about the PTY behind it. Measured against CLI 2.1.263: a freshly created background session runs at 200x50; one attach at 80x24 leaves the PTY at 80x24; a viewer that stays attached at 190x45 puts it at 190x45, and every other attacher's stream becomes 190 columns wide at that moment. When an attacher disconnects, the daemon restores the size from whoever is still attached; with nobody left, the last size stays. A client that opens a fresh attach on a cadence therefore reshapes the session on every tick — with a 190-column viewer attached, eight seconds of once-a-second polling produced nine 190→80→190 transitions, against zero over the same span with the polling stopped.

The size a session is currently running at is reported nowhere: it is absent from a `list` record (see section 4) and from the attach header above. A client that wants to leave a session's geometry alone therefore has nothing to send — the only geometry it can name is its own.

`attachId` names this attacher to the daemon, which keys its attachers by it and lets a later `resize` be addressed to exactly this one (see `resize` below). A client that intends to resize an attach it keeps open must choose the id itself, keep it private, and make it unique per connection: the daemon answers a resize for an id it does not know with success and changes nothing. For the same reason it must stop resizing by that id the moment its attach connection ends — closed by the client, kicked, or ended by the session exiting — because from then on the id names nobody, and the daemon will keep answering success. `internal/daemon`'s `Attachment.Resize` refuses in that case with `ErrAttachmentClosed` rather than sending the request.

The stream carries the terminal modes the session has set, as ordinary escape sequences, so a terminal emulator on the client side picks them up without reading the header's `decModes`. Captured from a Claude Code session: `?1000h`, `?1002h`, `?1003h` and `?1006h` (the application tracks the mouse — a click-drag is reported to it rather than selecting text, and the wheel scrolls inside it), `?2004h` (bracketed paste), `?1004h` (focus events), `?2031h`, and `?2026` (synchronized output). `?1049` is absent: the session draws on the main screen, not the alternate one.

Three distinct uses are built on the same `attach` connection:

- **Holding a live terminal**: keep the connection open, hand bytes to a terminal emulator as they arrive, and write the operator's keystrokes into it. A keystroke's echo arrives in about ten milliseconds (9.6 to 21.3ms across five measurements through `internal/daemon`'s `Client.Attach`). Geometry after opening is changed with `resize`, never by reconnecting: a second attach would itself resize the session. A key is required to write, as below.
- **Reading the screen**: after the header, read the streamed bytes for a while (until the stream goes idle, or a byte cap is reached, or the caller's deadline fires) and return what arrived. No key is required for this.
- **Sending keys**: after the header, write key bytes into the connection. The daemon wires the connection's incoming bytes straight into the session's PTY writer. There is **no per-delivery acknowledgement** beyond the header — once the header is accepted, nothing in the protocol confirms that a specific byte sequence was received. A key is required for this, exactly like `reply`, because it is a write into someone's session.

  The only observable signal past the header is the connection closing, which happens when the session exits, when the daemon drops a reader that fell behind (section 1), or — on a Windows daemon only — when another attacher arrives and evicts this one (a "kick", section 8). On macOS a second attacher joins rather than evicts. The session exiting can be a *direct result* of the very keys just delivered (e.g. `/exit` and Enter). A connection that closes right after a successful write is therefore a **normal outcome, not a delivery failure**, and must not be reported as one: doing so invites a caller to retry, and a retry here means typing into a live session a second time. A write that itself fails, before any bytes are confirmed sent, is the only case that should be reported as "not delivered" — and even that must never be retried blindly, since a partial write to a stream socket is possible.

  Where the daemon evicts an attacher — a Windows daemon, when a new attach arrives (section 8) — it does so by writing a plain-text `EKICKED: ...` marker into the stream and then closing the connection, rather than a structured JSON message (the connection is long past the JSON header by that point). Both the screen-reading path and the key-sending path must recognise this marker and surface it as a distinct, typed error instead of treating the bytes as ordinary screen content, or the write that preceded it as a successful key delivery.

### `resize` — change one attacher's geometry

Its own connection, one request and one reply, like `reply`.

```text
request:  {"proto": <n>, "op": "resize", "short": "<short id>", "cols": <int>, "rows": <int>,
           "attachId": "<id the attach was opened with>"}
response: {"ok": true, "op": "resize"}
```

With an `attachId` the daemon updates that attacher's recorded size, resizes the session's PTY to it, and repaints that attacher's connection — which stays open. Without one it resizes the session directly. Measured against CLI 2.1.263 by reading the session's tty: an attach opened at 100x30 and resized by its id to 130x35 left the PTY at 130x35, and the held connection received a fresh frame (1675 bytes) without reconnecting.

- **No key is required.** A resize sent with no `auth` field took effect. A client sends the key when it has one all the same.
- **An unknown `attachId` is answered `{"ok": true}` and does nothing.** Measured: a resize addressed to an id no attacher holds left the PTY exactly as it was. The reply therefore confirms nothing about the effect; see `attachId` under `attach` for what a client has to do about that.
- **Zero is refused** the same way it is on `attach`: `{"ok": false, "error": "malformed request: Invalid input", "code": "EUNKNOWN"}`.

### `dispatch` — start a session, and the one way to resume a stopped one

**Requires auth.**

This is the only operation in this document that starts a process. Everything else reads a session, types into one, or resizes one; this one brings a session that is not running back into the daemon's own list, under its own short id, with its history.

```text
request:  {"proto": <n>, "op": "dispatch", "d": <descriptor>, "timeoutMs": <int>, "auth": "<control key>"}
response: {"ok": true, "op": "dispatch", "short": "<short id>"}
```

The descriptor, for a resume. Every key below was sent in the request that resumed a real stopped session on 2026-09-12 against CLI 2.1.263; nothing here is guessed from a shape that looked plausible.

```json
{
  "proto": 1,
  "short": "<the session's own short id>",
  "nonce": "<8 hex characters, fresh per dispatch>",
  "sessionId": "<the id the session is known by>",
  "createdAt": 1789200000000,
  "source": "fleet",
  "cwd": "<the directory the session ran in>",
  "launch": {
    "mode": "resume",
    "sessionId": "<the id to resume BY>",
    "fork": false,
    "flagArgs": ["--model", "..."],
    "transcriptPath": "<optional>"
  },
  "env": {},
  "isolation": "none",
  "respawnFlags": ["--model", "..."],
  "seed": {"intent": "<the last prompt>", "name": "<the session's name>"}
}
```

Five things about it are load-bearing, and four of them are silent when got wrong.

- **`launch.sessionId` is the id resumed *by*, which is not always the id in `sessionId`.** They are equal on an ordinary session and differ on one that was itself resumed from another — Claude Code's job store carries both, as `sessionId` and `resumeSessionId`. Both are UUIDs, so swapping them resumes a different conversation and nothing in the protocol objects.
- **`launch.fork` must be `false`.** True starts a second session beside the stopped one rather than bringing that one back.
- **`transcriptPath` is present or absent, never empty.** The daemon reads the key's presence; an empty string points the resumed worker at nothing instead of letting it find the transcript itself. Where it is absent, the worker looks the conversation up relative to `cwd`.
- **`flagArgs` and `respawnFlags` are the session's own command line** — its name, its model, its permission mode, its settings — and a resume that omits them brings the history back as a different session. The job store records them as `respawnFlags`.
- **`nonce` is fresh per dispatch.** Eight hex characters is the shape the daemon's other clients send.

**The reply says nothing about the session.** `{"ok": true}` means the daemon accepted the descriptor. The worker is started after it, and every way it can fail to start happens past that point — so a client that treats the reply as the answer reports success for a resume that never came up. The answer is in `list` afterwards: the session appears there, and a resume is done when it has held a state other than `""`, `"resuming"` and `"crashed"` for long enough to be more than a flicker.

**A resumed worker that dies is respawned, several times, before the daemon gives up.** Measured on 2026-09-12 by dispatching a resume that could not work — a session with no transcript at all (see `claude-jobs-store.md` §4 for why such a session still reads as resumable). The `list` record went to `state: "crashed"`, `detail: "exit 1; respawning"`, and stayed listed as live for tens of seconds while the daemon tried again, before the session settled back to not-running. Two consequences for a client:

- the first `crashed` reading is the answer, not a state to wait through: the daemon's respawns of a worker that cannot start are not going to produce a different outcome, and waiting out a full timeout only delays the report;
- nothing needs cleaning up afterwards. The daemon retires the session itself, and there is no operation in this protocol to retire one with — `claude stop` is a CLI command, not a socket op.

The cost is paid by the person watching: for as long as the respawns last, a session they asked to resume is listed as live and crashed. That is the argument for a client checking what it can before dispatching — an id to resume by, a working directory that still exists, a transcript that exists — rather than dispatching and relaying whatever comes back.

## 4. The job record

These are the keys a `list` reply can carry on a job record — eighteen in total. Not every record carries every optional key; a key that was never set for a given session is simply absent rather than present with an empty value.

```text
agent, attempt, backend, cliVersion, createdAt, cwd, detail, intent, name, needs, nonce,
pid, sessionId, short, source, startedAt, state, tempo
```

Notes on specific fields:

- **There is no `options` field anywhere in the protocol.** A session waiting on a multiple-choice question does not carry its choices as structured data; they are glued into `needs` as plain text (see below). Do not invent a structured type for this.
- `needs` is one pre-rendered, human-readable string. When a session is waiting on an actual question it looks like `"answer: <question> (A · B · C)"` or `"choose: <description>"` — the available options, when there are discrete ones, are glued into the text inside parentheses, separated by ` · `. This must be treated as an opaque string and never parsed into structured choices; the parentheses-and-dot format is a rendering choice on the daemon's side, not a contract. `needs` is also non-empty, independent of waiting, for sessions stopped on a reason no answer fixes — a closed vocabulary of prefixes such as `"usage limit reached ..."`, `"rate limited ..."`, `"login required ..."`, `"API error ..."` (see section 5). A non-empty `needs` on its own does **not** mean the session is waiting for a human answer; see the determination rule below.
- `detail` is the session's own short status line. On a session waiting via a rendered question in `needs`, it typically holds the same decision text without the glued-in choices. On a session stalled purely by `state`/`tempo` with an empty `needs` (see section 5, rule 2), `detail` is the only field that can still say whether the stop is actually a person-facing decision or just the session coordinating its own subagents — a client must show it verbatim in that case, not discard it because the session landed in the quiet counter.
- `intent` is the last prompt submitted into the session. It is not a question and must never be presented as one.
- There is no `id`, `title`, `label`, `status`, `kind`, `effect`, `streamTail`, `options`, `live`, `pinned`, or `resumable` field on the wire. Anything using those names is synthesised elsewhere, not carried by the socket.
- `pinned` is out of scope for this client entirely: carrying it through would add a fourth source of truth to a design that intentionally has three (daemon session state, transcript, board card).
- "Live" and "resumable", where a caller wants them, are derived rather than read: a session's mere presence in a `list` reply with no `dying` flag means it is alive.
- **A stopped session is not in the reply at all**, and nothing in the protocol reports one. It leaves no flag on its way out, so over this socket alone "stopped" and "never existed" are the same answer. They are not the same thing — a stopped session resumes with its full history — and telling them apart needs a second source: Claude Code's own job store under `~/.claude/jobs`, which is documented, with the dependency it creates, in [claude-jobs-store.md](claude-jobs-store.md).

## 5. Determining whether a session is waiting for a human, or merely stalled

A session that has stopped making progress is in one of two distinct states, and a client must not conflate them: **waiting**, where a person has to answer something before the session moves again, and **stalled**, where no answer helps — the session needs time, or an action outside this client entirely (a usage limit resetting, a login refresh, an upstream API error clearing, a rate limit expiring, or simply its own subagents still working). A counter that lumps both together, or that blinks on every rate limit the same way it does on a real question, teaches a person to stop trusting it — and under-reporting is the worse failure of the two, since it hides someone who is genuinely waiting on a person.

`state` and `tempo` are set by a mechanism the session does not control, and that mechanism cannot tell "waiting for a person" apart from "waiting on its own subagents" — both look identical in those two flags. Only `needs`, and where `needs` is empty `detail`, say anything in words about *why* the session stopped. The rule below is built on that fact: the flags alone can promote a session to **stalled**, never to **waiting**; only words can make it **waiting**.

The rule:

1. **`needs` non-empty — it decides, and the flags are never read.** A value matching the closed vocabulary below (a usage limit, a login prompt, an upstream API error, a rate limit) means **stalled**. Everything else, including a prefix this client has never seen before, means **waiting**.
2. **`needs` empty and `state == "blocked"` or `tempo == "blocked"` — stalled, never waiting.** With no words from the daemon, a bare flag cannot be told apart from a session that is merely coordinating its own subagents; it is never promoted to the counter a person is expected to act on.
3. **Everything else — neither.** The session is making ordinary progress.

Illustrative examples (not verbatim from any real session):

```text
tempo=blocked  state=working  needs="answer: Which colour should the probe use? (Red · Green · Blue)"   -> waiting  (rule 1)
tempo=active   state=blocked  needs=""                                                                   -> stalled  (rule 2)
tempo=blocked  state=blocked  needs="choose: (1) ... (2) ... (3) ..."                                    -> waiting  (rule 1: words outrank both flags)
```

The closed "no person needed" vocabulary — matched by prefix, case-sensitively, against the daemon's own wording, extracted from the installed CLI 2.1.263 binary:

```text
usage limit reached   (limit)
login required        (login)
API error              \
API overloaded          } (API error)
API unavailable        /
invalid API request   /
rate limited           (rate limit)
```

This list is closed **on purpose**. The vocabulary belongs to the daemon and will grow, and this client cannot know in advance whether a future prefix means "no person needed" or "someone must act". So an unfamiliar value is never guessed into the quiet counter — it always falls to **waiting**, the loud one. Calling someone unnecessarily is a cost that gets noticed and corrected on the spot; staying silent about a session that actually needed a person is a cost that is never noticed at all. The daemon also emits a few other non-question forms that this client deliberately does **not** treat as "no person needed" — `"account on hold ..."`, `"org disabled OAuth ..."`, and `"request too large ..."` (fixing the last one means a person running `/compact` inside the session) — so these fall to waiting under rule 1 above, same as any unrecognised value.

Waiting and stalled are mutually exclusive by construction under this rule: a session with `needs` non-empty lands in exactly one of the two depending on whether it matches the closed vocabulary; a session with `needs` empty can only be stalled (via the flags) or neither — never waiting.

This partition has one carve-out: a session carrying `"dying": true` (see section 4) is never waiting and never stalled, regardless of what `needs` or the flags say. A session being killed or retired needs nothing from anyone — there is no one left to answer a question or to wait on a rate limit for — so `dying` overrides the rule above rather than participating in it.

**A stalled-by-rule-2 session must still show its `detail` verbatim.** Once `needs` is empty, `detail` is the *only* field left that can distinguish "awaiting a decision from a person" from "awaiting my own work" — a client that hides it because the session landed in the quiet counter throws away the one piece of evidence a person could use to notice they are actually needed. This is a presentation obligation on whatever renders the session, not something `Waiting`/`Stalled` themselves can or should encode: the interface that honours it is out of scope for this document.

## 6. The control key

Path: `~/.claude/daemon/control.key`. Mode `0600`, owned by the user, inside a `0700` directory. Contents: 32 lowercase hex characters, read as UTF-8 and trimmed of whitespace. The client runs as the same user as the daemon, so an ordinary file read plus a trim is sufficient — no elevated privileges are involved.

Rules governing the key, binding on every code path that touches it:

- The key's value is never logged, never written to any snapshot or cache on disk, never returned from an API response, and never appears inside an error message. This is the same rule the daemon's own OAuth-adjacent credentials live under.
- When the key file is missing, empty, or unreadable, **every write path is disabled as a whole and visibly** — never silently degraded to a read-only mode that looks like it is working. The user-facing error is a fixed, generic message; it names neither the key file's path nor its contents.
- Reading is entirely unaffected by a missing key: `list`, `ping`, and reading the screen via `attach` all work with no key present.
- A client-side security check on the key file necessarily uses `lstat`, not `stat`, so it can refuse a symlink rather than following it. A symlink's own mode bits are conventionally world-readable/-writable (`0777`) on every platform regardless of what it points to, so a check that only inspects mode bits will refuse a symlinked key file for the wrong stated reason ("readable or writable by group or other") unless it detects the symlink case explicitly and says so in whatever internal (non-user-facing) diagnostic it keeps.

## 7. Error codes

| Code | Meaning |
| --- | --- |
| `EPROTO` | The request's `proto` does not match the daemon's current protocol number. |
| `EAUTH` | The `auth` field is missing or wrong for an operation that requires it. |
| `EPEERUID` | The connecting process's uid does not match the daemon's own. |
| `ETOOLARGE` | The request line exceeded the 1 MB cap. |
| `ENOJOB` | No session matches the given short id. |
| `ENOREPLY` | The session exists but is not currently accepting a `reply`. |
| `ERESPAWNING` | The session is mid-respawn; the caller should retry shortly. |
| `ESTARTING` | The daemon itself is still starting up. |
| (absent, or any other value) | An error the client has no specific mapping for. When `code` is present but unrecognised, report it verbatim; when `code` is absent entirely, report a clean, generic message rather than a code-shaped string with nothing after it. |

An error response may also carry an `error` field holding the daemon's own sentence about what went wrong. For most operations that sentence adds nothing the code does not already say, which is why the table above is the whole mapping. `dispatch` is the exception: its refusals are the only ones a client has no closed vocabulary for, and the sentence is what distinguishes a daemon that is restarting from a session that is already running. A client should carry it through for that one operation rather than flattening every refusal to its code.

## 8. Kick detection

Past the `attach` header, a `EKICKED: <reason>` marker written into the stream — instead of ordinary PTY bytes — means this attach connection has been evicted, either by another attacher taking over — which only a Windows daemon does, see below — or by the daemon itself. Both the read path and the key-delivery path built on `attach` must detect this marker and surface it as a distinct, typed error rather than treating the marker's bytes as if they were ordinary terminal output (read path), or treating a write that happened to land just before a kick as a confirmed, successful key delivery (key-delivery path).

The marker is written flush against whatever PTY bytes were already in flight — it is **not** anchored to the start of a line or to offset 0 of the stream. A terminal screen almost never ends with a trailing newline (the cursor sits wherever it sits: mid-escape-sequence, mid-prompt, mid-line), so a detector that only accepts the marker at offset 0 or immediately after a `\n` will miss it in the common case.

Detection must never fire on the marker's presence alone, or even on its *last occurrence* alone — only on it being the **last thing sent**, immediately before a close. "Last occurrence" and "last thing sent" are different things: the same literal text can legitimately appear earlier in an ordinary screen as unrelated content (this very document contains the string `EKICKED:` several times), and a session can display that text — for example by grepping this document — and then exit normally, with the marker's last occurrence sitting mid-screen, followed by more ordinary output and then a completely unrelated close. A detector that accepts the last occurrence plus a close fires on exactly that case, which is not a kick.

A real kick requires both of the following to hold; each is individually insufficient:

1. **The connection actually closed.** An ordinary, live, polled session's attach connection stays open indefinitely — it only closes on an actual kick or the session exiting — and may happen to display the literal marker text as part of its own screen content while remaining open. Without this, any screen containing the text at all would be misreported as a kick.
2. **What follows the marker's last occurrence looks like a short reason, not a screen**: no newline in it, and bounded in length (256 bytes is ample). The daemon's real reason text is the last thing it sends, so nothing of substance follows it. A screen that merely *displays* the marker almost always has more rendered content after it, typically containing at least one newline, or is simply too long to be a reason — the grep-and-exit case above is caught this way: the last occurrence of `EKICKED:` is immediately followed by ` marker\n$ exit\n`, which this condition rejects on the embedded newline.

A client should locate the marker's last occurrence relative to the end of the accumulated stream, not relative to a line boundary (the marker is written flush against whatever PTY bytes were already in flight, never anchored to offset 0 or a line boundary — see above), and validate what follows it (condition 2) before ever treating a close (condition 1) as evidence of a kick.

**An earlier version of this section listed a third condition — "the marker sits within a bounded window of the end of the stream" — as independently necessary, alongside a claim that "each has been tried alone and shown insufficient." That claim was false.** With the daemon's marker fixed at `EKICKED:` (8 bytes) and the reason-length ceiling at 256 bytes, condition 2 above already rejects any occurrence more than 264 bytes from the end of the stream — a tighter bound than any window a client might apply on top of it. A window of a few hundred bytes therefore never changes which streams are accepted as a kick; it can only ever agree with condition 2, never overrule it in either direction. A client implementation may still bound how far back it *searches* for the marker's last occurrence, purely as a performance guard against scanning a stream that can grow to a megabyte on every poll tick — but that bound is an implementation detail of locating the marker, not a fourth fact this document asserts about the protocol, and it must not be presented as a correctness condition the way conditions 1 and 2 are.

Where a kick happens at all: in CLI 2.1.263 the daemon evicts attachers in exactly one place, when a new attach arrives, and only when it runs on Windows — `if(P()==="windows")for(let D of r.attachers.values())D.kick();` is the binary's only call to `kick()`. Everywhere else a new attach joins the ones already open: on macOS two held attaches to one session were observed streaming side by side, with the geometry rule under `attach` deciding the size both of them see. A client still has to detect a kick, since it may meet a Windows daemon and the rule is the daemon's to change, but on macOS a kick is not the explanation for a close.

`EKICKED:` is one marker of a family, and the only one `internal/daemon` recognises. The same binary writes `ESTALLED: Session <short> keeps stalling at startup — ...` into an attach stream before killing a session that never finished starting, and elsewhere matches `ESTALLED|EUNVERIFIED|EHOSTDEAD` and `^E[A-Z]+:`. It also writes dimmed plain-text lines into the stream while a session has not repainted — `Waiting for session to redraw… Ctrl+Z to detach` and `Session is starting — it will appear once ready. Ctrl+Z to detach` — which name the detach key of `claude attach`, not of any other client. All of these reach a client that does not look for them as ordinary screen bytes, the markers followed by a close. Read from the binary, not observed live.

A real kick is a normal event — someone attached by hand and took over — not a failure, so a client should not discard the screen accumulated before the marker when reporting it: the bytes preceding the marker's position are still a valid, complete screen up to that point.

Condition 1 carries a deliberate, accepted residual risk. The daemon writes the marker and then closes the connection in close succession, but a client reading with a bounded ceiling (e.g. a 2-second screen-read deadline) can, in principle, receive the marker's bytes and hit its own ceiling before the resulting EOF is read. In that case, closed is never observed to be true, and the marker is reported as ordinary screen content rather than a kick. This is deliberately not addressed by treating an end-of-buffer marker as sufficient without an observed close — that reopens exactly the false-positive shape condition 2 exists to rule out. The risk is accepted, not eliminated, because it requires the close to be delayed relative to the marker by longer than the read ceiling, which is not how the daemon actually writes it.
