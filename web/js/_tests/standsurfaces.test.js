// web/js/_tests/standsurfaces.test.js
//
// What the stand hears of the window's surfaces beyond the board's sideways
// scrolling (web/js/standreport.js): the board's scrolling down its height,
// and the grounds the sessions surface paints under its list.
//
// v0.10.1 on the operator's glass: a column longer than the window made the
// whole board scroll down, and WebKit drew the classic 15 px bar for it at the
// board's right edge, which is the sessions glass's left edge. It read as a
// second, darker island behind the sessions one. The stand's board had a card
// per stage, never taller than the window, and its report said nothing of
// height.
//
// Run with: node --test web/js/_tests/standsurfaces.test.js

import test from "node:test";
import assert from "node:assert/strict";
import { boardScrollReport, groundsReport, watchGrounds } from "../standreport.js";

const NO_BORDER = { borderTopWidth: "0px", borderBottomWidth: "0px", borderLeftWidth: "0px", borderRightWidth: "0px" };

// A board in the window, its columns boxes of their own with the sizes WebKit
// gives them: offsetWidth less clientWidth is the bar down a box's right side.
function tallBoard({ board = {}, overflowY = "auto", columns = [] } = {}) {
  const cols = columns.map((c) => ({ getBoundingClientRect: () => ({ right: 600 }), ...c }));
  const el = {
    scrollWidth: 800,
    clientWidth: 800,
    offsetWidth: 800,
    scrollHeight: 700,
    clientHeight: 700,
    offsetHeight: 700,
    scrollLeft: 0,
    getBoundingClientRect: () => ({ left: 0, right: 800 }),
    querySelectorAll: () => cols,
    ...board,
  };
  const styles = new Map([[el, { overflowX: "auto", overflowY, ...NO_BORDER }]]);
  for (const c of cols) styles.set(c, { ...NO_BORDER });
  const root = { clientWidth: 1000 };
  const win = {
    document: { documentElement: root },
    getComputedStyle: (node) => (node === root ? { getPropertyValue: () => "200px" } : styles.get(node)),
  };
  return { win, board: el };
}

const column = (sizes) => ({ offsetWidth: 214, clientWidth: 214, scrollHeight: 300, clientHeight: 640, ...sizes });

test("a board taller than its room reports the bar down its right edge", () => {
  const { win, board } = tallBoard({
    board: { clientWidth: 785, scrollHeight: 2400 },
    columns: [column({ scrollHeight: 2300, clientHeight: 2300 })],
  });
  const report = boardScrollReport(win, board);
  assert.equal(report.scrollHeight, 2400);
  assert.equal(report.clientHeight, 700);
  assert.equal(report.overflowY, "auto");
  assert.equal(report.scrollbarWidth, 15);
  assert.equal(report.columnScrollbarWidth, 0);
  assert.equal(report.contentTallerThanRoom, true);
});

test("a board whose columns scroll on their own reports their bars and none of its own", () => {
  const { win, board } = tallBoard({
    overflowY: "hidden",
    columns: [column({ clientWidth: 208, scrollHeight: 2000 }), column({})],
  });
  const report = boardScrollReport(win, board);
  assert.equal(report.overflowY, "hidden");
  assert.equal(report.scrollbarWidth, 0);
  assert.equal(report.columnScrollbarWidth, 6);
  assert.equal(report.contentTallerThanRoom, true);
});

test("a board that fits its room reports nothing taller than it", () => {
  const { win, board } = tallBoard({ columns: [column({}), column({})] });
  const report = boardScrollReport(win, board);
  assert.equal(report.scrollbarWidth, 0);
  assert.equal(report.columnScrollbarWidth, 0);
  assert.equal(report.contentTallerThanRoom, false);
});

// A document whose elements have the computed grounds given by selector.
function surfaceDocument(glass, grounds) {
  const styles = new Map();
  const bySelector = {};
  for (const [selector, list] of Object.entries(grounds)) {
    bySelector[selector] = list.map((style) => {
      const el = { selector };
      styles.set(el, { backgroundColor: "rgba(0, 0, 0, 0)", backgroundImage: "none", borderRadius: "0px", boxShadow: "none", ...style });
      return el;
    });
  }
  const root = { dataset: glass ? { glass } : {} };
  return {
    document: { documentElement: root, querySelectorAll: (selector) => bySelector[selector] ?? [] },
    getComputedStyle: (el) => styles.get(el),
  };
}

test("the sessions surface reports the ground and the corners of every box its list lies in", () => {
  const win = surfaceDocument("glass", {
    html: [{}],
    body: [{}],
    main: [{}],
    "#sessions": [{ borderRadius: "8px", backgroundColor: "rgb(27, 30, 36)" }],
    "#header": [{}],
    ".col-size": [{}],
    ".slist-head": [{}],
    ".fleet-group-head": [{}, { boxShadow: "rgb(0, 0, 0) 0px 1px 0px 0px" }],
  });
  const report = groundsReport(win, "sessions");
  assert.equal(report.surface, "sessions");
  assert.equal(report.report, "grounds");
  assert.equal(report.glass, "glass");
  assert.deepEqual(
    report.elements.map((e) => e.selector),
    ["html", "body", "main", "#sessions", "#header", ".col-size", ".slist-head", ".fleet-group-head", ".fleet-group-head"],
  );
  assert.deepEqual(report.elements[3], {
    selector: "#sessions",
    background: "rgb(27, 30, 36)",
    image: "none",
    radius: "8px",
    shadow: "none",
  });
  assert.equal(report.elements[8].shadow, "rgb(0, 0, 0) 0px 1px 0px 0px");
});

// A surface that has not drawn a head yet reports what it has; the window
// without a material says so rather than a guess.
test("the sessions surface reports only the boxes it has, and no material it was not given", () => {
  const report = groundsReport(surfaceDocument(undefined, { body: [{}] }), "sessions");
  assert.equal(report.glass, null);
  assert.deepEqual(
    report.elements.map((e) => e.selector),
    ["body"],
  );
});

test("the window hears the sessions surface's grounds once, and again when its list draws something new", () => {
  const win = surfaceDocument("opaque", { body: [{ backgroundColor: "rgb(255, 255, 255)" }] });
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
  const list = {};
  const heard = [];
  watchGrounds(win, list, (r) => heard.push(r));
  assert.equal(heard.length, 1);
  assert.equal(observed.target, list);
  assert.equal(observed.options.childList, true);
  assert.equal(observed.options.subtree, true);
  onMutation();
  assert.equal(heard.length, 1, "nothing changed");
  const head = {};
  win.document.querySelectorAll = (selector) => (selector === ".slist-head" ? [head] : selector === "body" ? [] : []);
  win.getComputedStyle = () => ({ backgroundColor: "rgb(1, 2, 3)", backgroundImage: "none", borderRadius: "0px", boxShadow: "none" });
  onMutation();
  assert.equal(heard.length, 2);
  assert.equal(heard[1].elements[0].selector, ".slist-head");
});
