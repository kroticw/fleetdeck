// Who wrote a card's document, and what that session is doing: what the card's
// document tab puts next to the document and the dot on its tab (T-091).

import { test } from "node:test";
import assert from "node:assert/strict";

import { authorOf, authorState, sessionOf } from "../js/docauthor.js";

test("a document that names its session is authored by it, whatever the card says", () => {
  assert.deepEqual(authorOf({ session: "a41c09d2" }, { session: "909bf9b2" }), { short: "a41c09d2", from: "document" });
});

test("a document without a session falls back to the card it is opened from, and says so", () => {
  assert.deepEqual(authorOf({}, { session: "909bf9b2" }), { short: "909bf9b2", from: "card" });
  assert.deepEqual(authorOf({ session: "" }, { session: "909bf9b2" }), { short: "909bf9b2", from: "card" });
});

test("the card tab is authored by the card's session", () => {
  assert.deepEqual(authorOf(null, { session: "909bf9b2" }), { short: "909bf9b2", from: "card" });
});

test("nobody to answer when neither the document nor the card names a session", () => {
  assert.equal(authorOf({}, {}), null);
  assert.equal(authorOf(null, { session: "" }), null);
  assert.equal(authorOf(null, null), null);
});

const snap = (sessions, orchestratorSession = "") => ({ sessions, orchestratorSession });

test("a session is found in the snapshot by its short id", () => {
  const s = { short: "a41c09d2", name: "x" };
  assert.equal(sessionOf("a41c09d2", snap([{ short: "b" }, s])), s);
  assert.equal(sessionOf("zzz", snap([s])), null);
  assert.equal(sessionOf("a41c09d2", null), null);
});

test("the orchestrator's pinned session is the orchestrator, even while it waits", () => {
  assert.equal(authorState("0c7e1a2b", snap([{ short: "0c7e1a2b", needs: "answer: x?" }], "0c7e1a2b")), "orchestrator");
});

test("a session the snapshot does not list is unknown", () => {
  assert.equal(authorState("a41c09d2", snap([])), "unknown");
  assert.equal(authorState("a41c09d2", snap([], "")), "unknown", "an empty pin is not a match for anything");
});

test("where a session is in its life comes before what it last said", () => {
  assert.equal(authorState("a", snap([{ short: "a", lifecycle: "dead", needs: "answer: x?" }])), "dead");
  assert.equal(authorState("a", snap([{ short: "a", lifecycle: "stopped", needs: "answer: x?" }])), "stopped");
});

test("stalled, waiting and working follow needs.js", () => {
  assert.equal(authorState("a", snap([{ short: "a", needs: "usage limit reached" }])), "stalled");
  assert.equal(authorState("a", snap([{ short: "a", needs: "", state: "blocked" }])), "stalled");
  assert.equal(authorState("a", snap([{ short: "a", needs: "answer: first or after?" }])), "waiting");
  assert.equal(authorState("a", snap([{ short: "a", needs: "" }])), "working");
  assert.equal(authorState("a", snap([{ short: "a", lifecycle: "live", needs: "" }])), "working");
});
