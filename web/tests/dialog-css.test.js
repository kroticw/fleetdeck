// The dialog's glass (web/app.css, the .dialog block).
//
// The rule this pins is stated at the head of web/tests/glass-controls-css.test.js
// and measured there: the window's web view is transparent over the window's own
// glass, so a backdrop-filter inside the page is a CABackdropLayer above that
// native glass, and only what floats over CONTENT may be frosted.
//
// The window of the dialog floats over content: its blur is its own and legal.
// The scrim does not. It is position: fixed over the whole web view, including
// the room where the page has nothing — in the fleetdeck window that room is
// the window's own glass and nothing else, so a blur there blurs the native
// material, which is exactly what the rule forbids. The scrim dims, and only
// dims.
//
// With no glass (reduce transparency) nothing is blurred anywhere.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const css = readFileSync(new URL("../app.css", import.meta.url), "utf8");
const stripped = css.replace(/\/\*[\s\S]*?\*\//g, "");

// Every selector in the sheet, one per entry, with the body it carries — the
// same reading glass-controls-css.test.js does.
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

const OPAQUE = ':root[data-glass="opaque"]';

test("the dialog's window is frosted, with a blur of its own", () => {
  const body = ruleBody(".dialog");
  assert.match(body, /-webkit-backdrop-filter:\s*blur\(\d+px\) saturate\(\d+%\)/);
  assert.match(body, /(^|[;\s])backdrop-filter:\s*blur\(\d+px\) saturate\(\d+%\)/);
  assert.match(body, /background-color:\s*var\(--surface-on-glass\)/);
});

// Not "no blur in this one rule" but "no blur anywhere in the sheet": the
// property put back under a media query or a surface would be the same defect
// in a place this could not see by naming one selector.
test("the scrim dims and never blurs, wherever the sheet speaks of it", () => {
  for (const { selector, body } of rules()) {
    if (!/\.dialog-scrim(?![\w-])/.test(selector)) continue;
    assert.doesNotMatch(
      body,
      /backdrop-filter:\s*(?!none)/,
      `${selector}: the scrim covers the whole web view, and in the window most of that is the window's own glass`,
    );
  }
});

test("with no glass the dialog is solid and nothing is blurred", () => {
  const body = ruleBody(`${OPAQUE} .dialog`);
  assert.match(body, /-webkit-backdrop-filter:\s*none/);
  assert.match(body, /(^|[;\s])backdrop-filter:\s*none/);
  assert.match(body, /background-color:\s*var\(--surface-raised\)/);
});

// The dialog is the page's own primitive, not the window's: a browser tab,
// where it opens too, is served without a data-surface at all
// (web/js/main.js sets it, and a browser tab has none), so a rule written under
// one would never reach it.
test("the dialog's rules hold wherever the page is shown", () => {
  for (const selector of [".dialog-scrim", ".dialog", ".dialog-title", ".dialog-body", ".dialog-foot"]) {
    assert.ok(
      rules().some((r) => r.selector === selector),
      `web/app.css states ${selector} only under a surface, so a browser tab and the start page have none of it`,
    );
  }
});

test("the dialog floats over the page and is centred in it", () => {
  const scrim = ruleBody(".dialog-scrim");
  assert.match(scrim, /position:\s*fixed/);
  assert.match(scrim, /inset:\s*0/);
  assert.match(scrim, /place-items:\s*center/);
  assert.match(scrim, /background:\s*var\(--dialog-scrim\)/);
  // No [hidden] rule of its own: the sheet forces display:none on anything
  // hidden (app-css.test.js pins that), and a second rule here would be one
  // more place to forget.
  assert.equal(rules().filter((r) => r.selector === ".dialog-scrim[hidden]").length, 0);
});

// A scrim of its own, not the sheet's. The two answer different questions: a
// drawer blocks nothing and its scrim only pushes the background back, while a
// dialog holds the work until it is answered and its scrim has to say so. Asked
// of every theme, and by the value rather than by the name alone — a token that
// is merely spelled differently but set to the same number has been merged back
// in all but name.
test("the dialog's scrim is darker than a drawer's, in every theme", () => {
  const blocks = [
    ["light", stripped.match(/:root\s*\{([^}]*)\}/)[1]],
    ["the system's dark", stripped.match(/@media \(prefers-color-scheme: dark\)\s*\{\s*:root:not\(\[data-theme="light"\]\)\s*\{([^}]*)\}/)[1]],
    ["the chosen dark", stripped.match(/:root\[data-theme="dark"\]\s*\{([^}]*)\}/)[1]],
  ];
  const alpha = (block, token) => {
    const found = new RegExp(`${token}:\\s*rgb\\([^)]*/\\s*(\\d+)%\\s*\\)`).exec(block);
    assert.ok(found, `${token} is not stated as an rgb() with an alpha`);
    return Number(found[1]);
  };
  for (const [theme, block] of blocks) {
    const dialog = alpha(block, "--dialog-scrim");
    const sheet = alpha(block, "--sheet-scrim");
    assert.ok(dialog > sheet, `${theme}: the dialog's scrim is ${dialog}% and a drawer's ${sheet}%`);
  }
});

