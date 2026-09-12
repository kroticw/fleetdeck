// web/js/_tests/fleet.test.js
//
// Which fleet a tab shows, and how a machine's sessions group around it.
//
// Run with: node --test web/js/_tests/fleet.test.js

import test from "node:test";
import assert from "node:assert/strict";

import {
  fleetFromSearch,
  withFleet,
  fleetSearch,
  isMultiFleet,
  groupSessions,
  headerSessions,
  fleetEntries,
  switchFleet,
  belongsTo,
  rememberFleet,
  rememberedFleet,
} from "../fleet.js";

// The shape the server serves for fleet B of three: A, B, C.
function snapB() {
  return {
    fleet: "B",
    fleets: ["A", "B", "C"],
    sessions: [
      { short: "a1", fleets: ["A"] },
      { short: "b1", fleets: ["B"], needs: "answer: which one?" },
      { short: "ab", fleets: ["A", "B"] },
      { short: "ac", fleets: ["A", "C"], needs: "answer: go on?" },
      { short: "n1" },
      { short: "n2", fleets: [] },
    ],
  };
}

const waitingOnNeeds = (s) => Boolean(s.needs);

test("the fleet is read from the address, and absent is empty", () => {
  assert.equal(fleetFromSearch("?fleet=clining"), "clining");
  assert.equal(fleetFromSearch("?x=1&fleet=%D0%BE%D1%81%D0%BD%D0%BE%D0%B2%D0%BD%D0%BE%D0%B9"), "основной");
  assert.equal(fleetFromSearch(""), "");
  assert.equal(fleetFromSearch("?x=1"), "");
});

test("a request carries the tab's fleet, and no fleet leaves the path alone", () => {
  assert.equal(withFleet("/ws", "B"), "/ws?fleet=B");
  assert.equal(withFleet("/api/docs/content?path=a.md", "основной"), "/api/docs/content?path=a.md&fleet=%D0%BE%D1%81%D0%BD%D0%BE%D0%B2%D0%BD%D0%BE%D0%B9");
  assert.equal(withFleet("/ws", ""), "/ws");
});

test("switching keeps the address's other parameters", () => {
  assert.equal(fleetSearch("?x=1&fleet=A", "B"), "?x=1&fleet=B");
  assert.equal(fleetSearch("", "a b"), "?fleet=a+b");
});

test("one fleet is not a multi-fleet panel, and neither is an old snapshot", () => {
  assert.equal(isMultiFleet({ fleets: ["A", "B"] }), true);
  assert.equal(isMultiFleet({ fleets: ["A"] }), false);
  assert.equal(isMultiFleet({}), false);
  assert.equal(isMultiFleet(null), false);
});

test("sessions group into own, unclaimed and one group per other fleet", () => {
  const { own, unclaimed, others } = groupSessions(snapB());
  assert.deepEqual(own.map((s) => s.short), ["b1", "ab"]);
  assert.deepEqual(unclaimed.map((s) => s.short), ["n1", "n2"]);
  assert.deepEqual(
    others.map((g) => [g.name, g.sessions.map((s) => s.short)]),
    [
      ["A", ["a1", "ac"]],
      ["C", ["ac"]],
    ],
  );
});

test("another fleet with no sessions still has its group, empty", () => {
  const snap = snapB();
  snap.sessions = snap.sessions.filter((s) => !(s.fleets ?? []).includes("C"));
  const { others } = groupSessions(snap);
  // "ac" went with C's sessions, so A keeps only a1.
  assert.deepEqual(others.map((g) => [g.name, g.sessions.length]), [["A", 1], ["C", 0]]);
});

test("the header counts this fleet and the unclaimed, not the others", () => {
  assert.deepEqual(headerSessions(snapB()).map((s) => s.short), ["b1", "ab", "n1", "n2"]);
});

test("with one fleet the header counts every session, as before fleets", () => {
  const one = { fleet: "A", fleets: ["A"], sessions: [{ short: "a1", fleets: ["A"] }, { short: "n1" }] };
  assert.deepEqual(headerSessions(one).map((s) => s.short), ["a1", "n1"]);
  assert.deepEqual(headerSessions({ sessions: [{ short: "x" }] }).map((s) => s.short), ["x"]);
});

