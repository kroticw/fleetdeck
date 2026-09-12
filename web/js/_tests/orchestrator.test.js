// web/js/_tests/orchestrator.test.js
//
// The orchestrator column: which session a pin resolves to, the context
// percentage, the picker and the label edit in its head, the controls that
// size it — and the session's live terminal it holds, which is what the column
// shows. The terminal itself is web/js/liveterminal.js, pinned in
// web/tests/liveterminal.test.js and web/tests/session.test.js; what is pinned
// here is when the column opens it, moves it and lets it go. See this
// directory's header.test.js for why this subtree is invisible to
// web/embed.go's go:embed.
//
// Run with: node --test web/js/_tests/orchestrator.test.js

import test from "node:test";
import assert from "node:assert/strict";
import { resolveOrchestrator, contextPercent, pickableSessions, pickerLabel, briefWarning } from "../orchestrator.js";

test("no snapshot yet resolves to nothing pinned and no sessions", () => {
  const r = resolveOrchestrator(null);
  assert.equal(r.short, "");
  assert.equal(r.session, undefined);
  assert.deepEqual(r.sessions, []);
});

test("a configured pin resolves to the session matching by short id, never by sessionId", () => {
  const snap = {
    orchestratorSession: "abc",
    sessions: [
      // A decoy whose sessionId equals the pinned short, listed FIRST: a
      // resolver that matches by sessionId (or falls back to it) would find
      // this one before ever reaching the session that actually matches by
      // short id, below.
      { short: "xyz", sessionId: "abc" },
      { short: "abc", sessionId: "11111111-1111-1111-1111-111111111111", name: "orchestrator" },
    ],
  };
  const r = resolveOrchestrator(snap);
  assert.equal(r.session.short, "abc");
  assert.equal(r.session.name, "orchestrator");
});

test("a pin naming a session the daemon no longer lists resolves to no session, not a crash", () => {
  const r = resolveOrchestrator({ orchestratorSession: "gone", sessions: [{ short: "other" }] });
  assert.equal(r.session, undefined);
  assert.equal(r.short, "gone");
});

test("an empty pin resolves to no session, even against a session whose short is empty too", () => {
  // The decoy matters: an empty pin is "nothing is pinned", not "find whatever
  // also has no short id". A resolver that dropped the guard would match it.
  const r = resolveOrchestrator({
    orchestratorSession: "",
    sessions: [{ short: "", name: "a session with no short id" }, { short: "a" }],
  });
  assert.equal(r.session, undefined);
  assert.equal(r.short, "");
});

test("contextPercent computes tokens/window as a rounded percentage", () => {
  assert.equal(contextPercent({ tokens: 50000, window: 200000, estimated: true }), 25);
  // A value that does not divide evenly, so dropping the rounding is visible:
  // 123456/200000 is 61.728%, and a bar labelled "61.728%" is not a label.
  assert.equal(contextPercent({ tokens: 123456, window: 200000 }), 62);
});

test("pickableSessions offers every session that has a short id", () => {
  const list = pickableSessions([{ short: "a" }, { short: "" }, { name: "no short" }, { short: "b" }]);
  assert.deepEqual(list.map((s) => s.short), ["a", "b"]);
});

test("pickableSessions offers this fleet's sessions and the unclaimed, never another fleet's", () => {
  const list = pickableSessions(
    [{ short: "a", fleets: ["A"] }, { short: "b", fleets: ["B"] }, { short: "n" }, { short: "ab", fleets: ["A", "B"] }],
    "B",
  );
  assert.deepEqual(list.map((s) => s.short), ["b", "n", "ab"]);
});

test("pickableSessions on nothing at all is an empty list, not a crash", () => {
  assert.deepEqual(pickableSessions(undefined), []);
});

test("pickerLabel prefers the operator's own label, then the name, then falls back to the short id", () => {
  assert.equal(pickerLabel({ short: "abc", label: "my orchestrator", name: "orchestrator" }), "my orchestrator");
  assert.equal(pickerLabel({ short: "abc", label: "", name: "orchestrator" }), "orchestrator");
  assert.equal(pickerLabel({ short: "abc", name: "orchestrator" }), "orchestrator");
  assert.equal(pickerLabel({ short: "abc", name: "" }), "abc");
  assert.equal(pickerLabel({ short: "abc" }), "abc");
});

test("contextPercent is null without a usable window", () => {
  assert.equal(contextPercent(null), null);
  assert.equal(contextPercent(undefined), null);
  assert.equal(contextPercent({ tokens: 1, window: 0 }), null);
});

// --- the column, driven over time ---------------------------------------------
//
// These drive the real module against a fake DOM, a fake terminal and fake
// sockets, and push snapshots through the real store. Nothing is stubbed
// between store.js and orchestrator.js: which snapshot reaches the column, and
// what the column makes of it, is the seam a defect here would live in.

import { afterEach } from "node:test";
import { installDOM, fireEvent, settle } from "../../tests/fake-dom.js";
import { fakeTimers, answer, installTerminal, installFit, installObserver, installSocket, ready, asText } from "../../tests/terminal-fakes.js";
import { viewSignature } from "../orchestrator.js";
import { t } from "../i18n.js";

// --- the snapshot gate, on its own ---

const PIN = {
  orchestratorSession: "abc",
  sessions: [
    { short: "abc", name: "orchestrator", sessionId: "u-1", context: { tokens: 20, window: 100 } },
    { short: "zzz", name: "someone else", sessionId: "u-2", context: { tokens: 10, window: 100 } },
  ],
};

test("another session's context moving changes nothing this column shows", () => {
  const later = structuredClone(PIN);
  later.sessions[1].context.tokens = 90;
  assert.equal(viewSignature(PIN, true), viewSignature(later, true));
});

test("the pinned session's own context moving does change it", () => {
  const later = structuredClone(PIN);
  later.sessions[0].context.tokens = 90;
  assert.notEqual(viewSignature(PIN, true), viewSignature(later, true));
});

test("losing the socket changes it, pinned or not", () => {
  assert.notEqual(viewSignature(PIN, true), viewSignature(PIN, false));
});

// The dropdown that assigns the orchestrator lives in the head at all times,
// pinned or not, so a session joining or leaving the fleet has to reach it.
test("a session joining the fleet changes the signature even while pinned, for the dropdown's sake", () => {
  const bigger = structuredClone(PIN);
  bigger.sessions.push({ short: "new", name: "newcomer", sessionId: "u-9" });
  assert.notEqual(viewSignature(PIN, true), viewSignature(bigger, true));
});

