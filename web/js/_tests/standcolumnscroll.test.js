// web/js/_tests/standcolumnscroll.test.js
//
// On a stand the board scrolls its tall column once and says where that column
// is after the panel's next snapshots (web/js/standreport.js,
// probeColumnScroll). The panel sends a snapshot every second, and the board
// draws its columns again for each that changes them: a column drawn anew at
// its top is a column no one can read to its end, and a screenshot of a still
// frame never shows it. A snapshot that changes nothing draws nothing (T-226),
// so the probe counts snapshots, not draws.
//
// Run with: node --test web/js/_tests/standcolumnscroll.test.js

import test from "node:test";
import assert from "node:assert/strict";
import { probeColumnScroll } from "../standreport.js";

// A board whose columns are drawn by draw(): each draw puts new column boxes in,
// keeping a column's scroll only when keep says so, and tells the observer.
function standBoard({ folded = false } = {}) {
  let columns = [];
  let observer = null;
  let doneFolded = folded;
  let draw = null;
  const unfold = {
    click: () => {
      doneFolded = false;
      draw();
    },
  };
  const board = {
    querySelector: (selector) => {
      const stage = /data-stage="([^"]+)"/.exec(selector)?.[1];
      if (selector.includes("kcol-unfold")) return doneFolded && stage === "done" ? unfold : null;
      if (stage === "done" && doneFolded) return null;
      return columns.find((c) => c.dataset.stage === stage) ?? null;
    },
  };
  const win = {
    MutationObserver: class {
      constructor(callback) {
        this.callback = callback;
        this.disconnected = false;
        observer = this;
      }
      observe(target, options) {
        this.target = target;
        this.options = options;
      }
      disconnect() {
        this.disconnected = true;
      }
    },
  };
  draw = ({ tall = true, keep = false } = {}) => {
    const before = columns.find((c) => c.dataset.stage === "done");
    columns = [
      { dataset: { stage: "new" }, scrollTop: 0, scrollHeight: 300, clientHeight: 600 },
      {
        dataset: { stage: "done" },
        scrollTop: keep && before ? before.scrollTop : 0,
        scrollHeight: tall ? 2400 : 600,
        clientHeight: 600,
      },
    ];
    mutated = true;
  };
  // The store: a snapshot the board draws for, when it changes the columns,
  // and then every other subscriber hears. A mutation observer is told after
  // all of them, as a browser tells it in a microtask; a draw it causes -- the
  // unfold -- tells it again.
  let mutated = false;
  const subscribers = [];
  const subscribe = (fn) => subscribers.push(fn);
  const snapshot = ({ redraw = true, ...how } = {}) => {
    if (redraw) draw(how);
    for (const fn of subscribers) fn();
    while (mutated && observer && !observer.disconnected) {
      mutated = false;
      observer.callback();
    }
  };
  return { win, board, draw, subscribe, snapshot, observer: () => observer, done: () => columns.find((c) => c.dataset.stage === "done") };
}

test("the stand scrolls the tall column once it is drawn, and hears where it is two snapshots later", () => {
  const stand = standBoard();
  const heard = [];
  probeColumnScroll(stand.win, stand.board, (r) => heard.push(r), stand.subscribe);
  assert.equal(stand.observer().target, stand.board);
  assert.equal(stand.observer().options.childList, true);

  stand.snapshot({ tall: false });
  assert.equal(stand.done().scrollTop, 0, "a column with nothing to scroll is left alone");
  stand.snapshot();
  assert.equal(stand.done().scrollTop, 120);
  stand.snapshot({ keep: true });
  assert.equal(heard.length, 0, "one snapshot is not two");
  stand.snapshot({ keep: true });
  assert.deepEqual(heard, [{ report: "columnScroll", stage: "done", asked: 120, renders: 2, scrollTop: 120 }]);
  assert.equal(stand.observer().disconnected, true);
});

// The board leaves columns that would not change as they are (T-226): the
// snapshots still come, and the column is heard where it was scrolled to.
test("snapshots that draw nothing are counted, and the column is heard where it is", () => {
  const stand = standBoard();
  const heard = [];
  probeColumnScroll(stand.win, stand.board, (r) => heard.push(r), stand.subscribe);
  stand.snapshot();
  stand.snapshot({ redraw: false });
  stand.snapshot({ redraw: false });
  assert.deepEqual(heard, [{ report: "columnScroll", stage: "done", asked: 120, renders: 2, scrollTop: 120 }]);
});

test("a board that draws its column anew at the top is heard at the top", () => {
  const stand = standBoard();
  const heard = [];
  probeColumnScroll(stand.win, stand.board, (r) => heard.push(r), stand.subscribe);
  stand.snapshot();
  stand.snapshot();
  stand.snapshot();
  assert.deepEqual(heard, [{ report: "columnScroll", stage: "done", asked: 120, renders: 2, scrollTop: 0 }]);
});

test("a board whose column never grows taller than its room is never scrolled and never heard", () => {
  const stand = standBoard();
  const heard = [];
  probeColumnScroll(stand.win, stand.board, (r) => heard.push(r), stand.subscribe);
  for (let i = 0; i < 5; i++) stand.snapshot({ tall: false });
  assert.equal(heard.length, 0);
});

// The done column is folded to a strip by default (the redesign): the stand
// unfolds it, as an operator would, before it scrolls it.
test("a folded done column is unfolded first, then scrolled and heard", () => {
  const stand = standBoard({ folded: true });
  const heard = [];
  probeColumnScroll(stand.win, stand.board, (r) => heard.push(r), stand.subscribe);
  stand.snapshot();
  assert.equal(stand.done().scrollTop, 120, "the unfolded column was not scrolled");
  stand.snapshot({ keep: true });
  stand.snapshot({ keep: true });
  assert.deepEqual(heard, [{ report: "columnScroll", stage: "done", asked: 120, renders: 2, scrollTop: 120 }]);
});
