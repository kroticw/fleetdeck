// web/js/_tests/unanswered.test.js
//
// A session that has owed its next move and said nothing. Mirrors
// internal/state/unanswered_test.go: the verdict is computed twice, once in Go for the
// banners and once here for the screen, and the two must agree case by case.
//
// Lives under _tests/ for the same reason header.test.js does: web/embed.go's plain
// directory pattern excludes it from the binary.
//
// Run with: node --test web/js/_tests/unanswered.test.js

import test from "node:test";
import assert from "node:assert/strict";

// sessions.js and needs.js reach for navigator.language at import time through i18n.js.
globalThis.navigator ??= { language: "en" };

const { waiting, isWaiting, WAITING_NO, WAITING_UNKNOWN, WAITING_YES, UNANSWERED_LIMIT_MS } = await import("../needs.js");
const { rowHtml } = await import("../sessions.js");
// The dictionary is picked from the machine's locale (Node has its own navigator), so
// wording is compared through t(), never against one language's text.
const { t } = await import("../i18n.js");

const NS_PER_MS = 1e6;
const minutes = (m) => m * 60 * 1000 * NS_PER_MS;

// A session the daemon says has no question, left owing its move for the given number
// of nanoseconds -- the wire shape of state.SessionView. With detail carrying the last
// message sent to it, which is what the daemon puts there.
function unanswered(forNs, extra = {}) {
  return {
    short: "un01",
    name: "n",
    needs: "",
    state: "working",
    tempo: "active",
    detail: "<agent-message from=\"06a1f607\">are you there?</agent-message>",
    silentFor: forNs,
    unansweredFor: forNs,
    ...extra,
  };
}

const readCall = { tool: "Read", since: "2026-09-12T18:00:00Z" };

test("a session left unanswered past the limit is unknown, not no", () => {
  assert.equal(waiting(unanswered(UNANSWERED_LIMIT_MS * NS_PER_MS)), WAITING_UNKNOWN);
});

test("a session unanswered under the limit is still no", () => {
  assert.equal(waiting(unanswered((UNANSWERED_LIMIT_MS - 1000) * NS_PER_MS)), WAITING_NO);
});

test("silence in a finished turn keeps the daemon's no", () => {
  assert.equal(waiting({ needs: "", state: "working", silentFor: minutes(40) }), WAITING_NO);
});

test("words still decide however long a session has owed its move", () => {
  assert.equal(waiting(unanswered(minutes(30), { needs: "answer: Allow ssh to a new host? (yes · no)" })), WAITING_YES);
  assert.equal(waiting(unanswered(minutes(30), { needs: "rate limited" })), WAITING_NO);
});

test("an unmeasured unanswered stretch is still no", () => {
  assert.equal(waiting(unanswered(0)), WAITING_NO);
});

test("a dying session long unanswered is no", () => {
  assert.equal(waiting(unanswered(minutes(30), { dying: true })), WAITING_NO);
});

test("a long unanswered session is never counted as waiting for a person", () => {
  assert.equal(isWaiting(unanswered(minutes(30))), false);
});

test("the unanswered limit is sixteen minutes, the same number Go uses", () => {
  // internal/state/unanswered_browser_test.go reads this constant out of needs.js and
  // compares it with state.UnansweredLimit; this pins the value from the other side.
  assert.equal(UNANSWERED_LIMIT_MS, 16 * 60 * 1000);
});

test("a long unanswered session says so on its row", () => {
  const html = rowHtml(unanswered(minutes(20)));
  assert.ok(html.includes("sbadge-unknown"), "it is the same not-known badge, not a new state");
  assert.ok(html.includes("sbadge-unanswered"), "and it says which kind of not-known it is");
  assert.ok(html.includes(`>${t("waiting_unanswered")}<`), "the fact the panel has, in words");
  assert.ok(!html.includes("sbadge-waiting"), "not known is not a question to a person");
});

test("a call is named only when the transcript shows one", () => {
  const named = rowHtml(unanswered(minutes(20), { inCall: readCall }));
  assert.ok(named.includes("sbadge-incall"));
  assert.ok(named.includes("Read"), "the call is the fact the row states");
  const unnamed = rowHtml(unanswered(minutes(20)));
  assert.ok(!unnamed.includes("sbadge-incall"), "naming a call nobody can see would be a precise lie");
});

test("the unanswered badge carries no guess about why", () => {
  const html = rowHtml(unanswered(minutes(20), { inCall: readCall }));
  for (const guess of ["frozen", "stuck", "hung", "dead"]) {
    assert.ok(!html.toLowerCase().includes(guess), `the row must not claim "${guess}"; slow and frozen look the same`);
  }
});

test("the unanswered badge leaves on the next snapshot once the session speaks", () => {
  const frozen = unanswered(minutes(20), { inCall: readCall });
  const back = { ...frozen, inCall: undefined, unansweredFor: undefined, silentFor: 2 * 1e9 };
  assert.ok(rowHtml(frozen).includes("sbadge-unanswered"));
  const next = rowHtml(back);
  assert.ok(!next.includes("sbadge-unanswered"), "a row must not outlive its cause");
  assert.ok(!next.includes("sbadge-unknown"), "with the session speaking, the daemon's no stands again");
});

test("a session briefly owing its move has no badge at all", () => {
  const html = rowHtml(unanswered(minutes(3)));
  assert.ok(!html.includes("sbadge-unknown"));
  assert.ok(!html.includes("sbadge-unanswered"));
});

test("a long unanswered session is named even while its flags make it stalled", () => {
  // Seen on a live frozen session on 2026-09-12: state stayed "blocked" the whole time,
  // so after ten minutes of silence the stall tracker counts it too. A bare flag says
  // less than the unanswered stretch does -- "stalled" with the text of the last message
  // sent to it as the reason -- so the row says what is known.
  const html = rowHtml(unanswered(minutes(20), { state: "blocked" }), true);
  assert.ok(html.includes("sbadge-unanswered"), "the unanswered stretch is the more specific thing that can be said");
  assert.ok(!html.includes("sbadge-stalled"), "one badge, the most specific one");
});

test("a stall in words still outranks everything but a question", () => {
  const html = rowHtml(unanswered(minutes(20), { needs: "usage limit reached" }), true);
  assert.ok(html.includes("sbadge-stalled"), "words decide: this session is stalled on a limit");
  assert.ok(!html.includes("sbadge-unanswered"));
});

test("a source that never reported needs keeps its own not-reported badge", () => {
  const html = rowHtml({ short: "uu11", name: "n", state: "working", detail: "working away" });
  assert.ok(html.includes("sbadge-unknown"));
  assert.ok(!html.includes("sbadge-unanswered"), "the unanswered wording belongs only to a session seen owing its move");
});