// --- mounting a column ---

globalThis.navigator ??= { language: "en" };
globalThis.location = { protocol: "http:", host: "127.0.0.1:7777" };
// The terminal follows its pane through a ResizeObserver when there is one;
// what it does with one is pinned in web/tests/session.test.js. Here the pane
// never moves, except in the one case that installs an observer of its own.
delete globalThis.ResizeObserver;

// Every request the column makes, in order, and what answers it: a case puts a
// handler under a part of the URL; the terminal's token is always given.
let requests = [];
let routes = {};

globalThis.fetch = async (url, init = {}) => {
  requests.push({ url: String(url), init });
  for (const [part, handler] of Object.entries(routes)) {
    if (String(url).includes(part)) return handler(url, init);
  }
  if (String(url).startsWith("/api/terminal-token")) return answer({ body: { token: "token-for-this-test" } });
  throw new Error(`unexpected request ${url}`);
};

const saved = (status = 204) => answer({ status, body: {} });

// store.js is a module singleton, so its one socket serves this whole file: it
// is opened by the first mount, and every case pushes its frames through it.
let storeSocket = null;
let current = null;

// Mount a column and give it its first snapshot. The snapshot goes into the
// store BEFORE the column subscribes, so the column's first draw is of this
// case's fleet and not of whatever the previous case left in the store.
async function column(first, { pane = "own", observe = false, links = null } = {}) {
  const dom = installDOM();
  const sockets = installSocket();
  const terminals = installTerminal();
  installFit(pane);
  const observers = observe ? installObserver() : [];
  const timers = fakeTimers();
  requests = [];
  routes = {};
  const store = await import("../store.js");
  const { renderOrchestrator } = await import("../orchestrator.js");
  store.connect();
  storeSocket ??= sockets.find((s) => s.url.endsWith("/ws"));
  storeSocket.serverSend(JSON.stringify(first));

  // In a <main> of its own, as it is in the page: the column puts its resize
  // edge NEXT to itself, and a column with no parent has nowhere to put one.
  const main = dom.element("main");
  const root = dom.element("section");
  main.appendChild(root);
  const stop = renderOrchestrator(root, { timers, links });
  await settle();
  await settle();

  current = {
    root,
    dom,
    timers,
    terminals,
    observers,
    stop,
    // The column's own terminal sockets, in the order it opened them.
    ptys: () => sockets.filter((s) => s.url.includes("/pty")),
    push: async (snapshot) => {
      storeSocket.serverSend(JSON.stringify(snapshot));
      await settle();
      await settle();
    },
  };
  return current;
}

// Every column is put away after its case, or it would keep listening to the
// shared store and open terminals for the next case's snapshots.
afterEach(() => {
  delete globalThis.ResizeObserver;
  if (!current) return;
  current.stop();
  current.dom.restore();
  current = null;
});

const text = (c, selector) => c.root.querySelector(selector)?.textContent ?? "";

// --- the gate, in the column ---

test("a snapshot that changes nothing does not run the render at all", async () => {
  const c = await column(structuredClone(PIN));
  const before = c.root.queries;

  await c.push(structuredClone(PIN));
  await c.push(structuredClone(PIN));
  await c.push(structuredClone(PIN));

  // The render cannot help but query its own root; counting those is how a
  // render that did not happen is told apart from one that happened and
  // changed nothing.
  assert.equal(c.root.queries, before, "a snapshot about other sessions must not reach this column's render");
  assert.equal(c.ptys().length, 1, "and must not open the terminal again");
});

test("a snapshot that does change something runs the render", async () => {
  const c = await column(structuredClone(PIN));
  const before = c.root.queries;

  const moved = structuredClone(PIN);
  moved.sessions[0].context.tokens = 90;
  await c.push(moved);

  assert.ok(c.root.queries > before, "the gate must not swallow a change this column shows");
});

// --- the terminal ---

test("the column shows the pinned session's own live terminal", async () => {
  const c = await column(structuredClone(PIN));

  const ptys = c.ptys();
  assert.equal(ptys.length, 1, "no terminal was opened for the pinned session");
  assert.match(ptys[0].url, /\/api\/sessions\/abc\/pty\?/, "attached by the pin's short id");
  const host = c.root.querySelector(".o-term");
  assert.ok(host, "the terminal has no place in the column");
  assert.equal(c.terminals.at(-1).host, host, "the terminal was drawn somewhere other than the column");
  // Escape inside it belongs to the session, where it interrupts a turn; the
  // card panel lets a key from an element marked this way pass.
  assert.equal(host.dataset.terminal, "", "the terminal's element is not marked as one");
});

test("the feed and its box are gone: nothing is polled, nothing is typed into a box", async () => {
  const c = await column(structuredClone(PIN));
  await c.timers.tick();
  await c.timers.tick();

  assert.equal(c.root.querySelector("textarea"), null, "the column still has a box of its own to type into");
  assert.equal(c.root.querySelector(".o-thread"), null, "the column still draws a feed");
  assert.deepEqual(requests.filter((r) => r.url.includes("/digest")), [], "the column still polls the transcript");
});

test("what is typed into the column's terminal goes into the pinned session", async () => {
  const c = await column(structuredClone(PIN));
  ready(c.ptys()[0]);

  c.terminals.at(-1).type("y");

  const typed = c.ptys()[0].sent.filter((d) => typeof d !== "string");
  assert.equal(typed.length, 1);
  assert.equal(asText(typed[0]), "y");
});

// The column has no tab to reopen. A panel that restarts under it, or a
// connection that drops, would otherwise leave it on "connection lost" until
// someone reloads the page.
test("the column's terminal comes back by itself when its connection is lost", async () => {
  const c = await column(structuredClone(PIN));
  ready(c.ptys()[0]);

  c.ptys()[0].serverClose(1006);
  assert.ok(text(c, ".o-error-stream").includes(t("terminal_reconnecting")), "the column does not say it is coming back");

  await c.timers.tick();
  assert.equal(c.ptys().length, 2, "the column did not try again");
  assert.match(c.ptys()[1].url, /\/api\/sessions\/abc\/pty\?/);
  ready(c.ptys()[1]);
  assert.equal(c.root.querySelector(".o-error-stream"), null, "the loss is still reported once the terminal is back");
});

