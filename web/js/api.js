// The panel's write client, and the reads that cannot come from the snapshot.
//
// Almost every module renders from the snapshot the socket pushes into
// store.js. The session panel is the exception: the cards a session has worked
// on include the ones archived off the board, which the snapshot never reads,
// and are wanted only while somebody is looking at one session, so they are
// fetched here on demand — see fetchSessionCards at the bottom. A session's
// screen is not read here at all: it is the live terminal
// (web/js/liveterminal.js).
//
// application/json is mandatory on every route with a body, not a habit: the
// server's guard answers 415 to anything else, and that requirement is one of
// the two layers standing between a page in another tab and a live Claude Code
// session (see internal/server/guard.go). Never send a body without this
// header.

import { withFleet, fleetFromSearch } from "./fleet.js";
import { langCode } from "./i18n.js";

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
function messageFor(response, body) {
  const detail = typeof body?.error === "string" && body.error !== "" ? body.error : response.statusText;
  return detail || `HTTP ${response.status}`;
}

// refusal is the error a failed route throws. A refusal by one of the board's
// rules comes with a code beside the words, and the code rides on the error
// for the panel to translate by; without one the property is absent, not
// empty, so a caller can tell "no code" from a code it cannot read.
async function refusal(response) {
  const body = await readJSON(response);
  const err = new Error(messageFor(response, body));
  if (typeof body?.code === "string" && body.code !== "") err.code = body.code;
  return err;
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

// inFleet is a panel path in the fleet this tab's address names (see
// fleet.js): a card started or edited and an orchestrator pinned land in that
// fleet, which the server reads from the same parameter as the snapshot. No
// fleet in the address leaves the path as it always was.
export function inFleet(path) {
  return withFleet(path, fleetFromSearch(globalThis.location?.search ?? ""));
}

// attachmentURL is where a card's link to one of its attachments is served:
// the card writes it as ../attachments/T-NNN/<name>, relative to itself. Any
// other target is not an attachment, and gets null.
export function attachmentURL(src) {
  const rest = String(src).match(/^\.\.\/attachments\/(.+)$/)?.[1];
  return rest ? inFleet(`/api/attachments?path=${encodeURIComponent(rest)}`) : null;
}

export async function setCardField(path, field, value, expect) {
  const body = { path: String(path), field: String(field), value: String(value) };
  // Sent only when the caller named one: the key is a pointer on the far side,
  // and an absent one is "write it whatever the card holds" — which is what
  // every edit but a drag means.
  if (expect !== undefined && expect !== null) body.expect = String(expect);
  // The page's language: a stage set by hand is announced to the session
  // keeping the card in it.
  body.lang = langCode;
  const response = await fetch(inFleet("/api/cards"), {
    method: "PATCH",
    headers: JSON_HEADERS,
    // The route takes strings; progress arrives here as a number from a
    // snapshot and as a string from a select, and the server rejects a number.
    body: JSON.stringify(body),
  });
  if (response.status === 204) {
    return { committed: true };
  }
  if (response.status === 200) {
    const body = await readJSON(response);
    // A 200 whose body could not be read is treated as not committed. That is
    // the conservative reading: the server only ever answers 200 to say
    // something did not go plainly, and claiming a commit we cannot see is the
    // one mistake here that leaves the operator believing the board's history
    // is complete.
    //
    // steps are what the server did around the write — the agent told its card
    // moved, the session behind an accepted card tidied away. Always an array,
    // so a caller never has to tell an answer without the key from one with an
    // empty list; both mean nothing happened worth reporting.
    return {
      committed: body?.committed === true,
      reason: String(body?.reason ?? ""),
      steps: Array.isArray(body?.steps) ? body.steps : [],
    };
  }
  throw await refusal(response);
}

// createCard starts a card on the board from a title, a zone, the repository
// its worker starts in and a description of the task — the fields the route
// takes, and nothing else. An empty repo is a card without one, an empty
// description a card with only its title. It answers {path, committed, reason}: 201
// both when the card was committed and when it reached the board without its
// commit, because the card exists either way and creating it again would make a
// second one. A thrown error means no card was made.
export async function createCard(title, zone, repo = "", description = "", attachments = []) {
  const card = { title: String(title), zone: String(zone), repo: String(repo), description: String(description) };
  // Only when there are any: {name, data} with data in base64.
  if (attachments.length > 0) card.attachments = attachments;
  const response = await fetch(inFleet("/api/cards"), {
    method: "POST",
    headers: JSON_HEADERS,
    body: JSON.stringify(card),
  });
  if (response.status !== 201) {
    throw await refusal(response);
  }
  const body = await readJSON(response);
  // As with setCardField: a commit that cannot be seen is not claimed.
  return {
    path: String(body?.path ?? ""),
    committed: body?.committed === true,
    reason: String(body?.reason ?? ""),
  };
}

// pickDirectory asks the panel for the Finder's folder dialog, prompt as its
// title, and answers the folder as a card's repo holds it, "" when the dialog
// was cancelled. The page cannot ask this of the browser: a page is never told
// where a folder it was given lives. It waits for as long as the dialog is up.
export async function pickDirectory(prompt) {
  const response = await fetch("/api/pick-directory", {
    method: "POST",
    headers: JSON_HEADERS,
    body: JSON.stringify({ prompt: String(prompt) }),
  });
  if (response.status !== 200) {
    throw await refusal(response);
  }
  const body = await readJSON(response);
  return String(body?.repo ?? "");
}

// startWork starts a session for one card and hands the card to it: the
// session comes up with no prompt, its short id is written into the card, and
// the task is sent after that. The order is the server's to keep (see
// internal/orchestrator.Dispatcher), and it is the reason this is one call
// rather than the page starting a session and then writing the card.
//
// Like resumeSession it can take the better part of a minute — a session is
// started and waited for — and the caller must show that it is waiting. It
// resolves with the steps the dispatch took; it throws with the server's own
// words, and a throw can still leave a session running, which is why those
// words have to reach the operator rather than a retry.
export async function startWork(card) {
  const response = await fetch(inFleet("/api/sessions"), {
    method: "POST",
    headers: JSON_HEADERS,
    // The page's language: the session is sent its card in it, as the
    // orchestrator is sent its working order in the wizard's.
    body: JSON.stringify({ card: String(card), lang: langCode }),
  });
  if (!response.ok) {
    throw await refusal(response);
  }
  return (await readJSON(response)) ?? {};
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

// resumeSession brings a stopped session back, under its own short id and with
// its history, and resolves only once it is actually up.
//
// It is addressed by short id, not by the transcript UUID the label route
// takes: a stopped session is one the daemon is no longer listing, and the
// short id is what Claude Code's job store — the only source that still knows
// anything about it — names its directory by.
//
// It sends an empty object rather than no body at all. There is nothing to say
// beyond the id in the path, but the server's guard requires application/json
// on every POST, and a POST with a content type and no body is a shape not
// worth being clever about.
//
// This one call can take the better part of a minute: the panel does not
// answer until it has watched the session come up or fail to, because a resume
// reported as "started" and then silently dead is exactly the silence the
// button replaces. The caller must show that it is waiting.
export function resumeSession(short) {
  return post(`/api/sessions/${encodeURIComponent(short)}/resume`, {});
}

// setOrchestratorSession pins, or unpins, the session the left column shows.
//
// An empty id is a legal value and means "no session pinned": the route takes a
// pointer, so an absent key is a 400 and an explicit empty string is the unpin.
// Which is why this sends the key unconditionally rather than omitting it.
export async function setOrchestratorSession(id) {
  const response = await fetch(inFleet("/api/config"), {
    method: "PATCH",
    headers: JSON_HEADERS,
    body: JSON.stringify({ orchestratorSession: String(id ?? "") }),
  });
  if (!response.ok) {
    throw await refusal(response);
  }
}

// setSessionLabel writes, or given an empty label removes, the operator's own
// name for one session. sessionId is the transcript UUID
// (internal/server's route is PATCH /api/sessions/{id}/label — never the
// daemon's short id, which is reassigned on every restart and cannot durably
// name anything).
//
// An empty label is a legal value and means "forget this session's name": the
// route takes a pointer, so an absent key is a 400 and an explicit empty
// string is the removal. Which is why this sends the key unconditionally
// rather than omitting it, the same reasoning setOrchestratorSession's own
// comment gives.
export async function setSessionLabel(sessionId, label) {
  const response = await fetch(`/api/sessions/${encodeURIComponent(sessionId)}/label`, {
    method: "PATCH",
    headers: JSON_HEADERS,
    body: JSON.stringify({ label: String(label ?? "") }),
  });
  if (!response.ok) {
    throw await refusal(response);
  }
}

// fetchSessionCards returns the cards a session has worked on, oldest first, as
// {id, title, stage, created, path, archived} (internal/board.SessionCard). It
// is addressed by the daemon's short id, which is what a card's session field
// holds, and in the fleet the tab's address names, whose board it reads.
//
// It throws when the board cannot be read, carrying the server's own words: a
// history that could not be read has to look different from a session that
// took no card, and an empty list is what the second one looks like.
export async function fetchSessionCards(short) {
  const response = await fetch(inFleet(`/api/sessions/${encodeURIComponent(short)}/cards`));
  if (!response.ok) {
    throw await refusal(response);
  }
  const cards = await readJSON(response);
  return Array.isArray(cards) ? cards : [];
}

// fetchTerminalToken reads the token a terminal socket must send as its first
// message (internal/server/pty.go, authenticateTerminal).
//
// Once per socket, never once per page: the token lives exactly as long as the
// panel's process, so a page that kept the one it read at load would be refused
// by every terminal it opened after the panel restarted, until somebody thought
// to reload the page. no-store for the same reason — a cached answer is an old
// process's token.
//
// It throws rather than returning an empty token: a socket opened with nothing
// to prove is refused anyway, and saying why here is clearer than a refusal
// from the other end.
export async function fetchTerminalToken() {
  const response = await fetch("/api/terminal-token", { cache: "no-store" });
  if (!response.ok) {
    throw await refusal(response);
  }
  const body = await readJSON(response);
  if (typeof body?.token !== "string" || body.token === "") {
    throw new Error("the panel answered without a terminal token");
  }
  return body.token;
}
