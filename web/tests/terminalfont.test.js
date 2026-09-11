// How big a live terminal's type is (web/js/terminalfont.js): what a stored
// value comes back as, what a key asks for, and what survives a reload.
//
// Storage is faked rather than mocked away, for the reason columnwidth.test.js
// gives: what is pinned is that the size comes back, so a stand-in that
// remembers nothing would make every case vacuous.

import { test, beforeEach, afterEach } from "node:test";
import assert from "node:assert/strict";

import {
  FONT_KEYS,
  DEFAULT_FONT_SIZE,
  MIN_FONT_SIZE,
  MAX_FONT_SIZE,
  clampFontSize,
  storedFontSize,
  rememberFontSize,
  fontStep,
} from "../js/terminalfont.js";

let real;

function fakeStorage(initial = {}) {
  const map = new Map(Object.entries(initial));
  return {
    getItem: (k) => (map.has(String(k)) ? map.get(String(k)) : null),
    setItem: (k, v) => map.set(String(k), String(v)),
    removeItem: (k) => map.delete(String(k)),
    map,
  };
}

beforeEach(() => {
  real = Object.hasOwn(globalThis, "localStorage") ? globalThis.localStorage : undefined;
  globalThis.localStorage = fakeStorage();
});

afterEach(() => {
  if (real === undefined) delete globalThis.localStorage;
  else globalThis.localStorage = real;
});

// The keys are literal strings on purpose: once an operator has chosen a size,
// these exact entries sit in the window's storage, and renaming one silently
// resets his choice.
test("each place a terminal is drawn keeps its own size, under its own key", () => {
  assert.deepEqual(FONT_KEYS, {
    orchestrator: "fleetdeck-terminal-font-orchestrator",
    screen: "fleetdeck-terminal-font-screen",
  });
});

test("the range is 9 to 24 px, and the default is the 12 px the terminal has always had", () => {
  assert.equal(DEFAULT_FONT_SIZE, 12);
  assert.equal(MIN_FONT_SIZE, 9);
  assert.equal(MAX_FONT_SIZE, 24);
});

test("a first run is the default size", () => {
  assert.equal(storedFontSize(FONT_KEYS.orchestrator), 12);
});

test("a remembered size comes back, and only for its own place", () => {
  globalThis.localStorage = fakeStorage({ "fleetdeck-terminal-font-orchestrator": "16" });

  assert.equal(storedFontSize(FONT_KEYS.orchestrator), 16);
  assert.equal(storedFontSize(FONT_KEYS.screen), 12);
});

test("nothing from storage reaches the terminal unchecked", () => {
  const cases = [
    ["", 12],
    ["   ", 12],
    ["big", 12],
    ["NaN", 12],
    ["Infinity", 12],
    ["-Infinity", 12],
    ["0", 9],
    ["3", 9],
    ["100", 24],
    ["13.6", 14],
    [" 15 ", 15],
  ];
  for (const [raw, want] of cases) {
    globalThis.localStorage = fakeStorage({ "fleetdeck-terminal-font-screen": raw });
    assert.equal(storedFontSize(FONT_KEYS.screen), want, `stored ${JSON.stringify(raw)}`);
  }
});

test("a terminal with no place to remember its size starts at the default", () => {
  globalThis.localStorage = fakeStorage({ null: "20", undefined: "20", "": "20" });

  assert.equal(storedFontSize(null), 12);
  assert.equal(storedFontSize(undefined), 12);
  assert.equal(storedFontSize(""), 12);
});

test("a chosen size is written, and choosing the default again forgets the choice", () => {
  const storage = fakeStorage();
  globalThis.localStorage = storage;

  rememberFontSize(FONT_KEYS.orchestrator, 15);
  assert.equal(storage.map.get("fleetdeck-terminal-font-orchestrator"), "15");

  rememberFontSize(FONT_KEYS.orchestrator, 12);
  assert.equal(storage.map.has("fleetdeck-terminal-font-orchestrator"), false);
});

test("storage that is switched off costs the memory, never the terminal", () => {
  globalThis.localStorage = {
    getItem() {
      throw new Error("SecurityError");
    },
    setItem() {
      throw new Error("SecurityError");
    },
    removeItem() {
      throw new Error("SecurityError");
    },
  };

  assert.equal(storedFontSize(FONT_KEYS.orchestrator), 12);
  assert.doesNotThrow(() => rememberFontSize(FONT_KEYS.orchestrator, 15));
  assert.doesNotThrow(() => rememberFontSize(FONT_KEYS.orchestrator, 12));
});

test("a page with no storage at all still starts at the default", () => {
  delete globalThis.localStorage;

  assert.equal(storedFontSize(FONT_KEYS.orchestrator), 12);
  assert.doesNotThrow(() => rememberFontSize(FONT_KEYS.orchestrator, 15));
});

test("clamping keeps every size inside the range, in whole pixels", () => {
  assert.equal(clampFontSize(8), 9);
  assert.equal(clampFontSize(9), 9);
  assert.equal(clampFontSize(12.4), 12);
  assert.equal(clampFontSize(24), 24);
  assert.equal(clampFontSize(25), 24);
});

const key = (over) => ({ type: "keydown", key: "", metaKey: false, ctrlKey: false, altKey: false, shiftKey: false, ...over });

test("Cmd with = or + makes the type bigger, Cmd with - smaller, and Cmd+0 puts it back", () => {
  assert.equal(fontStep(key({ key: "=", metaKey: true })), 1);
  assert.equal(fontStep(key({ key: "+", metaKey: true, shiftKey: true })), 1);
  assert.equal(fontStep(key({ key: "-", metaKey: true })), -1);
  assert.equal(fontStep(key({ key: "0", metaKey: true })), 0);
});

test("the same keys without Cmd, or with another modifier, are left to the terminal", () => {
  for (const k of ["=", "+", "-", "0"]) {
    assert.equal(fontStep(key({ key: k })), null, `plain ${k}`);
    assert.equal(fontStep(key({ key: k, metaKey: true, ctrlKey: true })), null, `Cmd+Ctrl+${k}`);
    assert.equal(fontStep(key({ key: k, metaKey: true, altKey: true })), null, `Cmd+Option+${k}`);
    assert.equal(fontStep(key({ key: k, ctrlKey: true })), null, `Ctrl+${k}`);
  }
  assert.equal(fontStep(key({ key: "a", metaKey: true })), null);
});

test("only the key going down is a step: its release and its keypress are not", () => {
  assert.equal(fontStep(key({ type: "keyup", key: "=", metaKey: true })), null);
  assert.equal(fontStep(key({ type: "keypress", key: "=", metaKey: true })), null);
});
