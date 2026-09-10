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
import { isWaiting, isStalled, stallReason } from "./header.js";

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