test("the switcher lists every fleet with its own waiting count", () => {
  assert.deepEqual(fleetEntries(snapB(), waitingOnNeeds), [
    { name: "A", current: false, waiting: 1 },
    { name: "B", current: true, waiting: 1 },
    { name: "C", current: false, waiting: 1 },
  ]);
});

test("a session is this fleet's to take unless another fleet claims it alone", () => {
  // The orchestrator column's picker and the wizard offer exactly these: a
  // session only another fleet claims is that fleet's work, and its
  // orchestrator cannot lead a second fleet.
  assert.equal(belongsTo({ fleets: ["B"] }, "B"), true);
  assert.equal(belongsTo({ fleets: ["A", "B"] }, "B"), true);
  assert.equal(belongsTo({}, "B"), true, "an unclaimed session belongs to every fleet");
  assert.equal(belongsTo({ fleets: [] }, "B"), true);
  assert.equal(belongsTo({ fleets: ["A"] }, "B"), false);
  assert.equal(belongsTo({}, ""), true, "a snapshot from before fleets tags nothing");
});

test("switching forgets the open session and navigates to the fleet", () => {
  const removed = [];
  const storage = {
    setItem() {
      throw new Error("switching must not remember a session");
    },
    removeItem: (key) => removed.push(key),
  };
  const assigned = [];
  const location = { pathname: "/", search: "?fleet=A", assign: (url) => assigned.push(url) };
  switchFleet("C", { storage, location });
  assert.deepEqual(removed, ["fleetdeck-open-session"]);
  assert.deepEqual(assigned, ["/?fleet=C"]);
});

// The exception: the start page carrying a page back into the fleet it
// reloaded out of. The tab is going back where it was, so the session panel
// that was up is this fleet's and must survive the hop.
test("carrying a page back keeps the session it had open", () => {
  const touched = [];
  const storage = {
    setItem: (key) => touched.push(`set:${key}`),
    removeItem: (key) => touched.push(`remove:${key}`),
  };
  const assigned = [];
  const location = { pathname: "/", search: "", assign: (url) => assigned.push(url) };
  switchFleet("C", { storage, location, keepSession: true });
  assert.deepEqual(touched, [], "the open session must not be touched when carrying back");
  assert.deepEqual(assigned, ["/?fleet=C"]);
});

// The fleet the panel was last opened in, for the start page to mark. Written
// to localStorage rather than the session storage the open session uses: the
// point is the next launch of the application, which a tab's session storage
// does not survive.
test("the fleet last worked in is remembered across launches, not across a tab", () => {
  const store = new Map();
  const original = Object.getOwnPropertyDescriptor(globalThis, "localStorage");
  Object.defineProperty(globalThis, "localStorage", {
    value: {
      getItem: (key) => store.get(key) ?? null,
      setItem: (key, value) => store.set(key, value),
    },
    configurable: true,
  });
  try {
    assert.equal(rememberedFleet(), "", "nothing remembered reads as no mark, never as a fleet");
    rememberFleet("vpn");
    assert.equal(store.get("fleetdeck-fleet"), "vpn");
    assert.equal(rememberedFleet(), "vpn");
  } finally {
    if (original) Object.defineProperty(globalThis, "localStorage", original);
    else delete globalThis.localStorage;
  }
});

// A private window, or a browser with storage switched off, throws on either
// call. It costs a mark on a list and must never take the page down with it.
test("storage that throws costs the mark and nothing else", () => {
  const original = Object.getOwnPropertyDescriptor(globalThis, "localStorage");
  Object.defineProperty(globalThis, "localStorage", {
    value: {
      getItem() {
        throw new Error("denied");
      },
      setItem() {
        throw new Error("denied");
      },
    },
    configurable: true,
  });
  try {
    assert.equal(rememberedFleet(), "");
    rememberFleet("vpn");
  } finally {
    if (original) Object.defineProperty(globalThis, "localStorage", original);
    else delete globalThis.localStorage;
  }
});
