// web/js/_tests/host.test.js
//
// Run with: node --test web/js/_tests/host.test.js

import test from "node:test";
import assert from "node:assert/strict";
import { HOST_VERSION, readHost, callHost, onHostMessage } from "../host.js";

test("a page with no host is the browser layout", () => {
  assert.equal(readHost({}), null);
});

test("a host of an unknown version is treated as no host", () => {
  assert.equal(readHost({ fleetdeckHost: { version: 2, surface: "board", glass: "glass" } }), null);
});

test("a host with an unknown surface or glass is treated as no host", () => {
  assert.equal(readHost({ fleetdeckHost: { version: HOST_VERSION, surface: "header", glass: "glass" } }), null);
  assert.equal(readHost({ fleetdeckHost: { version: HOST_VERSION, surface: "board", glass: "frosted" } }), null);
});

test("a valid host names its surface and glass", () => {
  const win = { fleetdeckHost: { version: HOST_VERSION, surface: "sessions", glass: "opaque" } };
  assert.deepEqual(readHost(win), { surface: "sessions", glass: "opaque" });
});

test("calling the window without its binding answers null, not a rejected promise", () => {
  assert.equal(callHost({}, "fleetdeckOpen", { kind: "card", path: "a.md" }), null);
});

test("calling the window passes the payload and resolves with the answer", async () => {
  const seen = [];
  const win = {
    fleetdeckOpen: (payload) => {
      seen.push(payload);
      return "ok";
    },
  };
  assert.equal(await callHost(win, "fleetdeckOpen", { kind: "doc", path: "/d.md" }), "ok");
  assert.deepEqual(seen, [{ kind: "doc", path: "/d.md" }]);
});

test("a message from the window reaches the handler; a malformed one does not", () => {
  const win = { fleetdeckHost: { version: HOST_VERSION, surface: "board", glass: "glass", receive() {} } };
  const got = [];
  const stop = onHostMessage(win, (m) => got.push(m.type));
  win.fleetdeckHost.receive({ type: "show", section: "docs" });
  win.fleetdeckHost.receive({ section: "docs" });
  win.fleetdeckHost.receive(null);
  stop();
  win.fleetdeckHost.receive({ type: "show", section: "board" });
  assert.deepEqual(got, ["show"]);
});
