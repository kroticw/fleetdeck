import test from "node:test";
import assert from "node:assert/strict";
import { installDOM, fireEvent } from "./fake-dom.js";

const dom = installDOM();

const { SCROLLABLE_CLASS, markScrollable, markScrollablesWithin, watchScrollables } =
  await import("../js/scrollable.js");

// A box that a test describes rather than lays out: how wide its content is,
// how wide its window on that content is, and how far along it has scrolled.
//
// Appended to the document, and not as a formality: a node outside the page has
// no layout, so both widths read zero however they were set, and a measurement
// taken from it is false in the one direction that matters — it always says
// "fits". A test that measures a detached box proves nothing, which is the
// defect this file exists to catch.
function box(scrollWidth, clientWidth, scrollLeft = 0, parent = dom.document.body) {
  const el = dom.element("div");
  el.scrollWidth = scrollWidth;
  el.clientWidth = clientWidth;
  el.scrollLeft = scrollLeft;
  parent.appendChild(el);
  return el;
}

test("content wider than its box is marked", () => {
  const el = box(600, 300);
  markScrollable(el);
  assert.ok(el.classList.contains(SCROLLABLE_CLASS));
});

test("content that fits is not marked", () => {
  const el = box(300, 300);
  markScrollable(el);
  assert.ok(!el.classList.contains(SCROLLABLE_CLASS));
});

test("scrolled to the end is not marked", () => {
  // The indicator promises something further right. At the end there is
  // nothing further right, and a promise that cannot be kept is worse than
  // none: it fades the last cell of the table for no reason.
  const el = box(600, 300, 300);
  markScrollable(el);
  assert.ok(!el.classList.contains(SCROLLABLE_CLASS));
});

test("a one-pixel remainder is layout rounding, not content", () => {
  const el = box(601, 300, 300);
  markScrollable(el);
  assert.ok(!el.classList.contains(SCROLLABLE_CLASS));
});

test("two pixels is content", () => {
  const el = box(602, 300, 300);
  assert.ok(el.classList.contains(SCROLLABLE_CLASS) === false);
  markScrollable(el);
  assert.ok(el.classList.contains(SCROLLABLE_CLASS));
});

test("the mark comes off again when scrolling reaches the end", () => {
  const el = box(600, 300);
  markScrollable(el);
  assert.ok(el.classList.contains(SCROLLABLE_CLASS));
  el.scrollLeft = 300;
  markScrollable(el);
  assert.ok(!el.classList.contains(SCROLLABLE_CLASS), "toggled off, not left behind");
});

test("marking keeps the classes a node already had", () => {
  const el = box(600, 300);
  el.className = "md-table";
  markScrollable(el);
  assert.ok(el.className.includes("md-table"));
  el.scrollLeft = 300;
  markScrollable(el);
  assert.equal(el.className.trim(), "md-table");
});

// The finding this module exists for: the mechanism was written once and
// applied to the first place that needed it. Every place gets it, not the
// first — so the helper walks the whole list.

test("every matching box is marked, not the first", () => {
  const root = dom.element("div");
  dom.document.body.appendChild(root);
  const wide = box(600, 300, 0, root);
  const narrow = box(300, 300, 0, root);
  const alsoWide = box(900, 300, 0, root);
  for (const el of [wide, narrow, alsoWide]) el.className = "md-table";
  markScrollablesWithin(root, ".md-table");
  assert.ok(wide.classList.contains(SCROLLABLE_CLASS));
  assert.ok(!narrow.classList.contains(SCROLLABLE_CLASS), "one that fits stays unmarked");
  assert.ok(alsoWide.classList.contains(SCROLLABLE_CLASS), "the last one too");
});

test("watching twice does not attach two listeners", () => {
  // Panels re-render; a watcher attached per render would fire N times per
  // scroll after N renders.
  const root = dom.element("div");
  watchScrollables(root, ".md-table");
  watchScrollables(root, ".md-table");
  assert.equal(root.listeners.get("scroll")?.size ?? 0, 1);
});

test("scrolling inside the root re-measures", () => {
  const root = dom.element("div");
  dom.document.body.appendChild(root);
  const table = box(600, 300, 0, root);
  table.className = "md-table";
  watchScrollables(root, ".md-table");
  fireEvent(root, "scroll");
  assert.ok(table.classList.contains(SCROLLABLE_CLASS));
  table.scrollLeft = 300;
  fireEvent(root, "scroll");
  assert.ok(!table.classList.contains(SCROLLABLE_CLASS));
});

test("a window resize re-measures", () => {
  // The same content crosses the fits/doesn't boundary on a resize with no new
  // snapshot to trigger a render.
  const root = dom.element("div");
  dom.document.body.appendChild(root);
  const table = box(600, 300, 0, root);
  table.className = "md-table";
  watchScrollables(root, ".md-table");
  fireEvent(dom.window, "resize");
  assert.ok(table.classList.contains(SCROLLABLE_CLASS));
});

// --- measuring a node that is not in the page yet ---
//
// The defect a live browser found and this file could not: the mark was taken
// while the body was still being built, before it was put into the document.
// A detached node has no layout, so every box "fitted" and nothing was ever
// marked. It only appeared after a resize — that is, after the reader had
// already discovered the scrolling for themselves.

test("a detached box cannot be measured and is not marked", () => {
  const loose = dom.element("div");
  loose.scrollWidth = 900;
  loose.clientWidth = 300;
  assert.equal(loose.scrollWidth, 0, "no layout outside the document");
  assert.equal(loose.clientWidth, 0, "neither width, not just the one");
  markScrollable(loose);
  assert.ok(!loose.classList.contains(SCROLLABLE_CLASS));
});

test("the same box, once in the page, is marked", () => {
  // The control for the test above: the widths are unchanged, only the node's
  // place in the page is — which is exactly the difference the old tests could
  // not express, and so did not notice.
  const el = dom.element("div");
  el.scrollWidth = 900;
  el.clientWidth = 300;
  dom.document.body.appendChild(el);
  markScrollable(el);
  assert.ok(el.classList.contains(SCROLLABLE_CLASS));
});

test("marking a subtree that is not in the page marks nothing", () => {
  const detachedRoot = dom.element("div");
  const table = dom.element("div");
  table.className = "md-table";
  table.scrollWidth = 900;
  table.clientWidth = 300;
  detachedRoot.appendChild(table);
  markScrollablesWithin(detachedRoot, ".md-table");
  assert.ok(!table.classList.contains(SCROLLABLE_CLASS), "measured too early");
  dom.document.body.appendChild(detachedRoot);
  markScrollablesWithin(detachedRoot, ".md-table");
  assert.ok(table.classList.contains(SCROLLABLE_CLASS), "and correct once it is in the page");
});

