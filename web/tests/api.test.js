// The write client. Its whole job is to keep the server's two different
// successes apart, so most of this file is about the difference between a 204
// and a 200.

import { test, beforeEach, afterEach } from "node:test";
import assert from "node:assert/strict";

import { setCardField, sendText, sendKeys, fetchDigest, fetchScreen } from "../js/api.js";

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

test("a card write is a PATCH of three strings, declared as JSON", async () => {
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
  });
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

test("a refusal with no JSON body still throws something readable", async () => {
  stubFetch(answer({ status: 503, statusText: "Service Unavailable" }));
  await assert.rejects(() => setCardField("/board/fleet-ui.md", "stage", "review"), {
    message: "Service Unavailable",
  });
});

test("sending text posts JSON and says it submits", async () => {
  stubFetch(answer({ status: 204 }));

  await sendText("a1b2c3", "yes, go ahead");

  assert.equal(calls[0].url, "/api/sessions/a1b2c3/text");
  assert.equal(calls[0].init.method, "POST");
  assert.equal(calls[0].init.headers["Content-Type"], "application/json");
  assert.deepEqual(JSON.parse(calls[0].init.body), { text: "yes, go ahead", submit: true });
});

test("a session id is escaped into the path", async () => {
  stubFetch(answer({ status: 204 }));
  await sendKeys("a/b c", "escape");
  assert.equal(calls[0].url, "/api/sessions/a%2Fb%20c/keys");
  assert.deepEqual(JSON.parse(calls[0].init.body), { keys: "escape" });
});

test("a refused session write throws", async () => {
  stubFetch(answer({ status: 502, body: { error: "daemon: EAUTH" } }));
  await assert.rejects(() => sendText("a1b2c3", "hello"), { message: "daemon: EAUTH" });
});

// The two reads. They are here rather than in the snapshot because a transcript
// digest and a terminal screen are too large to push to every tab once a second
// and are wanted only while somebody has a session open.

test("a digest asks for the limit it was given and comes back as steps", async () => {
  const steps = [{ role: "user", text: "go" }, { role: "assistant", text: "done" }];
  stubFetch(answer({ status: 200, body: steps }));

  assert.deepEqual(await fetchDigest("abc123", 30), steps);
  assert.equal(calls[0].url, "/api/sessions/abc123/digest?limit=30");
});

test("a transcript that cannot be read throws the server's own words", async () => {
  // The route answers 404 for a session that has left no transcript, and that
  // has to reach the operator: an empty pane reads as a quiet session.
  stubFetch(answer({ status: 404, statusText: "Not Found", body: { error: "transcript not found: abc123" } }));

  await assert.rejects(() => fetchDigest("abc123", 30), /transcript not found: abc123/);
});

test("a refusal whose body is not JSON still produces words, not undefined", async () => {
  // What a route the server does not register answers: the request falls through
  // to the static file server, whose 404 body is plain text.
  stubFetch(answer({ status: 404, statusText: "Not Found" }));

  await assert.rejects(() => fetchDigest("abc123", 30), (err) => {
    assert.equal(err.message, "Not Found");
    return true;
  });
});

test("a screen read sends no tail: the byte count belongs to the server", async () => {
  // tail is a byte count, not a line count. A number picked here would be a
  // second copy of one the server already holds, free to drift — and one chosen
  // as though it counted lines would truncate the screen to a fragment.
  stubFetch(answer({ status: 200, body: { screen: "$ " } }));

  assert.deepEqual(await fetchScreen("abc123"), { screen: "$ ", error: "" });
  assert.equal(calls[0].url, "/api/sessions/abc123/screen");
});

test("a screen read that failed keeps the bytes that did arrive", async () => {
  // handleScreen sends both fields on a failure on purpose: the output read
  // before the attach broke is often the very output being looked at. Throwing
  // it away would be the panel discarding what the server took care to send.
  stubFetch(
    answer({ status: 502, statusText: "Bad Gateway", body: { error: "attach evicted", screen: "half a line" } }),
  );

  assert.deepEqual(await fetchScreen("abc123"), { screen: "half a line", error: "attach evicted" });
});
