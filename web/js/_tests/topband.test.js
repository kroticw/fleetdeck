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

// A window whose page never draws a frame: the window that takes over from an
// updated one comes up behind it, and its board page loads with nothing of it
// on screen. WebKit runs no frame callback for a page that is not drawn, so a
// band measured only in one is never measured at all: v0.11.0's window, started
// by an update, heard nothing about its band and could not be dragged anywhere.
function fakeWindow(elementAt, { drawsFrames = true } = {}) {
  const listeners = {};
  let observer = null;
  const timers = [];
  const frames = [];
  const win = {
    innerWidth: 1440,
    document: {
      readyState: "complete",
      elementFromPoint: (x, y) => elementAt.current(x, y),
      addEventListener() {},
    },
    requestAnimationFrame: (f) => {
      if (drawsFrames) f();
      else frames.push(f);
    },
    setTimeout: (f) => timers.push(f),
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
  const run = (queue) => {
    for (const f of queue.splice(0, queue.length)) f();
  };
  return { win, listeners, mutate: () => observer(), tick: () => run(timers), draw: () => run(frames) };
}

test("the window hears the band at once, and again only when it changes", () => {
  const elementAt = { current: () => BODY };
  const { win, listeners, mutate, tick } = fakeWindow(elementAt);
  const heard = [];
  watchTopBand(win, (height) => heard.push(height));
  assert.deepEqual(heard, [TOP_BAND_MAX]);
  tick();
  assert.deepEqual(heard, [TOP_BAND_MAX], "the frame measured it; the timer behind it must not measure it again");
  mutate();
  listeners.resize();
  assert.deepEqual(heard, [TOP_BAND_MAX], "nothing changed at the top");
  const sheet = el("SECTION");
  elementAt.current = () => sheet;
  mutate();
  listeners.scroll();
  assert.deepEqual(heard, [TOP_BAND_MAX, 0]);
});

test("a page that never draws still tells the window its band", () => {
  const elementAt = { current: () => BODY };
  const { win, tick } = fakeWindow(elementAt, { drawsFrames: false });
  const heard = [];
  watchTopBand(win, (height) => heard.push(height));
  tick();
  assert.deepEqual(heard, [TOP_BAND_MAX], "the page drew no frame, so the window was left with no band");
});

test("a page that never draws is still heard every time its top changes", () => {
  const elementAt = { current: () => BODY };
  const { win, listeners, mutate, tick } = fakeWindow(elementAt, { drawsFrames: false });
  const heard = [];
  watchTopBand(win, (height) => heard.push(height));
  tick();
  const sheet = el("SECTION");
  elementAt.current = () => sheet;
  mutate();
  tick();
  elementAt.current = () => BODY;
  listeners.resize();
  tick();
  assert.deepEqual(heard, [TOP_BAND_MAX, 0, TOP_BAND_MAX], "a frame that never came left every later word unasked");
});

test("a frame that comes late does not report the band twice", () => {
  const elementAt = { current: () => BODY };
  const { win, tick, draw } = fakeWindow(elementAt, { drawsFrames: false });
  const heard = [];
  watchTopBand(win, (height) => heard.push(height));
  tick();
  draw();
  assert.deepEqual(heard, [TOP_BAND_MAX]);
});

test("a page that draws is measured once for one change, not again on the timer behind it", () => {
  const elementAt = { current: () => BODY, sweeps: 0 };
  const at = (x, y) => {
    if (x === 12 && y === 0) elementAt.sweeps += 1;
    return elementAt.current(x, y);
  };
  const { win, mutate, tick } = fakeWindow({ current: at });
  watchTopBand(win, () => {});
  mutate();
  tick();
  assert.equal(elementAt.sweeps, 2, "one measurement on the load and one for the change, none from the timers behind them");
});
