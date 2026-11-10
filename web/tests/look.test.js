// web/tests/look.test.js
// The rest of the panel's look beside the theme: the accent preset, the glass
// level and the terminals' ground. They are settings (config ui.accent,
// ui.glass, ui.terminal_background) drawn in the appearance section, and
// attributes on <html> that app.css reads (web/js/theme.js).

import { test, beforeEach } from "node:test";
import assert from "node:assert/strict";

import { initTheme, setConfiguredLook } from "../js/theme.js";
import { settingsHTML } from "../js/settings.js";

function fakeStorage() {
  const store = new Map();
  return {
    getItem: (key) => (store.has(key) ? store.get(key) : null),
    setItem: (key, value) => store.set(key, String(value)),
    removeItem: (key) => store.delete(key),
  };
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

test("the configured accent, glass and terminal ground are shown and reported only when they change", () => {
  assert.equal(setConfiguredLook({ accent: "rose", glass: "opaque", terminalBackground: "dark" }), true);
  assert.equal(root.getAttribute("data-accent"), "rose");
  assert.equal(root.getAttribute("data-glass-level"), "opaque");
  assert.equal(root.getAttribute("data-terminal-bg"), "dark");
  assert.equal(setConfiguredLook({ accent: "rose", glass: "opaque", terminalBackground: "dark" }), false);
});

test("an unset look is the default: indigo, the window's glass, the theme's ground", () => {
  setConfiguredLook({ accent: "rose", glass: "opaque", terminalBackground: "dark" });
  assert.equal(setConfiguredLook({}), true);
  assert.equal(root.getAttribute("data-accent"), "indigo");
  assert.equal(root.getAttribute("data-glass-level"), null);
  assert.equal(root.getAttribute("data-terminal-bg"), null);
});

test("an unknown look value is the default, never written onto the page", () => {
  setConfiguredLook({ accent: "pink", glass: "frosted", terminalBackground: "light" });
  assert.equal(root.getAttribute("data-accent"), "indigo");
  assert.equal(root.getAttribute("data-glass-level"), null);
  assert.equal(root.getAttribute("data-terminal-bg"), null);
});

test("a reload opens on the look last shown, before its first snapshot", () => {
  setConfiguredLook({ accent: "teal", glass: "reduced" });
  root = fakeDocumentElement();
  globalThis.document = { documentElement: root };
  initTheme();
  assert.equal(root.getAttribute("data-accent"), "teal");
  assert.equal(root.getAttribute("data-glass-level"), "reduced");
});

test("a blocked storage does not keep the look from being shown", () => {
  const boom = () => {
    throw new DOMException("blocked", "SecurityError");
  };
  globalThis.localStorage = { getItem: boom, setItem: boom, removeItem: boom };
  setConfiguredLook({ accent: "violet" });
  assert.equal(root.getAttribute("data-accent"), "violet");
  initTheme();
});

function page(values = {}) {
  return {
    values: {
      "workers.model": "", "workers.effort": "high", "workers.permission_mode": "", "workers.sandbox": false,
      "default_executor": "claude", "executors.codex.enabled": false, "executors.codex.model": "",
      "executors.codex.effort": "", "executors.codex.sandbox": "", "executors.codex.approval_policy": "",
      "executors.codex.home": "", "executors.codex.profile": "", "notify.enabled.waiting": true, "notify.enabled.failed": true,
      "notify.enabled.silent": true, "notify.enabled.card_blocked": true, "notify.silence_after": "30m", "usage.enabled": true,
      "server.port": 7777, "daemon.poll_interval": "2s", "agent.command": [], "agent.config_dir": "", "statusline.wrap": "",
      "statusline.rate_limits_path": "", "ui.theme": "", "ui.terminal_font_size": 0,
      "ui.accent": "", "ui.glass": "", "ui.terminal_background": "", ...values,
    },
    restart: [],
    claude: { version: "2.1.316", defaultModel: "opus", models: ["opus"], efforts: ["high"], defaultPermissionMode: "auto", permissionModes: ["auto"] },
    codex: { version: "", models: [] },
  };
}

test("settings: the accent is six swatches, the chosen one marked, unset shown as indigo", () => {
  const html = settingsHTML(page({ "ui.accent": "teal" }));
  for (const a of ["indigo", "blue", "teal", "violet", "rose", "graphite"]) {
    assert.match(html, new RegExp(`data-key="ui.accent" data-value="${a}"`));
  }
  assert.match(html, /aria-checked="true" data-key="ui.accent" data-value="teal"/);
  assert.match(html, /aria-checked="false" data-key="ui.accent" data-value="indigo"/);
  assert.match(settingsHTML(page()), /aria-checked="true" data-key="ui.accent" data-value="indigo"/);
});

test("settings: the glass and the terminal ground are segmented choices", () => {
  const html = settingsHTML(page({ "ui.terminal_background": "dark" }));
  for (const g of ["", "reduced", "opaque"]) assert.match(html, new RegExp(`data-key="ui.glass" data-value="${g}"`));
  assert.match(html, /aria-checked="true" data-key="ui.glass" data-value=""/);
  assert.match(html, /aria-checked="true" data-key="ui.terminal_background" data-value="dark"/);
  assert.match(html, /aria-checked="false" data-key="ui.terminal_background" data-value=""/);
});
