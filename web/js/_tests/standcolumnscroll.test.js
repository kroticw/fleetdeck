// web/js/_tests/standcolumnscroll.test.js
//
// On a stand the board scrolls its tall column once and says where that column
// is after the panel's next snapshots (web/js/standreport.js,
// probeColumnScroll). The panel sends a snapshot every second, and the board
// draws its columns again for each: a column drawn anew at its top is a column
// no one can read to its end, and a screenshot of a still frame never shows it.
//
// Run with: node --test web/js/_tests/standcolumnscroll.test.js

import test from "node:test";
import assert from "node:assert/strict";
import { probeColumnScroll } from "../standreport.js";

// A board whose columns are drawn by draw(): each draw puts new column boxes in,
// keeping a column's scroll only when keep says so, and tells the observer.
function standBoard() {
  let columns = [];
  let observer = null;
  const board = {
    querySelector: (selector) => {
      const stage = /data-stage="([^"]+)"/.exec(selector)?.[1];
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
  const draw = ({ tall = true, keep = false } = {}) => {
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
    if (observer && !observer.disconnected) observer.callback();
  };
  return { win, board, draw, observer: () => observer, done: () => columns.find((c) => c.dataset.stage === "done") };
}

test("the stand scrolls the tall column once it is drawn, and hears where it is two snapshots later", () => {
  const stand = standBoard();
  const heard = [];
  probeColumnScroll(stand.win, stand.board, (r) => heard.push(r));
  assert.equal(stand.observer().target, stand.board);
  assert.equal(stand.observer().options.childList, true);

  stand.draw({ tall: false });
  assert.equal(stand.done().scrollTop, 0, "a column with nothing to scroll is left alone");
  stand.draw();
  assert.equal(stand.done().scrollTop, 120);
  stand.draw({ keep: true });
  assert.equal(heard.length, 0, "one snapshot is not two");
  stand.draw({ keep: true });
  assert.deepEqual(heard, [{ report: "columnScroll", stage: "done", asked: 120, renders: 2, scrollTop: 120 }]);
  assert.equal(stand.observer().disconnected, true);
});

test("a board that draws its column anew at the top is heard at the top", () => {
  const stand = standBoard();
  const heard = [];
  probeColumnScroll(stand.win, stand.board, (r) => heard.push(r));
  stand.draw();
  stand.draw();
  stand.draw();
  assert.deepEqual(heard, [{ report: "columnScroll", stage: "done", asked: 120, renders: 2, scrollTop: 0 }]);
});

test("a board whose column never grows taller than its room is never scrolled and never heard", () => {
  const stand = standBoard();
  const heard = [];
  probeColumnScroll(stand.win, stand.board, (r) => heard.push(r));
  for (let i = 0; i < 5; i++) stand.draw({ tall: false });
  assert.equal(heard.length, 0);
});