test("a snapshot that only moves the head leaves the terminal alone", async () => {
  const c = await column(structuredClone(PIN));
  const terminal = c.terminals.at(-1);

  const moved = structuredClone(PIN);
  moved.sessions[0].context.tokens = 90;
  await c.push(moved);

  assert.equal(text(c, ".o-ctx"), "90%", "control: the column did redraw");
  assert.equal(c.ptys().length, 1, "a redraw opened the terminal again");
  assert.equal(terminal.disposed, 0, "a redraw threw the terminal away");
});

test("moving the pin moves the terminal to the session pinned now", async () => {
  const c = await column(structuredClone(PIN));
  const first = c.terminals.at(-1);

  await c.push({ ...structuredClone(PIN), orchestratorSession: "zzz" });

  assert.equal(c.ptys()[0].closedWith?.code, 1000, "the previous session's terminal was left attached");
  assert.equal(first.disposed, 1, "and its terminal left drawn");
  assert.equal(c.ptys().length, 2);
  assert.match(c.ptys()[1].url, /\/api\/sessions\/zzz\/pty\?/, "the new pin got no terminal");
});

test("a pinned session the daemon stops listing lets its terminal go, and has it back once listed again", async () => {
  const c = await column(structuredClone(PIN));

  await c.push({ orchestratorSession: "abc", sessions: [structuredClone(PIN.sessions[1])] });
  assert.equal(c.ptys()[0].closedWith?.code, 1000, "a session that is not listed kept its terminal");
  assert.equal(c.root.querySelector(".o-term"), null);
  assert.equal(text(c, ".o-screen-empty"), t("session_not_listed"), "the column does not say why it is empty");

  await c.push(structuredClone(PIN));
  assert.equal(c.ptys().length, 2, "the session came back and its terminal did not");
  assert.match(c.ptys()[1].url, /\/api\/sessions\/abc\/pty\?/);
  assert.equal(c.root.querySelector(".o-screen-empty"), null, "the sentence stayed next to the terminal");
});

test("with nothing pinned there is no terminal, and the column says so — never a list of sessions to browse", async () => {
  const c = await column({ orchestratorSession: "", sessions: structuredClone(PIN.sessions) });

  assert.equal(c.ptys().length, 0, "a terminal was opened with nothing pinned");
  assert.equal(text(c, ".o-screen-empty"), t("no_orchestrator_thread"));
  // The only session-shaped things anywhere in the column are the dropdown's
  // own <option>s — nothing clickable, nothing that opens another view.
  assert.equal(c.root.querySelectorAll("button[data-short], a[data-short]").length, 0);
});

// A folded column is not where the session is read, and what it attached with
// would hold the session at a size nobody is looking at.
test("folding the column lets its terminal go, and unfolding attaches again", async () => {
  const c = await column(structuredClone(PIN));
  const first = c.terminals.at(-1);

  fireEvent(c.root.querySelector(".col-size-fold"), "click");
  await settle();
  assert.equal(c.ptys()[0].closedWith?.code, 1000, "a folded column kept its terminal attached");
  assert.equal(first.disposed, 1);

  fireEvent(c.root.querySelector(".col-size-unfold"), "click");
  await settle();
  assert.equal(c.ptys().length, 2, "unfolding did not bring the terminal back");
  assert.match(c.ptys()[1].url, /\/api\/sessions\/abc\/pty\?/);
});

// Two rows because they are cleared by different things: the terminal clears
// its own once it is back, the operator's next action clears the other. One row
// for both let each erase the other.
test("a connection being retried and a choice that was not saved are on screen together, each in its own row", async () => {
  const c = await column(structuredClone(PIN));
  ready(c.ptys()[0]);
  c.ptys()[0].serverClose(1006);
  routes["/api/config"] = () => answer({ status: 503, statusText: "config is read-only", body: { error: "config is read-only" } });

  const select = c.root.querySelector(".o-pick-select");
  select.value = "zzz";
  fireEvent(select, "change");
  await settle();
  await settle();

  assert.ok(text(c, ".o-error-stream").includes(t("terminal_reconnecting")), "the retry lost its row");
  assert.ok(text(c, ".o-error-action").includes(t("orchestrator_pin_failed")), "the failed choice lost its row");
  assert.equal(c.root.querySelectorAll(".o-error").length, 2);
});

// The pin means this session has been given the fleet's working order. The
// dropdown in this column's own head moves the pin and writes nothing, and
// the file can be deleted after a real appointment wrote it — so the panel
// can come to claim an orchestrator that has nothing to work from. It used to
// claim it in silence, which is the whole of what these pin.

test("a pin whose working order is not on disk says so, and names the file", () => {
  const said = briefWarning({
    orchestratorSession: "abc",
    orchestratorBriefMissing: true,
    orchestratorBriefPath: "/board/docs/orchestrator.md",
  });
  assert.ok(said.includes(t("orchestrator_brief_missing")), "the row does not say what is wrong");
  assert.ok(said.includes("/board/docs/orchestrator.md"), "the row does not say which file, so there is nowhere to look");
});

test("a brief that is on disk says nothing, and neither does a snapshot that has not arrived", () => {
  assert.equal(briefWarning({ orchestratorSession: "abc", orchestratorBriefPath: "/board/docs/orchestrator.md" }), "");
  assert.equal(briefWarning({}), "");
  assert.equal(briefWarning(null), "");
});

// Nothing pinned is not this failure, and the panel must not say it is: the
// row would then stand on every fresh panel, and a warning that is always
// there is one nobody reads when it finally means something. The collector
// never sets the flag without a pin (cmd/fleetdeck's briefState); this pins
// that the column does not invent it either.
test("nothing pinned raises no working-order row", () => {
  assert.equal(briefWarning({ orchestratorSession: "", orchestratorBriefPath: "/board/docs/orchestrator.md" }), "");
});

test("the missing working order is on screen in its own row, above the connection", async () => {
  const c = await column({ ...structuredClone(PIN), orchestratorBriefMissing: true, orchestratorBriefPath: "/board/docs/orchestrator.md" });

  const row = c.root.querySelector(".o-error-brief");
  assert.ok(row, "a pinned orchestrator with no working order on disk is shown as nothing at all");
  assert.ok(row.textContent.includes("/board/docs/orchestrator.md"), "the row is on screen without the file it is about");

  ready(c.ptys()[0]);
  c.ptys()[0].serverClose(1006);
  await settle();
  const rows = [...c.root.querySelectorAll(".o-error")];
  assert.equal(rows.length, 2, "the two states did not each keep a row");
  assert.ok(rows[0].classList.contains("o-error-brief"), "the connection retrying pushed the standing failure below itself");
});

