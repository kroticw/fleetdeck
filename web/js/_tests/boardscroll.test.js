// web/js/_tests/boardscroll.test.js
//
// In the fleetdeck window the board's columns scroll on their own (web/app.css),
// and the panel sends a snapshot every second (internal/server/ws.go), for each
// of which the board draws its columns again. A column drawn anew is at its
// top: the operator could never read a long done column to its end. A column
// keeps where it was scrolled to, whether the snapshot changed or not.
//
// Run with: node --test web/js/_tests/boardscroll.test.js

import test from "node:test";
import assert from "node:assert/strict";
import { render } from "../board.js";

// A board whose innerHTML makes new column boxes, at their top, as a browser's
// does: only the stage each column is for is read off the markup.
function boardRoot() {
  let html = "";
  let columns = [];
  return {
    classList: { toggle: () => {} },
    scrollLeft: 0,
    clientWidth: 800,
    scrollWidth: 800,
    get innerHTML() {
      return html;
    },
    set innerHTML(value) {
      html = value;
      columns = [...value.matchAll(/class="kcol[^"]*" data-stage="([^"]*)"/g)].map((m) => ({ dataset: { stage: m[1] }, scrollTop: 0 }));
    },
    querySelectorAll(selector) {
      return selector === ":scope > .kcol" ? columns : [];
    },
    column(stage) {
      return columns.find((c) => c.dataset.stage === stage);
    },
  };
}

const card = (n, stage) => ({ path: `/b/T-${n}.md`, id: `T-${n}`, title: `card ${n}`, stage, zone: "planned", progress: 0 });
const snapshot = (done) => ({ cards: [card(1, "active"), ...Array.from({ length: done }, (_, i) => card(100 + i, "done"))] });

test("a scrolled column keeps its place when the same snapshot comes again", () => {
  const root = boardRoot();
  render(root, snapshot(24));
  root.column("done").scrollTop = 240;
  render(root, snapshot(24));
  assert.equal(root.column("done").scrollTop, 240);
});

test("a scrolled column keeps its place when the snapshot changes, and the others stay at their top", () => {
  const root = boardRoot();
  render(root, snapshot(24));
  root.column("done").scrollTop = 240;
  render(root, snapshot(25));
  assert.equal(root.column("done").scrollTop, 240);
  assert.equal(root.column("active").scrollTop, 0);
});

// A board that failed to load has no columns; when it loads again they start
// at their top, not where columns of a board long gone were.
test("columns drawn after a board error start at their top", () => {
  const root = boardRoot();
  render(root, snapshot(24));
  root.column("done").scrollTop = 240;
  render(root, { boardError: "no cards directory" });
  render(root, snapshot(24));
  assert.equal(root.column("done").scrollTop, 0);
});
