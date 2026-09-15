// The folded orchestrator panel in the fleetdeck window (web/app.css, the E
// layout section), and what a browser tab keeps of the same rules.
//
// v0.11.0's dev build on a stand: folded, the orchestrator island is a 48 px
// strip, and a scroll bar showed at its foot. The page's #header is a sibling
// of the column, and folding hides only the column's own children; on the
// orchestrator surface the header starts past the window's buttons, wider than
// the strip, and the page scrolled sideways under it. The strip has no head:
// the window's buttons sit in its corner, and its unfold control is all it
// shows.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const css = readFileSync(new URL("../app.css", import.meta.url), "utf8");
const stripped = css.replace(/\/\*[\s\S]*?\*\//g, "");

// Every selector in the sheet, one per entry, with the body it carries.
function rules() {
  const out = [];
  for (const match of stripped.matchAll(/([^{}]+)\{([^}]*)\}/g)) {
    for (const selector of match[1].split(",")) out.push({ selector: selector.trim(), body: match[2] });
  }
  return out;
}

function ruleBody(selector) {
  const found = rules().find((r) => r.selector === selector);
  if (!found) throw new Error(`web/app.css has no rule for ${selector}`);
  return found.body;
}

const FOLDED_HEAD = ':root[data-surface="orchestrator"]:has(.col-orchestrator[data-folded="1"]) #header';

test("the folded orchestrator panel in the window has no head", () => {
  assert.match(ruleBody(FOLDED_HEAD), /display:\s*none/);
});

// The unfold control is what the strip shows: no rule of the window's hides it
// or the strip of column controls it sits in.
test("the folded orchestrator strip in the window still shows its unfold control", () => {
  for (const { selector, body } of rules()) {
    if (!selector.includes("data-surface")) continue;
    if (/\.col-size(-unfold|-btn)?\b/.test(selector) && !/:not\(\[data-folded="1"\]\)/.test(selector)) {
      assert.doesNotMatch(body, /display:\s*none|visibility:\s*hidden|opacity:\s*0[;\s]|pointer-events:\s*none/, selector);
    }
  }
  assert.doesNotMatch(ruleBody('.col[data-folded="1"] .col-size-unfold'), /display:\s*none/);
});

// Run 34941628912: with the head hidden the unfold control rose to the strip's
// top, under the window's close and minimize buttons. Out of full screen the
// folded strip keeps the header's room above it, the buttons' line twice; in
// full screen there are no buttons.
const FOLDED_STRIP = ':root[data-surface="orchestrator"][data-titlebar]:not([data-fullscreen]) .col-orchestrator[data-folded="1"]';

test("the folded orchestrator strip keeps the room the window's buttons take above its unfold control", () => {
  assert.match(ruleBody(FOLDED_STRIP), /padding-top:\s*calc\(var\(--host-titlebar-center\)\s*\*\s*2\)/);
});

// Only the window's: a browser tab keeps its header, folded column or not.
test("in a browser tab the header stays whether the orchestrator column is folded or not", () => {
  for (const { selector, body } of rules()) {
    if (!/#header\b/.test(selector) || !/display:\s*none/.test(body)) continue;
    assert.match(selector, /:root\[data-surface/, `${selector} hides the header outside the window`);
  }
});
