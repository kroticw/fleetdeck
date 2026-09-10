// The panel's write client, and the two reads that cannot come from the
// snapshot.
//
// Almost every module renders from the snapshot the socket pushes into
// store.js. The session panel is the exception: a transcript digest and a
// terminal screen are far too large to push to every open tab once a second,
// and are wanted only while somebody is looking at one session, so they are
// fetched here on demand — see fetchDigest and fetchScreen at the bottom.
//
// application/json is mandatory on every route with a body, not a habit: the
// server's guard answers 415 to anything else, and that requirement is one of
// the two layers standing between a page in another tab and a live Claude Code
// session (see internal/server/guard.go). Never send a body without this
// header.

const JSON_HEADERS = { "Content-Type": "application/json" };

// readJSON never throws. A 204 has no body at all and a failing route may answer
// with something that is not JSON, and in both cases the caller has a better
// answer than an exception raised while reading an answer it already has.
async function readJSON(response) {
  try {
    return await response.json();
  } catch {
    return null;
  }
}

// messageFor decides the words a refusal is reported with: the server's own
// error text when it sent one, its status line otherwise. A route the server
// does not register falls through to the static file server, whose 404 body is
// plain text and parses as nothing — the status line is then all there is, and
// it is still words rather than "undefined".
//
// Split out from refusal because a response body can only be read once, and
// fetchScreen needs both the body and the message from a single read.
function messageFor(response, body) {
  const detail = typeof body?.error === "string" && body.error !== "" ? body.error : response.statusText;
  return detail || `HTTP ${response.status}`;
}

async function refusal(response) {
  return new Error(messageFor(response, await readJSON(response)));
}

// setCardField writes one frontmatter field of one card and reports which of the
// server's two successes happened.
//
// PATCH /api/cards answers 204 when the field was written and committed (or when
// the card already held the value, which needed no commit), and 200 with
// {written, committed, reason} when the field reached the file but the git commit
// did not happen — a signing prompt on a cold gpg-agent, say. Both are successes
// and res.ok is true for both, which is precisely why this returns a shape rather
// than nothing: the panel has to tell the operator that the edit applied and did
// not reach the history, and it must not invite a retry, because retrying a
// progress edit applies it twice (spec section 7).
//
// Everything else throws, carrying the server's own error text. A thrown error
// means nothing was written.
export async function setCardField(path, field, value) {
  const response = await fetch("/api/cards", {
    method: "PATCH",
    headers: JSON_HEADERS,
    // The route takes three strings; progress arrives here as a number from a
    // snapshot and as a string from a select, and the server rejects a number.
    body: JSON.stringify({ path: String(path), field: String(field), value: String(value) }),
  });
  if (response.status === 204) {
    return { committed: true };
  }
  if (response.status === 200) {
    const body = await readJSON(response);
    // A 200 whose body could not be read is treated as not committed. That is
    // the conservative reading: the server only ever answers 200 to say a commit
    // did not happen, and claiming a commit we cannot see is the one mistake
    // here that leaves the operator believing the board's history is complete.
    return { committed: body?.committed === true, reason: String(body?.reason ?? "") };
  }
  throw await refusal(response);
}

async function post(url, body) {
  const response = await fetch(url, {
    method: "POST",
    headers: JSON_HEADERS,
    body: JSON.stringify(body),
  });
  if (!response.ok) {
    throw await refusal(response);
  }
}

// sendText types text into a session and sends it.
//
// submit defaults to true and there is no working way to pass false: the daemon's
// reply operation always delivers and submits, so the server refuses an explicit
// submit:false with 400 rather than accepting a promise it cannot keep. The
// parameter exists because the call site reads better with the submission stated.
export function sendText(sessionId, text, submit = true) {
  return post(`/api/sessions/${encodeURIComponent(sessionId)}/text`, { text, submit });
}

export function sendKeys(sessionId, keys) {
  return post(`/api/sessions/${encodeURIComponent(sessionId)}/keys`, { keys });
}

// setOrchestratorSession pins, or unpins, the session the left column shows.
//
// An empty id is a legal value and means "no session pinned": the route takes a
// pointer, so an absent key is a 400 and an explicit empty string is the unpin.
// Which is why this sends the key unconditionally rather than omitting it.
export async function setOrchestratorSession(id) {
  const response = await fetch("/api/config", {
    method: "PATCH",
    headers: JSON_HEADERS,
    body: JSON.stringify({ orchestratorSession: String(id ?? "") }),
  });
  if (!response.ok) {
    throw await refusal(response);
  }
}

// fetchDigest returns a session's most recent readable steps, oldest first, as
// {role, text, at} (internal/transcript.Step).
//
// It throws when the transcript cannot be read, carrying the server's own words
// — the route answers 404 with {"error": "transcript not found: …"} for a
// session that has left no transcript. That has to reach the operator: an empty
// pane is indistinguishable from a session that is simply quiet, which is the
// same reason the route refuses to answer an unreadable transcript with an
// empty list.
export async function fetchDigest(sessionId, limit) {
  const response = await fetch(
    `/api/sessions/${encodeURIComponent(sessionId)}/digest?limit=${encodeURIComponent(limit)}`,
  );
  if (!response.ok) {
    throw await refusal(response);
  }
  const steps = await readJSON(response);
  return Array.isArray(steps) ? steps : [];
}

// fetchScreen reads the tail of a session's terminal.
//
// Alone among these, it returns its failure instead of throwing it, because the
// server deliberately sends both: a read that ends in an eviction still carries
// every byte that arrived before it failed, and that prefix is often exactly
// what the operator was looking at (internal/server/api.go, handleScreen).
// Throwing would discard it. The caller gets {screen, error} and shows both.
//
// No ?tail= is sent. It is a byte count, and how many bytes a terminal screen
// costs is a fact the server already holds (defaultTailBytes, 64 KiB); naming a
// number here would be a second copy of it, free to drift — and a number chosen
// as though it were a line count would silently truncate the screen to a
// fragment.
export async function fetchScreen(sessionId) {
  const response = await fetch(`/api/sessions/${encodeURIComponent(sessionId)}/screen`);
  const body = await readJSON(response);
  const screen = typeof body?.screen === "string" ? body.screen : "";
  if (response.ok) {
    return { screen, error: "" };
  }
  return { screen, error: messageFor(response, body) };
}
