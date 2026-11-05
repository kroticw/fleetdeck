// The limits block (variant B): a capsule naming each provider with its worst
// window, and a menu with every window of every provider. Two providers'
// limits are never one number.

import { test, beforeEach, afterEach } from "node:test";
import assert from "node:assert/strict";

import { installDOM, fireEvent } from "./fake-dom.js";

let dom;
let header;

beforeEach(async () => {
  dom = installDOM();
  header = await import("../js/header.js");
});

afterEach(() => dom.restore());

const now = Date.parse("2026-11-05T12:00:00Z");
const claude = { fiveHour: { utilization: 13, resetsAt: "2026-11-05T14:00:00Z" }, sevenDay: { utilization: 51, resetsAt: "2026-11-09T00:00:00Z" }, fetchedAt: "2026-11-05T11:59:30Z" };
const codex = { fiveHour: { utilization: 20, resetsAt: "2026-11-05T15:00:00Z" }, sevenDay: { utilization: 64, resetsAt: "2026-11-10T00:00:00Z" }, fetchedAt: "2026-11-05T11:59:00Z" };

test("limits: each provider is summarised by its worst window", () => {
  const providers = header.providerLimits({ limits: claude, codexLimits: codex }, now);
  assert.deepEqual(
    providers.map((p) => [p.name, p.pct, p.level]),
    [
      ["Claude", 51, "cool"],
      ["Codex", 64, "warm"],
    ],
  );
  assert.deepEqual(providers[1].windows.map((w) => w.pct), [20, 64]);
});

test("limits: without Codex limits only Claude is listed, and without any a dash", () => {
  assert.deepEqual(header.providerLimits({ limits: claude }, now).map((p) => p.name), ["Claude"]);
  const none = header.providerLimits({}, now);
  assert.equal(none.length, 1);
  assert.equal(none[0].pct, null);
});

test("limits: an old Codex reading is stale whatever Claude's error says", () => {
  const old = header.providerLimits({ limits: claude, codexLimits: { ...codex, fetchedAt: "2026-11-05T11:00:00Z" } }, now);
  assert.equal(old[1].level, "stale");
  const fresh = header.providerLimits({ limits: claude, usageError: "keychain", codexLimits: codex }, now);
  assert.equal(fresh[0].level, "stale");
  assert.equal(fresh[1].level, "warm");
});

test("limits: the window capsules carry one entry per provider", () => {
  assert.deepEqual(
    header.limitsOf({ limits: claude, codexLimits: codex }, now).map((l) => [l.label, l.pct]),
    [
      ["Claude", 51],
      ["Codex", 64],
    ],
  );
});

test("limits: the button names both providers and the menu shows every window", () => {
  const snap = { limits: claude, codexLimits: codex };
  const button = header.limitsButtonHTML(snap, now, false);
  assert.match(button, /Claude 51%/);
  assert.match(button, /Codex 64%/);
  assert.match(button, /aria-expanded="false"/);
  // The label is what VoiceOver reads instead of the content: it carries the values.
  assert.match(button, /aria-label="[^"]*Claude 51%, Codex 64%"/);
  const table = header.limitsTableHTML(snap, now);
  for (const pct of ["13%", "51%", "20%", "64%"]) assert.match(table, new RegExp(pct));
});

test("limits: the window's popover opens, follows the snapshot and closes on Escape", async () => {
  const { createLimitsPopover } = await import("../js/limitsmenu.js");
  let snap = { limits: claude };
  const popover = createLimitsPopover(dom.document, () => snap, () => now);
  popover.toggle();
  const node = dom.document.body.querySelector(".limits-popover");
  assert.equal(node.hidden, false);
  assert.doesNotMatch(node.innerHTML, /Codex/);
  snap = { limits: claude, codexLimits: codex };
  popover.refresh();
  assert.match(node.innerHTML, /Codex/);
  fireEvent(dom.document, "keydown", { key: "Escape" });
  assert.equal(node.hidden, true);
});
