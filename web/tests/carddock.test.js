// Where a card keeps the session that wrote its open document: below the
// document or beside it, the choice remembered, and below whatever was chosen
// when the sheet has no room beside it (T-091).

import { test } from "node:test";
import assert from "node:assert/strict";

import { DOCK_KEYS, DOCK_RIGHT_MIN, clampDockSize, effectiveDock, readDockPrefs, writeDockPref } from "../js/carddock.js";

function memoryStorage(initial = {}) {
  const data = new Map(Object.entries(initial));
  return {
    getItem: (k) => (data.has(k) ? data.get(k) : null),
    setItem: (k, v) => data.set(k, String(v)),
    removeItem: (k) => data.delete(k),
    data,
  };
}

const refusing = {
  getItem() {
    throw new Error("denied");
  },
  setItem() {
    throw new Error("denied");
  },
  removeItem() {
    throw new Error("denied");
  },
};

test("beside is taken only from the threshold up", () => {
  assert.equal(DOCK_RIGHT_MIN, 640);
  assert.equal(effectiveDock("right", 639), "bottom");
  assert.equal(effectiveDock("right", 640), "right");
  assert.equal(effectiveDock("bottom", 2000), "bottom");
});

test("anything but right reads as below", () => {
  assert.equal(effectiveDock("left", 2000), "bottom");
  assert.equal(effectiveDock(undefined, 2000), "bottom");
});

test("sizes are held inside their range, and anything not a number gives the default", () => {
  assert.equal(clampDockSize("bottom", 10), 25);
  assert.equal(clampDockSize("bottom", 95), 80);
  assert.equal(clampDockSize("bottom", 60), 60);
  assert.equal(clampDockSize("right", 10), 30);
  assert.equal(clampDockSize("right", 95), 60);
  assert.equal(clampDockSize("right", "40"), 40);
  assert.equal(clampDockSize("bottom", Number.NaN), 55);
  assert.equal(clampDockSize("right", "abc"), 45);
  assert.equal(clampDockSize("bottom", null), 55);
});

test("with nothing stored the session is below, at the default sizes", () => {
  assert.deepEqual(readDockPrefs(memoryStorage()), { place: "bottom", height: 55, width: 45 });
});

test("stored choices come back, sizes clamped", () => {
  const s = memoryStorage({
    "fleetdeck-card-session-dock": "right",
    "fleetdeck-card-session-height-pct": "90",
    "fleetdeck-card-session-width-pct": "40",
  });
  assert.deepEqual(readDockPrefs(s), { place: "right", height: 80, width: 40 });
});

test("a storage that refuses is not an error: the defaults hold and a write is dropped", () => {
  assert.deepEqual(readDockPrefs(refusing), { place: "bottom", height: 55, width: 45 });
  assert.doesNotThrow(() => writeDockPref("place", "right", refusing));
  assert.deepEqual(readDockPrefs(undefined), { place: "bottom", height: 55, width: 45 });
});

test("writing the place stores exactly that key", () => {
  const s = memoryStorage();
  writeDockPref("place", "right", s);
  assert.deepEqual([...s.data], [["fleetdeck-card-session-dock", "right"]]);
});

test("the keys are the ones the spec names", () => {
  assert.deepEqual(DOCK_KEYS, {
    place: "fleetdeck-card-session-dock",
    height: "fleetdeck-card-session-height-pct",
    width: "fleetdeck-card-session-width-pct",
  });
});
