// The write client. Its whole job is to keep the server's two different
// successes apart, so most of this file is about the difference between a 204
// and a 200.

import { test, beforeEach, afterEach } from "node:test";
import assert from "node:assert/strict";

import { setCardField, resumeSession, fetchSessionCards, fetchTerminalToken, createCard, pickDirectory, attachmentURL, setOrchestratorSession, startWork } from "../js/api.js";
import { langCode } from "../js/i18n.js";

let calls = [];
let realFetch;

function answer({ status, body, statusText = "" }) {
  return {
    status,
    ok: status >= 200 && status < 300,
    statusText,
    async json() {
      if (body === undefined) throw new SyntaxError("Unexpected end of JSON input");
      return body;
    },
  };
}

function stubFetch(response) {
  globalThis.fetch = async (url, init) => {
    calls.push({ url, init });
    return typeof response === "function" ? response(url, init) : response;
  };
}

beforeEach(() => {
  calls = [];
  realFetch = globalThis.fetch;
});

afterEach(() => {
  globalThis.fetch = realFetch;
});

// The page's language goes with every write: a stage set by hand is announced
// to the session keeping the card in it.
test("a card write with no expectation is a PATCH of its strings and the page's language, declared as JSON", async () => {
  stubFetch(answer({ status: 204 }));

  // progress arrives from a snapshot as a number; the route takes a string.
  await setCardField("/board/fleet-ui.md", "progress", 60);

  assert.equal(calls.length, 1);
  assert.equal(calls[0].url, "/api/cards");
  assert.equal(calls[0].init.method, "PATCH");
  // Without this header the server's guard answers 415 and nothing is written.
  assert.equal(calls[0].init.headers["Content-Type"], "application/json");
  assert.deepEqual(JSON.parse(calls[0].init.body), {
    path: "/board/fleet-ui.md",
    field: "progress",
    value: "60",
    lang: langCode,
  });
});

// The drag is the one write made against a picture of the card rather than
// against the card: the board is drawn from a snapshot up to a second old.
test("a card write made against a stage the board drew carries it as the expectation", async () => {
  stubFetch(answer({ status: 204 }));
  await setCardField("/board/fleet-ui.md", "stage", "review", "active");
  assert.deepEqual(JSON.parse(calls[0].init.body), {
    path: "/board/fleet-ui.md",
    field: "stage",
    value: "review",
    expect: "active",
    lang: langCode,
  });
});

// An empty session is a value a caller may legitimately expect, so it has to
// be told apart from naming no expectation at all.
test("an empty expectation is sent, and only an absent one is left out", async () => {
  stubFetch(answer({ status: 204 }));
  await setCardField("/board/c.md", "session", "abc12345", "");
  assert.equal(JSON.parse(calls[0].init.body).expect, "");
  stubFetch(answer({ status: 204 }));
  await setCardField("/board/c.md", "session", "abc12345");
  assert.ok(!("expect" in JSON.parse(calls[1].init.body)));
});

test("starting a session for a card names the card and the page's language", async () => {
  stubFetch(answer({ status: 200, body: { ok: true, session: "abc12345", steps: [] } }));
  const result = await startWork("/board/c.md");
  assert.equal(calls[0].url, "/api/sessions");
  assert.equal(calls[0].init.method, "POST");
  assert.equal(calls[0].init.headers["Content-Type"], "application/json");
  assert.deepEqual(JSON.parse(calls[0].init.body), { card: "/board/c.md", lang: langCode });
  assert.equal(result.session, "abc12345");
});

// A refusal can still leave a session running, so the server's own words have
// to reach the operator rather than a retry.
test("a refused dispatch throws with the server's own words", async () => {
  stubFetch(answer({ status: 409, body: { error: "the card already names a session: deadbeef" } }));
  await assert.rejects(() => startWork("/board/c.md", "claude"), /already names a session/);
});

test("204 is a plain success: written and committed", async () => {
  stubFetch(answer({ status: 204 }));
  assert.deepEqual(await setCardField("/board/fleet-ui.md", "stage", "review"), {
    committed: true,
  });
});

