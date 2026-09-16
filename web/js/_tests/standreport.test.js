// web/js/_tests/standreport.test.js
//
// Run with: node --test web/js/_tests/standreport.test.js

import test from "node:test";
import assert from "node:assert/strict";
import { boardScrollReport, topBandReport, watchBoardScroll } from "../standreport.js";
import { TOP_BAND_MAX } from "../topband.js";

// A window of the given width whose page carries the insets the fleetdeck
// window sends ("" when it has sent none).
function fakeWindow({ width = 1000, contentRight = "356px", extra = {} } = {}) {
  const root = { clientWidth: width };
  const boardStyle = { overflowX: "auto", borderTopWidth: "0px", borderBottomWidth: "1px" };
  return {
    document: { documentElement: root },
    getComputedStyle: (el) =>
      el === root ? { getPropertyValue: (name) => (name === "--host-inset-content-right" ? contentRight : "") } : boardStyle,
    ...extra,
  };
}

// A board whose box runs from left to right in the window, with its columns'
// right edges given as they stand at the current scrollLeft.
function fakeBoard({ left, right, scrollWidth, clientWidth, scrollLeft = 0, columnRights = [], offsetHeight = 641, clientHeight = 630 }) {
  return {
    scrollWidth,
    clientWidth,
    scrollLeft,
    offsetHeight,
    clientHeight,
    getBoundingClientRect: () => ({ left, right }),
    querySelectorAll: (selector) => {
      assert.equal(selector, ":scope > .kcol");
      return columnRights.map((r) => ({ getBoundingClientRect: () => ({ right: r }) }));
    },
  };
}

test("a board wider than itself reports the overflow and the bar under it", () => {
  const board = fakeBoard({ left: 0, right: 1000, scrollWidth: 1758, clientWidth: 1000 });
  const report = boardScrollReport(fakeWindow(), board);
  assert.equal(report.scrollWidth, 1758);
  assert.equal(report.clientWidth, 1000);
  assert.equal(report.scrollbarHeight, 10);
  assert.equal(report.overflowX, "auto");
});

test("a board that fits reports no overflow and no bar", () => {
  const board = fakeBoard({ left: 378, right: 644, scrollWidth: 266, clientWidth: 266, offsetHeight: 631 });
  const report = boardScrollReport(fakeWindow(), board);
  assert.equal(report.scrollWidth, report.clientWidth);
  assert.equal(report.scrollbarHeight, 0);
});

// The box between the panels: its scrollbar is as wide as it, and at the end of
// the scroll the last column is left of the sessions panel's edge at 644.
test("a board between the panels reports its box and a last column that comes out in the open", () => {
  // At scrollLeft 100 the last column ends at 1762; the end of the scroll is
  // 1500 - 266 = 1234, another 1134 to the right, which moves it to 628.
  const board = fakeBoard({ left: 378, right: 644, scrollWidth: 1500, clientWidth: 266, scrollLeft: 100, columnRights: [608, 1762] });
  const report = boardScrollReport(fakeWindow(), board);
  assert.equal(report.boardLeft, 378);
  assert.equal(report.boardRight, 644);
  assert.equal(report.sessionsLeft, 644);
  assert.equal(report.lastColumnRightAtEnd, 628);
  assert.equal(report.lastColumnClear, true);
  assert.equal(report.boardClearOfSessions, true);
});

// v0.10.0's board: as wide as the window, nothing kept on the right, the last
// column never out from under the sessions panel.
test("a board under the sessions panel reports that its last column stays under it", () => {
  const board = fakeBoard({ left: 0, right: 1000, scrollWidth: 1758, clientWidth: 1000, columnRights: [1758] });
  const report = boardScrollReport(fakeWindow(), board);
  assert.equal(report.lastColumnRightAtEnd, 1000);
  assert.equal(report.lastColumnClear, false);
  assert.equal(report.boardClearOfSessions, false);
});

// Both panels folded in a 1000 px window: the window sends a left inset of 74
// and a content-right inset of 56, so the box runs from 74 - 16 = 58 to the
// sessions panel's edge at 944. The stand draws its panels unfolded only, so
// this is where the folded case is proven.
test("with both panels folded the last column still comes out in the open", () => {
  const board = fakeBoard({ left: 58, right: 944, scrollWidth: 1500, clientWidth: 886, columnRights: [1484] });
  const report = boardScrollReport(fakeWindow({ contentRight: "56px" }), board);
  assert.equal(report.sessionsLeft, 944);
  assert.equal(report.boardLeft, 58);
  assert.equal(report.lastColumnRightAtEnd, 870);
  assert.equal(report.lastColumnClear, true);
  assert.equal(report.boardClearOfSessions, true);
});

// v0.10.0's board with both panels folded: the box is the whole window, and
// the last column ends under the folded sessions panel at the end of the scroll.
test("with both panels folded v0.10.0's board still leaves its last column under the sessions panel", () => {
  const board = fakeBoard({ left: 0, right: 1000, scrollWidth: 1580, clientWidth: 1000, columnRights: [1580] });
  const report = boardScrollReport(fakeWindow({ contentRight: "56px" }), board);
  assert.equal(report.lastColumnRightAtEnd, 1000);
  assert.equal(report.lastColumnClear, false);
  assert.equal(report.boardClearOfSessions, false);
});

