// The panel's one button system (web/app.css, the .btn block).
//
// A button is a capsule of the same glass the window's controls are made of: a
// translucent fill, a hairline rim and a highlight along the top, with no blur
// of its own (the rule and why are at the head of glass-controls-css.test.js).
// Its size is a step of one scale, and the step is the container's to choose:
// a dialog, a form or a page asks for the large one, so its buttons are in
// proportion to its fields, and a dense place asks for the small one by class.
// Every other button look the sheet used to carry for these controls is gone,
// so a button cannot be sized or coloured in two places that disagree.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const css = readFileSync(new URL("../app.css", import.meta.url), "utf8");
const stripped = css.replace(/\/\*[\s\S]*?\*\//g, "");

function rules() {
  const out = [];
  for (const match of stripped.matchAll(/([^{}]+)\{([^{}]*)\}/g)) {
    for (const selector of match[1].split(",")) out.push({ selector: selector.trim(), body: match[2] });
  }
  return out;
}

function ruleBody(selector) {
  const found = rules().filter((r) => r.selector === selector);
  if (found.length === 0) throw new Error(`web/app.css has no rule for ${selector}`);
  return found.map((r) => r.body).join("\n");
}

function px(body, name) {
  const m = body.match(new RegExp(`${name}:\\s*(\\d+)px`));
  assert.ok(m, `${name} is not a px value`);
  return Number(m[1]);
}

// The classes that are .btn now and carry no look of their own.
const MIGRATED = [
  "setup-choose", "setup-create",
  "wizard-create", "wizard-appoint", "wizard-open", "wizard-skip",
  "start-new", "start-choose",
  "bmove-go", "bmove-cancel", "dialog-close", "kcol-add",
  "card-close", "reader-close", "review-close", "s-close",
  "review-send", "review-form-ok", "review-form-cancel", "review-comment-action", "review-round-notify",
  "o-name-edit", "label-edit-btn",
  "card-dock-place", "card-dock-font", "card-dock-open", "card-dock-resume", "card-dock-write", "card-dock-orchestrator",
  "s-key", "s-font-btn", "sresume-btn", "build-reload", "update-button", "theme-toggle", "fleet-menu-button",
  "newcard-create", "newcard-close", "newcard-pick", "newcard-attach",
];

// What makes a button's look: none of it may come from anywhere but .btn.
const LOOK = /(^|[;\s])(height|min-height|padding(-inline|-block)?|font-size|font|line-height|background(-color)?|border(-radius)?|box-shadow):/;

test("the scale is three heights, none under a 24 px target, the large one a form field's", () => {
  const root = ruleBody(":root");
  const sm = px(root, "--btn-h-sm");
  const md = px(root, "--btn-h");
  const lg = px(root, "--btn-h-lg");
  assert.ok(sm >= 24, `small is ${sm}px`);
  assert.ok(md > sm && lg > md, `${sm} < ${md} < ${lg}`);
  assert.ok(lg >= 32 && lg <= 40, `large is ${lg}px`);
  for (const name of ["--btn-height", "--btn-pad", "--btn-font"]) assert.match(root, new RegExp(`${name}:`), name);
});

test("a button is a glass capsule sized by the scale, with no blur of its own", () => {
  const body = ruleBody(".btn");
  assert.match(body, /min-height:\s*var\(--btn-height\)/);
  assert.match(body, /min-width:\s*var\(--btn-height\)/);
  assert.match(body, /padding:\s*0 var\(--btn-pad\)/);
  assert.match(body, /font-size:\s*var\(--btn-font\)/);
  assert.match(body, /border-radius:\s*999px/);
  assert.match(body, /border:\s*none/);
  assert.match(body, /background-color:\s*var\(--glass-control\)/);
  assert.match(body, /box-shadow:\s*inset 0 0 0 1px var\(--glass-control-edge\),\s*inset 0 1px 0 var\(--glass-control-highlight\),\s*0 1px 2px var\(--shadow\)/);
  assert.doesNotMatch(body, /(height|width|padding|font-size):[^;]*\d+px/, "every size comes from a token");
  for (const { selector, body: b } of rules()) {
    if (/\.btn(?![\w-])/.test(selector)) assert.doesNotMatch(b, /backdrop-filter:\s*(?!none)/, selector);
  }
});

test("hover, press, keyboard focus and disabled each read", () => {
  assert.match(ruleBody(".btn:hover:not(:disabled)"), /background-color:\s*var\(--glass-control-hover\)/);
  assert.match(ruleBody(".btn:active:not(:disabled)"), /transform:\s*scale\(0\.\d+\)/);
  assert.match(ruleBody(".btn:focus-visible"), /outline:\s*2px solid var\(--accent-strong\)/);
  const disabled = ruleBody(".btn:disabled");
  assert.match(disabled, /opacity:\s*0\.\d+/);
  assert.match(disabled, /cursor:\s*default/);
});

test("the primary action is the accent, the secondary the plain glass", () => {
  const primary = ruleBody(".btn-primary");
  assert.match(primary, /background-color:\s*var\(--accent\)/);
  assert.match(primary, /color:\s*var\(--surface\)/);
  assert.match(ruleBody(".btn-primary:hover:not(:disabled)"), /background-color:\s*var\(--accent-strong\)/);
});

test("an icon button is round: as wide as it is tall", () => {
  const body = ruleBody(".btn-icon");
  assert.match(body, /width:\s*var\(--btn-height\)/);
  assert.match(body, /padding:\s*0/);
});

test("the size is the container's: dialogs, forms and pages large, a class for the rest", () => {
  for (const container of [".dialog", ".newcard", ".setup", ".start", ".btn-lg"]) {
    assert.match(ruleBody(container), /--btn-height:\s*var\(--btn-h-lg\)/, container);
  }
  assert.match(ruleBody(".btn-md"), /--btn-height:\s*var\(--btn-h\)/);
  assert.match(ruleBody(".btn-sm"), /--btn-height:\s*var\(--btn-h-sm\)/);
});

test("with no glass a button is solid, with a rim that reads", () => {
  const body = ruleBody(':root[data-glass="opaque"] .btn');
  assert.match(body, /background-color:\s*var\(--surface-raised\)/);
  assert.match(body, /box-shadow:\s*inset 0 0 0 1px var\(--border-strong\)/);
  assert.match(ruleBody(':root[data-glass="opaque"] .btn-primary'), /background-color:\s*var\(--accent\)/);
});

// A state the button is in (a menu open, a place chosen) is still its own to
// draw: that is what the state means, not a second size.
test("no button that is a .btn keeps a size or a look of its own anywhere in the sheet", () => {
  for (const { selector, body } of rules()) {
    const named = MIGRATED.filter((c) => new RegExp(`\\.${c}(?![\\w-])`).test(selector));
    if (named.length === 0 || /\[aria-(expanded|pressed)=/.test(selector)) continue;
    assert.doesNotMatch(body, LOOK, `${selector} (${named.join(", ")})`);
  }
});
