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

// The fleet's menu is the window's bar's, a system menu (T-226): no page has
// one for a stand to open.
test("a stand's page is not asked to open a fleet menu it no longer has", () => {
  assert.equal(readHost(win({ stand: true, standOpen: "fleetmenu" })).open, undefined);
  assert.equal(readHost(win({ stand: true, standOpen: "newcard,fleetmenu" })).open, undefined, "a list with a name not known is not followed");
});

// T-079: the band the window is dragged by went to nothing the moment a sheet
// opened over the dimmed board, and no stand had ever opened one.
test("a stand's board is asked to open a session", () => {
  assert.deepEqual(readHost(win({ stand: true, standOpen: "session" })).open, ["session"]);
  assert.deepEqual(readHost(win({ stand: true, standOpen: "newcard,session" })).open, ["newcard", "session"]);
});

test("a stand's board is asked to open a card's document with its author docked below or beside it", () => {
  assert.deepEqual(readHost(win({ stand: true, standOpen: "carddoc-bottom" })).open, ["carddoc-bottom"]);
  assert.deepEqual(readHost(win({ stand: true, standOpen: "carddoc-right" })).open, ["carddoc-right"]);
  // Folded beside the document, and moved below once attached: the window let
  // both through and the page opened nothing (run 36379262459).
  assert.deepEqual(readHost(win({ stand: true, standOpen: "carddoc-rail" })).open, ["carddoc-rail"]);
  assert.deepEqual(readHost(win({ stand: true, standOpen: "carddoc-flip" })).open, ["carddoc-flip"]);
  assert.equal(readHost(win({ stand: true, standOpen: "carddoc" })).open, undefined);
});

test("a page off a stand, or asked for anything else, opens nothing by itself", () => {
  assert.equal(readHost(win({ standOpen: "newcard" })).open, undefined);
  assert.equal(readHost(win({ stand: true, standOpen: "card" })).open, undefined);
  assert.equal(readHost(win({ stand: true, standOpen: "newcard,card" })).open, undefined);
  assert.equal(readHost(win({ stand: true })).open, undefined);
});