test("the row goes away on the cycle after the working order is written, without a reload", async () => {
  const c = await column({ ...structuredClone(PIN), orchestratorBriefMissing: true, orchestratorBriefPath: "/board/docs/orchestrator.md" });
  assert.ok(c.root.querySelector(".o-error-brief"), "the row was never there to begin with");

  await c.push({ ...structuredClone(PIN), orchestratorBriefPath: "/board/docs/orchestrator.md" });
  assert.equal(c.root.querySelector(".o-error-brief"), null, "the file is there and the row still says it is not");
});

// A file at the brief's path that fleetdeck did not write is the quieter
// sibling of a missing one: an orchestrator notices an absent brief when it
// goes to read it, but a foreign one it reads and works by. The wizard
// refuses to replace such a file, so telling the operator to run the wizard
// would send them straight into that refusal — the row has to say what is in
// the way.

const FOREIGN = {
  ...structuredClone(PIN),
  orchestratorBriefForeign: true,
  orchestratorBriefPath: "/board/docs/orchestrator.md",
};

test("a pin beside a file fleetdeck did not write says so, names the file, and does not call it missing", () => {
  const said = briefWarning(FOREIGN);
  assert.ok(said.includes(t("orchestrator_brief_foreign")), "the row does not say the file is not fleetdeck's");
  assert.ok(said.includes("/board/docs/orchestrator.md"), "the row does not say which file is in the way");
  assert.ok(!said.includes(t("orchestrator_brief_missing")), "a file that is on disk is called missing");
});

test("a foreign brief with nothing pinned raises no row", () => {
  assert.equal(briefWarning({ ...FOREIGN, orchestratorSession: "", orchestratorBriefForeign: false }), "");
});

test("the foreign working order is on screen in the working-order row", async () => {
  const c = await column(structuredClone(FOREIGN));
  const row = c.root.querySelector(".o-error-brief");
  assert.ok(row, "a pinned orchestrator beside a foreign brief is shown as nothing at all");
  assert.ok(row.textContent.includes(t("orchestrator_brief_foreign")), "the row is there but does not say what is wrong");
});

test("the foreign-brief row goes away on the cycle after the wizard writes its own, without a reload", async () => {
  const c = await column(structuredClone(FOREIGN));
  assert.ok(c.root.querySelector(".o-error-brief"), "the row was never there to begin with");

  await c.push({ ...structuredClone(PIN), orchestratorBriefPath: "/board/docs/orchestrator.md" });
  assert.equal(c.root.querySelector(".o-error-brief"), null, "the brief is fleetdeck's now and the row still says it is not");
});

// Missing and foreign are both a row in the same place, so a signature that
// only knew "there is a row" would keep the first text on screen when the
// state turned into the other one — a file deleted, a person's file put in
// its place, and the column still saying "run the wizard".
test("the row follows a missing brief turning into a foreign one", async () => {
  const c = await column({ ...structuredClone(PIN), orchestratorBriefMissing: true, orchestratorBriefPath: "/board/docs/orchestrator.md" });
  assert.ok(c.root.querySelector(".o-error-brief").textContent.includes(t("orchestrator_brief_missing")));

  await c.push(structuredClone(FOREIGN));
  const rows = c.root.querySelectorAll(".o-error-brief");
  assert.equal(rows.length, 1, "the two states each put up a row of their own");
  assert.ok(rows[0].textContent.includes(t("orchestrator_brief_foreign")), "the row still says what was true a cycle ago");
});

test("the view signature tells a missing brief, a foreign one and a sound one apart", () => {
  const sound = { ...structuredClone(PIN), orchestratorBriefPath: "/board/docs/orchestrator.md" };
  const missing = { ...sound, orchestratorBriefMissing: true };
  const signatures = new Set([sound, missing, FOREIGN].map((s) => viewSignature(s, true)));
  assert.equal(signatures.size, 3, "two of the three brief states redraw as the same column");
});

// Requirement 5 of the first-run wizard's card: the wizard can be run again,
// and the column the orchestrator lives in is where a person looks for it —
// pinned or not, since "nothing pinned" is exactly when it is wanted.
test("the column's head leads to the orchestrator wizard, pinned or not", async () => {
  for (const snapshot of [structuredClone(PIN), { ...structuredClone(PIN), orchestratorSession: "" }]) {
    const c = await column(snapshot);
    const link = c.root.querySelector("a.o-wizard");
    assert.ok(link, "no way to the wizard from the column");
    assert.equal(link.getAttribute("href"), "/setup.html");
    assert.equal(link.textContent, t("orchestrator_wizard"));
    assert.equal(link.getAttribute("title"), t("orchestrator_wizard_hint"));
    current.stop();
    current.dom.restore();
    current = null;
  }
});

test("in a fleet's tab the wizard is that fleet's", async () => {
  // The wizard writes a working order into a fleet's documentation and pins
  // that fleet's orchestrator; opened from fleet B's tab it must be B's.
  globalThis.location.search = "?fleet=B";
  try {
    const c = await column(structuredClone(PIN));
    assert.equal(c.root.querySelector("a.o-wizard").getAttribute("href"), "/setup.html?fleet=B");
    current.stop();
    current.dom.restore();
    current = null;
  } finally {
    delete globalThis.location.search;
  }
});

test("a connection error goes with the terminal it was about", async () => {
  const c = await column(structuredClone(PIN));
  ready(c.ptys()[0]);
  c.ptys()[0].serverClose(1006);
  assert.ok(c.root.querySelector(".o-error-stream"), "precondition: the loss is on screen");

  await c.push({ ...structuredClone(PIN), orchestratorSession: "" });

  assert.equal(c.root.querySelector(".o-error-stream"), null, "a sentence about a connection nobody holds any more");
});

test("a terminal that cannot type says so, in the quiet row rather than the red one", async () => {
  const c = await column(structuredClone(PIN));

  ready(c.ptys()[0], false);

  assert.equal(text(c, ".o-notice"), t("terminal_read_only"));
  assert.equal(c.root.querySelector(".o-error"), null, "a standing fact was painted as a failure");
});

