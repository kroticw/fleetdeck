# The daemon control socket protocol

This document describes the wire protocol implemented by `internal/daemon`. It replaces an earlier addendum that lived under a gitignored directory and was therefore invisible in a public checkout of this repository. Everything below reflects the daemon as it actually behaves, verified against a live socket, not the shape it was originally guessed to have.

No real path, session name, session id, or timestamp appears anywhere in this document. Every example below is illustrative.

## 1. Transport and framing

The socket is a Unix domain socket. One client connection carries one request and (for most operations) one response.

- The daemon reads bytes from the connection until the **first `\n`**. That line is the whole JSON request; anything after the newline is a payload tail (relevant only to `attach`, see below).
- A request sent without a trailing newline is never parsed: the daemon stays silent until its own idle timeout fires. This failure is invisible to the eye, so a correct client always appends the newline itself rather than relying on the caller to remember it.
- The request line is capped at **1 MB**. A larger line is rejected with `ETOOLARGE` (see the error table below) rather than being read into memory.
- The connection has a **30-second idle timeout** on the daemon side.
- The daemon rejects a connection whose peer uid differs from its own, before parsing anything at all, with `EPEERUID`. This is a defence against another local user connecting to a socket that happens to be reachable; it is not a substitute for the client verifying the socket's on-disk ownership before it ever dials (see the client ownership checks in `internal/daemon/client.go`).

Responses are a single JSON object per line, except `attach`, which answers with one JSON header line and then streams raw PTY bytes with no further framing until the connection closes.

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

`reply` always delivers and submits the text; there is no way to place text in a session's prompt without sending it. A caller asking for unsubmitted text is asking for something the protocol cannot do, and that must be reported as such rather than silently submitting anyway.

### `attach` — the interactive screen and keyboard

Auth is **optional** for `attach`: the daemon allows an attach with no `auth` field at all, relying on the peer-uid check instead, but it rejects a *wrong* key outright. Send the key when the caller has one; omit the field entirely when it does not. This is why reading (see the screen-reading use below) keeps working even when no control key is available, while anything that writes into the session must not proceed without one.

```text
request: {"proto": <n>, "op": "attach", "short": "<short id>", "cols": <int>, "rows": <int>}
response header line:
  {"ok": true, "op": "attach", "imarkNonce": "...", "decModes": {...}, "via": "...",
   "booting": false, "tempo": "...", "state": "...", "cached": false, "stale": false,
   "workerCliVersion": "..."}
then: raw terminal bytes, streamed until the connection is closed.
```

Two distinct uses are built on the same `attach` connection:

