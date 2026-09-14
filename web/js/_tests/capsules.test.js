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
  assert.deepEqual(m.limits, [{ label: "5ч", text: "37%", level: "cool", color: "#2f9e44" }]);
});

test("a limit with no number says so instead of a zero", () => {
  const m = capsuleModel({ section: "board", themeLabel: "", limits: [{ label: "7д", pct: null, level: "off" }], t, colors });
  assert.equal(m.limits[0].text, "—");
});
