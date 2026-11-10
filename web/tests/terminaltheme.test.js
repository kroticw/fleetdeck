// web/tests/terminaltheme.test.js
// A terminal is drawn on the theme's terminal ground (--term-bg, --term-fg) in
// the panel's monospace, its cursor the accent; and terminals already open take
// a new look the moment the theme, the accent or the ground changes.

import { test } from "node:test";
import assert from "node:assert/strict";

import { refreshTerminalThemes, terminalTheme, terminalFontFamily, trackTerminal } from "../js/liveterminal.js";

function withTokens(tokens, body) {
  const previous = { gcs: globalThis.getComputedStyle, document: globalThis.document };
  globalThis.document = { documentElement: {} };
  globalThis.getComputedStyle = () => ({ getPropertyValue: (name) => tokens[name] ?? "" });
  try {
    return body();
  } finally {
    globalThis.getComputedStyle = previous.gcs;
    globalThis.document = previous.document;
  }
}

const TOKENS = {
  "--term-bg": " #0c0d11",
  "--term-fg": "#e6e7ea",
  "--accent": "#8b93ff",
  "--surface-hover": "#1f2126",
  "--font-mono": '"JetBrains Mono", ui-monospace, monospace',
};

test("a terminal is drawn on the terminal ground, its cursor the accent", () => {
  withTokens(TOKENS, () => {
    const theme = terminalTheme();
    assert.equal(theme.background, "#0c0d11");
    assert.equal(theme.foreground, "#e6e7ea");
    assert.equal(theme.cursor, "#8b93ff");
    assert.equal(theme.cursorAccent, "#0c0d11");
  });
});

test("a terminal is set in the panel's monospace", () => {
  withTokens(TOKENS, () => assert.equal(terminalFontFamily(), '"JetBrains Mono", ui-monospace, monospace'));
});

test("open terminals take the new look, and a disposed one is let go", () => {
  const made = [];
  const fake = () => {
    const t = { options: {}, disposed: false, dispose() { this.disposed = true; } };
    made.push(t);
    return t;
  };
  const a = trackTerminal(fake());
  const b = trackTerminal(fake());
  b.dispose();
  withTokens({ ...TOKENS, "--term-bg": "#fbfbfc" }, () => refreshTerminalThemes());
  assert.equal(a.options.theme.background, "#fbfbfc");
  assert.equal(b.options.theme, undefined, "a disposed terminal was still repainted");
});
