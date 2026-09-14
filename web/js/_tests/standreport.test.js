// web/js/_tests/standreport.test.js
//
// Run with: node --test web/js/_tests/standreport.test.js

import test from "node:test";
import assert from "node:assert/strict";
import { boardScrollReport, watchBoardScroll } from "../standreport.js";

const style = { overflowX: "auto", borderTopWidth: "0px", borderBottomWidth: "1px" };

test("a board wider than itself reports the overflow and the bar under it", () => {
  const board = { scrollWidth: 1758, clientWidth: 1000, offsetHeight: 641, clientHeight: 630 };
  assert.deepEqual(boardScrollReport(board, style), { scrollWidth: 1758, clientWidth: 1000, scrollbarHeight: 10, overflowX: "auto" });
});

test("a board that fits reports no overflow and no bar", () => {
  const board = { scrollWidth: 900, clientWidth: 900, offsetHeight: 631, clientHeight: 630 };
  const report = boardScrollReport(board, style);
  assert.equal(report.scrollWidth, report.clientWidth);
  assert.equal(report.scrollbarHeight, 0);
});

test("the window hears the board once, and again only when it changes", () => {
  const board = { scrollWidth: 1758, clientWidth: 1000, offsetHeight: 640, clientHeight: 630 };
  const listeners = {};
  const win = {
    getComputedStyle: () => ({ overflowX: "auto", borderTopWidth: "0px", borderBottomWidth: "0px" }),
    requestAnimationFrame: (f) => f(),
    addEventListener: (name, f) => {
      listeners[name] = f;
    },
  };
  const heard = [];
  const again = watchBoardScroll(win, board, (r) => heard.push(r));
  assert.equal(heard.length, 1);
  again();
  listeners.resize();
  assert.equal(heard.length, 1, "nothing changed");
  board.clientWidth = 1200;
  again();
  assert.equal(heard.length, 2);
  assert.equal(heard[1].clientWidth, 1200);
});
