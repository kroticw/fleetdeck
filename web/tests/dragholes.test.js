// A side panel's controls in the window's top strip (web/js/dragholes.js): the
// window drags by the whole strip and lets presses through only where these are.

import { test } from "node:test";
import assert from "node:assert/strict";

import { controlRects, watchDragHoles } from "../js/dragholes.js";

const el = (left, top, width, height) => ({ getBoundingClientRect: () => ({ left, top, width, height, right: left + width, bottom: top + height }) });

test("only the controls that reach into the strip are holes, cut to it", () => {
  const doc = { querySelectorAll: () => [el(10, 12, 30, 20), el(10, 40, 30, 20), el(10, 60, 30, 20), el(0, 0, 0, 0)] };
  assert.deepEqual(controlRects(doc, 52), [
    { x: 10, y: 12, w: 30, h: 20 },
    { x: 10, y: 40, w: 30, h: 12 },
  ]);
});

test("the holes are told once, and again only when they change", () => {
  const sent = [];
  let rects = [el(10, 12, 30, 20)];
  const doc = { querySelectorAll: () => rects };
  const report = watchDragHoles(doc, (holes) => sent.push(holes), { height: 52, observe: false });
  report();
  report();
  rects = [el(10, 14, 30, 20)];
  report();
  assert.deepEqual(sent, [[{ x: 10, y: 12, w: 30, h: 20 }], [{ x: 10, y: 14, w: 30, h: 20 }]]);
});
