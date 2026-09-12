// Two letters standing in for a session, on a strip too narrow for its name.
//
// The whole difficulty is that a fleet's sessions are not named to be told
// apart by their first letters. This machine's own fleet is "fleetdeck:
// обновление из выпуска", "fleetdeck: колонка сессий — вид и подъём",
// "fleetdeck: разведка по разным агентам" — the first nine characters are the
// project, and a mark taken off the front of the name would read "FL" on every
// session there is.

import { test } from "node:test";
import assert from "node:assert/strict";

import { sessionMark, sessionMarks } from "../js/initials.js";

// --- the mark itself ---

test("the project prefix is dropped: the mark comes from what makes the name different", () => {
  assert.equal(sessionMark({ short: "aa11", name: "fleetdeck: колонка сессий" }), "КС");
  assert.equal(sessionMark({ short: "bb22", name: "fleetdeck: обновление из выпуска" }), "ОИ");
  // Without this the two above are both "FL", which is the whole problem.
  assert.notEqual(
    sessionMark({ short: "aa11", name: "fleetdeck: колонка сессий" }),
    sessionMark({ short: "bb22", name: "fleetdeck: обновление из выпуска" }),
  );
});

test("the operator's own name for a session wins over the daemon's", () => {
  assert.equal(sessionMark({ short: "aa11", name: "fleetdeck: колонка", label: "моя правка" }), "МП");
});

test("a one-word name gives its first two letters, not one letter and a gap", () => {
  assert.equal(sessionMark({ short: "aa11", name: "оркестр" }), "ОР");
});

test("a one-letter name is one letter, never padded with something invented", () => {
  assert.equal(sessionMark({ short: "aa11", name: "я" }), "Я");
});

test("punctuation and dashes are separators, not letters", () => {
  assert.equal(sessionMark({ short: "aa11", name: "fleetdeck: — вид и подъём" }), "ВИ");
  assert.equal(sessionMark({ short: "bb22", name: "BS-27572 fix" }), "BF");
});

// A prefix that leaves nothing behind is not a prefix: the whole name is all
// there is, and it is used as it stands rather than being thrown away for an
// id the operator never chose.
test("a name that is nothing but its prefix is still used as the name", () => {
  assert.equal(sessionMark({ short: "c8d3", name: "fleetdeck:" }), "FL");
});

// A name with no letters or digits in it at all leaves genuinely nothing to
// mark with, and an empty mark is a blank square that reads as a session with
// no name rather than as a session whose name says nothing.
test("a name with nothing in it falls back to the short id", () => {
  assert.equal(sessionMark({ short: "c8d3", name: "   " }), "C8");
  assert.equal(sessionMark({ short: "c8d3", name: "—" }), "C8");
  assert.equal(sessionMark({ short: "c8d3" }), "C8");
});

test("a session with nothing at all to go on still gets a mark rather than an empty square", () => {
  assert.equal(sessionMark({}), "??");
});

// --- telling the marks apart, which is the acceptance ---

test("sessions that would share a mark are given different ones", () => {
  const marks = sessionMarks([
    { short: "aa11", name: "fleetdeck: разведка по разным агентам" },
    { short: "bb22", name: "fleetdeck: разведка уходящих сессий" },
  ]);
  assert.equal(marks.get("aa11"), "РП");
  // "РУ" is already РП's neighbour by first letters; the second letter moves
  // along the name until the two differ.
  assert.notEqual(marks.get("bb22"), marks.get("aa11"));
});

test("a clash nothing in the name can settle falls back to the short id", () => {
  const marks = sessionMarks([
    { short: "aa11", name: "одно" },
    { short: "bb22", name: "одно" },
  ]);
  assert.notEqual(marks.get("aa11"), marks.get("bb22"));
  assert.equal(marks.size, 2);
});

// The mark must not change under the operator because the list reordered —
// and it does reorder, every time a session starts waiting and floats to the
// top. So which of two clashing sessions keeps the plain mark is decided by
// their short ids, which never change, not by where they happen to sit.
test("which session keeps the plain mark does not depend on the order they arrive in", () => {
  const a = { short: "aa11", name: "fleetdeck: разведка по разным агентам" };
  const b = { short: "bb22", name: "fleetdeck: разведка уходящих сессий" };
  const forwards = sessionMarks([a, b]);
  const backwards = sessionMarks([b, a]);
  assert.equal(forwards.get("aa11"), backwards.get("aa11"));
  assert.equal(forwards.get("bb22"), backwards.get("bb22"));
});

test("sessions that already differ are left alone", () => {
  const marks = sessionMarks([
    { short: "aa11", name: "fleetdeck: колонка сессий" },
    { short: "bb22", name: "fleetdeck: обновление из выпуска" },
    { short: "cc33", name: "оркестр" },
  ]);
  assert.deepEqual([...marks.values()], ["КС", "ОИ", "ОР"]);
});

test("an empty fleet produces no marks and no exception", () => {
  assert.equal(sessionMarks([]).size, 0);
});
