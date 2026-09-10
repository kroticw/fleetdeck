// The write client. Its whole job is to keep the server's two different
// successes apart, so most of this file is about the difference between a 204
// and a 200.

import { test, beforeEach, afterEach } from "node:test";
import assert from "node:assert/strict";

import { setCardField, sendText, sendKeys } from "../js/api.js";

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
