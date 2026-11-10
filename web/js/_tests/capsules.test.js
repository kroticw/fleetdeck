// web/js/_tests/capsules.test.js
//
// Run with: node --test web/js/_tests/capsules.test.js

import test from "node:test";
import assert from "node:assert/strict";
import { capsuleModel } from "../capsules.js";

const words = {
  fleet_menu: "флот",
  fleet_menu_all: "Все флоты",
  fleet_menu_new: "Завести флот…",
  limits_title: "Лимиты",
  update_available: "Доступна {version}",
  update_button: "Обновить",
  waiting_count: "ждут ответа",
  stalled_count: "остановились",
  set_theme: "Тема",
  set_theme_light: "Светлая",
  set_theme_auto: "Авто",
  set_theme_dark: "Тёмная",
};
const t = (key) => words[key] ?? key;
const colors = { cool: "#2f9e44", warm: "#a15c00", hot: "#c92a2a", stale: "#5b6470", off: "#8791a0", attn: "#c2410c", muted: "#6b7280" };

const base = {
  build: { version: "1.8.0", revision: "abcdef1234" },
  fleet: "obsidian",
  fleets: [{ name: "obsidian", current: true, waiting: 1 }, { name: "work", current: false, waiting: 0 }],
  limits: [],
  update: "",
  counts: { waiting: 1, stalled: 0, unknown: 0 },
  theme: null,
  t,
  colors,
};

// The bar of the redesign (T-226), in the page's words: the window keeps no
// strings of its own (cmd/fleetdeck-window/capsulemodel.go).
test("the bar carries the brand, the fleet and its menu in the page's words", () => {
  const m = capsuleModel(base);
  assert.equal(m.version, 1);
  assert.deepEqual(m.brand, { name: "fleetdeck", version: "1.8.0" });
  assert.deepEqual(m.fleet, {
    name: "obsidian",
    label: "флот",
    fleets: [{ name: "obsidian", current: true, waiting: 1 }, { name: "work", current: false, waiting: 0 }],
    all: "Все флоты",
    add: "Завести флот…",
  });
  assert.equal(m.limitsLabel, "Лимиты");
  assert.equal(m.tabs, undefined, "the tabs are the board's own head now");
  assert.equal(m.newCard, undefined, "and so is the new card button");
});

test("a build from a checkout is named as the web brand names it", () => {
  const m = capsuleModel({ ...base, build: { version: "dev", revision: "abcdef1234", modified: true } });
  assert.equal(m.brand.version, "dev abcdef1*");
});

test("a limit carries its level and the colour of that level in the current theme", () => {
  const m = capsuleModel({ ...base, limits: [{ label: "Codex", pct: 63, level: "warm" }] });
  assert.deepEqual(m.limits, [{ label: "Codex", text: "63%", level: "warm", color: "#a15c00" }]);
});

test("a limit with no number says so instead of a zero", () => {
  const m = capsuleModel({ ...base, limits: [{ label: "7д", pct: null, level: "off" }] });
  assert.equal(m.limits[0].text, "—");
});

test("a version on offer is the update capsule; none is no capsule", () => {
  assert.equal(capsuleModel(base).update, undefined);
  assert.deepEqual(capsuleModel({ ...base, update: "1.9.0" }).update, { text: "Доступна 1.9.0", action: "Обновить" });
});

// Someone waiting is the attention colour; nobody stalled is a hollow ring in
// the muted one, as the design draws them. A source that never said whether
// anyone waits keeps the waiting count honest with "+?".
test("the counters say how many wait and how many stalled, in their colours", () => {
  assert.deepEqual(capsuleModel(base).counters, [
    { text: "1 ждут ответа", color: "#c2410c" },
    { text: "0 остановились", color: "#6b7280", hollow: true },
  ]);
  const m = capsuleModel({ ...base, counts: { waiting: 0, stalled: 2, unknown: 3 } });
  assert.deepEqual(m.counters, [
    { text: "0+? ждут ответа", color: "#6b7280" },
    { text: "2 остановились", color: "#c2410c" },
  ]);
});

test("the theme is its three segments and the choice, the system's when none", () => {
  assert.deepEqual(capsuleModel(base).theme, { label: "Тема", choice: "system", light: "Светлая", system: "Авто", dark: "Тёмная" });
  assert.equal(capsuleModel({ ...base, theme: "dark" }).theme.choice, "dark");
});

// The window tints the fleet's dot and the update with the accent and goes
// opaque on the operator's "no transparency"; a model without them asks for
// neither.
test("the bar carries the accent and the wish for no transparency", () => {
  const m = capsuleModel({ ...base, accent: "#4f56d9", opaque: true });
  assert.equal(m.accent, "#4f56d9");
  assert.equal(m.opaque, true);
  const plain = capsuleModel(base);
  assert.equal(plain.accent, undefined);
  assert.equal(plain.opaque, undefined);
});
