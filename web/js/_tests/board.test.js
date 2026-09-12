// web/js/_tests/board.test.js
//
// See web/js/_tests/header.test.js for why this directory: a leading
// underscore excludes it from web/embed.go's plain (non "all:") //go:embed
// pattern, so it never ships in the binary or is served over HTTP.
//
// Run with: node --test web/js/_tests/board.test.js

import test from "node:test";
import assert from "node:assert/strict";
import { columnHTML } from "../board.js";

test("an empty column is marked kcol-empty, so app.css can shrink it", () => {
  const html = columnHTML("new", "new", [], new Set());
  assert.ok(/class="kcol[^"]*\bkcol-empty\b/.test(html), "zero cards must carry the empty marker");
});

test("a column carrying cards is not marked empty", () => {
  const html = columnHTML("done", "done", [{ path: "a.md", title: "x" }], new Set());
  assert.ok(!/\bkcol-empty\b/.test(html), "a non-empty column must not claim the empty marker");
});

// --- the card's number ---
//
// The number is why the board can be talked about out loud: a person reads
// T-018 off a column and hands it to card_path.py. Every test below is about
// one of the two ways that breaks — the number not being on screen at all, or
// being indistinguishable from the session's short id, which is a different
// identifier with a different lifetime.

const numbered = { path: "/b/T-018-x.md", id: "T-018", title: "a numbered card", session: "bb22", zone: "planned", progress: 40 };

test("a card shows its own number, not only its session's short id", () => {
  const html = columnHTML("active", "active", [numbered], new Set());
  assert.match(html, /class="knum"[^>]*>T-018</, "the number a person would say out loud is not on the card");
});

test("the card's number and the session's short id are told apart by more than their text", () => {
  const html = columnHTML("active", "active", [numbered], new Set());
  // Two identifiers on one card: one permanent and spoken, one temporary and
  // per-run. Sharing a class would leave them identical on screen, and the
  // operator naming the wrong one is the whole failure this guards.
  const number = /class="knum"[^>]*>T-018</.exec(html);
  const session = /class="ksession">bb22</.exec(html);
  assert.ok(number && session, "both identifiers must be on the card");
  assert.ok(
    !/class="ksession"[^>]*>T-018</.test(html) && !/class="knum"[^>]*>bb22</.test(html),
    "the number and the short id must not be drawn as the same kind of thing",
  );
});

test("the number says what it is, so a person knows which identifier to say", () => {
  const html = columnHTML("active", "active", [numbered], new Set());
  assert.match(html, /class="knum" title="[^"]+"/, "the number carries no hint saying it is the card's own");
});

test("a card with no number says so, rather than showing nothing", () => {
  const cards = [{ path: "/b/no-number.md", title: "unnumbered work", zone: "planned", progress: 0 }];
  const html = columnHTML("new", "new", cards, new Set());
  // Empty space reads as a panel that lost the number. A word reads as a card
  // that never had one — which is the truth, and is actionable.
  assert.match(html, /class="knum knum-none"/, "a card without a number must carry the missing marker");
  assert.ok(!/>T-/.test(html), "a missing number must never be invented");
});

test("an unreadable card is not accused of having no number", () => {
  const cards = [{ path: "/b/broken.md", parseError: "no frontmatter block" }];
  const html = columnHTML("other", "other", cards, new Set());
  // Its frontmatter did not parse, so nothing is known about its number. Saying
  // "no number" here would be a second, invented fact on top of a real one.
  assert.ok(!/knum/.test(html), "a card that does not parse must not claim anything about its number");
});

test("a card's number is escaped like every other field", () => {
  const cards = [{ path: "/b/x.md", id: 'T-1"><script>', title: "x", zone: "planned", progress: 0 }];
  const html = columnHTML("new", "new", cards, new Set());
  assert.ok(!html.includes("<script>"), "a card file is untrusted input, its id included");
});
