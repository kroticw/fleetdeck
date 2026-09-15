// The page's own controls in the fleetdeck window drawn as capsules of glass
// (web/app.css, the E layout section), and what a browser tab keeps of them.
//
// v0.10.2's dev build: the window's capsules over the board are the system's
// glass, and beside them the orchestrator island's head (the edit pencil, the
// orchestrator picker, A-/size/A+, the fleet menu) and the new card form were
// still a browser's bordered boxes. The operator asked for them as glass too.
//
// Measured on macOS 27 before this was drawn (T-070): a page cannot ask WebKit
// for the system's material (-apple-visual-effect is off for third-party
// content), and a backdrop-filter in a transparent web view is a CABackdropLayer
// in the window's own layer tree, over the native glass. So a control lying on
// the island's glass is a translucent fill and a rim, with no blur of its own:
// glass on glass. What floats over content -- the new card form over the board,
// the fleet menu's list over the island -- is frosted: a dense fill and a blur,
// whatever is blurred under it. With no glass (reduce transparency, increase
// contrast) all of it is solid, and nothing is blurred.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const css = readFileSync(new URL("../app.css", import.meta.url), "utf8");
const stripped = css.replace(/\/\*[\s\S]*?\*\//g, "");

// Every selector in the sheet, one per entry, with the body it carries. Rules
// inside an @media block are read as they are: the selector is what matters.
function rules() {
  const out = [];
  for (const match of stripped.matchAll(/([^{}]+)\{([^{}]*)\}/g)) {
    for (const selector of match[1].split(",")) out.push({ selector: selector.trim(), body: match[2] });
  }
  return out;
}

// Every body the sheet gives selector, one after another: a control's capsule
// and its select's chevron are rules of their own, and the cascade adds them up.
function ruleBody(selector) {
  const found = rules().filter((r) => r.selector === selector);
  if (found.length === 0) throw new Error(`web/app.css has no rule for ${selector}`);
  return found.map((r) => r.body).join("\n");
}

const ORCHESTRATOR = ':root[data-surface="orchestrator"]';
const BOARD = ':root[data-surface="board"]';
const OPAQUE_ORCHESTRATOR = ':root[data-surface="orchestrator"][data-glass="opaque"]';
const OPAQUE_BOARD = ':root[data-surface="board"][data-glass="opaque"]';

const ISLAND_CONTROLS = [".o-name-edit", ".o-pick-select", ".col-size-btn", ".fleet-menu-button"];
const FORM_CONTROLS = [".newcard-title", ".newcard-zone", ".newcard-create", ".newcard-cancel"];
const CONTROLS = [
  ...ISLAND_CONTROLS.map((c) => ({ surface: ORCHESTRATOR, opaque: OPAQUE_ORCHESTRATOR, control: c })),
  ...FORM_CONTROLS.map((c) => ({ surface: BOARD, opaque: OPAQUE_BOARD, control: c })),
];
const FLOATING = [
  { surface: BOARD, opaque: OPAQUE_BOARD, control: ".newcard" },
  { surface: ORCHESTRATOR, opaque: OPAQUE_ORCHESTRATOR, control: ".fleet-menu-list" },
];

test("in the window the island's head controls and the new card form's fields are capsules of glass", () => {
  for (const { surface, control } of CONTROLS) {
    const body = ruleBody(`${surface} ${control}`);
    assert.match(body, /border-radius:\s*999px/, control);
    assert.match(body, /border:\s*none/, control);
    assert.match(body, /background-color:\s*var\(--glass-control\)/, control);
    assert.match(body, /box-shadow:\s*inset 0 0 0 0\.5px var\(--glass-control-edge\),\s*inset 0 1px 0 var\(--glass-control-highlight\)/, control);
    assert.match(ruleBody(`${surface} ${control}:hover`), /background-color:\s*var\(--glass-control-hover\)/, control);
  }
});

// Glass on glass: a blur under a control on the island would blur the island's
// own glass a second time.
test("a capsule on the island or in the form has no blur of its own", () => {
  for (const { selector, body } of rules()) {
    if (!selector.includes("data-surface")) continue;
    if (CONTROLS.some(({ control }) => new RegExp(`\\${control}(?![\\w-])`).test(selector))) {
      assert.doesNotMatch(body, /backdrop-filter:\s*(?!none)/, selector);
    }
  }
});

test("what floats over content in the window is frosted: a dense fill and a blur", () => {
  for (const { surface, control } of FLOATING) {
    const body = ruleBody(`${surface} ${control}`);
    assert.match(body, /background-color:\s*var\(--surface-on-glass\)/, control);
    assert.match(body, /-webkit-backdrop-filter:\s*blur\(\d+px\) saturate\(\d+%\)/, control);
    assert.match(body, /(^|[;\s])backdrop-filter:\s*blur\(\d+px\) saturate\(\d+%\)/, control);
  }
});

test("with no glass every capsule is solid, and nothing floating is blurred", () => {
  for (const { opaque, control } of CONTROLS) {
    const body = ruleBody(`${opaque} ${control}`);
    assert.match(body, /background-color:\s*var\(--surface-raised\)/, control);
    assert.match(body, /box-shadow:\s*inset 0 0 0 1px var\(--border-strong\)/, control);
    assert.match(ruleBody(`${opaque} ${control}:hover`), /background-color:\s*var\(--surface-hover\)/, control);
  }
  for (const { opaque, control } of FLOATING) {
    const body = ruleBody(`${opaque} ${control}`);
    assert.match(body, /background-color:\s*var\(--surface-raised\)/, control);
    assert.match(body, /-webkit-backdrop-filter:\s*none/, control);
    assert.match(body, /(^|[;\s])backdrop-filter:\s*none/, control);
  }
});

// The closed control is drawn by the page; the list a select opens stays the
// system's menu.
test("a select in the window draws its own chevron in the text's colour", () => {
  for (const { surface, control } of CONTROLS.filter((c) => /select|zone/.test(c.control))) {
    const body = ruleBody(`${surface} ${control}`);
    assert.match(body, /-webkit-appearance:\s*none/, control);
    assert.match(body, /(^|[;\s])appearance:\s*none/, control);
    assert.match(body, /background-image:\s*linear-gradient\(45deg, transparent 50%, currentColor 50%\),\s*linear-gradient\(135deg, currentColor 50%, transparent 50%\)/, control);
  }
});

// Each theme block states the capsule's tokens: the light :root, the system's
// dark and the chosen dark.
test("every theme states the glass capsule's tokens", () => {
  const tokens = ["--glass-control", "--glass-control-edge", "--glass-control-hover", "--glass-control-highlight", "--surface-on-glass"];
  const blocks = [
    stripped.match(/:root\s*\{([^}]*)\}/)[1],
    stripped.match(/@media \(prefers-color-scheme: dark\)\s*\{\s*:root:not\(\[data-theme="light"\]\)\s*\{([^}]*)\}/)[1],
    stripped.match(/:root\[data-theme="dark"\]\s*\{([^}]*)\}/)[1],
  ];
  for (const block of blocks) {
    for (const token of tokens) assert.match(block, new RegExp(`${token}:\\s*rgb\\(`), token);
  }
});

// A browser tab has no data-surface: its controls keep the look they had.
test("a browser tab's controls are not capsules of glass", () => {
  const names = [...ISLAND_CONTROLS, ...FORM_CONTROLS, ".newcard", ".fleet-menu-list"];
  for (const { selector, body } of rules()) {
    if (selector.includes("data-surface")) continue;
    if (names.some((name) => new RegExp(`\\${name}(?![\\w-])`).test(selector))) {
      assert.doesNotMatch(body, /999px|backdrop-filter|--glass-control|--surface-on-glass/, selector);
    }
  }
});
