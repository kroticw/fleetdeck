// The column's width and folded state, and what must survive a reload.
//
// Storage is faked rather than mocked away: what is being pinned is that the
// state comes back, so a stand-in that remembers nothing would make every case
// vacuous. The last case takes storage away entirely, which is the state a
// private window is in.

import { test, beforeEach, afterEach } from "node:test";
import assert from "node:assert/strict";

import {
  createColumnWidth,
  storedPercent,
  storedFolded,
  clampPercent,
  DEFAULT_PERCENT,
  MIN_PERCENT,
  MAX_PERCENT,
  ORCHESTRATOR_KEYS,
  SESSIONS_KEYS,
} from "../js/columnwidth.js";

let real;

// Real Storage coerces its key argument with ToString rather than rejecting
// a non-string one (getItem(undefined) reads the entry literally named
// "undefined"). The fake matches that so a test relying on it — see the
// legacy-key coercion trap below — exercises the same trap a real browser
// would set, not one only the fake believes in.
function fakeStorage(initial = {}) {
  const map = new Map(Object.entries(initial));
  return {
    getItem: (k) => (map.has(String(k)) ? map.get(String(k)) : null),
    setItem: (k, v) => map.set(String(k), String(v)),
    removeItem: (k) => map.delete(String(k)),
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

  assert.equal(column.state().width, `${DEFAULT_PERCENT}%`);
  assert.equal(column.state().folded, false);
});

test("dragging moves the column and the width is remembered when the drag ends", () => {
  const column = createColumnWidth();
  column.setPercent(37.5);

  // Not written yet: a drag across the screen is a hundred moves and one
  // outcome, and only the outcome belongs in storage.
  assert.equal(createColumnWidth().state().percent, DEFAULT_PERCENT, "a move in progress was written to storage");

  column.remember();
  assert.equal(createColumnWidth().state().percent, 37.5, "the finished drag was not remembered");
});

// The floor is the whole answer to "a continuous range has no illegal value to
// reject". Break the clamp and this test fails with a column of nothing.
test("the column cannot be dragged to nothing, or past the far side", () => {
  const column = createColumnWidth();

  column.setPercent(-500);
  assert.equal(column.state().percent, MIN_PERCENT);

  column.setPercent(0);
  assert.equal(column.state().percent, MIN_PERCENT, "zero is the value that has to be unreachable");

  column.setPercent(5000);
  assert.equal(column.state().percent, MAX_PERCENT);
});

test("a move that is not a number leaves the column where it is", () => {
  const column = createColumnWidth();
  column.setPercent(30);

  for (const bad of [NaN, Infinity, -Infinity, undefined, null]) {
    column.setPercent(bad);
    assert.equal(column.state().percent, 30, `${String(bad)} moved the column`);
  }
});

test("being folded away survives a reload, and so does the width under it", () => {
  const first = createColumnWidth();
  first.setPercent(44);
  first.remember();
  first.fold();

  const second = createColumnWidth();
  assert.equal(second.state().folded, true);
  assert.equal(second.state().percent, 44, "the width was lost while the column was folded");
  assert.equal(storedFolded(), true);
});

test("unfolding gives back the column that was folded away", () => {
  const column = createColumnWidth();
  column.setPercent(44);
  column.remember();
  column.fold();
  column.unfold();

  assert.equal(column.state().percent, 44);
  assert.equal(column.state().folded, false);
});

test("the change is announced, so a caller never has to poll for it", () => {
  const seen = [];
  const column = createColumnWidth((s) => seen.push(s));

  column.setPercent(30);
  column.fold();
  column.unfold();

  assert.equal(seen.length, 3);
  assert.equal(seen.at(-1).folded, false);
});

test("a move that changes nothing says nothing", () => {
  const seen = [];
  const column = createColumnWidth((s) => seen.push(s));

  column.setPercent(DEFAULT_PERCENT);
  column.fold();
  column.fold();

  assert.equal(seen.length, 1, "a redraw was asked for with nothing to redraw");
});

// This is the test the ladder is remembered for, and it has to outlive the
// ladder. Every value here is one that has been seen or can be: a cleared
// entry, a hand-edited one, a number that arithmetic produced.
//
// Break the clamp or the blank-string branch and this fails — and the assertion
// is stronger than the one it replaces: not merely "landed on something legal",
// but "the column is still there to be grabbed".
test("nothing in storage can produce a column a person cannot reach", () => {
  const rubbish = ["", "   ", "abc", "0", "-1", "-40", "NaN", "Infinity", "-Infinity", "1e400", "null", "undefined", "12,5", "{}"];

  for (const bad of rubbish) {
    globalThis.localStorage = fakeStorage({ "fleetdeck-orchestrator-width-pct": bad });
    const percent = createColumnWidth().state().percent;
    assert.ok(
      Number.isFinite(percent) && percent >= MIN_PERCENT && percent <= MAX_PERCENT,
      `stored ${JSON.stringify(bad)} produced ${percent}`,
    );
    assert.equal(storedPercent(), percent);
  }
});

