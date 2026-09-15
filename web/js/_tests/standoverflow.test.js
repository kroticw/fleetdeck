// web/js/_tests/standoverflow.test.js
//
// On a stand a side surface says in the window's log what of it does not fit
// (web/js/standoverflow.js): every box drawn wider than the room it gives its
// content, every box that reaches past the surface's edges, and whether the
// page is wider than its web view. Folded, the surface is a 48 px strip, and it
// says what the strip shows and where its unfold control is. v0.11.0's dev
// build ran the sessions counters off that strip's edge and scrolled the
// orchestrator's page sideways under its strip; a screenshot shows cut text or
// a scroll bar, and only the page can say which box is at fault.
//
// Run with: node --test web/js/_tests/standoverflow.test.js

import test from "node:test";
import assert from "node:assert/strict";
import { overflowReport, watchOverflow } from "../standoverflow.js";

// A box as the page measures it: its class, its size and scroll size, and its
// rectangle; rects is how many client rectangles it has, 0 for one not drawn.
const box = ({ className = "", tag = "DIV", clientWidth = 40, scrollWidth = clientWidth, left = 0, right = left + clientWidth, top = 0, bottom = top + 20, rects = 1 } = {}) => ({
  tagName: tag,
  className,
  clientWidth,
  scrollWidth,
  getClientRects: () => Array.from({ length: rects }),
  getBoundingClientRect: () => ({ left, right, top, bottom }),
  contains(other) {
    return other === this;
  },
});

// surface is a side surface width wide whose page is scrollWidth wide, folded
// or not, drawing boxes; unfold is the column's unfold control, and hit what a
// press at a point reaches.
function surface({ width = 48, scrollWidth = width, folded = true, boxes = [], unfold = null, hit = (u) => u } = {}) {
  const column = {
    dataset: folded ? { folded: "1" } : {},
    querySelector: (selector) => (assert.equal(selector, ".col-size-unfold"), unfold),
  };
  return {
    column,
    win: {
      document: {
        documentElement: { clientWidth: width, scrollWidth },
        body: { querySelectorAll: (selector) => (assert.equal(selector, "*"), boxes) },
        elementFromPoint: () => hit(unfold),
      },
    },
  };
}

test("a folded rail where everything fits reports it folded, its width and nothing that overflows", () => {
  const unfold = box({ className: "col-size-btn col-size-unfold", tag: "BUTTON", left: 7, clientWidth: 34, top: 60, bottom: 94 });
  const { win, column } = surface({ boxes: [box({ className: "col-size", clientWidth: 48 }), unfold], unfold });
  assert.deepEqual(overflowReport(win, column, "orchestrator"), {
    surface: "orchestrator",
    report: "overflow",
    folded: true,
    width: 48,
    scrollWidth: 48,
    overflowing: [],
    shown: ["div.col-size", "button.col-size-btn.col-size-unfold"],
    unfold: { left: 7, top: 60, right: 41, bottom: 94, reachable: true },
  });
});

test("the report names the surface it is asked for", () => {
  const { win, column } = surface();
  assert.equal(overflowReport(win, column, "sessions").surface, "sessions");
});

test("a box wider than its room and a box past the rail's edge are both named", () => {
  const { win, column } = surface({
    boxes: [
      box({ className: "counters", clientWidth: 48, scrollWidth: 131 }),
      box({ className: "counter counter-waiting", tag: "SPAN", clientWidth: 0, scrollWidth: 0, left: 20, right: 92 }),
      box({ className: "sfold-mark", left: 7, clientWidth: 34 }),
    ],
  });
  assert.deepEqual(overflowReport(win, column, "sessions").overflowing, [
    { element: "div.counters", scrollWidth: 131, clientWidth: 48, left: 0, right: 48 },
    { element: "span.counter.counter-waiting", scrollWidth: 0, clientWidth: 0, left: 20, right: 92 },
  ]);
});

// The orchestrator's page under its folded strip: a header starting past the
// window's buttons, wider than the strip, and the page scrolling sideways.
test("a page wider than its web view says how wide, and a box drawn wholly past the edge is not shown", () => {
  const { win, column } = surface({
    scrollWidth: 312,
    boxes: [box({ tag: "HEADER", clientWidth: 48, scrollWidth: 312 }), box({ className: "brand", tag: "SPAN", left: 79, right: 160 })],
  });
  const report = overflowReport(win, column, "orchestrator");
  assert.equal(report.scrollWidth, 312);
  assert.deepEqual(
    report.overflowing.map((o) => o.element),
    ["header", "span.brand"],
  );
  assert.deepEqual(report.shown, ["header"]);
});

// Run 34940253751: the fleet menu's icon was named svg.[object.SVGAnimatedString].
test("an SVG box is named by its classes, and one with none by its tag", () => {
  const icon = box({ tag: "svg", left: 175, right: 195 });
  icon.className = { baseVal: "app-icon-glyph" };
  const bare = box({ tag: "path", left: 180, right: 190 });
  bare.className = { baseVal: "" };
  const { win, column } = surface({ boxes: [icon, bare] });
  assert.deepEqual(
    overflowReport(win, column, "orchestrator").overflowing.map((o) => o.element),
    ["svg.app-icon-glyph", "path"],
  );
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
  assert.deepEqual(overflowReport(win, column, "sessions").overflowing, []);
});

test("an unfold control that a press does not reach, or none drawn, is said so", () => {
  const unfold = box({ className: "col-size-unfold", tag: "BUTTON", left: 7, clientWidth: 34 });
  const covered = surface({ boxes: [unfold], unfold, hit: () => box({ className: "over" }) });
  assert.equal(overflowReport(covered.win, covered.column, "orchestrator").unfold.reachable, false);
  const nothing = surface({ boxes: [unfold], unfold, hit: () => null });
  assert.equal(overflowReport(nothing.win, nothing.column, "orchestrator").unfold.reachable, false);
  const hidden = box({ className: "col-size-unfold", tag: "BUTTON", rects: 0 });
  const none = surface({ unfold: hidden });
  assert.equal(overflowReport(none.win, none.column, "orchestrator").unfold, null);
});

test("an open panel says it is not folded", () => {
  const { win, column } = surface({ width: 348, folded: false, boxes: [box({ className: "o-head" })] });
  const report = overflowReport(win, column, "orchestrator");
  assert.equal(report.folded, false);
  assert.equal("shown" in report, false);
  assert.equal("unfold" in report, false);
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
  watchOverflow(win, column, "orchestrator", (r) => heard.push(r));
  assert.equal(heard.length, 1);
  assert.equal(heard[0].surface, "orchestrator");
  assert.equal(observed.target, win.document.body);
  assert.deepEqual(observed.options, { childList: true, subtree: true, attributes: true, attributeFilter: ["data-folded"] });
  onMutation();
  assert.equal(heard.length, 1, "nothing changed");
  boxes.push(box({ className: "counters", clientWidth: 48, scrollWidth: 131 }));
  onMutation();
  assert.equal(heard.length, 2);
  assert.equal(heard[1].overflowing.length, 1);
});