test("200 is a success with a caveat: written, not committed, with a reason", async () => {
  stubFetch(
    answer({
      status: 200,
      body: { written: true, committed: false, reason: "git commit timed out after 5s" },
    }),
  );

  const result = await setCardField("/board/fleet-ui.md", "stage", "review");

  assert.equal(result.committed, false);
  assert.equal(result.reason, "git commit timed out after 5s");
});

// A stage the operator set does more than write the card: the agent keeping it
// is told, and an accepted card has its session tidied away. What that came to
// rides on the same answer, and the page has nowhere else to read it from.
test("the steps taken around a write come back with it", async () => {
  stubFetch(
    answer({
      status: 200,
      body: {
        written: true,
        committed: true,
        steps: [
          { name: "message", error: "abc12345 was not told its card moved: the daemon is not running" },
          { name: "archive", note: "recorded in the board's archive" },
        ],
      },
    }),
  );

  const result = await setCardField("/board/fleet-ui.md", "stage", "done");

  assert.equal(result.committed, true);
  assert.equal(result.steps.length, 2);
  assert.match(result.steps[0].error, /not told its card moved/);
});

// An older panel, or a write with nothing around it, answers without the key;
// a caller reading .steps.length must not have to check for it first.
test("an answer with no steps in it reads as no steps", async () => {
  stubFetch(answer({ status: 200, body: { written: true, committed: false, reason: "no commit" } }));
  const result = await setCardField("/board/fleet-ui.md", "stage", "review");
  assert.deepEqual(result.steps, []);
});

test("a 200 whose body cannot be read is not reported as committed", async () => {
  stubFetch(answer({ status: 200 }));
  const result = await setCardField("/board/fleet-ui.md", "stage", "review");
  assert.equal(result.committed, false);
});

test("a refusal throws, carrying the server's own words", async () => {
  stubFetch(
    answer({
      status: 422,
      body: { error: "card /board/broken.md has no frontmatter to write into" },
    }),
  );

  await assert.rejects(() => setCardField("/board/broken.md", "stage", "review"), {
    message: "card /board/broken.md has no frontmatter to write into",
  });
});

// A refusal by one of the board's rules arrives with a code beside the words.
// The code is what the panel translates by, so it rides on the error; a
// refusal without one carries no code at all rather than an empty string,
// which reasonText would otherwise take for a key.
test("a refusal carries the server's code when it sent one", async () => {
  stubFetch(
    answer({
      status: 400,
      body: { error: "cannot set stage to active while session is empty", code: "session_required" },
    }),
  );

  await assert.rejects(() => setCardField("/board/c.md", "stage", "active"), {
    message: "cannot set stage to active while session is empty",
    code: "session_required",
  });
});

test("a refusal without a code carries none", async () => {
  stubFetch(answer({ status: 422, body: { error: "card has no stage field" } }));

  await assert.rejects(() => setCardField("/board/c.md", "stage", "active"), (err) => {
    assert.equal(err.message, "card has no stage field");
    assert.equal("code" in err, false, "a missing code became a property");
    return true;
  });
});

test("a refusal with no JSON body still throws something readable", async () => {
  stubFetch(answer({ status: 503, statusText: "Service Unavailable" }));
  await assert.rejects(() => setCardField("/board/fleet-ui.md", "stage", "review"), {
    message: "Service Unavailable",
  });
});

// A resume is addressed by short id, not by the transcript UUID its
// neighbouring routes take: a stopped session is one the daemon no longer
// lists, and the short id is what the job store names its directory by.
//
// The empty object is not an oversight. There is nothing to send beyond the id
// in the path, and the server's guard answers 415 to a POST that is not
// application/json — so the body is empty JSON rather than nothing at all.
test("a resume posts to the session's short id and sends no arguments", async () => {
  stubFetch(answer({ status: 204 }));

  await resumeSession("bb22cc33");

  assert.equal(calls[0].url, "/api/sessions/bb22cc33/resume");
  assert.equal(calls[0].init.method, "POST");
  assert.equal(calls[0].init.headers["Content-Type"], "application/json");
  assert.deepEqual(JSON.parse(calls[0].init.body), {});
});

