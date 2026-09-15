// The page's own controls in the fleetdeck window drawn as capsules of glass
// (web/app.css, the E layout section), and what a browser tab keeps of them.
//
// v0.11.0's dev build: the window's capsules over the board are the system's
// glass, and beside them the orchestrator island's head (the edit pencil, the
// orchestrator picker, A-/size/A+, the fleet menu) and the new card form were
// still a browser's bordered boxes. The operator asked for them as glass too.
//
// Measured on macOS 27 before this was drawn (T-070): WebKit's own glass
// material for pages is enabled only by a private preference an app cannot set
// through public API, and a backdrop-filter in a transparent web view is a
// CABackdropLayer in the window's own layer tree, over the native glass. So a
// control lying on the island's glass is a translucent fill and a rim, with no
// blur of its own: glass on glass. What floats over content -- the new card form
// over the board, the fleet menu's list over the island -- is frosted: a dense
// fill and a blur, whatever is blurred under it. With no glass (reduce
// transparency, increase contrast) all of it is solid, and nothing is blurred.

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

// class: the control's class; control: the selector the capsule rules use.
const ISLAND_CONTROLS = [
  { class: ".o-name-edit", control: ".o-name-edit" },
  { class: ".o-pick-select", control: ".o-pick-select" },
  { class: ".col-size-btn", control: ".col-size-btn:not(.col-size-unfold):not(.col-size-fold)" },
  { class: ".fleet-menu-button", control: ".fleet-menu-button" },
];
const FORM_CONTROLS = [".newcard-title", ".newcard-zone", ".newcard-create", ".newcard-cancel"].map((c) => ({ class: c, control: c }));
const CONTROLS = [
  ...ISLAND_CONTROLS.map((c) => ({ surface: ORCHESTRATOR, opaque: OPAQUE_ORCHESTRATOR, ...c })),
  ...FORM_CONTROLS.map((c) => ({ surface: BOARD, opaque: OPAQUE_BOARD, ...c })),
];
const FLOATING = [
  { surface: BOARD, opaque: OPAQUE_BOARD, control: ".newcard" },
  { surface: ORCHESTRATOR, opaque: OPAQUE_ORCHESTRATOR, control: ".fleet-menu-list" },
];

const mentions = (selector, name) => new RegExp(`\\${name}(?![\\w-])`).test(selector);

test("in the window the island's head controls and the new card form's fields are capsules of glass", () => {
  for (const { surface, control } of CONTROLS) {
    const body = ruleBody(`${surface} ${control}`);
    assert.match(body, /border-radius:\s*999px/, control);
    assert.match(body, /border:\s*none/, control);
    assert.match(body, /background-color:\s*var\(--glass-control\)/, control);
    assert.match(body, /box-shadow:\s*inset 0 0 0 0\.5px var\(--glass-control-edge\),\s*inset 0 1px 0 var\(--glass-control-highlight\)/, control);
    assert.match(ruleBody(`${surface} ${control}:hover:not(:disabled)`), /background-color:\s*var\(--glass-control-hover\)/, control);
  }
});

// The operator on v0.11.0's dev build: "the buttons that hide and show the
// panels do not look good, make them liquid glass and round". The fold button
// in each island's head and the unfold control in each folded strip are round
// glass buttons, as the capsules are: a see-through fill and a rim on glass,
// solid with a border with no glass. The unfold control is the only way back
// to a folded panel, and its round target is no smaller than the bordered one
// it replaces (26 by 40 pt on the stand): 40 pt across, centred in the 48 pt
// strip. The folded strip and the unfold control's own rules stay as a browser
// tab has them.
const FOLDS = [
  { surface: ':root[data-surface="orchestrator"]', opaque: ':root[data-surface="orchestrator"][data-glass="opaque"]' },
  { surface: ':root[data-surface="sessions"]', opaque: ':root[data-surface="sessions"][data-glass="opaque"]' },
];

const px = (body, property) => {
  const found = body.match(new RegExp(`(^|[;\\s])${property}:\\s*(\\d+)px`));
  return found ? Number(found[2]) : null;
};

test("in the window the panels' fold and unfold buttons are round glass, the unfold one no smaller a target than before", () => {
  for (const { surface, opaque } of FOLDS) {
    for (const [button, least] of [[".col-size-fold", 28], [".col-size-unfold", 40]]) {
      const body = ruleBody(`${surface} ${button}`);
      const width = px(body, "width");
      const height = px(body, "height");
      assert.ok(width >= least && height === width, `${surface} ${button}: ${width} by ${height}, want a circle at least ${least} across`);
      assert.match(body, /border-radius:\s*999px/, button);
      assert.match(body, /padding:\s*0/, button);
      assert.match(body, /border:\s*none/, button);
      assert.match(body, /background-color:\s*var\(--glass-control\)/, button);
      assert.match(body, /box-shadow:\s*inset 0 0 0 0\.5px var\(--glass-control-edge\),\s*inset 0 1px 0 var\(--glass-control-highlight\)/, button);
      assert.doesNotMatch(body, /display:/, `${button}: which of the two shows is the folded state's to say`);
      assert.match(ruleBody(`${surface} ${button}:hover:not(:disabled)`), /background-color:\s*var\(--glass-control-hover\)/, button);
      const solid = ruleBody(`${opaque} ${button}`);
      assert.match(solid, /background-color:\s*var\(--surface-raised\)/, button);
      assert.match(solid, /box-shadow:\s*inset 0 0 0 1px var\(--border-strong\)/, button);
      assert.match(ruleBody(`${opaque} ${button}:hover:not(:disabled)`), /background-color:\s*var\(--surface-hover\)/, button);
    }
  }
  // A browser tab keeps its bordered unfold control.
  const unfold = ruleBody('.col[data-folded="1"] .col-size-unfold');
  assert.match(unfold, /background:\s*var\(--surface-raised\)/);
  assert.match(unfold, /border-color:\s*var\(--border-strong\)/);
});

