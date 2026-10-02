// web/js/_tests/header-ru.test.js
//
// The limits' words on a Russian page. A file of its own because i18n.js picks
// its dictionary once, when the module loads: with the English dictionary an
// untranslated unit reads exactly like a translated one, which is how "21h 21m"
// sat beside a gauge labelled "5ч".
//
// Run with: node --test web/js/_tests/header-ru.test.js

import test from "node:test";
import assert from "node:assert/strict";

// Node has a navigator of its own, so it is replaced rather than filled in.
Object.defineProperty(globalThis, "navigator", { value: { language: "ru-RU" }, configurable: true });
const { gauge, limitsOf, windowWords } = await import("../header.js");

test("a gauge's age is in the page's own units", () => {
  const now = Date.parse("2026-09-14T10:00:00Z");
  const html = gauge("5ч", { utilization: 25, resetsAt: "2026-09-14T12:00:00Z" }, true, "2026-09-13T12:39:00Z", now, 300);
  assert.match(html, /21ч 21м/);
  assert.doesNotMatch(html, /21h|21m/);
});

// Russian needs three forms of a unit after a number.
test("a window in words agrees with its number", () => {
  assert.equal(windowWords(60), "1 час");
  assert.equal(windowWords(300), "5 часов");
  assert.equal(windowWords(2 * 24 * 60), "2 дня");
  assert.equal(windowWords(7 * 24 * 60), "7 дней");
});

test("the limits' tooltip is a Russian sentence", () => {
  const now = Date.parse("2026-09-14T10:00:00Z");
  const snap = { limits: { fetchedAt: "2026-09-14T09:59:30Z", fiveHour: { utilization: 37, resetsAt: "2026-09-14T12:00:00Z" } } };
  assert.equal(limitsOf(snap, now)[0].title, "расход лимита за 5 часов · сброс через 2ч 0м");
});
