// The limits menu as a list of every model the panel knows, the busiest first:
// each with the windows it has (five hours and a week, a week alone, or none
// at all for an API key that has a balance instead), and the window's capsule
// naming only the busiest, since a row of capsules cannot grow with the models.

import { test, beforeEach, afterEach } from "node:test";
import assert from "node:assert/strict";

import { installDOM } from "./fake-dom.js";

let dom;
let header;
let t;

beforeEach(async () => {
  dom = installDOM();
  header = await import("../js/header.js");
  ({ t } = await import("../js/i18n.js"));
});

afterEach(() => dom.restore());

const now = Date.parse("2026-11-05T12:00:00Z");
const claude = { fiveHour: { utilization: 13, resetsAt: "2026-11-05T14:00:00Z" }, sevenDay: { utilization: 51, resetsAt: "2026-11-09T00:00:00Z" }, fetchedAt: "2026-11-05T11:59:30Z" };
const codex = { fiveHour: { utilization: 20, resetsAt: "2026-11-05T15:00:00Z" }, sevenDay: { utilization: 64, resetsAt: "2026-11-10T00:00:00Z" }, fetchedAt: "2026-11-05T11:59:00Z" };
const gemini = { name: "Gemini", kind: "windows", windows: [{ id: "7d", utilization: 28, resetsAt: "2026-11-09T00:00:00Z" }], fetchedAt: "2026-11-05T11:58:00Z" };
const router = { name: "OpenRouter", kind: "balance", balance: { amount: 37.6, total: 50, currency: "USD", spentToday: 1.82 }, fetchedAt: "2026-11-05T11:58:00Z" };

test("limits list: the busiest model comes first", () => {
  const table = header.limitsTableHTML({ limits: claude, codexLimits: codex }, now);
  assert.ok(table.indexOf("Codex") < table.indexOf("Claude"), "Codex at 64% is not above Claude at 51%");
});

test("limits list: a model with only a weekly window shows that window alone", () => {
  const table = header.limitsTableHTML({ limits: claude, providers: [gemini] }, now);
  const card = table.slice(table.indexOf("Gemini"));
  assert.match(card, new RegExp(t("limits_kind_week")));
  assert.match(card, /28%/);
  assert.doesNotMatch(card.slice(0, card.indexOf("</section>")), new RegExp(t("limits_5h")), "a 5-hour window was drawn for a model that has none");
});

test("limits list: an API key shows its balance and today's spend, not a percentage", () => {
  const table = header.limitsTableHTML({ limits: claude, providers: [router] }, now);
  const card = table.slice(table.indexOf("OpenRouter"));
  assert.match(card, new RegExp(t("limits_kind_balance")));
  assert.match(card, /\$37\.60/);
  assert.match(card, /\$50\.00/);
  assert.match(card, /\$1\.82/);
});

test("limits list: the capsule names the busiest model only", () => {
  assert.deepEqual(header.limitsOf({ limits: claude, codexLimits: codex, providers: [gemini, router] }, now), [{ label: "Codex", pct: 64, level: "warm" }]);
  assert.deepEqual(header.limitsOf({}, now), [{ label: "Claude", pct: null, level: "off" }]);
});