// Glass on glass: a blur under a control on the island would blur the island's
// own glass a second time.
test("a capsule on the island or in the form has no blur of its own", () => {
  for (const { selector, body } of rules()) {
    if (!selector.includes("data-surface")) continue;
    if (CONTROLS.some((c) => mentions(selector, c.class))) {
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
    assert.match(ruleBody(`${opaque} ${control}:hover:not(:disabled)`), /background-color:\s*var\(--surface-hover\)/, control);
  }
  for (const { opaque, control } of FLOATING) {
    const body = ruleBody(`${opaque} ${control}`);
    assert.match(body, /background-color:\s*var\(--surface-raised\)/, control);
    assert.match(body, /-webkit-backdrop-filter:\s*none/, control);
    assert.match(body, /(^|[;\s])backdrop-filter:\s*none/, control);
  }
});

// Review of #185: Create stays pressed while the card is written
// (web/js/newcard.js), and a disabled capsule lit under the pointer looked
// pressable.
test("a disabled capsule is dimmed and not lit under the pointer", () => {
  for (const { selector } of rules()) {
    if (!selector.includes("data-surface") || !/:hover/.test(selector)) continue;
    if (CONTROLS.some((c) => mentions(selector, c.class))) assert.match(selector, /:hover:not\(:disabled\)$/, selector);
  }
  for (const button of [".newcard-create", ".newcard-cancel"]) {
    const body = ruleBody(`${BOARD} ${button}:disabled`);
    assert.match(body, /opacity:\s*0\.\d+/, button);
    assert.match(body, /cursor:\s*default/, button);
  }
});

// Review of #185: the button's accent edge while its list is open went with its
// border.
test("the fleet menu's button keeps an accent edge while its list is open", () => {
  assert.match(ruleBody(`${ORCHESTRATOR} .fleet-menu-button[aria-expanded="true"]`), /box-shadow:\s*inset 0 0 0 1px var\(--accent\)/);
  assert.match(ruleBody(`${OPAQUE_ORCHESTRATOR} .fleet-menu-button[aria-expanded="true"]`), /box-shadow:\s*inset 0 0 0 1px var\(--accent\)/);
});

// Run 34949576998 (#185): on the orchestrator island, 313 px wide, the fleet
// menu's list ran 224 px on from the button's left edge, past the surface's
// right edge, and the page scrolled sideways under the island. Anchored to the
// header's right edge and no wider than the header less its gaps, the list keeps
// inside the surface on both sides, wherever the button sits. The anchoring was
// the same before #185; its stand opened the list first.
test("the fleet menu's list in the window is anchored to the header's right edge and no wider than the surface", () => {
  assert.match(ruleBody(`${ORCHESTRATOR} #header`), /position:\s*relative/);
  assert.match(ruleBody(`${ORCHESTRATOR} .fleet-menu`), /position:\s*static/);
  const list = ruleBody(`${ORCHESTRATOR} .fleet-menu-list`);
  assert.match(list, /left:\s*auto/);
  assert.match(list, /right:\s*var\(--gap\)/);
  assert.match(list, /min-width:\s*min\(14rem,\s*calc\(100% - 2 \* var\(--gap\)\)\)/);
  assert.match(list, /max-width:\s*calc\(100% - 2 \* var\(--gap\)\)/);
  // A browser tab keeps the list under its button.
  assert.match(ruleBody(".fleet-menu"), /position:\s*relative/);
  assert.match(ruleBody(".fleet-menu-list"), /left:\s*0/);
});

// Review of #185: a stand's form opens empty, and what it shows is the
// placeholder, WebKit's pale default on a frosted panel.
test("the new card title's placeholder is read like muted text", () => {
  const body = ruleBody(`${BOARD} .newcard-title::placeholder`);
  assert.match(body, /color:\s*var\(--text-muted\)/);
  assert.match(body, /opacity:\s*1/);
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
  const names = [...CONTROLS.map((c) => c.class), ".newcard", ".fleet-menu-list"];
  for (const { selector, body } of rules()) {
    if (selector.includes("data-surface")) continue;
    if (names.some((name) => mentions(selector, name))) {
      assert.doesNotMatch(body, /999px|backdrop-filter|--glass-control|--surface-on-glass/, selector);
    }
  }
});