// Sub-pixel layout is not a column under the panel.
test("a last column a fraction of a pixel past the edge is still clear", () => {
  const board = fakeBoard({ left: 378, right: 644.4, scrollWidth: 1500, clientWidth: 266, columnRights: [1234 + 644.4] });
  assert.equal(boardScrollReport(fakeWindow(), board).lastColumnClear, true);
});

test("with no columns or no inset the board says it cannot tell", () => {
  const empty = fakeBoard({ left: 378, right: 644, scrollWidth: 266, clientWidth: 266 });
  const noColumns = boardScrollReport(fakeWindow(), empty);
  assert.equal(noColumns.lastColumnRightAtEnd, null);
  assert.equal(noColumns.lastColumnClear, null);

  const board = fakeBoard({ left: 378, right: 644, scrollWidth: 1500, clientWidth: 266, columnRights: [1862] });
  const noInset = boardScrollReport(fakeWindow({ contentRight: "" }), board);
  assert.equal(noInset.sessionsLeft, null);
  assert.equal(noInset.lastColumnClear, null);
  assert.equal(noInset.boardClearOfSessions, null);
});

test("the window hears the board once, and again only when it changes", () => {
  const board = fakeBoard({ left: 378, right: 644, scrollWidth: 1500, clientWidth: 266, columnRights: [1862] });
  const listeners = {};
  const win = fakeWindow({
    extra: {
      requestAnimationFrame: (f) => f(),
      addEventListener: (name, f) => {
        listeners[name] = f;
      },
    },
  });
  const heard = [];
  const again = watchBoardScroll(win, board, (r) => heard.push(r));
  assert.equal(heard.length, 1);
  again();
  listeners.resize();
  assert.equal(heard.length, 1, "nothing changed");
  board.clientWidth = 400;
  again();
  assert.equal(heard.length, 2);
  assert.equal(heard[1].clientWidth, 400);
});

// The board draws its columns when the fleet's snapshot arrives, which can be
// after the page was laid out and after the window sent its insets.
test("the window hears the board again when it draws its columns", () => {
  const board = fakeBoard({ left: 378, right: 644, scrollWidth: 266, clientWidth: 266 });
  let onMutation = null;
  let observed = null;
  const win = fakeWindow({
    extra: {
      requestAnimationFrame: (f) => f(),
      addEventListener: () => {},
      MutationObserver: class {
        constructor(f) {
          onMutation = f;
        }
        observe(target, options) {
          observed = { target, options };
        }
      },
    },
  });
  const heard = [];
  watchBoardScroll(win, board, (r) => heard.push(r));
  assert.equal(observed.target, board);
  assert.equal(observed.options.childList, true);
  board.scrollWidth = 1500;
  board.querySelectorAll = () => [{ getBoundingClientRect: () => ({ right: 1862 }) }];
  onMutation();
  assert.equal(heard.length, 2);
  assert.equal(heard[1].lastColumnRightAtEnd, 628);
});

// --- the band the window is dragged by ---------------------------------------
//
// topBandReport is the board's own side of the number the window measures for
// itself: the height the page reports, and for every row the window looks at,
// the first point across the width that is not the page's ground. With a session
// open v0.12.0 reported 0 and said nothing about what took it (T-079).

// A page whose centre column is ground everywhere, with a sheet and the dimmed
// board under it placed as web/app.css places them.
function fakeTopBandWindow({ sheetTop = null, sheetLeft = 396, scrimGround = true, width = 1000 } = {}) {
  const ground = (tagName, id) => ({ tagName, id, className: "", hasAttribute: (name) => name === "data-window-ground" });
  const plain = (tagName, id, className = "") => ({ tagName, id, className, hasAttribute: () => false });
  const board = ground("DIV", "board");
  const scrim = scrimGround ? ground("DIV", "sheet-scrim") : plain("DIV", "sheet-scrim");
  const sheet = plain("DIV", "session-panel", "session-panel");
  const open = sheetTop !== null;
  return {
    innerWidth: width,
    document: {
      elementFromPoint: (x, y) => {
        if (open && y >= sheetTop && x >= sheetLeft) return sheet;
        return open ? scrim : board;
      },
      getElementById: (id) => (id === "session-panel" ? { hidden: !open } : { hidden: true }),
    },
  };
}

test("with nothing open the board reports the whole band and nothing in its way", () => {
  const report = topBandReport(fakeTopBandWindow());
  assert.equal(report.report, "topband");
  assert.equal(report.height, TOP_BAND_MAX);
  assert.deepEqual(report.rows, []);
  assert.deepEqual(report.sheetOpen, []);
});

test("with a session open the board reports the band the sheet leaves, and the sheet as what ends it", () => {
  const report = topBandReport(fakeTopBandWindow({ sheetTop: 60 }));
  assert.equal(report.height, 60);
  assert.deepEqual(report.sheetOpen, ["session-panel"]);
  assert.equal(report.rows.length, 1);
  assert.deepEqual(report.rows[0], { y: 60, x: 396, tag: "DIV", id: "session-panel", class: "session-panel" });
});

// The defect itself: an unmarked scrim over the whole column takes the band from
// the very top, and the report has to name it rather than only say 0.
test("a dimmed board that is not the page's ground is reported as what took the band", () => {
  const report = topBandReport(fakeTopBandWindow({ sheetTop: 60, scrimGround: false }));
  assert.equal(report.height, 0);
  assert.equal(report.rows[0].id, "sheet-scrim");
  assert.equal(report.rows[0].y, 0);
  assert.equal(report.rows.length, TOP_BAND_MAX / 4, "every row the window looks at is reported, not the first alone");
});
