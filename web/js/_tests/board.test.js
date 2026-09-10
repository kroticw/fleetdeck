// web/js/_tests/board.test.js
//
// See web/js/_tests/header.test.js for why this directory: a leading
// underscore excludes it from web/embed.go's plain (non "all:") //go:embed
// pattern, so it never ships in the binary or is served over HTTP.
//
// Run with: node --test web/js/_tests/board.test.js

import test from "node:test";
import assert from "node:assert/strict";
import { columnHTML } from "../board.js";

test("an empty column is marked kcol-empty, so app.css can shrink it", () => {
  const html = columnHTML("new", "new", [], new Set());
  assert.ok(/class="kcol[^"]*\bkcol-empty\b/.test(html), "zero cards must carry the empty marker");
});

test("a column carrying cards is not marked empty", () => {
  const html = columnHTML("done", "done", [{ path: "a.md", title: "x" }], new Set());
  assert.ok(!/\bkcol-empty\b/.test(html), "a non-empty column must not claim the empty marker");
});
