// web/js/header.test.js
//
// Mirrors internal/daemon/types.go's Session.Waiting()/.Stalled() test cases
// (and the fixtures in internal/state/events_test.go) so the JS restatement
// in header.js cannot silently drift from the Go source of truth.
//
// Run with: node --test web/js/header.test.js
// This file is a development-only tool. Node is not a runtime dependency of
// the shipped frontend or of bin/fleetdeck -- nothing here is bundled or
// shipped.

import test from "node:test";
import assert from "node:assert/strict";
import { isWaiting, isStalled, stallReason, escapeHTML, stalledList } from "./header.js";

test("a question in needs is waiting, not stalled", () => {
  const s = { needs: "answer: pick one (A · B)" };
  assert.equal(isWaiting(s), true);
  assert.equal(isStalled(s), false);
});

test("a usage limit in needs is stalled, not waiting", () => {
  const s = { needs: "usage limit reached" };
  assert.equal(isWaiting(s), false);
  assert.equal(isStalled(s), true);
});

test("blocked state with empty needs is stalled, and detail is carried verbatim", () => {
  const s = { needs: "", state: "blocked", detail: "waiting on my own subagents" };
  assert.equal(isWaiting(s), false);
  assert.equal(isStalled(s), true);
  assert.equal(stallReason(s), "waiting on my own subagents");
});

test("blocked tempo with empty needs and non-blocked state is stalled", () => {
  const s = { needs: "", tempo: "blocked", state: "" };
  assert.equal(isStalled(s), true);
});

test("no needs, no blocked state or tempo is neither waiting nor stalled", () => {
  const s = { needs: "", state: "", tempo: "" };
  assert.equal(isWaiting(s), false);
  assert.equal(isStalled(s), false);
});

test("a dying session is never in either counter, even with a question pending", () => {
  const s = { needs: "answer: x", dying: true };
  assert.equal(isWaiting(s), false);
  assert.equal(isStalled(s), false);
});

// The fleet is open (spec 3.1): needs/detail come from sessions we did not
// write and must be treated as untrusted content, not developer-controlled
// text, wherever they reach innerHTML.
test("escapeHTML neutralizes the characters that matter in an HTML template literal", () => {
  const input = `usage limit reached<a href="http://evil.example/reauth">Re-authenticate here</a> & 'quoted'`;
  const escaped = escapeHTML(input);
  assert.equal(escaped.includes("<"), false);
  assert.equal(escaped.includes(">"), false);
  assert.equal(escaped.includes('"'), false);
  assert.equal(
    escaped,
    "usage limit reached&lt;a href=&quot;http://evil.example/reauth&quot;&gt;Re-authenticate here&lt;/a&gt; &amp; &#39;quoted&#39;",
  );
});

test("stalledList escapes a stalled session's reason text before it is joined into markup", () => {
  const malicious = { needs: 'usage limit reached<img src=x onerror="alert(1)">' };
  const html = stalledList([malicious]);
  assert.equal(html.includes("<img"), false);
  assert.equal(html.includes("&lt;img"), true);
});

test("stalledList drops reasons that are empty strings instead of leaving a stray separator", () => {
  // Stalled via the flag-only branch (state/tempo blocked) with no detail:
  // stallReason(s) is "" for this session per its own contract.
  const noReason = { needs: "", state: "blocked", detail: "" };
  const withReason = { needs: "usage limit reached" };
  const html = stalledList([withReason, noReason]);
  assert.equal(html.includes("; ;"), false);
  assert.equal(html.includes(";  ;"), false);
  assert.equal(html, '<span class="stall-reasons">usage limit reached</span>');
});
