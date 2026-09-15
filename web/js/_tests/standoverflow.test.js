// web/js/_tests/standoverflow.test.js
//
// On a stand the sessions surface says in the window's log what of it does not
// fit (web/js/standoverflow.js): every box drawn wider than the room it gives
// its content, and every box that reaches past the surface's edges. Folded, the
// surface is a 48 px rail, and v0.10.2's dev build ran its counters off that
// rail's edge; a screenshot shows cut text, and only the page can say which box
// is cut.
//
// Run with: node --test web/js/_tests/standoverflow.test.js

import test from "node:test";
import assert from "node:assert/strict";
import { overflowReport, watchOverflow } from "../standoverflow.js";

// A box as the page measures it: its class, its size and scroll size, and its
// rectangle; rects is how many client rectangles it has, 0 for one not drawn.
const box = ({ className = "", tag = "DIV", clientWidth = 40, scrollWidth = clientWidth, left = 0, right = left + clientWidth, rects = 1 } = {}) => ({
  tagName: tag,
  className,
  clientWidth,
  scrollWidth,
  getClientRects: () => Array.from({ length: rects }),
  getBoundingClientRect: () => ({ left, right }),
});

function surface({ width = 48, folded = true, boxes = [] } = {}) {
  const column = { dataset: folded ? { folded: "1" } : {} };
  return {
    column,
    win: {
      document: {
        documentElement: { clientWidth: width },
        body: { querySelectorAll: (selector) => (assert.equal(selector, "*"), boxes) },
      },
    },
  };
}

test("a folded rail where everything fits reports it folded, its width and nothing that overflows", () => {
  const { win, column } = surface({ boxes: [box({ className: "sfold", clientWidth: 48 }), box({ className: "sfold-mark", left: 7, clientWidth: 34 })] });
  assert.deepEqual(overflowReport(win, column), { surface: "sessions", report: "overflow", folded: true, width: 48, overflowing: [] });
});

test("a box wider than its room and a box past the rail's edge are both named", () => {
  const { win, column } = surface({
    boxes: [
      box({ className: "counters", clientWidth: 48, scrollWidth: 131 }),
      box({ className: "counter counter-waiting", tag: "SPAN", clientWidth: 0, scrollWidth: 0, left: 20, right: 92 }),
      box({ className: "sfold-mark", left: 7, clientWidth: 34 }),
    ],
  });
  assert.deepEqual(overflowReport(win, column).overflowing, [
    { element: "div.counters", scrollWidth: 131, clientWidth: 48, left: 0, right: 48 },
    { element: "span.counter.counter-waiting", scrollWidth: 0, clientWidth: 0, left: 20, right: 92 },
  ]);
});

// Layout rounds to fractions of a pixel; a box a fraction past the edge is not
// cut text. Neither is a box that is not drawn at all.
test("a fraction of a pixel and a box not drawn are not overflow", () => {
  const { win, column } = surface({
    boxes: [
      box({ className: "sfold-mark", left: 6.6, right: 48.4, clientWidth: 34, scrollWidth: 35 }),
      box({ className: "counters", clientWidth: 0, scrollWidth: 400, left: 0, right: 400, rects: 0 }),
    ],
  });
  assert.deepEqual(overflowReport(win, column).overflowing, []);
});

test("an open panel says it is not folded", () => {
  const { win, column } = surface({ width: 348, folded: false });
  assert.equal(overflowReport(win, column).folded, false);
});

test("the window hears the surface's overflow once, and again when it is drawn or folded anew", () => {
  const boxes = [];
  const { win, column } = surface({ boxes });
  let onMutation = null;
  let observed = null;
  win.requestAnimationFrame = (f) => f();
  win.addEventListener = () => {};
  win.MutationObserver = class {
    constructor(f) {
      onMutation = f;
    }
    observe(target, options) {
      observed = { target, options };
    }
  };
  const heard = [];
  watchOverflow(win, column, (r) => heard.push(r));
  assert.equal(heard.length, 1);
  assert.equal(observed.target, win.document.body);
  assert.deepEqual(observed.options, { childList: true, subtree: true, attributes: true, attributeFilter: ["data-folded"] });
  onMutation();
  assert.equal(heard.length, 1, "nothing changed");
  boxes.push(box({ className: "counters", clientWidth: 48, scrollWidth: 131 }));
  onMutation();
  assert.equal(heard.length, 2);
  assert.equal(heard[1].overflowing.length, 1);
});
