// The panel's write client. Reads never come through here: every module renders
// from the snapshot the socket pushes into store.js, so these three functions
// are the whole of what this page can change.
//
// application/json is mandatory on all three routes, not a habit: the server's
// guard answers 415 to anything else, and that requirement is one of the two
// layers standing between a page in another tab and a live Claude Code session
// (see internal/server/guard.go). Never send a body without this header.

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

async function refusal(response) {
  const body = await readJSON(response);
  const detail = typeof body?.error === "string" && body.error !== "" ? body.error : response.statusText;
  return new Error(detail || `HTTP ${response.status}`);
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
