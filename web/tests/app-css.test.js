// web/app.css is a shared file that every frontend task appends its own block
// to, and a stylesheet fails silently: an unclosed rule earlier in the file
// swallows everything appended after it into error recovery (or, where CSS
// Nesting is supported, turns it into descendant rules of whatever was left
// open), and the page still loads, still parses, and simply stops looking the
// way it should. Nothing else in this repository would notice.
//
// So: the braces must balance, and the panel's rules must be top-level rules,
// not nested inside somebody else's.
//
// The scanner below counts braces outside comments. A brace inside a CSS string
// or a url() would fool it; there is none in this file, and the failure mode
// would be a loud false positive rather than a silent pass.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const css = readFileSync(new URL("../app.css", import.meta.url), "utf8");

function scan(source) {
  const stripped = source.replace(/\/\*[\s\S]*?\*\//g, "");
  const topLevel = [];
  let depth = 0;
  let deepest = 0;
  let buffer = "";
  for (const ch of stripped) {
    if (ch === "{") {
      if (depth === 0) topLevel.push(buffer.trim().replace(/\s+/g, " "));
      depth += 1;
      deepest = Math.max(deepest, depth);
      buffer = "";
    } else if (ch === "}") {
      depth -= 1;
      buffer = "";
      if (depth < 0) return { topLevel, depth, deepest };
    } else {
      buffer += ch;
    }
  }
  return { topLevel, depth, deepest };
}

test("every rule in app.css is closed", () => {
  const { depth } = scan(css);
  assert.equal(
    depth,
    0,
    depth > 0
      ? `${depth} rule(s) in web/app.css are never closed, so everything after them is dead`
      : "web/app.css closes a rule that was never opened",
  );
});

test("the card panel's rules are top-level rules", () => {
  const { topLevel } = scan(css);
  const selectors = new Set(topLevel.flatMap((rule) => rule.split(",").map((s) => s.trim())));

  // #card-panel is the overlay itself: nested, the panel stops covering the
  // board and becomes a transparent block below it.
  assert.ok(selectors.has("#card-panel"), "#card-panel is not a top-level rule in web/app.css");

  // These four carry meaning, not decoration. Without them a refusal and a
  // written-but-not-committed notice look identical, a dead session reads as an
  // ordinary one, and a link to a card that does not exist looks like one that
  // does.
  for (const selector of [".card-error", ".card-notice", ".card-session-dead", ".wikilink-missing"]) {
    assert.ok(selectors.has(selector), `${selector} is not a top-level rule in web/app.css`);
  }
});

test("the documentation section's rules are top-level rules", () => {
  const { topLevel } = scan(css);
  const selectors = new Set(topLevel.flatMap((rule) => rule.split(",").map((s) => s.trim())));

  // .docs is the section's own two-pane layout: nested, the list and the body
  // stop being side by side and the document falls below the list.
  assert.ok(selectors.has(".docs"), ".docs is not a top-level rule in web/app.css");

  // These carry meaning rather than decoration. Without them the current tab and
  // the open document are indistinguishable from the rest, and a section that is
  // empty looks exactly like one that failed.
  for (const selector of ["#tabs .tab.on", ".docs-entry.on", ".docs-empty", ".docs-error"]) {
    assert.ok(selectors.has(selector), `${selector} is not a top-level rule in web/app.css`);
  }
});

// The switcher hides a section with the hidden attribute, and `hidden` is only
// display:none from the user-agent stylesheet: any author rule outranks it.
// #board carries display:flex for its columns, so hiding it did nothing until
// this rule existed — the board stayed on screen with the documentation section
// stacked under it, which no test saw and one screenshot did.
test("hidden actually hides, whatever display a section's own rule sets", () => {
  const stripped = css.replace(/\/\*[\s\S]*?\*\//g, "");
  const block = /\[hidden\]\s*\{([^}]*)\}/.exec(stripped);
  assert.ok(block, "web/app.css has no [hidden] rule, so hiding a section is at the mercy of its own display");
  assert.match(
    block[1].replace(/\s+/g, " "),
    /display\s*:\s*none\s*!important/,
    "[hidden] must force display:none, or a section with its own display stays on screen",
  );

  // The rule only matters for elements that have a display of their own. Both
  // sections do, which is exactly why the guarantee has to be unconditional.
  for (const selector of ["#board", "#docs"]) {
    assert.ok(
      new RegExp(`${selector}\\s*\\{`).test(stripped),
      `${selector} is not a top-level rule in web/app.css`,
    );
  }
});

test("the session panel's rules are top-level rules", () => {
  const { topLevel } = scan(css);
  const selectors = new Set(topLevel.flatMap((rule) => rule.split(",").map((s) => s.trim())));

  // #session-panel is the second overlay over the same column: nested, it stops
  // covering the board and the terminal is drawn behind a kanban.
  assert.ok(selectors.has("#session-panel"), "#session-panel is not a top-level rule in web/app.css");

  // .s-error is the only place the panel says a write did not happen — that the
  // text in the box was not sent, that the screen could not be read. Unstyled it
  // is a line of body text among the terminal's own output.
  assert.ok(selectors.has(".session-panel .s-error"), ".session-panel .s-error is not a top-level rule in web/app.css");

  // The keys write into a live session and the close button writes nothing
  // anywhere; these two rules are what keeps them apart on screen. Nested, the
  // keys fall back into the header's top-right corner beside ✕ — read as one
  // row of window controls, which is the complaint this panel was fixed for.
  // .s-who names the open session in the header. Unstyled it has no ellipsis,
  // and a long session name pushes the close button off the row.
  for (const selector of [
    ".session-panel .s-keys",
    ".session-panel .s-keys-label",
    ".session-panel .s-close",
    ".session-panel .s-who",
  ]) {
    assert.ok(selectors.has(selector), `${selector} is not a top-level rule in web/app.css`);
  }
});
