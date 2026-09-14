// web/js/_tests/hostroutes.test.js
//
// Run with: node --test web/js/_tests/hostroutes.test.js

import test from "node:test";
import assert from "node:assert/strict";
import { routesFor } from "../hostroutes.js";

function localSpy() {
  const calls = [];
  const local = new Proxy({}, { get: (_, name) => (...args) => calls.push([name, ...args]) });
  return { local, calls };
}

test("without a host every route stays in the page", () => {
  const { local, calls } = localSpy();
  const routes = routesFor({}, null, local);
  routes.openCard("a.md");
  routes.switchFleet("work");
  assert.deepEqual(calls, [["openCard", "a.md"], ["switchFleet", "work"]]);
});

test("a side surface sends opening, fleet switching and folding to the window", () => {
  const sent = [];
  const win = {
    fleetdeckOpen: (payload) => sent.push(["open", payload]),
    fleetdeckSwitchFleet: (name) => sent.push(["fleet", name]),
    fleetdeckPanel: (payload) => sent.push(["panel", payload]),
  };
  const { local, calls } = localSpy();
  const routes = routesFor(win, { surface: "sessions", glass: "glass" }, local);
  routes.openSession("abc12345");
  routes.openCard("cards/T-1.md");
  routes.openDoc("/docs/r.md");
  routes.switchFleet("home");
  routes.fold("sessions", true);
  assert.deepEqual(calls, []);
  assert.deepEqual(sent, [
    ["open", { kind: "session", short: "abc12345" }],
    ["open", { kind: "card", path: "cards/T-1.md" }],
    ["open", { kind: "doc", path: "/docs/r.md" }],
    ["fleet", "home"],
    ["panel", { side: "sessions", folded: true }],
  ]);
});

test("the board opens locally but switches fleet and focuses the orchestrator through the window", () => {
  const sent = [];
  const win = { fleetdeckSwitchFleet: (name) => sent.push(name), fleetdeckOpen: (payload) => sent.push(payload) };
  const { local, calls } = localSpy();
  const routes = routesFor(win, { surface: "board", glass: "glass" }, local);
  routes.openCard("a.md");
  routes.switchFleet("home");
  routes.openOrchestrator();
  assert.deepEqual(calls, [["openCard", "a.md"]]);
  assert.deepEqual(sent, ["home", { kind: "orchestrator" }]);
});