test("a short id is escaped into the resume path", async () => {
  stubFetch(answer({ status: 204 }));
  await resumeSession("a/b c");
  assert.equal(calls[0].url, "/api/sessions/a%2Fb%20c/resume");
});

// Both refusals the route can give have to arrive as words. 502 is the daemon
// having failed at it, 409 is the session being the reason it cannot happen,
// and the operator decides what to do next from the sentence, not the code.
test("a resume that failed throws the server's own words", async () => {
  stubFetch(answer({ status: 502, body: { error: "resumed worker crashed during startup: exit 1" } }));
  await assert.rejects(() => resumeSession("bb22cc33"),
    { message: "resumed worker crashed during startup: exit 1" });
});

test("a session that cannot be resumed throws the reason it cannot", async () => {
  stubFetch(answer({
    status: 409,
    body: { error: "session cannot be resumed: bb22cc33 has no transcript to resume from — it was never prompted" },
  }));
  await assert.rejects(() => resumeSession("bb22cc33"), /never prompted/);
});

// A session's card history is read here rather than taken from the snapshot:
// closed cards leave the board for its archive, which the snapshot never reads,
// and the history is wanted only while somebody has a session open.

test("a session's cards are asked for under its short id and come back as the list", async () => {
  const cards = [{ id: "T-001", title: "one", stage: "done", created: "2026-09-11", path: "/b/archive/one.md", archived: true }];
  stubFetch(answer({ status: 200, body: cards }));

  assert.deepEqual(await fetchSessionCards("abc12345"), cards);
  assert.equal(calls[0].url, "/api/sessions/abc12345/cards");
});

test("a short id is escaped into the cards path", async () => {
  stubFetch(answer({ status: 200, body: [] }));
  await fetchSessionCards("a/b c");
  assert.equal(calls[0].url, "/api/sessions/a%2Fb%20c/cards");
});

test("an answer that is not a list is no cards, never undefined", async () => {
  stubFetch(answer({ status: 200, body: null }));
  assert.deepEqual(await fetchSessionCards("abc12345"), []);
});

test("a history that cannot be read throws the server's own words", async () => {
  stubFetch(answer({ status: 503, statusText: "Service Unavailable", body: { error: "this panel is not wired to a board" } }));
  await assert.rejects(() => fetchSessionCards("abc12345"), /not wired to a board/);
});

test("a refusal whose body is not JSON still produces words, not undefined", async () => {
  // What a route the server does not register answers: the request falls through
  // to the static file server, whose 404 body is plain text.
  stubFetch(answer({ status: 404, statusText: "Not Found" }));

  await assert.rejects(() => fetchSessionCards("abc12345"), (err) => {
    assert.equal(err.message, "Not Found");
    return true;
  });
});

// The token a terminal socket presents first. Read fresh for every socket, and
// never from a cache: after the panel restarts, a cached answer is a token the
// new process refuses.
test("the terminal token is read fresh, past every cache", async () => {
  stubFetch(answer({ status: 200, body: { token: "abc123" } }));
  assert.equal(await fetchTerminalToken(), "abc123");
  assert.equal(calls.length, 1);
  assert.equal(calls[0].url, "/api/terminal-token");
  assert.equal(calls[0].init?.cache, "no-store");
});

test("a token that cannot be had throws words, never an empty token", async () => {
  stubFetch(answer({ status: 503, body: { error: "this panel is not wired to a terminal token" } }));
  await assert.rejects(fetchTerminalToken(), /not wired to a terminal token/);

  stubFetch(answer({ status: 200, body: { token: "" } }));
  await assert.rejects(fetchTerminalToken(), /token/);

  stubFetch(answer({ status: 200, body: [] }));
  await assert.rejects(fetchTerminalToken(), /token/);
});

test("a new card is a POST of its title, zone, repo and description, and nothing else", async () => {
  stubFetch(answer({ status: 201, body: { path: "/b/cards/2026-09-11-a-task.md", committed: true } }));

  const result = await createCard("A task", "planned", "src/fleetdeck", "what and why");

  assert.equal(calls.length, 1);
  assert.equal(calls[0].url, "/api/cards");
  assert.equal(calls[0].init.method, "POST");
  assert.equal(calls[0].init.headers["Content-Type"], "application/json");
  assert.deepEqual(JSON.parse(calls[0].init.body), { title: "A task", zone: "planned", repo: "src/fleetdeck", description: "what and why" });
  assert.deepEqual(result, { path: "/b/cards/2026-09-11-a-task.md", committed: true, reason: "" });
});

