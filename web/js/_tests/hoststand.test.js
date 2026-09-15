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

// A stand's frame with the new card form open is taken without a press
// (cmd/fleetdeck-window, FLEETDECK_STAND_OPEN): the page opens it only when the
// window says it is on a stand.
test("a stand's page is asked to open the new card form", () => {
  assert.deepEqual(readHost(win({ stand: true, standOpen: "newcard" })).open, ["newcard"]);
});

// T-070: the new card form and the fleet menu's list are drawn as frosted glass,
// and a stand's frame shows both open at once: one on the board, the other on
// the orchestrator island, clear of each other.
test("a stand's page is asked to open the fleet menu, alone or with the new card form", () => {
  assert.deepEqual(readHost(win({ stand: true, standOpen: "fleetmenu" })).open, ["fleetmenu"]);
  assert.deepEqual(readHost(win({ stand: true, standOpen: "newcard,fleetmenu" })).open, ["newcard", "fleetmenu"]);
});

test("a page off a stand, or asked for anything else, opens nothing by itself", () => {
  assert.equal(readHost(win({ standOpen: "newcard" })).open, undefined);
  assert.equal(readHost(win({ stand: true, standOpen: "card" })).open, undefined);
  assert.equal(readHost(win({ stand: true, standOpen: "newcard,card" })).open, undefined);
  assert.equal(readHost(win({ stand: true })).open, undefined);
});
