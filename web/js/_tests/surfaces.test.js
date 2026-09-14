// web/js/_tests/surfaces.test.js
//
// Run with: node --test web/js/_tests/surfaces.test.js

import test from "node:test";
import assert from "node:assert/strict";
import { regionsFor, layoutReport } from "../surfaces.js";

test("without a host every region is mounted, as in a browser tab", () => {
  assert.deepEqual([...regionsFor(null)].sort(), ["center", "header", "orchestrator", "sessions"]);
});

test("the board surface mounts the centre column only", () => {
  assert.deepEqual([...regionsFor({ surface: "board", glass: "glass" })], ["center"]);
});

test("the orchestrator surface mounts its column and the brand row", () => {
  assert.deepEqual([...regionsFor({ surface: "orchestrator", glass: "glass" })].sort(), ["brand", "orchestrator"]);
});

test("the sessions surface mounts its column and the counters", () => {
  assert.deepEqual([...regionsFor({ surface: "sessions", glass: "glass" })].sort(), ["counters", "sessions"]);
});

test("only the board reports the panel layout, and only once it knows its fleet", () => {
  const board = { surface: "board", glass: "glass" };
  assert.equal(layoutReport(null, { fleet: "work" }), null);
  assert.equal(layoutReport({ surface: "sessions", glass: "glass" }, { fleet: "work" }), null);
  assert.equal(layoutReport(board, null), null);
  assert.equal(layoutReport(board, {}), null);
  assert.deepEqual(layoutReport(board, { fleet: "work" }), { version: 1, mode: "panel", fleet: "work" });
  assert.deepEqual(layoutReport(board, { fleet: "" }), { version: 1, mode: "panel", fleet: "" });
});
