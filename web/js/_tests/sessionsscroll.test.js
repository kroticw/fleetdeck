// web/js/_tests/sessionsscroll.test.js
//
// Run with: node --test web/js/_tests/sessionsscroll.test.js
//
// On a CI stand the sessions surface says how wide the scrollbar beside its list
// is: the classic bar macOS draws for a mouse is 15 px, the one the islands ask
// for 6, and a screenshot cannot tell 6 from 8.

import test from "node:test";
import assert from "node:assert/strict";
import { listScrollReport, watchListScroll } from "../standreport.js";

const winWith = (style) => ({
  getComputedStyle: () => style,
  requestAnimationFrame: (f) => f(),
  addEventListener() {},
});

const style = { overflowY: "auto", borderLeftWidth: "0px", borderRightWidth: "1px" };

test("a list taller than itself reports the bar beside it", () => {
  const list = { scrollHeight: 1340, clientHeight: 594, offsetWidth: 348, clientWidth: 341 };
  assert.deepEqual(listScrollReport(winWith(style), list), {
    surface: "sessions",
    scrollHeight: 1340,
    clientHeight: 594,
    scrollbarWidth: 6,
    overflowY: "auto",
  });
});

test("a list that fits reports no bar", () => {
  const list = { scrollHeight: 500, clientHeight: 594, offsetWidth: 348, clientWidth: 347 };
  assert.equal(listScrollReport(winWith(style), list).scrollbarWidth, 0);
});

test("the window hears the list once, and again only when it changes", () => {
  const list = { scrollHeight: 1340, clientHeight: 594, offsetWidth: 348, clientWidth: 342 };
  const listeners = {};
  const win = { ...winWith({ overflowY: "auto", borderLeftWidth: "0px", borderRightWidth: "0px" }), addEventListener: (name, f) => (listeners[name] = f) };
  const heard = [];
  const again = watchListScroll(win, list, (r) => heard.push(r));
  assert.equal(heard.length, 1);
  again();
  listeners.resize();
  assert.equal(heard.length, 1, "nothing changed");
  list.clientWidth = 333;
  again();
  assert.equal(heard.length, 2);
  assert.equal(heard[1].scrollbarWidth, 15);
});
