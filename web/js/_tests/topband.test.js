// web/js/_tests/topband.test.js
//
// Run with: node --test web/js/_tests/topband.test.js

import test from "node:test";
import assert from "node:assert/strict";
import { TOP_BAND_MAX, freeTopHeight, isGround, watchTopBand } from "../topband.js";

const el = (tagName, ground = false) => ({ tagName, hasAttribute: (name) => ground && name === "data-window-ground" });
const BODY = el("BODY");
const BOARD = el("DIV", true);

test("an empty page gives the whole band", () => {
  assert.equal(freeTopHeight(() => BODY, 1440), TOP_BAND_MAX);
});

test("the page's marked ground and a point off the page count as empty", () => {
  assert.equal(isGround(BOARD), true);
  assert.equal(isGround(null), true);
  assert.equal(isGround(el("HTML")), true);
  assert.equal(isGround(el("MAIN")), true);
  assert.equal(freeTopHeight((x) => (x > 700 ? null : BOARD), 1440), TOP_BAND_MAX);
});

test("the band ends where something begins, anywhere across the page", () => {
  const banner = el("DIV");
  // The build banner or the new card form, 56 points down, somewhere in the middle.
  const at = (x, y) => (y >= 56 && x > 400 && x < 1000 ? banner : BOARD);
  assert.equal(freeTopHeight(at, 1440), 56);
});

// The new card form drops under the capsules, from the board's top inset less
// 8 pt (app.css, #tabs on the board): its top is where the band ends.
test("the open new card form under the capsules ends the band at its top", () => {
  const form = el("DIV");
  const at = (x, y) => (y >= TOP_BAND_MAX - 8 && x >= 394 && x < 842 ? form : BOARD);
  assert.equal(freeTopHeight(at, 1512), TOP_BAND_MAX - 8);
});

test("a dimmed board, a sheet or cards at the top leave no band at all", () => {
  const scrim = el("DIV");
  assert.equal(freeTopHeight(() => scrim, 1440), 0);
  const card = el("ARTICLE");
  assert.equal(freeTopHeight((x, y) => (x < 30 && y < 10 ? card : BOARD), 1440), 0);
});

test("a button or text is never ground", () => {
  assert.equal(isGround(el("BUTTON")), false);
  assert.equal(isGround(el("P")), false);
  assert.equal(isGround({ tagName: "SPAN" }), false);
});

function fakeWindow(elementAt) {
  const listeners = {};
  let observer = null;
  const win = {
    innerWidth: 1440,
    document: {
      readyState: "complete",
      elementFromPoint: (x, y) => elementAt.current(x, y),
      addEventListener() {},
    },
    requestAnimationFrame: (f) => f(),
    addEventListener: (name, f) => {
      listeners[name] = f;
    },
    MutationObserver: class {
      constructor(f) {
        observer = f;
      }
      observe() {}
    },
  };
  return { win, listeners, mutate: () => observer() };
}

test("the window hears the band at once, and again only when it changes", () => {
  const elementAt = { current: () => BODY };
  const { win, listeners, mutate } = fakeWindow(elementAt);
  const heard = [];
  watchTopBand(win, (height) => heard.push(height));
  assert.deepEqual(heard, [TOP_BAND_MAX]);
  mutate();
  listeners.resize();
  assert.deepEqual(heard, [TOP_BAND_MAX], "nothing changed at the top");
  const sheet = el("SECTION");
  elementAt.current = () => sheet;
  mutate();
  listeners.scroll();
  assert.deepEqual(heard, [TOP_BAND_MAX, 0]);
});
