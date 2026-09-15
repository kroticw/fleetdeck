// The folded sessions panel in the fleetdeck window (web/app.css, the E layout
// section), and what a browser tab keeps of the same rules.
//
// v0.11.0's dev build on the operator's glass: folded, the sessions island is a
// 48 px rail, and the counters "0 waiting for you" and "0 stalled" wrapped word
// by word and ran off its edge. On the sessions surface the counters are the
// page's #header, a sibling of the column, and folding hides only the column's
// own children. The rail has no head: its marks already say who waits (red)
// and who stalled (dashed), and an em dash where the count would be says the
// panel knows nothing.

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

// Every selector in the sheet, one per entry, with the body it carries.
function rules() {
  const out = [];
  for (const match of stripped.matchAll(/([^{}]+)\{([^}]*)\}/g)) {
    for (const selector of match[1].split(",")) out.push({ selector: selector.trim(), body: match[2] });
  }
  return out;
}

const FOLDED_HEAD = ':root[data-surface="sessions"]:has(.col-sessions[data-folded="1"]) #header';

test("the folded sessions panel in the window has no head", () => {
  assert.match(ruleBody(FOLDED_HEAD), /display:\s*none/);
});

// The rail's signals are all that is left once the head is gone: the window's
// rules must not hide or wash them out.
test("the folded rail in the window still shows who waits and that the panel knows nothing", () => {
  for (const { selector, body } of rules()) {
    if (!selector.includes("data-surface")) continue;
    if (/\.sfold-(waiting|unknown|count)\b/.test(selector)) {
      assert.doesNotMatch(body, /display:\s*none|visibility:\s*hidden|opacity:\s*0[;\s]/, selector);
      assert.doesNotMatch(body, /background/, `${selector} repaints the waiting mark`);
    }
    if (/(^|\s)\.sfold(\s|$)/.test(selector)) {
      assert.doesNotMatch(body, /display:\s*none/, selector);
    }
  }
  assert.match(ruleBody(".sfold-waiting"), /background:\s*var\(--danger\)/);
});

// Everything above is the window's: a browser tab keeps its header, folded
// column or not.
test("in a browser tab the header stays whether the sessions column is folded or not", () => {
  for (const { selector, body } of rules()) {
    if (!/#header\b/.test(selector) || !/display:\s*none/.test(body)) continue;
    assert.match(selector, /:root\[data-surface/, `${selector} hides the header outside the window`);
  }
  assert.match(ruleBody('.col[data-folded="1"] > *:not(.col-size):not(.sfold)'), /display:\s*none/);
});
