// web/js/_tests/incall.test.js
//
// A session silent inside one tool call. Mirrors internal/state/incall_test.go: the
// verdict is computed twice, once in Go for the banners and once here for the screen,
// and the two must agree case by case.
//
// Lives under _tests/ for the same reason header.test.js does: web/embed.go's plain
// directory pattern excludes it from the binary.
//
// Run with: node --test web/js/_tests/incall.test.js

import test from "node:test";
import assert from "node:assert/strict";

// sessions.js and needs.js reach for navigator.language at import time through i18n.js.
globalThis.navigator ??= { language: "en" };

const { waiting, isWaiting, WAITING_NO, WAITING_UNKNOWN, WAITING_YES, CALL_SILENCE_LIMIT_MS } = await import("../needs.js");
const { rowHtml } = await import("../sessions.js");

const NS_PER_MS = 1e6;
const minutes = (m) => m * 60 * 1000 * NS_PER_MS;

// A session the daemon says has no question, standing inside one tool call and silent
// for the given number of nanoseconds -- the wire shape of state.SessionView.
function inCall(tool, silentForNs, extra = {}) {
  return {
    short: "ic01",
    name: "n",
    needs: "",
    state: "working",
    tempo: "active",
    detail: "<agent-message from=\"06a1f607\">are you there?</agent-message>",
    silentFor: silentForNs,
    inCall: { tool, since: "2026-09-12T18:00:00Z" },
    ...extra,
  };
}

test("a session silent inside one call past the limit is unknown, not no", () => {
  assert.equal(waiting(inCall("Read", CALL_SILENCE_LIMIT_MS * NS_PER_MS)), WAITING_UNKNOWN);
});

test("a session inside one call under the limit is still no", () => {
  assert.equal(waiting(inCall("Bash", (CALL_SILENCE_LIMIT_MS - 1000) * NS_PER_MS)), WAITING_NO);
});

test("silence outside any call keeps the daemon's no", () => {
  assert.equal(waiting({ needs: "", state: "working", silentFor: minutes(40) }), WAITING_NO);
});

test("words still decide inside a long call", () => {
  assert.equal(waiting(inCall("Bash", minutes(30), { needs: "answer: Allow ssh to a new host? (yes · no)" })), WAITING_YES);
  assert.equal(waiting(inCall("Bash", minutes(30), { needs: "rate limited" })), WAITING_NO);
});

test("unmeasured silence inside a call is still no", () => {
  assert.equal(waiting(inCall("Read", 0)), WAITING_NO);
});

test("a dying session inside a long call is no", () => {
  assert.equal(waiting(inCall("Read", minutes(30), { dying: true })), WAITING_NO);
});

test("a session silent inside a call is never counted as waiting for a person", () => {
  assert.equal(isWaiting(inCall("Read", minutes(30))), false);
});

test("the call silence limit is eleven minutes, the same number Go uses", () => {
  // internal/state/incall_js_test.go reads this constant out of needs.js and compares
  // it with state.CallSilenceLimit; this pins the value from the other side.
  assert.equal(CALL_SILENCE_LIMIT_MS, 11 * 60 * 1000);
});

test("a session silent inside a call says so on its row, naming the call", () => {
  const html = rowHtml(inCall("mcp__claude-agents__send_message", minutes(14)));
  assert.ok(html.includes("sbadge-unknown"), "it is the same not-known badge, not a new state");
  assert.ok(html.includes("sbadge-incall"), "and it says which kind of not-known it is");
  assert.ok(html.includes("mcp__claude-agents__send_message"), "the call is the fact the row states");
  assert.ok(!html.includes("sbadge-waiting"), "not known is not a question to a person");
});

test("the in-call badge carries no guess about why the call has not returned", () => {
  const html = rowHtml(inCall("Read", minutes(14)));
  for (const guess of ["frozen", "stuck", "hung", "dead"]) {
    assert.ok(!html.toLowerCase().includes(guess), `the row must not claim "${guess}"; slow and frozen look the same`);
  }
});

test("the in-call badge leaves on the next snapshot once the call has returned", () => {
  const frozen = inCall("Read", minutes(14));
  const back = { ...frozen, inCall: undefined, silentFor: 2 * 1e9 };
  assert.ok(rowHtml(frozen).includes("sbadge-incall"));
  const next = rowHtml(back);
  assert.ok(!next.includes("sbadge-incall"), "a row must not outlive its cause");
  assert.ok(!next.includes("sbadge-unknown"), "with the call back, the daemon's no stands again");
});

test("a session silent inside a call is named even while its flags make it stalled", () => {
  // Seen on a live frozen session on 2026-09-12: state stayed "blocked" the whole time,
  // so after ten minutes of silence the stall tracker counts it too. A bare flag says
  // less than the call does -- "stalled" with the text of the last message sent to it
  // as the reason, against "silent inside Read" -- so the call is what the row names.
  const html = rowHtml(inCall("Read", minutes(14), { state: "blocked" }), true);
  assert.ok(html.includes("sbadge-incall"), "the call is the more specific thing that can be said");
  assert.ok(!html.includes("sbadge-stalled"), "one badge, the most specific one");
});

test("a stall in words still outranks everything but a question", () => {
  const html = rowHtml(inCall("Read", minutes(14), { needs: "usage limit reached" }), true);
  assert.ok(html.includes("sbadge-stalled"), "words decide: this session is stalled on a limit");
  assert.ok(!html.includes("sbadge-incall"));
});

test("a session in an ordinary short call has no badge at all", () => {
  const html = rowHtml(inCall("Bash", minutes(3)));
  assert.ok(!html.includes("sbadge-unknown"));
  assert.ok(!html.includes("sbadge-incall"));
});

test("a source that never reported needs keeps its own not-reported badge", () => {
  const html = rowHtml({ short: "uu11", name: "n", state: "working", detail: "working away" });
  assert.ok(html.includes("sbadge-unknown"));
  assert.ok(!html.includes("sbadge-incall"), "the in-call wording belongs only to a session seen inside a call");
});
