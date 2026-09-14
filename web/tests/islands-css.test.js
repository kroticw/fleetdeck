// The side surfaces' islands in the fleetdeck window (web/app.css, the E layout
// section), and what a browser tab keeps of the same rules.
//
// v0.10.0 drew, inside each glass panel, the column a browser tab has: a header
// row on a ground of its own with a rule under it, the strip of column controls
// with another, the orchestrator's terminal in a rounded box of its own, and a
// sessions list that read as a second panel inside the first.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const css = readFileSync(new URL("../app.css", import.meta.url), "utf8");
const stripped = css.replace(/\/\*[\s\S]*?\*\//g, "");

// The body of the first rule whose selector list holds selector as one of its
// selectors.
function ruleBody(selector) {
  for (let from = 0; ; ) {
    const at = stripped.indexOf(selector, from);
    if (at < 0) throw new Error(`web/app.css has no rule for ${selector}`);
    const open = stripped.indexOf("{", at);
    const tail = stripped.slice(at + selector.length, open).trim();
    const before = stripped.slice(stripped.lastIndexOf("}", at) + 1, at).trim();
    if ((tail === "" || tail.startsWith(",")) && (before === "" || before.endsWith(","))) {
      return stripped.slice(open + 1, stripped.indexOf("}", open));
    }
    from = at + selector.length;
  }
}

const SIDE = ':root[data-surface]:not([data-surface="board"])';

test("in a side surface the head rows lie on the glass, with no ground and no rule between them", () => {
  for (const selector of ["#header", ".col-size", ".o-head", ".slist-head", ".fleet-group-head"]) {
    const body = ruleBody(`${SIDE} ${selector}`);
    assert.match(body, /background:\s*transparent/, selector);
    assert.match(body, /border-top:\s*none/, selector);
    assert.match(body, /border-bottom:\s*none/, selector);
  }
});

// The title bar's buttons float over the orchestrator panel's top corner; the
// window sends where they end (cmd/fleetdeck-window/titlebar.go). In full
// screen there are none.
test("the orchestrator's header starts past the title bar's buttons, except in full screen", () => {
  assert.match(
    ruleBody(':root[data-surface="orchestrator"] #header'),
    /padding-left:\s*max\(calc\(var\(--gap\) \* 2\),\s*var\(--host-inset-titlebar,\s*0px\)\)/,
  );
  assert.match(ruleBody(':root[data-surface="orchestrator"][data-fullscreen] #header'), /padding-left:\s*calc\(var\(--gap\) \* 2\)/);
});

test("the orchestrator's terminal is the island's lower part, not a box inside it", () => {
  assert.match(ruleBody(':root[data-surface="orchestrator"] .o-screen'), /padding:\s*0;/);
  const term = ruleBody(':root[data-surface="orchestrator"] .o-term');
  assert.match(term, /border-radius:\s*0/);
  assert.match(term, /box-shadow:\s*none/);
  // A terminal over glass has to be read in both themes: its ground stays dense.
  assert.match(term, /background:\s*var\(--surface\)/);
  // The text keeps off the island's edges and its rounded bottom corners. The
  // fit addon takes .xterm's padding out of the room the terminal has.
  assert.match(ruleBody(':root[data-surface="orchestrator"] .o-term .xterm'), /padding:\s*0 10px 6px/);
});

test("the sessions island has one head: the fold button and the name on a row, the counters under them", () => {
  const open = ':root[data-surface="sessions"] .col-sessions:not([data-folded="1"])';
  assert.match(ruleBody(`${open} > .col-size`), /position:\s*fixed/);
  assert.match(ruleBody(`${open} > .slist-head`), /position:\s*fixed/);
  assert.match(
    ruleBody(':root[data-surface="sessions"]:has(.col-sessions:not([data-folded="1"])) #header'),
    /margin-top:\s*var\(--sessions-head\)/,
  );
});

// A scrollbar-width or scrollbar-color other than auto makes WebKit ignore
// ::-webkit-scrollbar (the board's finding, web/tests/app-css.test.js); the
// classic bar macOS draws for a mouse is 15 px wide.
test("the sessions list has a thin scrollbar that WebKit draws as asked", () => {
  const list = ruleBody(':root[data-surface="sessions"] .col-sessions');
  assert.match(list, /scrollbar-width:\s*auto/);
  assert.match(list, /scrollbar-color:\s*auto/);
  assert.match(ruleBody(':root[data-surface="sessions"] .col-sessions::-webkit-scrollbar'), /width:\s*6px/);
  assert.match(ruleBody(':root[data-surface="sessions"] .col-sessions::-webkit-scrollbar-thumb'), /background:\s*var\(--border-strong\)/);
});

// A name was one line, and the edit button and the badge beside it never gave
// way: "silent inside AskUserQuestion" left a long name its first two letters.
test("a session's name takes two lines and its badge gives way to it", () => {
  assert.match(ruleBody(':root[data-surface="sessions"] .srow-head'), /flex-wrap:\s*wrap/);
  const name = ruleBody(':root[data-surface="sessions"] .sname');
  assert.match(name, /white-space:\s*normal/);
  assert.match(name, /-webkit-line-clamp:\s*2/);
  assert.match(name, /min-width:\s*55%/);
  // Grown from nothing, so the edit button keeps to the name's row.
  assert.match(name, /flex:\s*1 1 0;/);
  const badge = ruleBody(':root[data-surface="sessions"] .srow-head .sbadge');
  assert.match(badge, /flex:\s*0 1 auto/);
  assert.match(badge, /max-width:\s*100%/);
  assert.match(badge, /text-overflow:\s*ellipsis/);
});

// The list scrolled to the island's very edge, and its rounded corner cut the
// card under it.
test("the sessions list stops short of the island's rounded bottom edge", () => {
  assert.match(ruleBody(':root[data-surface="sessions"] main'), /padding-bottom:\s*10px/);
  assert.match(ruleBody(':root[data-surface="sessions"] .col-sessions'), /padding-bottom:\s*10px/);
});

// The terminal's scroll bars read as the edge of one more frame down its right
// side. xterm's viewport scrolls natively under its own bar (web/vendor/xterm.css,
// overflow-y: scroll), and WebKit draws a classic bar for it that no one uses:
// the orchestrator's surface hides that one without taking the scrolling away.
// A scrollbar-width other than auto would make WebKit ignore the width asked
// for, and none is ever hidden that way (web/tests/app-css.test.js).
test("the orchestrator's terminal draws no native scroll bar beside its own", () => {
  const viewport = ruleBody(':root[data-surface="orchestrator"] .o-term .xterm-viewport');
  assert.match(viewport, /scrollbar-width:\s*auto/);
  assert.doesNotMatch(viewport, /overflow/);
  assert.match(ruleBody(':root[data-surface="orchestrator"] .o-term .xterm-viewport::-webkit-scrollbar'), /width:\s*0/);
});

// xterm hides its own bar with a timer after it shows it, and on the macOS 26
// stand that timer never ran: the bar stayed in sight at opacity 1 for as long
// as the stand watched. At rest the bar is out of sight by the stylesheet alone,
// whatever class xterm left on it.
test("the orchestrator's terminal keeps its scroll bar out of sight at rest", () => {
  const rest = ruleBody(':root[data-surface="orchestrator"] .o-term .xterm-scrollable-element > .scrollbar.vertical');
  assert.match(rest, /opacity:\s*0/);
  assert.match(rest, /pointer-events:\s*none/);
});

// Under the pointer it shows, and a wheel or a trackpad scrolls a terminal
// under the pointer, so it shows while one scrolls it too.
test("the orchestrator's terminal shows its scroll bar under the pointer", () => {
  const hover = ruleBody(':root[data-surface="orchestrator"] .o-term:hover .xterm-scrollable-element > .scrollbar.vertical');
  assert.match(hover, /opacity:\s*1/);
  assert.match(hover, /pointer-events:\s*auto/);
});

// Everything above is the window's: a browser tab keeps its columns.
test("in a browser tab the columns keep their rows, rules, terminal and names", () => {
  assert.match(ruleBody("#header"), /background:\s*var\(--surface\)/);
  assert.match(ruleBody("#header"), /border-bottom:\s*1px solid var\(--border\)/);
  assert.match(ruleBody(".col-size"), /border-bottom:\s*1px solid var\(--border\)/);
  assert.match(ruleBody(".o-head"), /border-bottom:\s*1px solid var\(--border\)/);
  assert.match(ruleBody(".slist-head"), /position:\s*sticky/);
  assert.match(ruleBody(".fleet-group-head"), /border-top:\s*1px solid var\(--border\)/);
  assert.match(ruleBody(".o-screen"), /padding:\s*4px/);
  assert.match(ruleBody(".sname"), /white-space:\s*nowrap/);
  for (const [, selector, body] of stripped.matchAll(/([^{}]+)\{([^{}]*)\}/g)) {
    if (!/--host-inset-titlebar|--sessions-head/.test(body)) continue;
    for (const one of selector.split(",")) {
      assert.match(one.trim(), /^:root\[data-surface/, `${one.trim()} reaches a browser tab`);
    }
  }
});