test("a new card that reached the board but not its history says so", async () => {
  stubFetch(answer({ status: 201, body: { path: "/b/cards/x.md", committed: false, reason: "the commit timed out" } }));
  const result = await createCard("A task", "planned");
  assert.equal(result.committed, false);
  assert.equal(result.reason, "the commit timed out");
});

test("a new card's attachments go in its body, and only when there are any", async () => {
  stubFetch(answer({ status: 201, body: { path: "/b/cards/x.md", committed: true } }));
  await createCard("A task", "planned", "", "", [{ name: "a.png", data: "cG5n" }]);
  assert.deepEqual(JSON.parse(calls[0].init.body).attachments, [{ name: "a.png", data: "cG5n" }]);
});

test("a card's link to its attachment is served from the board, anything else is not an attachment", () => {
  assert.equal(attachmentURL("../attachments/T-001/отчёт.pdf"), `/api/attachments?path=${encodeURIComponent("T-001/отчёт.pdf")}`);
  assert.equal(attachmentURL("https://example.com/x.png"), null);
  assert.equal(attachmentURL("../cards/T-001.md"), null);
});

// The Finder's folder, asked of the panel: a page is never told where a folder
// lives. It is no fleet's, so it carries none.
test("picking a folder is a POST of the dialog's prompt that answers the repo", async () => {
  stubFetch(answer({ status: 200, body: { repo: "src/fleetdeck" } }));
  assert.equal(await pickDirectory("Choose"), "src/fleetdeck");
  assert.equal(calls[0].url, "/api/pick-directory");
  assert.equal(calls[0].init.method, "POST");
  assert.equal(calls[0].init.headers["Content-Type"], "application/json");
  assert.deepEqual(JSON.parse(calls[0].init.body), { prompt: "Choose" });
});

test("a cancelled folder dialog is an empty repo, and a refusal throws the server's words", async () => {
  stubFetch(answer({ status: 200, body: { repo: "" } }));
  assert.equal(await pickDirectory("Choose"), "");
  stubFetch(answer({ status: 503, body: { error: "this panel is not wired to a folder dialog: it is macOS only" } }));
  await assert.rejects(pickDirectory("Choose"), /macOS only/);
});

test("a refused new card throws the server's words", async () => {
  stubFetch(answer({ status: 400, body: { error: 'invalid card: unknown zone "someday"' } }));
  await assert.rejects(createCard("A task", "someday"), /unknown zone/);
});

// A card started, a card edited and an orchestrator pinned land in the fleet
// the tab's address names: the server reads it from the same parameter the
// snapshot does.
test("card and pin writes carry the tab's fleet", async () => {
  globalThis.location = { search: "?fleet=%D0%BE%D1%81%D0%BD%D0%BE%D0%B2%D0%BD%D0%BE%D0%B9" };
  try {
    stubFetch((url, init) => answer({ status: init.method === "POST" ? 201 : 204, body: { path: "/b/cards/x.md", committed: true } }));
    await createCard("A task", "planned");
    await setCardField("/b/cards/x.md", "stage", "review");
    await setOrchestratorSession("cafe0001");
    const fleet = "fleet=%D0%BE%D1%81%D0%BD%D0%BE%D0%B2%D0%BD%D0%BE%D0%B9";
    assert.deepEqual(calls.map((c) => c.url), [`/api/cards?${fleet}`, `/api/cards?${fleet}`, `/api/config?${fleet}`]);
  } finally {
    delete globalThis.location;
  }
});

test("without a fleet in the address the writes go where they always did", async () => {
  stubFetch((url, init) => answer({ status: init.method === "POST" ? 201 : 204, body: { path: "/b/cards/x.md", committed: true } }));
  await createCard("A task", "planned");
  await setOrchestratorSession("cafe0001");
  assert.deepEqual(calls.map((c) => c.url), ["/api/cards", "/api/config"]);
});
