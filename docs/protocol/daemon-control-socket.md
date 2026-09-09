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

```
request:  {"op": "ping"}
response: {"ok": true, "op": "ping", "version": "<daemon version>", "proto": <integer>}
```

A reply that omits `proto`, or carries a non-numeric value for it, is a malformed reply and must be treated as an error by the client — never silently cached as protocol 0. A client that accepts `Info{Proto: 0}` from such a reply will re-ping on every subsequent call (0 is indistinguishable from "not yet negotiated"), roughly doubling request volume against a poller.

### `list` — the session list

No auth.

```
request:  {"proto": <n>, "op": "list"}
response: {"ok": true, "op": "list", "jobs": [<job record>, ...]}
```

A job that is being killed or retired carries an extra `"dying": true`.

An error response (`"ok": false`) may or may not carry a `code` field. When it does not, the client must report a clean "unknown error" rather than a code-shaped error string with nothing after the colon.

### `reply` — deliver text into a session

**Requires auth.**

```
request:  {"proto": <n>, "op": "reply", "short": "<short id>", "text": "<text>", "auth": "<control key>"}
response: {"ok": true, "op": "reply"}
```

Failure codes: `EAUTH` (missing or wrong key), `ENOJOB` (no such session), `ENOREPLY` (session is not accepting replies), `ERESPAWNING` (retry shortly).

`reply` always delivers and submits the text; there is no way to place text in a session's prompt without sending it. A caller asking for unsubmitted text is asking for something the protocol cannot do, and that must be reported as such rather than silently submitting anyway.

### `attach` — the interactive screen and keyboard

Auth is **optional** for `attach`: the daemon allows an attach with no `auth` field at all, relying on the peer-uid check instead, but it rejects a *wrong* key outright. Send the key when the caller has one; omit the field entirely when it does not. This is why reading (see the screen-reading use below) keeps working even when no control key is available, while anything that writes into the session must not proceed without one.

```
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

```
agent, attempt, backend, cliVersion, createdAt, cwd, detail, intent, name, needs, nonce,
pid, sessionId, short, source, startedAt, state, tempo
```

Notes on specific fields:

- **There is no `options` field anywhere in the protocol.** A session waiting on a multiple-choice question does not carry its choices as structured data; they are glued into `needs` as plain text (see below). Do not invent a structured type for this.
- `needs` is one pre-rendered, human-readable string. When a session is waiting it looks like `"answer: <question> (A · B · C)"` or `"choose: <description>"` — the available options, when there are discrete ones, are glued into the text inside parentheses, separated by ` · `. This must be treated as an opaque string and never parsed into structured choices; the parentheses-and-dot format is a rendering choice on the daemon's side, not a contract. `needs` is also non-empty, independent of waiting, for sessions that are merely rate-limited or need a login refresh (forms like "usage limit reached ...", "rate limited ...", "login required ..."). A non-empty `needs` on its own does **not** mean the session is waiting for a human answer; see the waiting forms below.
- `detail` is the session's own short status line. On a waiting session it typically holds the question or decision text without the glued-in choices.
- `intent` is the last prompt submitted into the session. It is not a question and must never be presented as one.
- There is no `id`, `title`, `label`, `status`, `kind`, `effect`, `streamTail`, `options`, `live`, `pinned`, or `resumable` field on the wire. Anything using those names is synthesised elsewhere, not carried by the socket.
- `pinned` is out of scope for this client entirely: carrying it through would add a fourth source of truth to a design that intentionally has three (daemon session state, transcript, board card).
- "Live" and "resumable", where a caller wants them, are derived rather than read: a session's mere presence in a `list` reply with no `dying` flag means it is alive.

## 5. Determining whether a session is waiting for a human

Three forms have been observed on a live daemon (all examples below are illustrative, not verbatim from any real session):

```
tempo=blocked  state=working  needs="answer: Which colour should the probe use? (Red · Green · Blue)"
tempo=active   state=blocked  needs=""    detail="awaiting user decision on a dependency version"
tempo=blocked  state=blocked  needs="choose: (1) ... (2) ... (3) ..."
```

The rule this implies: **a session is waiting when `state == "blocked"` OR `tempo == "blocked"`.** Neither field is sufficient on its own — the second form above has `tempo="active"` and is still waiting (via `state`); the first and third forms have a `state` value other than "blocked" and are still waiting (via `tempo`). `needs` being non-empty is neither necessary (form 2 has an empty `needs`) nor sufficient (a rate-limited or login-required session has a non-empty `needs` and is not waiting for a human decision) as a standalone signal, and must not be used as one.

## 6. The control key

Path: `~/.claude/daemon/control.key`. Mode `0600`, owned by the user, inside a `0700` directory. Contents: 32 lowercase hex characters, read as UTF-8 and trimmed of whitespace. The client runs as the same user as the daemon, so an ordinary file read plus a trim is sufficient — no elevated privileges are involved.

Rules governing the key, binding on every code path that touches it:

- The key's value is never logged, never written to any snapshot or cache on disk, never returned from an API response, and never appears inside an error message. This is the same rule the daemon's own OAuth-adjacent credentials live under.
- When the key file is missing, empty, or unreadable, **every write path is disabled as a whole and visibly** — never silently degraded to a read-only mode that looks like it is working. The user-facing error is a fixed, generic message; it names neither the key file's path nor its contents.
- Reading is entirely unaffected by a missing key: `list`, `ping`, and reading the screen via `attach` all work with no key present.

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
