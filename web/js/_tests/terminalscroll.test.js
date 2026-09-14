// web/js/_tests/terminalscroll.test.js
//
// Run with: node --test web/js/_tests/terminalscroll.test.js
//
// On a CI stand the orchestrator surface says what edges its terminal draws down
// its right side: the width of the native bar under xterm's viewport, which
// WebKit draws for overflow-y: scroll at all times, and whether xterm's own bar
// is out of sight. A screenshot at rest cannot tell a hidden bar from a faint one.

import test from "node:test";
import assert from "node:assert/strict";
import { terminalScrollReport, watchTerminalScroll } from "../standreport.js";

// parts is read at each query: a terminal comes into the column after it exists.
function column(parts) {
  return {
    querySelector: (selector) => {
      if (selector === ".xterm-viewport") return parts.viewport;
      if (selector === ".xterm-scrollable-element > .scrollbar.vertical") return parts.bar;
      return null;
    },
  };
}

const win = (opacity) => ({
  getComputedStyle: (el) => (el.isBar ? { opacity } : { borderLeftWidth: "0px", borderRightWidth: "0px" }),
  requestAnimationFrame: (f) => f(),
  setTimeout: (f) => f(),
  addEventListener() {},
});

test("a terminal with a native bar beside its viewport and its own bar at rest says so", () => {
  const viewport = { offsetWidth: 368, clientWidth: 353 };
  const bar = { isBar: true, offsetWidth: 14 };
  assert.deepEqual(terminalScrollReport(win("0"), column({ viewport, bar })), {
    surface: "orchestrator",
    viewportScrollbarWidth: 15,
    ownBarOpacity: 0,
  });
});

test("a column with no terminal yet reports nothing", () => {
  assert.equal(terminalScrollReport(win("0"), column({ viewport: null, bar: null })), null);
});

test("the window hears the terminal once it is there, and again only when it changes", () => {
  const parts = { viewport: null, bar: null };
  const listeners = {};
  let observed = null;
  const w = {
    ...win("1"),
    addEventListener: (name, f) => (listeners[name] = f),
    MutationObserver: class {
      constructor(f) {
        observed = f;
      }
      observe() {}
    },
  };
  const heard = [];
  watchTerminalScroll(w, column(parts), (r) => heard.push(r));
  assert.equal(heard.length, 0, "no terminal yet");
  parts.viewport = { offsetWidth: 368, clientWidth: 368 };
  parts.bar = { isBar: true, offsetWidth: 14 };
  observed();
  assert.equal(heard.length, 1);
  observed();
  assert.equal(heard.length, 1, "nothing changed");
  parts.viewport.clientWidth = 353;
  listeners.resize();
  assert.equal(heard.length, 2);
  assert.equal(heard[1].viewportScrollbarWidth, 15);
});
