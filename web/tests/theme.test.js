// web/tests/theme.test.js
// web/js/theme.js: the one override on top of the system theme app.css
// already reads correctly on its own. The override is the configuration's
// (ui.theme, a setting), shown when a snapshot carries it and kept for the
// next load; a storage failure (a private window) is swallowed rather than
// thrown at the caller.

import { test, beforeEach } from "node:test";
import assert from "node:assert/strict";

import { initTheme, setConfiguredTheme, applyTheme } from "../js/theme.js";

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

// The theme is the configuration's (ui.theme): shown when a snapshot says it,
// kept for the next load, and reported only when it changed what is shown.
test("the configured theme is shown, kept, and reported only when it changes", () => {
  assert.equal(setConfiguredTheme("dark"), true);
  assert.equal(root.getAttribute("data-theme"), "dark");
  assert.equal(localStorage.getItem("fleetdeck-theme"), "dark");
  assert.equal(setConfiguredTheme("dark"), false);
  assert.equal(setConfiguredTheme(""), true);
  assert.equal(root.getAttribute("data-theme"), null);
  assert.equal(localStorage.getItem("fleetdeck-theme"), null);
  assert.equal(setConfiguredTheme("sepia"), false, "an unknown theme is the system's");
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
  assert.doesNotThrow(() => setConfiguredTheme("light"));
  // The choice still applies for the rest of this load even though it could
  // not be written down for the next one.
  assert.equal(root.getAttribute("data-theme"), "light");
});

// In the fleetdeck window the orchestrator and sessions surfaces are told the
// board's choice. Following it must not write it down: the board's own cycle is
// the one record, and a follower writing too would race it.
test("a choice applied from the window is shown and not remembered", () => {
  applyTheme("dark");
  assert.equal(root.getAttribute("data-theme"), "dark");
  assert.equal(localStorage.getItem("fleetdeck-theme"), null);
  applyTheme("auto");
  assert.equal(root.getAttribute("data-theme"), null);
  applyTheme("sepia");
  assert.equal(root.getAttribute("data-theme"), null, "an unknown choice is the system's, not an attribute");
});
