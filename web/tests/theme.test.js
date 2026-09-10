// web/js/theme.js: the one override on top of the system theme app.css
// already reads correctly on its own. Three things have to hold: the cycle
// visits all three states in the right order and does not get stuck on
// "dark" (the ?? bug the module's own comment documents), a stored choice is
// what a fresh load applies, and a storage failure (a private window) is
// swallowed rather than thrown at the caller.

import { test, beforeEach } from "node:test";
import assert from "node:assert/strict";

import { initTheme, cycleTheme } from "../js/theme.js";

function fakeStorage() {
  const store = new Map();
  return {
    getItem: (key) => (store.has(key) ? store.get(key) : null),
    setItem: (key, value) => store.set(key, String(value)),
    removeItem: (key) => store.delete(key),
  };
}

function throwingStorage() {
  const boom = () => {
    throw new DOMException("blocked", "SecurityError");
  };
  return { getItem: boom, setItem: boom, removeItem: boom };
}

function fakeDocumentElement() {
  const attrs = new Map();
  return {
    setAttribute: (key, value) => attrs.set(key, value),
    removeAttribute: (key) => attrs.delete(key),
    getAttribute: (key) => (attrs.has(key) ? attrs.get(key) : null),
  };
}

let root;

beforeEach(() => {
  globalThis.localStorage = fakeStorage();
  root = fakeDocumentElement();
  globalThis.document = { documentElement: root };
});

test("the cycle visits no-override, light, dark, and back to no-override", () => {
  assert.equal(cycleTheme(), "light");
  assert.equal(root.getAttribute("data-theme"), "light");
  assert.equal(cycleTheme(), "dark");
  assert.equal(root.getAttribute("data-theme"), "dark");
  // The bug this pins: NEXT.dark is the legitimate value null, and treating
  // "no entry" and "entry is null" alike (a bare ?? fallback does) would send
  // this back to "light" instead of clearing the override.
  assert.equal(cycleTheme(), null);
  assert.equal(root.getAttribute("data-theme"), null);
  assert.equal(cycleTheme(), "light");
});

test("initTheme applies whatever was already remembered", () => {
  localStorage.setItem("fleetdeck-theme", "dark");
  initTheme();
  assert.equal(root.getAttribute("data-theme"), "dark");
});

test("initTheme with nothing stored leaves the attribute unset", () => {
  initTheme();
  assert.equal(root.getAttribute("data-theme"), null);
});

test("a storage failure is swallowed, not thrown", () => {
  globalThis.localStorage = throwingStorage();
  assert.doesNotThrow(() => initTheme());
  assert.doesNotThrow(() => cycleTheme());
  // The choice still applies for the rest of this load even though it could
  // not be written down for the next one.
  assert.equal(root.getAttribute("data-theme"), "light");
});