// A pane that stops being measurable later — not at open, where the column
// repaints its rows anyway — reaches the column through nothing but the
// terminal's own report. Without it the notice would appear only on the next
// unrelated repaint.
test("a column the terminal can no longer measure says so when it happens", async () => {
  const pane = { cols: 80, rows: 24, hidden: false };
  const c = await column(structuredClone(PIN), { pane, observe: true });
  assert.equal(c.root.querySelector(".o-notice"), null, "precondition: the pane was measured");

  pane.hidden = true;
  c.observers.at(-1).resize();
  await c.timers.tick();

  assert.equal(text(c, ".o-notice"), t("terminal_not_fitted"));
});

test("a key the terminal could not send is the operator's error, not the connection's", async () => {
  const c = await column(structuredClone(PIN));

  // Typed before the bridge has attached.
  c.terminals.at(-1).type("y");

  assert.equal(text(c, ".o-error-action"), t("terminal_not_connected"));
  assert.equal(c.root.querySelector(".o-error-stream"), null, "a refused key was reported as the connection's state");
});

test("the empty column's sentence follows the pin as it changes", async () => {
  const c = await column({ orchestratorSession: "abc", sessions: [] });
  assert.equal(text(c, ".o-screen-empty"), t("session_not_listed"), "precondition");

  await c.push({ orchestratorSession: "", sessions: [] });

  assert.equal(text(c, ".o-screen-empty"), t("no_orchestrator_thread"), "the sentence about the old pin stayed");
});

test("the column's terminal is given the page's links", async () => {
  const links = { resolve: () => null, open: () => {} };
  const c = await column(structuredClone(PIN), { links });

  assert.equal(c.terminals.at(-1).linkProviders?.length, 1, "the column's terminal links nothing");
});

test("putting the column away leaves no socket and nothing armed", async () => {
  const c = await column(structuredClone(PIN));
  ready(c.ptys()[0]);
  c.ptys()[0].serverClose(1006);
  assert.equal(c.timers.count(), 1, "precondition: a retry is armed");

  c.stop();

  assert.equal(c.timers.count(), 0, "the retry outlived the column");
  await c.push({ ...structuredClone(PIN), orchestratorSession: "zzz" });
  assert.equal(c.ptys().length, 1, "a column put away still answered a snapshot");
});

// --- assigning the orchestrator through the dropdown, not a screen of its own ---
//
// The operator's own complaint: a full-screen session picker used to replace
// this whole column, offering a second, confusable way to do what clicking a
// session in the task list on the right already does. It is gone; a plain
// <select> in the head does the one thing that is not already available
// elsewhere — saying which session the orchestrator is — without ever
// leaving the column.

test("choosing the empty option in the dropdown clears the pin", async () => {
  const c = await column(structuredClone(PIN));
  const select = c.root.querySelector(".o-pick-select");
  assert.ok(select, "an operator must not be locked into the session they picked");
  assert.equal(select.value, "abc", "the dropdown starts on the true pin");

  const patches = [];
  routes["/api/config"] = (url, init) => {
    patches.push(JSON.parse(init.body));
    return saved();
  };

  select.value = "";
  fireEvent(select, "change");
  await settle();
  await settle();

  assert.deepEqual(patches, [{ orchestratorSession: "" }], "the pin is cleared, so a reload does not lock them in again");
  assert.ok(c.root.querySelector(".o-screen"), "the column never left its own view to do it");
});

test("when the write fails, the dropdown reverts to what is actually pinned and says so", async () => {
  const c = await column(structuredClone(PIN));
  const select = c.root.querySelector(".o-pick-select");
  routes["/api/config"] = () => answer({ status: 503, statusText: "config is read-only", body: { error: "config is read-only" } });

  select.value = "";
  fireEvent(select, "change");
  await settle();
  await settle();

  assert.ok(c.root.querySelector(".o-error-action"), "the failure to save it is not hidden");
  // The configuration never actually changed, so the very next draw() reads
  // the true pin back and the dropdown reverts to it — no separate "still
  // really pinned" mark to maintain, the selected option already is the
  // truth.
  assert.equal(select.value, "abc", "the dropdown shows what is actually pinned, not what was merely clicked");

  // The next snapshot still carries the old pin; it must not read as a
  // change and must not disturb the terminal.
  await c.push(structuredClone(PIN));
  assert.equal(select.value, "abc");
  assert.equal(c.ptys().length, 1, "a choice that was not saved moved the terminal anyway");
});

test("choosing a session in the dropdown pins it, without leaving the column", async () => {
  const c = await column({ orchestratorSession: "", sessions: structuredClone(PIN.sessions) });
  let pinnedTo = null;
  routes["/api/config"] = (url, init) => {
    pinnedTo = JSON.parse(init.body).orchestratorSession;
    return saved();
  };

  const select = c.root.querySelector(".o-pick-select");
  assert.ok(select, "the dropdown is offered even with nothing pinned yet");
  const options = [...select.options].map((o) => o.value);
  assert.ok(options.includes("abc") && options.includes("zzz"), "it offers the fleet's own sessions");

  select.value = "abc";
  fireEvent(select, "change");
  await settle();
  await settle();

  assert.equal(pinnedTo, "abc", "picking pins that session");
  assert.ok(c.root.querySelector(".o-screen"), "the column was never replaced by a separate screen to do it");
});

test("a session named as markup reaches the dropdown as text, not as an element", async () => {
  // The fleet is open (spec 3.1): a session name is not this codebase's text.
  // The dropdown's options are built with createElement and textContent, so a
  // name spelled as a tag is a label that reads like a tag — there is no
  // parser between it and the DOM.
  const hostile = {
    orchestratorSession: "",
    sessions: [{ short: 'q"><img src=x onerror=alert(1)>', name: "<script>alert(1)</script>", sessionId: "u-1" }],
  };
  const c = await column(hostile);
  const select = c.root.querySelector(".o-pick-select");
  const option = [...select.options].find((o) => o.value === 'q"><img src=x onerror=alert(1)>');
  assert.ok(option, "the session is offered");
  assert.equal(option.textContent, "<script>alert(1)</script>", "the name is the label, verbatim and inert");
  assert.equal(option.children.length, 0, "no element was created from it");
});

test("a hostile label reaches .o-name and the dropdown only as text", async () => {
  const hostile = 'q"><img src=x onerror=alert(1)>';
  const withLabel = structuredClone(PIN);
  withLabel.sessions[0].label = hostile;
  const c = await column(withLabel);
  const name = c.root.querySelector(".o-name");
  assert.equal(name.textContent, hostile, "the label wins over the name, verbatim and inert");
  assert.equal(name.children.length, 0, "no element was created from it");

  const select = c.root.querySelector(".o-pick-select");
  const option = [...select.options].find((o) => o.value === "abc");
  assert.equal(option.textContent, hostile, "the dropdown prefers the same label");
  assert.equal(option.children.length, 0);
});

