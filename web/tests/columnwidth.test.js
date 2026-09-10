// The column's width and folded state, and what must survive a reload.
//
// Storage is faked rather than mocked away: what is being pinned is that the
// state comes back, so a stand-in that remembers nothing would make every case
// vacuous. The last two cases take storage away entirely, which is the state a
// private window is in.

import { test, beforeEach, afterEach } from "node:test";
import assert from "node:assert/strict";

import { createColumnWidth, storedStep, storedFolded, STEPS } from "../js/columnwidth.js";

let real;

function fakeStorage(initial = {}) {
  const map = new Map(Object.entries(initial));
  return {
    getItem: (k) => (map.has(k) ? map.get(k) : null),
    setItem: (k, v) => map.set(k, String(v)),
    removeItem: (k) => map.delete(k),
    map,
  };
}

beforeEach(() => {
  real = Object.hasOwn(globalThis, "localStorage") ? globalThis.localStorage : undefined;
  globalThis.localStorage = fakeStorage();
});

afterEach(() => {
  if (real === undefined) delete globalThis.localStorage;
  else globalThis.localStorage = real;
});

test("a first run is the width the column has always had, not a surprise", () => {
  const column = createColumnWidth();

  assert.equal(column.state().width, "25%");
  assert.equal(column.state().folded, false);
});

test("wider and narrower walk the ladder", () => {
  const column = createColumnWidth();
  const start = column.state().step;

  column.widen();
  assert.equal(column.state().step, start + 1);

  column.narrow();
  column.narrow();
  assert.equal(column.state().step, start - 1);
});

// A control that wraps from widest to narrowest reads as a misclick. Break it
// by removing the guards and this test fails with the step outside the ladder.
test("the ladder has ends, and they hold", () => {
  const column = createColumnWidth();
  for (let i = 0; i < STEPS.length + 3; i += 1) column.widen();
  assert.equal(column.state().step, STEPS.length - 1);
  assert.equal(column.state().canWiden, false, "the widest step still offers to widen");

  for (let i = 0; i < STEPS.length + 3; i += 1) column.narrow();
  assert.equal(column.state().step, 0);
  assert.equal(column.state().canNarrow, false, "the narrowest step still offers to narrow");
});

test("the width survives a reload", () => {
  const first = createColumnWidth();
  first.widen();
  first.widen();
  const chosen = first.state().width;

  // A new instance over the same storage is what a reload is.
  assert.equal(createColumnWidth().state().width, chosen);
});

test("so does being folded away", () => {
  createColumnWidth().fold();

  assert.equal(createColumnWidth().state().folded, true);
  assert.equal(storedFolded(), true);
});

// Folding is meant to be reversible. A person who folds a column they had made
// wide and gets back a narrow one has lost something they chose.
test("unfolding gives back the width that was folded away", () => {
  const column = createColumnWidth();
  column.widen();
  column.widen();
  const chosen = column.state().width;

  column.fold();
  column.unfold();

  assert.equal(column.state().width, chosen);
  assert.equal(column.state().folded, false);
});

test("the change is announced, so a caller never has to poll for it", () => {
  const seen = [];
  const column = createColumnWidth((state) => seen.push(state));

  column.widen();
  column.fold();
  column.unfold();

  assert.equal(seen.length, 3);
  assert.equal(seen.at(-1).folded, false);
});

// A repeated fold is not a change, and announcing it would redraw the column
// for nothing.
test("folding what is already folded says nothing", () => {
  const seen = [];
  const column = createColumnWidth((state) => seen.push(state));
  column.fold();
  column.fold();

  assert.equal(seen.length, 1);
});

// A value from an older version, a hand-edited one, or a step that no longer
// exists. A column of width `undefined` is a column that has disappeared, so
// nonsense lands on the default. Break it by trusting the stored number and
// this test fails with a width of undefined.
test("nonsense in storage is a first run, not a broken column", () => {
  for (const bad of ["", "abc", "-1", "999", "2.5", "null"]) {
    globalThis.localStorage = fakeStorage({ "fleetdeck-orchestrator-width": bad });
    assert.equal(createColumnWidth().state().width, "25%", `stored ${JSON.stringify(bad)} did not fall back`);
    assert.equal(storedStep(), STEPS.indexOf("25%"));
  }
});

// A private window throws on every access. Losing a remembered preference is a
// fair trade; taking the page down over one is not.
test("storage that throws on reading is an ordinary first run", () => {
  globalThis.localStorage = {
    getItem() {
      throw new Error("storage is off");
    },
    setItem() {
      throw new Error("storage is off");
    },
    removeItem() {
      throw new Error("storage is off");
    },
  };

  const column = createColumnWidth();
  assert.equal(column.state().width, "25%");
  // And it must still work for this session, silently.
  column.widen();
  assert.equal(column.state().width, "32%");
  column.fold();
  assert.equal(column.state().folded, true);
});
