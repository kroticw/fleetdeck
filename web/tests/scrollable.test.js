import test from "node:test";
import assert from "node:assert/strict";
import { installDOM, fireEvent } from "./fake-dom.js";

const dom = installDOM();

const { SCROLLABLE_CLASS, markScrollable, markScrollablesWithin, watchScrollables } =
  await import("../js/scrollable.js");

// A box that a test describes rather than lays out: how wide its content is,
// how wide its window on that content is, and how far along it has scrolled.
function box(scrollWidth, clientWidth, scrollLeft = 0) {
  const el = dom.element("div");
  el.scrollWidth = scrollWidth;
  el.clientWidth = clientWidth;
  el.scrollLeft = scrollLeft;
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
  const wide = box(600, 300);
  const narrow = box(300, 300);
  const alsoWide = box(900, 300);
  for (const el of [wide, narrow, alsoWide]) {
    el.className = "md-table";
    root.appendChild(el);
  }
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
  const table = box(600, 300);
  table.className = "md-table";
  root.appendChild(table);
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
  const table = box(600, 300);
  table.className = "md-table";
  root.appendChild(table);
  watchScrollables(root, ".md-table");
  fireEvent(dom.window, "resize");
  assert.ok(table.classList.contains(SCROLLABLE_CLASS));
});