// --- editing the pinned session's own name in place ---

test("the edit button is always present and never only a hover affordance", async () => {
  const c = await column(structuredClone(PIN));
  const editBtn = c.root.querySelector(".o-name-edit");
  assert.ok(editBtn, "a control nobody can find by hovering does not exist for a first-time viewer");
  assert.equal(editBtn.tagName, "BUTTON", "a real control, not a span styled to look like one");
});

test("Enter saves the typed label", async () => {
  const c = await column(structuredClone(PIN));
  const patches = [];
  routes["/label"] = (url, init) => {
    patches.push({ url: String(url), body: JSON.parse(init.body) });
    return saved();
  };

  fireEvent(c.root.querySelector(".o-name-edit"), "click");
  await settle();
  const input = c.root.querySelector(".o-name-input");
  assert.ok(input, "the name became an editable field");
  assert.equal(input.value, "", "an unset label starts the field empty, never pre-filled with the fallback");
  assert.equal(input.placeholder, "orchestrator", "the fallback is offered as a placeholder instead");

  input.value = "my own name for it";
  fireEvent(input, "keydown", { key: "Enter" });
  await settle();
  await settle();

  assert.deepEqual(patches, [{ url: "/api/sessions/u-1/label", body: { label: "my own name for it" } }]);
  assert.equal(c.root.querySelector(".o-name-input"), null, "the field is gone once saved");
  assert.equal(text(c, ".o-name"), "my own name for it");
});

test("losing focus saves too, the same as Enter", async () => {
  const c = await column(structuredClone(PIN));
  const patches = [];
  routes["/label"] = (url, init) => {
    patches.push(JSON.parse(init.body));
    return saved();
  };

  fireEvent(c.root.querySelector(".o-name-edit"), "click");
  await settle();
  const input = c.root.querySelector(".o-name-input");
  input.value = "typed then clicked away";
  fireEvent(input, "blur");
  await settle();
  await settle();

  assert.deepEqual(patches, [{ label: "typed then clicked away" }], "a click away must not silently lose the edit");
  assert.equal(text(c, ".o-name"), "typed then clicked away");
});

test("Escape discards the edit and never calls the write route", async () => {
  const c = await column(structuredClone(PIN));
  let called = false;
  routes["/label"] = () => {
    called = true;
    return saved();
  };

  fireEvent(c.root.querySelector(".o-name-edit"), "click");
  await settle();
  const input = c.root.querySelector(".o-name-input");
  input.value = "this must never be saved";
  fireEvent(input, "keydown", { key: "Escape" });
  await settle();

  assert.equal(called, false, "Esc must discard, not save");
  assert.equal(c.root.querySelector(".o-name-input"), null);
  assert.equal(text(c, ".o-name"), "orchestrator", "the previous display is restored, unchanged");
});

test("an empty label resets the display to name, then to short id", async () => {
  const c = await column(structuredClone(PIN));
  routes["/label"] = () => saved();

  fireEvent(c.root.querySelector(".o-name-edit"), "click");
  await settle();
  fireEvent(c.root.querySelector(".o-name-input"), "keydown", { key: "Enter" });
  await settle();
  await settle();

  // Nothing was typed, so this is the empty-string reset — and the session's
  // own name is what the fallback chain shows next, not a blank space.
  assert.equal(text(c, ".o-name"), "orchestrator");
});

test("editing is disabled for a pinned session the daemon no longer lists", async () => {
  const c = await column({ orchestratorSession: "abc", sessions: [] });
  const editBtn = c.root.querySelector(".o-name-edit");
  assert.ok(editBtn, "the button still exists");
  assert.equal(editBtn.disabled, true, "there is no sessionId to write a label against");
});

// --- nothing pinned yet ---

test("with nothing pinned, the header names nothing rather than showing a blank", async () => {
  const c = await column({ orchestratorSession: "", sessions: structuredClone(PIN.sessions) });
  assert.notEqual(text(c, ".o-name"), "", "a blank name reads as a bug, not as an unmade choice");
});

test("with nothing pinned, there is nothing to label", async () => {
  const c = await column({ orchestratorSession: "", sessions: structuredClone(PIN.sessions) });
  assert.equal(c.root.querySelector(".o-name-edit").disabled, true, "there is no session to label");
});

test("a session pinned but not currently listed reads differently from nothing pinned at all", async () => {
  const notListed = await column({ orchestratorSession: "abc", sessions: [] });
  const notListedText = text(notListed, ".o-screen-empty");
  notListed.stop();
  notListed.dom.restore();
  current = null;

  const nothingPinned = await column({ orchestratorSession: "", sessions: structuredClone(PIN.sessions) });
  const nothingPinnedText = text(nothingPinned, ".o-screen-empty");

  assert.ok(notListedText && nothingPinnedText, "control: both say something");
  assert.notEqual(notListedText, nothingPinnedText, "these are two different facts and must not read as the same message");
});

// --- sizing the column ------------------------------------------------------
//
// The ladder and the remembering are pinned in web/tests/columnwidth.test.js.
// What is pinned here is the wiring: that the controls exist where a person can
// reach them, that they move the column, and above all that a folded column
// still shows the one control that brings it back.

const sizeButton = (c, what) => c.root.querySelector(`.col-size-${what}`);

test("the column carries its remembered width from the first paint, not the first click", async () => {
  const c = await column(structuredClone(PIN));

  // Break it by applying the width only when it changes and this test fails
  // with nothing set: a reload would show the default and then jump.
  assert.equal(c.root.style.getPropertyValue("--col-width"), "25%");
});

// The orchestrator column sits at the window's LEFT edge — its controls
// belong on its own right (toward the centre), and its fold arrow points
// left (away, toward the window's own edge) while its unfold arrow points
// right (back, toward the centre). This is the side the mechanism has
// always used; a session-list sibling test pins the mirrored case, and the
// two together are what a bare existence check cannot tell apart.
test("the column's controls sit on its left-hand side, with arrows pointing the left column's own way", async () => {
  const c = await column(structuredClone(PIN));

  assert.match(String(sizeButton(c, "fold")?.parentNode?.className), /\bcol-size-left\b/, "the strip is not marked as a left column's");
  assert.equal(sizeButton(c, "fold").textContent, "«", "folding away must point toward this column's own edge, not the centre");
  assert.equal(sizeButton(c, "unfold").textContent, "»", "coming back must point toward the centre");
});

