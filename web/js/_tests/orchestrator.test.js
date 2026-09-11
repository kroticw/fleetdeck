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
import { resolveOrchestrator, contextPercent, pickableSessions, pickerLabel } from "../orchestrator.js";

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
async function column(first, { pane = "own", observe = false } = {}) {
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
  const stop = renderOrchestrator(root, { timers });
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

  fireEvent(c.root.querySelector(".o-size-fold"), "click");
  await settle();
  assert.equal(c.ptys()[0].closedWith?.code, 1000, "a folded column kept its terminal attached");
  assert.equal(first.disposed, 1);

  fireEvent(c.root.querySelector(".o-size-unfold"), "click");
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

const sizeButton = (c, what) => c.root.querySelector(`.o-size-${what}`);

test("the column carries its remembered width from the first paint, not the first click", async () => {
  const c = await column(structuredClone(PIN));

  // Break it by applying the width only when it changes and this test fails
  // with nothing set: a reload would show the default and then jump.
  assert.equal(c.root.style.getPropertyValue("--o-width"), "25%");
});

// The buttons that used to widen and narrow are gone: the operator looked at
// them and asked for the edge instead. What is pinned here is that the edge
// exists, that it is put where a pointer can reach it, and that it says what it
// is — the dragging arithmetic and whether a person can SEE it are answered by
// a browser, in the acceptance run, because neither is a thing this stand-in
// can be asked.
const grip = (c) => c.root.parentElement?.children.find((n) => String(n.className).includes("o-grip"));

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
  const chosen = c.root.style.getPropertyValue("--o-width");

  fireEvent(sizeButton(c, "fold"), "click");
  await settle();
  fireEvent(sizeButton(c, "unfold"), "click");
  await settle();

  assert.equal(c.root.dataset.folded, undefined, "the column stayed folded");
  assert.equal(c.root.style.getPropertyValue("--o-width"), chosen, "the width was lost by folding");
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

// Last on purpose: closing the socket leaves the real store in its reconnect
// backoff, and every case in this file shares that one store.
test("the disconnected marker shows even with nothing pinned, once the socket drops", async () => {
  const c = await column({ orchestratorSession: "", sessions: [{ short: "abc", sessionId: "u-1" }] });
  assert.equal(c.root.querySelector(".o-stale"), null, "connected: no marker");

  storeSocket.serverClose(1006);
  await settle();

  assert.ok(c.root.querySelector(".o-stale"), "a dropped socket must never look like a live one");
});
