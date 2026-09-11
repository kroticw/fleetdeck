// The mechanism two columns share, mirrored for the one that sits on the
// other side of the window: which edge of it stays put while it resizes,
// which way the drag handle's pixels are measured, and where in the DOM the
// grip itself sits. See web/js/columnresize.js's own header for why a "side"
// parameter exists at all rather than a second copy of any of this.
//
// Run with: node --test web/tests/columnresize.test.js

import { test } from "node:test";
import assert from "node:assert/strict";

import { widthPercentFrom, mountColumnGrip } from "../js/columnresize.js";
import { MIN_PIXELS, createColumnWidth, SESSIONS_KEYS } from "../js/columnwidth.js";
import { installDOM } from "./fake-dom.js";

// widthPercentFrom is deliberately tested with plain numbers, not a real
// drag: this project's test harness has no layout engine, so
// getBoundingClientRect inside a test returns nothing meaningful, and a
// browser is the only thing that could otherwise answer "does this resize
// the right way". Pulling the arithmetic out into a pure function is what
// makes the direction provable here instead of only by eye.

test("a left column widens as the pointer moves away from its own fixed edge", () => {
  const nearer = widthPercentFrom({ side: "left", clientX: 300, mainWidth: 1000, rootLeft: 0, rootRight: 300 });
  const farther = widthPercentFrom({ side: "left", clientX: 400, mainWidth: 1000, rootLeft: 0, rootRight: 300 });
  assert.equal(nearer, 30);
  assert.equal(farther, 40, "moving the pointer right must widen a left column, not narrow it");
});

// The mirror of the case above. A right column's RIGHT edge is the one
// anchored to the window, so its width is measured from the pointer to
// THAT edge — moving the pointer left (away from the window's own right
// edge) is what widens it, the opposite sign from the left column's case.
test("a right column widens as the pointer moves away from ITS fixed edge, on the other side", () => {
  const nearer = widthPercentFrom({ side: "right", clientX: 700, mainWidth: 1000, rootLeft: 700, rootRight: 1000 });
  const farther = widthPercentFrom({ side: "right", clientX: 600, mainWidth: 1000, rootLeft: 700, rootRight: 1000 });
  assert.equal(nearer, 30);
  assert.equal(farther, 40, "moving the pointer left must widen a right column, not narrow it");
});

test("the pixel floor holds for both sides", () => {
  assert.equal(
    widthPercentFrom({ side: "left", clientX: 1, mainWidth: 1000, rootLeft: 0, rootRight: 300 }),
    (MIN_PIXELS / 1000) * 100,
  );
  assert.equal(
    widthPercentFrom({ side: "right", clientX: 999, mainWidth: 1000, rootLeft: 700, rootRight: 1000 }),
    (MIN_PIXELS / 1000) * 100,
  );
});

test("no track to measure against is no answer, not a wrong one", () => {
  assert.equal(widthPercentFrom({ side: "left", clientX: 300, mainWidth: 0, rootLeft: 0, rootRight: 300 }), null);
});

// mountColumnGrip's own placement: a left column's resizable edge faces the
// centre by being the edge AFTER it in `main`; a right column's faces the
// centre by being the edge BEFORE it. Placed at the wrong one, a person
// still finds the grip in the DOM (it exists) but has nothing to grab it
// against — the exact defect a bare existence check would miss, which is
// why these assert position, not presence.

test("a left column's grip sits after it, toward the centre", () => {
  const dom = installDOM();
  const main = dom.element("main");
  const root = dom.element("aside");
  const center = dom.element("section");
  main.append(root, center);

  const width = createColumnWidth();
  const grip = mountColumnGrip(root, width, { side: "left" });

  assert.equal(main.children.indexOf(grip), main.children.indexOf(root) + 1, "the grip is not immediately after the column");
  assert.equal(main.children.indexOf(grip), main.children.indexOf(center) - 1, "the grip is not immediately before the centre");
  dom.restore();
});

test("a right column's grip sits before it, toward the centre — mirrored, not the same place", () => {
  const dom = installDOM();
  const main = dom.element("main");
  const center = dom.element("section");
  const root = dom.element("aside");
  main.append(center, root);

  const width = createColumnWidth(() => {}, SESSIONS_KEYS);
  const grip = mountColumnGrip(root, width, { side: "right" });

  assert.equal(main.children.indexOf(grip), main.children.indexOf(root) - 1, "the grip is not immediately before the column");
  assert.equal(main.children.indexOf(grip), main.children.indexOf(center) + 1, "the grip is not immediately after the centre");
  // The defect this whole file exists to catch: pinned against the window's
  // own outer edge instead, with nothing past it to drag against.
  assert.notEqual(main.children.indexOf(grip), main.children.length - 1, "the grip ended up pinned against the window's outer edge");
  dom.restore();
});

test("side defaults to left, so every call site written before this parameter existed is unaffected", () => {
  const dom = installDOM();
  const main = dom.element("main");
  const root = dom.element("aside");
  const center = dom.element("section");
  main.append(root, center);

  const width = createColumnWidth();
  const grip = mountColumnGrip(root, width);

  assert.equal(main.children.indexOf(grip), main.children.indexOf(root) + 1);
  dom.restore();
});