- **Reading the screen**: after the header, read the streamed bytes for a while (until the stream goes idle, or a byte cap is reached, or the caller's deadline fires) and return what arrived. No key is required for this.
- **Sending keys**: after the header, write key bytes into the connection. The daemon wires the connection's incoming bytes straight into the session's PTY writer. There is **no per-delivery acknowledgement** beyond the header — once the header is accepted, nothing in the protocol confirms that a specific byte sequence was received. A key is required for this, exactly like `reply`, because it is a write into someone's session.

  The only observable signal past the header is the connection closing, which happens when another attacher takes over (a "kick") or when the session exits — both of which can happen *as a direct result* of the very keys just delivered (e.g. pressing Enter ends the session's current turn). A connection that closes right after a successful write is therefore a **normal outcome, not a delivery failure**, and must not be reported as one: doing so invites a caller to retry, and a retry here means typing into a live session a second time. A write that itself fails, before any bytes are confirmed sent, is the only case that should be reported as "not delivered" — and even that must never be retried blindly, since a partial write to a stream socket is possible.

  The daemon evicts an existing attacher by writing a plain-text `EKICKED: ...` marker into the stream and then closing the connection, rather than a structured JSON message (the connection is long past the JSON header by that point). Both the screen-reading path and the key-sending path must recognise this marker and surface it as a distinct, typed error instead of treating the bytes as ordinary screen content, or the write that preceded it as a successful key delivery.

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

## 8. Kick detection

Past the `attach` header, a `EKICKED: <reason>` marker written into the stream — instead of ordinary PTY bytes — means this attach connection has been evicted, either by another attacher taking over or by the daemon itself. Both the read path and the key-delivery path built on `attach` must detect this marker and surface it as a distinct, typed error rather than treating the marker's bytes as if they were ordinary terminal output (read path), or treating a write that happened to land just before a kick as a confirmed, successful key delivery (key-delivery path).

The marker is written flush against whatever PTY bytes were already in flight — it is **not** anchored to the start of a line or to offset 0 of the stream. A terminal screen almost never ends with a trailing newline (the cursor sits wherever it sits: mid-escape-sequence, mid-prompt, mid-line), so a detector that only accepts the marker at offset 0 or immediately after a `\n` will miss it in the common case.

Detection must never fire on the marker's presence alone, or even on its *last occurrence* alone — only on it being the **last thing sent**, immediately before a close. "Last occurrence" and "last thing sent" are different things: the same literal text can legitimately appear earlier in an ordinary screen as unrelated content (this very document contains the string `EKICKED:` several times), and a session can display that text — for example by grepping this document — and then exit normally, with the marker's last occurrence sitting mid-screen, followed by more ordinary output and then a completely unrelated close. A detector that accepts the last occurrence plus a close fires on exactly that case, which is not a kick.

A real kick requires both of the following to hold; each is individually insufficient:

1. **The connection actually closed.** An ordinary, live, polled session's attach connection stays open indefinitely — it only closes on an actual kick or the session exiting — and may happen to display the literal marker text as part of its own screen content while remaining open. Without this, any screen containing the text at all would be misreported as a kick.
2. **What follows the marker's last occurrence looks like a short reason, not a screen**: no newline in it, and bounded in length (256 bytes is ample). The daemon's real reason text is the last thing it sends, so nothing of substance follows it. A screen that merely *displays* the marker almost always has more rendered content after it, typically containing at least one newline, or is simply too long to be a reason — the grep-and-exit case above is caught this way: the last occurrence of `EKICKED:` is immediately followed by ` marker\n$ exit\n`, which this condition rejects on the embedded newline.

A client should locate the marker's last occurrence relative to the end of the accumulated stream, not relative to a line boundary (the marker is written flush against whatever PTY bytes were already in flight, never anchored to offset 0 or a line boundary — see above), and validate what follows it (condition 2) before ever treating a close (condition 1) as evidence of a kick.

**An earlier version of this section listed a third condition — "the marker sits within a bounded window of the end of the stream" — as independently necessary, alongside a claim that "each has been tried alone and shown insufficient." That claim was false.** With the daemon's marker fixed at `EKICKED:` (8 bytes) and the reason-length ceiling at 256 bytes, condition 2 above already rejects any occurrence more than 264 bytes from the end of the stream — a tighter bound than any window a client might apply on top of it. A window of a few hundred bytes therefore never changes which streams are accepted as a kick; it can only ever agree with condition 2, never overrule it in either direction. A client implementation may still bound how far back it *searches* for the marker's last occurrence, purely as a performance guard against scanning a stream that can grow to a megabyte on every poll tick — but that bound is an implementation detail of locating the marker, not a fourth fact this document asserts about the protocol, and it must not be presented as a correctness condition the way conditions 1 and 2 are.

A real kick is a normal event — someone attached by hand and took over — not a failure, so a client should not discard the screen accumulated before the marker when reporting it: the bytes preceding the marker's position are still a valid, complete screen up to that point.

Condition 1 carries a deliberate, accepted residual risk. The daemon writes the marker and then closes the connection in close succession, but a client reading with a bounded ceiling (e.g. a 2-second screen-read deadline) can, in principle, receive the marker's bytes and hit its own ceiling before the resulting EOF is read. In that case, closed is never observed to be true, and the marker is reported as ordinary screen content rather than a kick. This is deliberately not addressed by treating an end-of-buffer marker as sufficient without an observed close — that reopens exactly the false-positive shape conditions 2 and 3 exist to rule out. The risk is accepted, not eliminated, because it requires the close to be delayed relative to the marker by longer than the read ceiling, which is not how the daemon actually writes it.
