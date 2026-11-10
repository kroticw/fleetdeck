// web/js/_tests/boardfold.test.js
//
// The done column holds every card ever finished, and reading it is the rare
// case: it is folded into a narrow strip with its count, opened by a click,
// and stays a column a card can be dropped on either way.

import test from "node:test";
import assert from "node:assert/strict";
import { columnHTML, foldedStages, toggleFolded } from "../board.js";

function storage() {
  const store = new Map();
  return {
    getItem: (k) => (store.has(k) ? store.get(k) : null),
    setItem: (k, v) => store.set(k, String(v)),
  };
}

const done = [{ path: "/b/a.md", title: "finished", stage: "done" }, { path: "/b/b.md", title: "also", stage: "done" }];

test("a folded column is a strip with its name and count, and still a drop target", () => {
  const html = columnHTML("done", "done", done, new Set(), new Set(), [], new Map(), true);
  assert.match(html, /class="kcol kcol-folded" data-stage="done"/);
  assert.match(html, /class="kcol-unfold" data-stage="done" aria-expanded="false"/);
  assert.match(html, /class="kcount">2</);
  assert.doesNotMatch(html, /class="kcard/, "a folded column draws none of its cards");
});

test("an open done column offers to fold, other columns do not", () => {
  assert.match(columnHTML("done", "done", done, new Set()), /class="kcol-fold" data-stage="done" aria-expanded="true"/);
  assert.doesNotMatch(columnHTML("review", "review", [], new Set()), /kcol-fold/);
});

test("done is folded until the operator opens it, and the choice is kept", () => {
  const s = storage();
  assert.deepEqual([...foldedStages(s)], ["done"]);
  toggleFolded("done", s);
  assert.deepEqual([...foldedStages(s)], []);
  toggleFolded("done", s);
  assert.deepEqual([...foldedStages(s)], ["done"]);
});

test("a storage that throws leaves done folded and does not break the board", () => {
  const boom = { getItem: () => { throw new Error("blocked"); }, setItem: () => { throw new Error("blocked"); } };
  assert.deepEqual([...foldedStages(boom)], ["done"]);
  toggleFolded("done", boom);
});
