// web/js/_tests/hoststand.test.js
//
// Run with: node --test web/js/_tests/hoststand.test.js

import test from "node:test";
import assert from "node:assert/strict";
import { readHost } from "../host.js";

const win = (extra) => ({ fleetdeckHost: { version: 1, surface: "board", glass: "glass", receive() {}, ...extra } });

// The window's first stand run with the board's scrolling report logged
// nothing: the page read the host object and left stand behind.
test("a stand's page knows it is on a stand", () => {
  assert.equal(readHost(win({ stand: true })).stand, true);
});

test("a person's window's page is not on a stand, whatever else the object carries", () => {
  assert.equal(readHost(win({})).stand, undefined);
  assert.equal(readHost(win({ stand: "yes" })).stand, undefined);
});