// A number small enough to be legal arithmetic and useless on screen.
test("a stored width below the floor comes back at the floor, not below it", () => {
  globalThis.localStorage = fakeStorage({ "fleetdeck-orchestrator-width-pct": "0.0001" });

  assert.equal(createColumnWidth().state().percent, MIN_PERCENT);
});

// The value the previous version wrote is a perfectly good number, and it will
// arrive from a real browser rather than from a test. Read as a percentage, "3"
// is a three-percent column; read as what it is, it is the width the operator
// chose. Break the migration and this fails with a column at the default.
test("a width chosen under the old stepped version is carried across, once", () => {
  globalThis.localStorage = fakeStorage({ "fleetdeck-orchestrator-width": "4" });

  assert.equal(createColumnWidth().state().percent, 42, "the operator's chosen width was thrown away");

  // And the old key stops existing, so it cannot be read a second time with
  // different meaning later.
  assert.equal(globalThis.localStorage.getItem("fleetdeck-orchestrator-width"), null);
  assert.equal(globalThis.localStorage.getItem("fleetdeck-orchestrator-width-pct"), "42");
});

test("a step index the old version could never have written is a first run", () => {
  for (const bad of ["9", "-1", "", "x"]) {
    globalThis.localStorage = fakeStorage({ "fleetdeck-orchestrator-width": bad });
    assert.equal(createColumnWidth().state().percent, DEFAULT_PERCENT, `legacy ${JSON.stringify(bad)} was believed`);
  }
});

// A private window throws on every access. Losing a remembered preference is a
// fair trade; taking the page down over one is not.
test("storage that throws is an ordinary first run, and the column still works", () => {
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
  assert.equal(column.state().percent, DEFAULT_PERCENT);
  column.setPercent(40);
  column.remember();
  assert.equal(column.state().percent, 40, "the column stopped moving because storage is off");
  column.fold();
  assert.equal(column.state().folded, true);
});

test("clampPercent is the one place the range is decided", () => {
  assert.equal(clampPercent(1), MIN_PERCENT);
  assert.equal(clampPercent(99), MAX_PERCENT);
  assert.equal(clampPercent(30), 30);
});

// The session list is a second column sharing the same module, not a second
// copy of it. Every case above already proves the rules; what is new here is
// only that a second set of keys stays out of the first set's way.
test("a second column's keys are a separate column, not a second read of the first one's", () => {
  const orchestrator = createColumnWidth(() => {}, ORCHESTRATOR_KEYS);
  orchestrator.setPercent(50);
  orchestrator.remember();
  orchestrator.fold();

  const sessions = createColumnWidth(() => {}, SESSIONS_KEYS);
  assert.equal(sessions.state().percent, DEFAULT_PERCENT, "the session list inherited the orchestrator's width");
  assert.equal(sessions.state().folded, false, "the session list inherited the orchestrator's folded state");

  sessions.setPercent(20);
  sessions.remember();

  assert.equal(
    createColumnWidth(() => {}, ORCHESTRATOR_KEYS).state().percent,
    50,
    "writing the session list's width moved the orchestrator's",
  );
});

// The ladder never shipped for the session list, so SESSIONS_KEYS carries no
// `legacy` entry. storedPercent's migration branch must treat that as "there
// is nothing to migrate", not dereference a key that was never there.
test("a column with no legacy key has nothing to migrate, and does not fail trying", () => {
  // "undefined" is the literal key `read(keys.legacy)` would hit if the
  // `keys.legacy ?` guard were dropped and a missing key coerced to a string
  // instead of short-circuiting — a real risk in a browser's real
  // localStorage, which stringifies whatever key it is given rather than
  // throwing. Planting a legal-looking legacy value there catches exactly
  // that regression; without the guard this test reads it back as "42".
  globalThis.localStorage = fakeStorage({
    "fleetdeck-orchestrator-width": "4",
    undefined: "4",
  });

  assert.equal(storedPercent(SESSIONS_KEYS), DEFAULT_PERCENT);
  assert.equal(storedFolded(SESSIONS_KEYS), false);
  // Neither the orchestrator's own legacy key nor the coercion trap were
  // consumed by a search that was never meant to reach them.
  assert.equal(globalThis.localStorage.getItem("fleetdeck-orchestrator-width"), "4");
  assert.equal(globalThis.localStorage.getItem(undefined), "4");
});
