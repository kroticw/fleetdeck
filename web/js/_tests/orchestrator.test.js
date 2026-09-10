// web/js/_tests/orchestrator.test.js
//
// Covers the pure logic orchestrator.js exports alongside renderOrchestrator:
// which session a pin resolves to, the context percentage math, the picker's
// escaping, and the stale-socket banner. renderOrchestrator itself touches
// the DOM, fetch and timers directly and is exercised by hand per the task's
// acceptance steps instead — see this directory's header.test.js for why
// this subtree is invisible to web/embed.go's go:embed.
//
// Run with: node --test web/js/_tests/orchestrator.test.js

import test from "node:test";
import assert from "node:assert/strict";
import { resolveOrchestrator, contextPercent, pickerItemsHTML, staleBannerHTML } from "../orchestrator.js";

test("no snapshot yet resolves to nothing pinned and no sessions", () => {
  const r = resolveOrchestrator(null);
  assert.equal(r.short, "");
  assert.equal(r.session, undefined);
  assert.deepEqual(r.sessions, []);
});

test("a configured pin resolves to the session matching by short id, never by sessionId", () => {
  const snap = {
    orchestratorSession: "abc",
    sessions: [
      // A decoy whose sessionId equals the pinned short, listed FIRST: a
      // resolver that matches by sessionId (or falls back to it) would find
      // this one before ever reaching the session that actually matches by
      // short id, below.
      { short: "xyz", sessionId: "abc" },
      { short: "abc", sessionId: "11111111-1111-1111-1111-111111111111", name: "orchestrator" },
    ],
  };
  const r = resolveOrchestrator(snap);
  assert.equal(r.session.short, "abc");
  assert.equal(r.session.name, "orchestrator");
});

test("a pin naming a session the daemon no longer lists resolves to no session, not a crash", () => {
  const r = resolveOrchestrator({ orchestratorSession: "gone", sessions: [{ short: "other" }] });
  assert.equal(r.session, undefined);
  assert.equal(r.short, "gone");
});

test("an empty pin resolves to no session even when sessions exist", () => {
  const r = resolveOrchestrator({ orchestratorSession: "", sessions: [{ short: "a" }] });
  assert.equal(r.session, undefined);
  assert.equal(r.short, "");
});

test("contextPercent computes tokens/window as a rounded percentage", () => {
  assert.equal(contextPercent({ tokens: 50000, window: 200000, estimated: true }), 25);
});

test("contextPercent is null without a usable window", () => {
  assert.equal(contextPercent(null), null);
  assert.equal(contextPercent(undefined), null);
  assert.equal(contextPercent({ tokens: 1, window: 0 }), null);
});

// The fleet is open (spec 3.1): a session's `short`/`name` come from the
// daemon, not from this codebase, and pickerItemsHTML interpolates `short`
// into an HTML attribute — the one spot a stray quote actually breaks out.
test("pickerItemsHTML escapes a short id so it cannot break out of the data-short attribute", () => {
  const malicious = `a" onmouseover="alert(1)`;
  const html = pickerItemsHTML([{ short: malicious }]);
  assert.equal(html.includes(`data-short="${malicious}"`), false);
  assert.equal(html.includes('onmouseover="alert'), false);
  assert.equal(html.includes("&quot;"), true);
});

test("pickerItemsHTML escapes a session name used as the button label", () => {
  const html = pickerItemsHTML([{ short: "ok", name: `<img src=x onerror=alert(1)>` }]);
  assert.equal(html.includes("<img"), false);
  assert.equal(html.includes("&lt;img"), true);
});

test("pickerItemsHTML falls back to the short id when a session has no name", () => {
  const html = pickerItemsHTML([{ short: "ok" }]);
  assert.equal(html.includes(">ok<"), true);
});

test("pickerItemsHTML skips a session with no short id — there is nothing to pin to", () => {
  const html = pickerItemsHTML([{ name: "no short" }, { short: "" }]);
  assert.equal(html, "");
});

test("staleBannerHTML renders nothing when connected", () => {
  assert.equal(staleBannerHTML(true), "");
});

test("staleBannerHTML renders a visible marker when disconnected", () => {
  const html = staleBannerHTML(false);
  assert.notEqual(html, "");
  assert.equal(html.includes("o-stale"), true);
});
