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
  "setTheme",
  "pressUpdate",
  "applyTheme",
  "setInsets",
  "setGlass",
  "setFolded",
  "focusTerminal",
  "setFullscreen",
  "setTitlebar",
];

// The panels are under the window's bar (T-226): no page has the title bar's
// buttons over it, and none takes a word about them.
test("no surface takes the title bar any more", () => {
  for (const surface of ["orchestrator", "sessions", "board"]) {
    const { send, calls } = setup(surface);
    send({ type: "titlebar", inset: 79, center: 18 });
    assert.deepEqual(calls, [], surface);
  }
});

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

// The theme is a setting (web/js/settings.js): the window has no capsule to
// cycle it from any more, and a stray message changes nothing.
test("the board no longer cycles the theme for the window", () => {
  const { send, calls } = setup("board");
  send({ type: "cycleTheme" });
  assert.deepEqual(calls, []);
});

test("the board keeps clear of the panels by the insets the window sends", () => {
  const { send, calls } = setup("board");
  send({ type: "insets", top: 52, left: 396, right: 0, contentRight: 358 });
  assert.deepEqual(calls, [["setInsets", { top: 52, left: 396, right: 0, contentRight: 358 }]]);
});

// The tabs and the new card button are the board's own head now (T-226): the
// window sends neither, and a stray one changes nothing.
test("the board takes no tab or new card from the window", () => {
  const { send, calls } = setup("board");
  send({ type: "show", section: "docs" });
  send({ type: "newCard" });
  assert.deepEqual(calls, []);
});

// The bar's theme segments: the board saves the choice, "" for the system's.
test("the board sets the theme the bar chose", () => {
  const { send, calls } = setup("board");
  send({ type: "setTheme", choice: "dark" });
  send({ type: "setTheme", choice: "" });
  send({ type: "setTheme", choice: "sepia" });
  assert.deepEqual(calls, [["setTheme", "dark"], ["setTheme", ""]]);
});

// The bar's update: the orchestrator's page presses its own update, which asks
// about unsent text in its terminal first.
test("only the orchestrator surface takes the bar's update press", () => {
  for (const surface of ["orchestrator", "sessions", "board"]) {
    const { send, calls } = setup(surface);
    send({ type: "update" });
    assert.deepEqual(calls, surface === "orchestrator" ? [["pressUpdate"]] : [], surface);
  }
});

test("a side surface ignores what only the board handles", () => {
  const { send, calls } = setup("sessions");
  send({ type: "open", kind: "card", path: "x.md" });
  send({ type: "setTheme", choice: "dark" });
  send({ type: "theme", choice: "light" });
  assert.deepEqual(calls, [["applyTheme", "light"]]);
});

test("only the orchestrator surface focuses its terminal and follows full screen", () => {
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
