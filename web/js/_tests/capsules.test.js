// web/js/_tests/capsules.test.js
//
// Run with: node --test web/js/_tests/capsules.test.js

import test from "node:test";
import assert from "node:assert/strict";
import { capsuleModel } from "../capsules.js";

const t = (key) => ({ tab_board: "Доска", tab_docs: "Доки", tab_settings: "Настройки", new_card: "+ карточка" })[key] ?? key;
const colors = { cool: "#2f9e44", warm: "#a15c00", hot: "#c92a2a", stale: "#5b6470", off: "#8791a0" };

test("the capsules carry the page's own words and the selected tab", () => {
  const m = capsuleModel({ section: "settings", limits: [], t, colors });
  assert.deepEqual(m.tabs, [
    { id: "board", label: "Доска", selected: false },
    { id: "docs", label: "Доки", selected: false },
    { id: "settings", label: "Настройки", selected: true },
  ]);
  assert.equal(m.newCard.label, "+ карточка");
  // The theme is a setting now, not a capsule (the settings design, variant A).
  assert.equal(m.theme, undefined);
  assert.equal(m.version, 1);
});

test("a limit carries its level and the colour of that level in the current theme", () => {
  const m = capsuleModel({ section: "board", limits: [{ label: "5ч", pct: 37, level: "cool" }], t, colors });
  assert.deepEqual(m.limits, [{ label: "5ч", text: "37%", level: "cool", color: "#2f9e44" }]);
});

test("a limit with no number says so instead of a zero", () => {
  const m = capsuleModel({ section: "board", limits: [{ label: "7д", pct: null, level: "off" }], t, colors });
  assert.equal(m.limits[0].text, "—");
});

// The window tints its selected tab with the accent and goes opaque on the
// operator's "no transparency"; a model without them asks for neither.
test("the capsules carry the accent and the wish for no transparency", () => {
  const m = capsuleModel({ section: "board", limits: [], t, colors, accent: "#4f56d9", opaque: true });
  assert.equal(m.accent, "#4f56d9");
  assert.equal(m.opaque, true);
  const plain = capsuleModel({ section: "board", limits: [], t, colors });
  assert.equal(plain.accent, undefined);
  assert.equal(plain.opaque, undefined);
});
