// web/js/_tests/hostactions.test.js
//
// Run with: node --test web/js/_tests/hostactions.test.js

import test from "node:test";
import assert from "node:assert/strict";
import { wireHostActions } from "../hostactions.js";

const TARGETS = [
  "openCard",
  "openDoc",
  "openSession",
  "showSection",
  "toggleNewCard",
  "cycleTheme",
  "applyTheme",
  "setInsets",
  "setGlass",
  "setFolded",
  "focusTerminal",
  "setFullscreen",
];

function setup(surface) {
  const calls = [];
  const win = {
    fleetdeckHost: { version: 1, surface, glass: "glass", receive() {} },
    fleetdeckTheme: (choice) => calls.push(["report", choice]),
  };
  const record = (name) => (...args) => {
    calls.push([name, ...args]);
    return "dark";
  };
  const targets = Object.fromEntries(TARGETS.map((name) => [name, record(name)]));
  wireHostActions(win, { surface, glass: "glass" }, targets);
  return { send: (message) => win.fleetdeckHost.receive(message), calls };
}

test("the board opens what the window asks it to open", () => {
  const { send, calls } = setup("board");
  send({ type: "open", kind: "card", path: "cards/T-1.md" });
  send({ type: "open", kind: "doc", path: "/docs/r.md" });
  send({ type: "open", kind: "session", short: "abc12345" });
  assert.deepEqual(calls, [["openCard", "cards/T-1.md"], ["openDoc", "/docs/r.md"], ["openSession", "abc12345"]]);
});

test("the board cycles the theme and reports the result to the window", () => {
  const { send, calls } = setup("board");
  send({ type: "cycleTheme" });
  assert.deepEqual(calls, [["cycleTheme"], ["report", "dark"]]);
});

test("the board keeps clear of the panels by the insets the window sends", () => {
  const { send, calls } = setup("board");
  send({ type: "insets", top: 64, left: 394, right: 0, contentRight: 356 });
  send({ type: "show", section: "docs" });
  send({ type: "newCard" });
  assert.deepEqual(calls, [["setInsets", { top: 64, left: 394, right: 0, contentRight: 356 }], ["showSection", "docs"], ["toggleNewCard"]]);
});

test("a side surface ignores what only the board handles", () => {
  const { send, calls } = setup("sessions");
  send({ type: "open", kind: "card", path: "x.md" });
  send({ type: "show", section: "docs" });
  send({ type: "theme", choice: "light" });
  assert.deepEqual(calls, [["applyTheme", "light"]]);
});

test("only the orchestrator surface focuses its terminal and makes room for the window buttons", () => {
  const orchestrator = setup("orchestrator");
  orchestrator.send({ type: "focusTerminal" });
  orchestrator.send({ type: "fullscreen", on: true });
  const sessions = setup("sessions");
  sessions.send({ type: "focusTerminal" });
  sessions.send({ type: "fullscreen", on: true });
  assert.deepEqual(orchestrator.calls, [["focusTerminal"], ["setFullscreen", true]]);
  assert.deepEqual(sessions.calls, []);
});

test("a folded or glass message reaches a side surface as a plain value", () => {
  const { send, calls } = setup("sessions");
  send({ type: "folded", folded: true });
  send({ type: "folded", folded: "yes" });
  send({ type: "glass", glass: "opaque" });
  assert.deepEqual(calls, [["setFolded", true], ["setFolded", false], ["setGlass", "opaque"]]);
});

test("an unknown message is skipped", () => {
  const { send, calls } = setup("board");
  send({ type: "teleport" });
  assert.deepEqual(calls, []);
});
