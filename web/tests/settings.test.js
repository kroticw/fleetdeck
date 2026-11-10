// The settings section (web/js/settings.js): what it draws from the page the
// server answers, and what each control writes.

import { test } from "node:test";
import assert from "node:assert/strict";

import { settingsHTML, renderSettings } from "../js/settings.js";

function page(values = {}) {
  return {
    values: {
      "workers.model": "", "workers.effort": "high", "workers.permission_mode": "", "workers.sandbox": false,
      "default_executor": "claude", "executors.codex.enabled": true, "executors.codex.model": "gpt-6.1-sol",
      "executors.codex.effort": "medium", "executors.codex.sandbox": "workspace-write", "executors.codex.approval_policy": "on-request",
      "executors.codex.home": "", "executors.codex.profile": "", "notify.enabled.waiting": true, "notify.enabled.failed": true,
      "notify.enabled.silent": true, "notify.enabled.card_blocked": true, "notify.silence_after": "30m", "usage.enabled": true,
      "server.port": 7777, "daemon.poll_interval": "2s", "agent.command": [], "agent.config_dir": "", "statusline.wrap": "",
      "statusline.rate_limits_path": "", "ui.theme": "", "ui.terminal_font_size": 0, ...values,
    },
    restart: ["agent.command", "agent.config_dir", "daemon.poll_interval", "server.port", "statusline.rate_limits_path", "statusline.wrap"],
    claude: { version: "2.1.316", defaultModel: "opus", models: ["fable", "opus", "sonnet", "haiku"], efforts: ["low", "medium", "high", "xhigh", "max"], defaultPermissionMode: "auto", permissionModes: ["acceptEdits", "auto", "bypassPermissions", "manual", "dontAsk", "plan"] },
    codex: {
      version: "0.166.0",
      models: [
        { id: "gpt-6.1-sol", name: "GPT-6.1-Sol", default: true, efforts: ["low", "medium", "high", "xhigh", "max", "ultra"], defaultEffort: "low" },
        { id: "gpt-6-luna", name: "GPT-6-Luna", efforts: ["low", "medium"], defaultEffort: "medium" },
      ],
    },
  };
}

test("settings: the page draws every section with what the file holds", () => {
  const html = settingsHTML(page());
  for (const id of ["settings-executors", "settings-notify", "settings-panel", "settings-look"]) assert.match(html, new RegExp(`id="${id}"`));
  assert.match(html, /CLI 2\.1\.316/);
  assert.match(html, /codex-cli 0\.166\.0/);
  // The chosen effort is the one marked, and the Codex efforts are the model's own.
  assert.match(html, /aria-checked="true" data-key="workers.effort" data-value="high"/);
  assert.match(html, /data-key="executors.codex.effort" data-value="ultra"/);
  assert.match(html, /aria-checked="true" data-key="executors.codex.effort" data-value="medium"/);
  // Only the keys read at start carry the restart badge.
  assert.equal(html.match(/class="set-badge"/g).length, 6);
});

test("settings: a Codex model's efforts follow the model chosen", () => {
  const html = settingsHTML(page({ "executors.codex.model": "gpt-6-luna", "executors.codex.effort": "" }));
  assert.doesNotMatch(html, /data-key="executors.codex.effort" data-value="ultra"/);
  assert.match(html, /aria-checked="true" data-key="executors.codex.effort" data-value=""/);
});

test("settings: Codex off cannot be the default executor", () => {
  const html = settingsHTML(page({ "executors.codex.enabled": false }));
  assert.match(html, /data-key="default_executor" data-value="codex" data-kind="string" disabled=""/);
});

function rig(answer = page()) {
  const handlers = {};
  const root = { classList: { add() {} }, innerHTML: "", scrollTop: 0, addEventListener: (type, fn) => (handlers[type] = fn) };
  const writes = [];
  const saved = [];
  renderSettings(root, {
    load: async () => answer,
    save: async (values) => {
      writes.push(values);
      if (values["workers.effort"] === "huge") throw new Error("workers.effort refused");
      return answer;
    },
    onSaved: (p) => saved.push(p),
  });
  const click = (dataset, extra = {}) => {
    const control = { dataset, disabled: false, ...extra };
    handlers.click({ target: { closest: (sel) => (sel.startsWith("button") ? control : null) }, preventDefault() {} });
  };
  const change = (control) => handlers.change({ target: control });
  return { root, writes, saved, click, change };
}

const settle = () => new Promise((resolve) => setTimeout(resolve, 0));

test("settings: each control writes its key in the kind it holds", async () => {
  const r = rig();
  await settle();
  r.click({ key: "workers.sandbox", value: "true", kind: "bool" });
  r.click({ key: "ui.terminal_font_size", value: "13", kind: "int" });
  r.click({ key: "approvals", value: "full", kind: "set", set: JSON.stringify({ "executors.codex.sandbox": "danger-full-access", "executors.codex.approval_policy": "never" }) });
  r.change({ tagName: "TEXTAREA", dataset: { key: "agent.command", kind: "list" }, value: "/opt/claude\n --x \n\n" });
  r.change({ tagName: "INPUT", dataset: { key: "notify.silence_after" }, value: "45m" });
  await settle();
  assert.deepEqual(r.writes, [
    { "workers.sandbox": true },
    { "ui.terminal_font_size": 13 },
    { "executors.codex.sandbox": "danger-full-access", "executors.codex.approval_policy": "never" },
    { "agent.command": ["/opt/claude", "--x"] },
    { "notify.silence_after": "45m" },
  ]);
  assert.equal(r.saved.length, 5);
});

test("settings: a refused write is said on the page, and nothing is applied", async () => {
  const r = rig();
  await settle();
  r.change({ tagName: "SELECT", dataset: { key: "workers.effort" }, value: "huge" });
  await settle();
  assert.match(r.root.innerHTML, /role="alert"/);
  assert.match(r.root.innerHTML, /workers\.effort refused/);
  assert.equal(r.saved.length, 0);
});

// Unset is not the model Codex calls its default: shown as chosen, that model
// could not be picked to write it, and sessions would stay on Codex's choice.
test("settings: an unset Codex model is its own option, and the default model can be picked", () => {
  const html = settingsHTML(page({ "executors.codex.model": "" }));
  assert.match(html, /<option value="" selected>по умолчанию Codex — gpt-6\.1-sol<\/option>|<option value="" selected>Codex default — gpt-6\.1-sol<\/option>/);
  assert.match(html, /<option value="gpt-6\.1-sol">gpt-6\.1-sol<\/option>/);
});

// The limits menu's Sources button opens the panel section, even when the
// settings have not loaded yet.
test("settings: a section asked for before the values load is the one drawn", async () => {
  const root = { classList: { add() {} }, innerHTML: "", scrollTop: 0, addEventListener() {}, querySelector: () => null };
  const view = renderSettings(root, { load: async () => page(), save: async () => page() });
  view.showSection("panel");
  await settle();
  assert.match(root.innerHTML, /class="set-nav-link on" data-section="panel"/);
});
