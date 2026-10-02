// web/js/_tests/windowdrag.test.js
//
// Run with: node --test web/js/_tests/windowdrag.test.js

import test from "node:test";
import assert from "node:assert/strict";
import { WINDOW_DRAG_BINDING, titleBarPress } from "../windowdrag.js";

const el = (tagName, ground = false) => ({ tagName, hasAttribute: (name) => ground && name === "data-window-ground" });
const HEADER = el("HEADER", true);
const BUTTON = el("BUTTON");

function press(target, { button = 0, detail = 1 } = {}) {
  const event = {
    target,
    button,
    detail,
    prevented: false,
    preventDefault() {
      this.prevented = true;
    },
  };
  const calls = [];
  const taken = titleBarPress(event, (clicks) => calls.push(clicks));
  return { taken, calls, prevented: event.prevented };
}

test("the binding is the window's", () => {
  assert.equal(WINDOW_DRAG_BINDING, "fleetdeckWindowDrag");
});

test("a press on the header's ground asks the window to drag, and is kept from the page", () => {
  assert.deepEqual(press(HEADER), { taken: true, calls: [1], prevented: true });
});

test("two clicks on the ground ask for the title bar's double click", () => {
  assert.deepEqual(press(HEADER, { detail: 2 }), { taken: true, calls: [2], prevented: true });
});

// A button, a select, the fleet menu: the control's press stays the control's,
// and the window does not move under it.
test("a press on a control in the header is the control's", () => {
  assert.deepEqual(press(BUTTON), { taken: false, calls: [], prevented: false });
  assert.deepEqual(press(el("SELECT"), { detail: 2 }), { taken: false, calls: [], prevented: false });
  assert.deepEqual(press(el("SPAN")), { taken: false, calls: [], prevented: false });
});

test("only the main button drags, and only the first two clicks of it", () => {
  assert.deepEqual(press(HEADER, { button: 2 }), { taken: false, calls: [], prevented: false });
  assert.deepEqual(press(HEADER, { button: 1 }), { taken: false, calls: [], prevented: false });
  assert.deepEqual(press(HEADER, { detail: 3 }), { taken: false, calls: [], prevented: false });
});

// A browser tab has no window to drag: the binding is not defined there.
test("without the window's binding a press is a press", () => {
  const event = { target: HEADER, button: 0, detail: 1, preventDefault: () => assert.fail("prevented") };
  assert.equal(titleBarPress(event, undefined), false);
});
