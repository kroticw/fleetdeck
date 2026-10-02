// web/js/_tests/capsules.test.js
//
// Run with: node --test web/js/_tests/capsules.test.js

import test from "node:test";
import assert from "node:assert/strict";
import { capsuleModel } from "../capsules.js";

const t = (key) => ({ tab_board: "Доска", tab_docs: "Доки", new_card: "+ карточка" })[key] ?? key;
const colors = { cool: "#2f9e44", warm: "#a15c00", hot: "#c92a2a", stale: "#5b6470", off: "#8791a0" };

test("the capsules carry the page's own words and the selected tab", () => {
  const m = capsuleModel({ section: "docs", themeLabel: "тема: авто", limits: [], t, colors });
  assert.deepEqual(m.tabs, [
    { id: "board", label: "Доска", selected: false },
    { id: "docs", label: "Доки", selected: true },
  ]);
  assert.equal(m.newCard.label, "+ карточка");
  assert.equal(m.theme.label, "тема: авто");
  assert.equal(m.version, 1);
});

test("a limit carries its level and the colour of that level in the current theme", () => {
  const m = capsuleModel({ section: "board", themeLabel: "", limits: [{ label: "5ч", pct: 37, level: "cool" }], t, colors });
  assert.deepEqual(m.limits, [{ label: "5ч", text: "37%", level: "cool", color: "#2f9e44", tooltip: "" }]);
});

// The window drew a day-old reading exactly like a fresh one: the age reached
// only the pointer and the fill colour of a thin bar. It goes in the text now,
// as the browser tab's gauge has it.
test("an aged limit says how old it is in its text", () => {
  const m = capsuleModel({ section: "board", themeLabel: "", limits: [{ label: "5ч", pct: 25, level: "stale", age: "21ч 21м" }], t, colors });
  assert.equal(m.limits[0].text, "25% · 21ч 21м");
});

test("a limit carries the board's sentence for the pointer", () => {
  const title = "расход лимита за 5 часов · сброс через 2ч 0м";
  const m = capsuleModel({ section: "board", themeLabel: "", limits: [{ label: "5ч", pct: 37, level: "cool", age: "", title }], t, colors });
  assert.equal(m.limits[0].tooltip, title);
  assert.equal(m.limits[0].text, "37%");
});

test("a limit with no number says so instead of a zero", () => {
  const m = capsuleModel({ section: "board", themeLabel: "", limits: [{ label: "7д", pct: null, level: "off" }], t, colors });
  assert.equal(m.limits[0].text, "—");
});