// The storage keys are literal strings, not the exported constant, on
// purpose: an operator who resized this column before columnresize.js
// existed has these exact entries sitting in a real browser's storage right
// now, and this proves the column this task rebuilt still reads them —
// not merely that ORCHESTRATOR_KEYS in columnwidth.js still says what it
// always said. Break the rename (a stray "fleetdeck-orchestrator-width-pct"
// missed somewhere, or a column that reads the right key but paints the
// wrong CSS variable) and this fails with the operator's chosen width
// silently reset to the default, read back as "it did that on its own".
test("a width and fold chosen before this task rebuilt the column come back exactly the same", async () => {
  const previous = Object.hasOwn(globalThis, "localStorage") ? globalThis.localStorage : undefined;
  const map = new Map([
    ["fleetdeck-orchestrator-width-pct", "37"],
    ["fleetdeck-orchestrator-folded", "1"],
  ]);
  globalThis.localStorage = {
    getItem: (k) => (map.has(k) ? map.get(k) : null),
    setItem: (k, v) => map.set(k, String(v)),
    removeItem: (k) => map.delete(k),
  };

  try {
    const c = await column(structuredClone(PIN));
    assert.equal(c.root.style.getPropertyValue("--col-width"), "37%", "the operator's chosen width was not carried over");
    assert.equal(c.root.dataset.folded, "1", "the operator's folded column came back open");
    // The grip only hides itself once it has been told to — the same fact
    // "the edge goes away while the column is folded" pins for a fold done
    // through the UI; this is that fact for a fold read from storage.
    assert.equal(grip(c).hidden, true, "a column that was already folded showed an edge to drag it wider with");
  } finally {
    if (previous === undefined) delete globalThis.localStorage;
    else globalThis.localStorage = previous;
  }
});

// The buttons that used to widen and narrow are gone: the operator looked at
// them and asked for the edge instead. What is pinned here is that the edge
// exists, that it is put where a pointer can reach it, and that it says what it
// is — the dragging arithmetic and whether a person can SEE it are answered by
// a browser, in the acceptance run, because neither is a thing this stand-in
// can be asked.
const grip = (c) => c.root.parentElement?.children.find((n) => String(n.className).includes("col-grip"));

test("the column has an edge to drag, next to it rather than inside it", async () => {
  const c = await column(structuredClone(PIN));

  const handle = grip(c);
  assert.ok(handle, "no resize handle was created at all");
  // Inside the column it would scroll away with the content: .col carries
  // overflow: auto. Break it by appending to `root` and this fails.
  assert.equal(handle.parentNode, c.root.parentElement, "the handle was put inside the column");
  assert.notEqual(handle.attributes["aria-label"], undefined, "the handle does not say what it is");
});

test("no buttons are left offering to do what the edge does", async () => {
  const c = await column(structuredClone(PIN));

  assert.equal(sizeButton(c, "widen"), null, "the widen button is back");
  assert.equal(sizeButton(c, "narrow"), null, "the narrow button is back");
});

// The one that matters most. A folded column that hides everything hides the
// way back with it, and a person is left with two zones and no idea there were
// three. Break it by hiding the whole column when folded and this test fails.
test("a folded column still carries the control that brings it back", async () => {
  const c = await column(structuredClone(PIN));

  fireEvent(sizeButton(c, "fold"), "click");
  await settle();

  assert.equal(c.root.dataset.folded, "1", "the column was not marked folded");
  const back = sizeButton(c, "unfold");
  assert.ok(back, "the way back is not in the page at all");
  assert.equal(back.disabled, false, "the way back is there but refuses to be pressed");
  assert.notEqual(back.attributes["aria-label"], undefined, "and it does not say what it does");

  // Left unfolded for the cases after this one, which share the stored state.
  fireEvent(sizeButton(c, "unfold"), "click");
  await settle();
});

test("and pressing it gives the column back at the width it had", async () => {
  const c = await column(structuredClone(PIN));
  const chosen = c.root.style.getPropertyValue("--col-width");

  fireEvent(sizeButton(c, "fold"), "click");
  await settle();
  fireEvent(sizeButton(c, "unfold"), "click");
  await settle();

  assert.equal(c.root.dataset.folded, undefined, "the column stayed folded");
  assert.equal(c.root.style.getPropertyValue("--col-width"), chosen, "the width was lost by folding");
});

// A folded column has no edge, and an edge with nothing behind it is a strip a
// person can drag that does nothing. Break it by leaving the grip on screen and
// this fails.
test("the edge goes away while the column is folded, and comes back with it", async () => {
  const c = await column(structuredClone(PIN));
  assert.equal(grip(c).hidden, false, "precondition: the edge is there to begin with");

  fireEvent(sizeButton(c, "fold"), "click");
  await settle();
  assert.equal(grip(c).hidden, true, "a folded column kept an edge that resizes nothing");

  fireEvent(sizeButton(c, "unfold"), "click");
  await settle();
  assert.equal(grip(c).hidden, false, "the edge did not come back with the column");
});

// The column's terminal keeps the size of its type under the column's own key,
// written as a literal for the reason the width test above gives: once an
// operator has chosen a size, this is the entry holding it. The screen tab's
// entry is set too, so reading the wrong one shows.
test("the column's terminal is the size remembered for the column, and Cmd+= changes that one", async () => {
  const previous = Object.hasOwn(globalThis, "localStorage") ? globalThis.localStorage : undefined;
  const map = new Map([
    ["fleetdeck-terminal-font-orchestrator", "16"],
    ["fleetdeck-terminal-font-screen", "10"],
  ]);
  globalThis.localStorage = {
    getItem: (k) => (map.has(k) ? map.get(k) : null),
    setItem: (k, v) => map.set(k, String(v)),
    removeItem: (k) => map.delete(k),
  };

  try {
    const c = await column(structuredClone(PIN));
    const terminal = c.terminals.at(-1);
    assert.equal(terminal.options.fontSize, 16, "the column's terminal did not start at the column's size");

    let prevented = false;
    const passed = terminal.keyHandler({ type: "keydown", key: "=", metaKey: true, preventDefault: () => (prevented = true) });

    assert.equal(passed, false);
    assert.equal(prevented, true);
    assert.equal(map.get("fleetdeck-terminal-font-orchestrator"), "17");
    assert.equal(map.get("fleetdeck-terminal-font-screen"), "10", "the column's key changed the screen tab's size");
  } finally {
    if (previous === undefined) delete globalThis.localStorage;
    else globalThis.localStorage = previous;
  }
});

