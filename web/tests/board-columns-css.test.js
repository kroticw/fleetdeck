// The board's columns in the fleetdeck window (web/app.css, the E layout
// section), and what a browser tab keeps of the same rules.
//
// v0.10.1 on the operator's glass: a column longer than the window made the
// whole board scroll down its height, and WebKit drew the classic 15 px bar for
// it at the board's right edge, which is the sessions glass's left edge. It
// read as a second, darker island behind the sessions one. In the window the
// board does not scroll down; each column does, under a thin bar of its own.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const css = readFileSync(new URL("../app.css", import.meta.url), "utf8");
const stripped = css.replace(/\/\*[\s\S]*?\*\//g, "");

// Every rule whose selector list holds selector as one of its selectors, in
// file order.
function ruleBodies(selector) {
  const bodies = [];
  for (let from = 0; ; ) {
    const at = stripped.indexOf(selector, from);
    if (at < 0) return bodies;
    const open = stripped.indexOf("{", at);
    const tail = stripped.slice(at + selector.length, open).trim();
    const before = stripped.slice(stripped.lastIndexOf("}", at) + 1, at).trim();
    if ((tail === "" || tail.startsWith(",")) && (before === "" || before.endsWith(","))) {
      bodies.push(stripped.slice(open + 1, stripped.indexOf("}", open)));
    }
    from = at + selector.length;
  }
}

function ruleBody(selector) {
  const [body] = ruleBodies(selector);
  if (body === undefined) throw new Error(`web/app.css has no rule for ${selector}`);
  return body;
}

const BOARD = ':root[data-surface="board"]';

test("in the window the board does not scroll down its height, its columns do", () => {
  const board = ruleBody(`${BOARD} #board`);
  assert.match(board, /overflow-y:\s*hidden/);
  // The columns take the board's height, so a long one scrolls inside it.
  assert.match(board, /align-items:\s*stretch/);
  const column = ruleBody(`${BOARD} .kcol`);
  assert.match(column, /min-height:\s*0/);
  assert.match(column, /overflow-y:\s*auto/);
});

// A scrollbar-width or scrollbar-color other than auto makes WebKit ignore
// ::-webkit-scrollbar (web/tests/app-css.test.js), and the classic bar macOS
// draws for a mouse is 15 px wide: the sessions list's thin bar, the same way.
test("a column's bar is thin, and WebKit draws it as asked", () => {
  const column = ruleBody(`${BOARD} .kcol`);
  assert.match(column, /scrollbar-width:\s*auto/);
  assert.match(column, /scrollbar-color:\s*auto/);
  assert.match(ruleBody(`${BOARD} .kcol::-webkit-scrollbar`), /width:\s*6px/);
  assert.match(ruleBody(`${BOARD} .kcol::-webkit-scrollbar-track`), /background:\s*transparent/);
  assert.match(ruleBody(`${BOARD} .kcol::-webkit-scrollbar-thumb`), /background:\s*var\(--border-strong\)/);
});

test("a column's heading stays in place while its cards scroll under it", () => {
  const head = ruleBody(`${BOARD} .kcol h5`);
  assert.match(head, /position:\s*sticky/);
  assert.match(head, /top:\s*0/);
  // The cards pass under it: it lies on the board's own ground.
  assert.match(head, /background:\s*var\(--bg\)/);
});

// The form opens from the tab row, which is #board's sibling, not inside it: a
// board that clips what is past its height does not cut the form.
test("the new card form opens outside the box that clips the columns", () => {
  const html = readFileSync(new URL("../index.html", import.meta.url), "utf8");
  const tabs = html.indexOf('<nav id="tabs"></nav>');
  const board = html.indexOf('<div id="board" data-window-ground></div>');
  assert.ok(tabs >= 0 && board > tabs, "#tabs is no longer an empty sibling before #board in web/index.html");
  assert.match(ruleBody(`${BOARD} #tabs`), /position:\s*absolute/);
});

// Everything above is the window's: a browser tab keeps the board it had.
test("in a browser tab the board and its columns scroll as they did", () => {
  const board = ruleBodies("#board").find((body) => /overflow-x/.test(body));
  assert.ok(board, "web/app.css has no base #board rule with overflow-x");
  assert.doesNotMatch(board, /overflow-y/);
  assert.match(board, /align-items:\s*flex-start/);
  const column = ruleBody(".kcol");
  assert.match(column, /flex:\s*3 1 160px/);
  assert.match(column, /overflow-y:\s*auto/);
  assert.doesNotMatch(column, /scrollbar/);
  assert.doesNotMatch(ruleBody(".kcol h5"), /position/);
  assert.equal(ruleBodies(".kcol::-webkit-scrollbar").length, 0, "a column's bar is styled outside the window");
});
