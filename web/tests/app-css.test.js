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