// --- the font buttons -------------------------------------------------------
//
// A−, the size, A+ in the strip of the column's own controls, on the side the
// fold button is on — the side facing the centre. They press the terminal's
// stepFont, the function Cmd+= goes through, and they are there before the
// terminal is: a row that grows under a terminal makes its pane shorter, and
// that reshapes the session.

async function withFontStorage(entries, body) {
  const previous = Object.hasOwn(globalThis, "localStorage") ? globalThis.localStorage : undefined;
  const map = new Map(Object.entries(entries));
  globalThis.localStorage = {
    getItem: (k) => (map.has(k) ? map.get(k) : null),
    setItem: (k, v) => map.set(k, String(v)),
    removeItem: (k) => map.delete(k),
  };
  try {
    await body(map);
  } finally {
    if (previous === undefined) delete globalThis.localStorage;
    else globalThis.localStorage = previous;
  }
}

// Looked for inside the strip, so buttons that ended up anywhere else in the
// column are not found at all.
const fontButtons = (root) => {
  const strip = root.querySelector(".col-size");
  return {
    group: strip?.querySelector(".term-font") ?? null,
    smaller: strip?.querySelector(".term-font-smaller") ?? null,
    reset: strip?.querySelector(".term-font-reset") ?? null,
    bigger: strip?.querySelector(".term-font-bigger") ?? null,
  };
};

test("the column's font buttons sit in its strip of column controls, right before the fold button", async () => {
  const c = await column(structuredClone(PIN));
  const strip = c.root.querySelector(".col-size");
  const { group } = fontButtons(c.root);

  assert.ok(group, "the column's strip has no font buttons");
  assert.equal(group.parentNode, strip);
  const order = strip.children.map((n) => String(n.className));
  const at = order.findIndex((name) => name.split(" ").includes("term-font"));
  assert.ok(order[at + 1].split(" ").includes("col-size-fold"), `strip order ${JSON.stringify(order)}`);
  for (const b of group.children) assert.ok(String(b.className).split(" ").includes("col-size-btn"), "the buttons do not look like the strip's own");
});

test("the column's buttons show the column's size and press the column's terminal", async () => {
  await withFontStorage({ "fleetdeck-terminal-font-orchestrator": "16", "fleetdeck-terminal-font-screen": "10" }, async (map) => {
    const c = await column(structuredClone(PIN));
    const terminal = c.terminals.at(-1);
    const { reset, bigger, smaller } = fontButtons(c.root);
    assert.equal(reset.textContent, "16 px");

    fireEvent(bigger, "click");

    assert.equal(terminal.options.fontSize, 17, "A+ did not reach the column's terminal");
    assert.equal(map.get("fleetdeck-terminal-font-orchestrator"), "17");
    assert.equal(map.get("fleetdeck-terminal-font-screen"), "10");
    assert.equal(reset.textContent, "17 px", "the buttons kept claiming the old size");

    fireEvent(smaller, "click");
    fireEvent(smaller, "click");
    assert.equal(terminal.options.fontSize, 15);

    fireEvent(reset, "click");
    assert.equal(terminal.options.fontSize, 12);
    assert.equal(reset.disabled, true, "at 12 px there is nothing to put back");
  });
});

test("a size changed by the keys is shown on the column's buttons too", async () => {
  await withFontStorage({}, async () => {
    const c = await column(structuredClone(PIN));
    const terminal = c.terminals.at(-1);

    terminal.keyHandler({ type: "keydown", key: "=", metaKey: true, preventDefault() {} });

    assert.equal(fontButtons(c.root).reset.textContent, "13 px");
  });
});

test("at the end of the range the column's button for that end is off", async () => {
  await withFontStorage({ "fleetdeck-terminal-font-orchestrator": "24" }, async () => {
    const c = await column(structuredClone(PIN));
    const { smaller, bigger } = fontButtons(c.root);
    assert.equal(bigger.disabled, true, "A+ at 24 px looked like it would do something");
    assert.equal(smaller.disabled, false);
  });
  await withFontStorage({ "fleetdeck-terminal-font-orchestrator": "9" }, async () => {
    const c = await column(structuredClone(PIN));
    const { smaller, bigger } = fontButtons(c.root);
    assert.equal(smaller.disabled, true, "A− at 9 px looked like it would do something");
    assert.equal(bigger.disabled, false);
  });
});

test("the column's buttons are there before its terminal is, and the same ones after", async () => {
  const c = await column({ orchestratorSession: "", sessions: PIN.sessions });
  const before = fontButtons(c.root);
  const rowsBefore = c.root.children.length;
  assert.ok(before.group, "with nothing pinned the strip has no font buttons, so they would appear later");
  assert.deepEqual([before.smaller.disabled, before.reset.disabled, before.bigger.disabled], [true, true, true], "buttons with no terminal to size looked usable");

  await c.push(structuredClone(PIN));
  assert.ok(c.terminals.length > 0, "the pin did not open a terminal");

  const after = fontButtons(c.root);
  assert.equal(after.group, before.group, "the buttons were rebuilt when the terminal came");
  assert.equal(c.root.children.length, rowsBefore, "a row was added above the terminal when it came");
  assert.equal(after.reset.disabled, true, "at 12 px there is nothing to put back");
  assert.equal(after.bigger.disabled, false, "the column's terminal came and its buttons stayed off");
});

test("a column whose terminal goes away turns its buttons off", async () => {
  const c = await column(structuredClone(PIN));
  assert.equal(fontButtons(c.root).bigger.disabled, false);

  await c.push({ orchestratorSession: "", sessions: PIN.sessions });

  const { smaller, reset, bigger } = fontButtons(c.root);
  assert.deepEqual([smaller.disabled, reset.disabled, bigger.disabled], [true, true, true]);
});

// Last on purpose: closing the socket leaves the real store in its reconnect
// backoff, and every case in this file shares that one store.
test("the disconnected marker shows even with nothing pinned, once the socket drops", async () => {
  const c = await column({ orchestratorSession: "", sessions: [{ short: "abc", sessionId: "u-1" }] });
  assert.equal(c.root.querySelector(".o-stale"), null, "connected: no marker");

  storeSocket.serverClose(1006);
  await settle();

  assert.ok(c.root.querySelector(".o-stale"), "a dropped socket must never look like a live one");
});
