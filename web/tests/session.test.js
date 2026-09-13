// The session panel, driven through the same handlers a person's clicks reach.
//
// The panel is one screen: the session's live terminal, the keys that press into
// it, and the list of cards the session has worked on. What is worth pinning is
// wiring rather than any pure function: that the terminal holds exactly one
// socket, that every failure reaches the screen as words, that a key button
// sends bytes rather than the word printed on it — and that the history says
// "none" rather than nothing, and is not asked for again on every snapshot.
//
// What none of it shows is that any of this looks right. No browser runs here.

import { test, beforeEach, afterEach } from "node:test";
import assert from "node:assert/strict";

import { installDOM, fireEvent, settle } from "./fake-dom.js";
import {
  fakeTimers,
  answer,
  installTerminal,
  installFit,
  installObserver,
  installSocket,
  ready,
  asText,
  frame,
} from "./terminal-fakes.js";
import { KEYS, renderSession } from "../js/session.js";
// The panel's own dictionary, not a copy of its strings: what is pinned below is
// that the label the operator reads comes from a key that exists, in whichever
// language this machine runs in.
import { t } from "../js/i18n.js";

const SHORT = "sess-1";
const FULL = "sess-1-4f2c-11ee-9d3a-0242ac120002";

let dom;
let calls;
let realFetch;
let realTerminal;

// The terminal token the panel reads before every socket it opens. Those reads
// are kept out of `calls`, which the tests about the panel's own requests count,
// and answered with TOKEN unless a test says otherwise.
const TOKEN = "token-for-this-test-panel";
let tokenCalls = [];
let tokenAnswer = () => answer({ body: { token: TOKEN } });

function stubFetch(respond) {
  globalThis.fetch = async (url, init) => {
    if (String(url).startsWith("/api/terminal-token")) {
      tokenCalls.push({ url, init });
      return tokenAnswer();
    }
    calls.push({ url, init });
    return typeof respond === "function" ? respond(url, init) : respond;
  };
}

const cardCalls = () => calls.filter((c) => String(c.url).includes("/cards"));

// What went into a socket as keystrokes. The token goes first, as text; typed
// keys are binary.
const keystrokes = (socket) => socket.sent.filter((data) => typeof data !== "string");

let sockets;
let realSocket;
let realFit;
let realObserver;

beforeEach(() => {
  realObserver = Object.hasOwn(globalThis, "ResizeObserver") ? globalThis.ResizeObserver : undefined;
  dom = installDOM();
  calls = [];
  tokenCalls = [];
  tokenAnswer = () => answer({ body: { token: TOKEN } });
  realFetch = globalThis.fetch;
  realTerminal = Object.hasOwn(globalThis, "Terminal") ? globalThis.Terminal : undefined;
  realSocket = Object.hasOwn(globalThis, "WebSocket") ? globalThis.WebSocket : undefined;
  realFit = Object.hasOwn(globalThis, "FitAddon") ? globalThis.FitAddon : undefined;
  sockets = installSocket();
  installFit();
});

afterEach(() => {
  dom.restore();
  globalThis.fetch = realFetch;
  if (realTerminal === undefined) delete globalThis.Terminal;
  else globalThis.Terminal = realTerminal;
  if (realSocket === undefined) delete globalThis.WebSocket;
  else globalThis.WebSocket = realSocket;
  if (realFit === undefined) delete globalThis.FitAddon;
  else globalThis.FitAddon = realFit;
  if (realObserver === undefined) delete globalThis.ResizeObserver;
  else globalThis.ResizeObserver = realObserver;
});

// The snapshot store, reduced to what the panel touches: a subscriber is called
// at once with what is known, and again on every snapshot after that.
function fakeStore(initial) {
  let snapshot = initial;
  const listeners = new Set();
  return {
    subscribe(fn) {
      listeners.add(fn);
      fn(snapshot, true);
      return () => listeners.delete(fn);
    },
    async emit(next) {
      snapshot = next;
      for (const fn of [...listeners]) fn(snapshot, true);
      await settle();
    },
    listeners: () => listeners.size,
  };
}

const listed = (extra = {}) => ({ sessions: [{ short: SHORT, sessionId: FULL, ...extra }], cards: [] });

async function mount({ store = fakeStore(listed()), short = SHORT, links = null, onOpenCard } = {}) {
  const root = dom.element("div");
  // In the page before the panel draws into it, as it is in a browser: a node
  // outside the document has no layout, and anything measured against it reads
  // zero.
  dom.document.body.appendChild(root);
  const timers = fakeTimers();
  let closed = 0;
  const stop = renderSession(root, short, () => {
    closed += 1;
  }, { timers, subscribe: store.subscribe, links, onOpenCard });
  await settle(); // let the terminal open and the history land

  const errorText = () => {
    const line = root.querySelector(".s-error");
    return line.hidden ? "" : line.textContent;
  };
  const noticeText = () => {
    const line = root.querySelector(".s-notice");
    return line.hidden ? "" : line.textContent;
  };
  return {
    root,
    timers,
    stop,
    store,
    errorText,
    noticeText,
    closes: () => closed,
    async click(selector) {
      fireEvent(root.querySelector(selector), "click");
      await settle();
    },
  };
}

// --- one screen ----------------------------------------------------------------
//
// The panel had two tabs, a digest and a screen, and a box under them. The
// digest was the same conversation the screen shows, rebuilt from the transcript
// and behind it; the box was a second way into the same session that put text
// there differently from typing. One screen is left.

test("the panel opens straight onto the live terminal, with no tabs to switch", async () => {
  const terminals = installTerminal();
  stubFetch(answer({ body: [] }));
  const panel = await mount();

  assert.equal(panel.root.querySelectorAll("[data-tab]").length, 0, "a tab is still drawn");
  assert.equal(panel.root.querySelector(".s-tabs"), null, "a tab row is still drawn");
  assert.equal(terminals.length, 1, "no terminal until something was clicked");
  assert.equal(sockets.length, 1, "the terminal did not connect on opening");
  assert.equal(new URL(sockets[0].url).pathname, `/api/sessions/${SHORT}/pty`);
  assert.equal(calls.filter((c) => String(c.url).includes("/digest")).length, 0, "the transcript digest is still read");
});

test("there is no box to write into: writing goes through the terminal", async () => {
  const terminals = installTerminal();
  stubFetch(answer({ body: [] }));
  const panel = await mount();
  ready(sockets[0]);

  assert.equal(panel.root.querySelector("textarea"), null, "a text box is still drawn");
  assert.equal(panel.root.querySelector(".s-input"), null);
  assert.equal(panel.root.querySelector(".s-form"), null);

  terminals[0].type("run the tests\r");
  assert.equal(asText(keystrokes(sockets[0])[0]), "run the tests\r", "what is typed into the terminal reaches the session");
  assert.equal(calls.filter((c) => String(c.url).includes("/text")).length, 0, "and nothing goes around it");
});

// --- the cards the session worked on ---------------------------------------------
//
// The one thing about a session its own screen cannot show: the path, not the
// present. Which cards it took, in what order, closed ones included.

const HISTORY = [
  { id: "T-003", title: "the first job", stage: "done", created: "2026-09-10", path: "/board/archive/2026-09-10-first.md", archived: true },
  { id: "T-020", title: "the second job", stage: "done", created: "2026-09-11", path: "/board/cards/T-020-second.md", archived: false },
  { id: "T-051", title: "the job at hand", stage: "active", created: "2026-09-13", path: "/board/cards/T-051-at-hand.md", archived: false },
];

const historyAnswer = (body = HISTORY) => (url) => (String(url).includes("/cards") ? answer({ body }) : answer({ body: [] }));

test("the history is asked for under the session's short id", async () => {
  installTerminal();
  stubFetch(historyAnswer());
  await mount();

  assert.equal(cardCalls().length, 1);
  assert.equal(cardCalls()[0].url, `/api/sessions/${SHORT}/cards`);
});

test("the history is asked for in the fleet the tab's address names", async () => {
  installTerminal();
  stubFetch(historyAnswer());
  globalThis.location = { search: "?fleet=B" };
  try {
    await mount();
  } finally {
    delete globalThis.location;
  }
  assert.equal(cardCalls()[0].url, `/api/sessions/${SHORT}/cards?fleet=B`);
});

test("every card the session worked on is listed, in order, closed and archived ones included", async () => {
  installTerminal();
  stubFetch(historyAnswer());
  const panel = await mount();

  const row = panel.root.querySelector(".s-cards");
  assert.notEqual(row, null, "the panel has no history row");
  assert.equal(row.querySelector(".s-cards-label")?.textContent, t("session_cards"));

  const items = row.querySelectorAll(".s-card");
  assert.equal(items.length, HISTORY.length, "a card is missing from the history");
  HISTORY.forEach((card, i) => {
    assert.equal(items[i].dataset.path, card.path, `card ${i} is out of order`);
    assert.ok(items[i].textContent.includes(card.id), `${card.id}: the number is not shown`);
    assert.ok(items[i].textContent.includes(card.title), `${card.id}: the title is not shown`);
    assert.equal(items[i].querySelector(".s-card-stage")?.textContent, card.stage, `${card.id}: the stage is not shown`);
  });
  // Archived is a fact about the card worth seeing: it has left the board, and
  // it cannot be opened from there.
  assert.ok(items[0].className.split(" ").includes("s-card-archived"), "an archived card looks like one on the board");
  assert.ok(items[0].title.includes(t("session_card_archived")));
  assert.ok(!items[1].className.split(" ").includes("s-card-archived"));
});

test("a session that has taken no card says so, rather than showing an empty row", async () => {
  installTerminal();
  stubFetch(historyAnswer([]));
  const panel = await mount();

  const row = panel.root.querySelector(".s-cards");
  assert.equal(row.querySelectorAll(".s-card").length, 0);
  const none = row.querySelector(".s-cards-none");
  assert.notEqual(none, null, "an empty history is drawn as nothing");
  assert.equal(none.textContent, t("session_cards_none"));
  assert.notEqual(none.textContent, "session_cards_none", "the sentence fell through to its own key name");
});

test("a history on its way says so, and is never a blank row", async () => {
  installTerminal();
  const waiting = [];
  stubFetch((url) => (String(url).includes("/cards") ? new Promise((resolve) => waiting.push(resolve)) : answer({ body: [] })));
  const panel = await mount();

  const row = panel.root.querySelector(".s-cards");
  assert.ok(row.textContent.includes(t("session_cards_loading")), `the row while waiting: "${row.textContent}"`);

  waiting[0](answer({ body: [] }));
  await settle();
  assert.equal(row.querySelector(".s-cards-none")?.textContent, t("session_cards_none"));
});

test("a history that cannot be read says so in its own row, and is asked for again", async () => {
  installTerminal();
  let refuse = true;
  stubFetch((url) => {
    if (!String(url).includes("/cards")) return answer({ body: [] });
    return refuse
      ? answer({ status: 503, statusText: "Service Unavailable", body: { error: "this panel is not wired to a board" } })
      : answer({ body: HISTORY });
  });
  const panel = await mount();

  const row = panel.root.querySelector(".s-cards");
  const failure = row.querySelector(".s-cards-error");
  assert.notEqual(failure, null, "a failed history is drawn as nothing");
  assert.ok(failure.textContent.includes(t("session_cards_failed")));
  assert.ok(failure.textContent.includes("not wired to a board"), "and in the server's own words");
  // Not on the error line under the terminal: that line appearing would take
  // height from the terminal and resize somebody's session.
  assert.equal(panel.errorText(), "");

  refuse = false;
  await panel.store.emit(listed());
  assert.equal(cardCalls().length, 2, "a failed history is not tried again");
  assert.equal(row.querySelectorAll(".s-card").length, HISTORY.length);
});

test("the history is asked for again when the session's cards change, and not on every snapshot", async () => {
  installTerminal();
  stubFetch(historyAnswer());
  const panel = await mount();
  assert.equal(cardCalls().length, 1);

  const onBoard = { path: "/board/cards/T-051-at-hand.md", session: SHORT, stage: "active", title: "the job at hand" };
  const other = { path: "/board/cards/T-060-other.md", session: "ffff0000", stage: "active", title: "not this session's" };
  await panel.store.emit({ ...listed(), cards: [onBoard, other] });
  const afterFirst = cardCalls().length;

  // The snapshot arrives every second; the same cards again are not news.
  await panel.store.emit({ ...listed(), cards: [onBoard, other] });
  await panel.store.emit({ ...listed(), cards: [onBoard, { ...other, stage: "done" }] });
  assert.equal(cardCalls().length, afterFirst, "asked again for a snapshot that changed nothing about this session");

  // Closed: the card this session holds changed.
  await panel.store.emit({ ...listed(), cards: [{ ...onBoard, stage: "review" }, other] });
  assert.equal(cardCalls().length, afterFirst + 1, "a card of this session changed and the history was not asked again");

  // Archived: the card left the board.
  await panel.store.emit({ ...listed(), cards: [other] });
  assert.equal(cardCalls().length, afterFirst + 2, "a card left the board and the history was not asked again");
});

test("an answer that arrives after a newer one is not drawn over it", async () => {
  installTerminal();
  const waiting = [];
  stubFetch((url) => (String(url).includes("/cards") ? new Promise((resolve) => waiting.push(resolve)) : answer({ body: [] })));
  const panel = await mount();
  await panel.store.emit({ ...listed(), cards: [{ path: "/board/cards/T-051-at-hand.md", session: SHORT, stage: "active" }] });
  assert.equal(waiting.length, 2);

  waiting[1](answer({ body: HISTORY }));
  await settle();
  waiting[0](answer({ body: [] }));
  await settle();

  const row = panel.root.querySelector(".s-cards");
  assert.equal(row.querySelectorAll(".s-card").length, HISTORY.length, "the older answer replaced the newer one");
});

test("a card still on the board opens from the history, and an archived one does not", async () => {
  installTerminal();
  stubFetch(historyAnswer());
  const opened = [];
  const panel = await mount({ onOpenCard: (path) => opened.push(path) });

  await panel.click('.s-card[data-path="/board/cards/T-020-second.md"]');
  assert.deepEqual(opened, ["/board/cards/T-020-second.md"]);

  await panel.click('.s-card[data-path="/board/archive/2026-09-10-first.md"]');
  assert.deepEqual(opened, ["/board/cards/T-020-second.md"], "an archived card was handed to a panel that reads only the board");
});

test("the history row is in the page before the terminal is built", async () => {
  // Built after the terminal was fitted, the row would take height from it and
  // resize the session for everyone watching — so it is there from the start
  // and says it is loading.
  const terminals = installTerminal();
  const Built = globalThis.Terminal;
  let rowAtBuild = null;
  globalThis.Terminal = class extends Built {
    constructor(options) {
      super(options);
      rowAtBuild = dom.document.body.querySelector(".s-cards");
    }
  };
  stubFetch(historyAnswer());
  await mount();

  assert.equal(terminals.length, 1);
  assert.notEqual(rowAtBuild, null, "the history row came after the terminal was built");
});

test("stopping the panel stops listening to snapshots", async () => {
  installTerminal();
  stubFetch(historyAnswer());
  const panel = await mount();
  assert.equal(panel.store.listeners(), 1);

  panel.stop();
  assert.equal(panel.store.listeners(), 0, "a stopped panel is still subscribed");
  await panel.store.emit({ ...listed(), cards: [{ path: "/x.md", session: SHORT, stage: "active" }] });
  assert.equal(cardCalls().length, 1, "a stopped panel asked for a history");
});

test("the close button stops the panel and calls back exactly once", async () => {
  installTerminal();
  stubFetch(answer({ body: [] }));
  const panel = await mount();

  await panel.click(".s-close");

  assert.equal(panel.closes(), 1);
  assert.equal(panel.store.listeners(), 0);
});

// Pins the fix for a real defect: xterm's own default theme is a fixed
// light-grey-on-black regardless of the page's own theme, which read fine
// for as long as the whole app was dark-only and became a solid black
// rectangle in an otherwise light panel once a light theme actually shipped.
// This cannot check the *colours* — fake-dom.js has no CSS engine, and
// terminalTheme() itself degrades to undefined without one — but it does
// pin that a terminal is never constructed with the option silently
// dropped, which is the shape a future refactor could plausibly break.
test("the terminal is always constructed with a theme option, even if undefined", async () => {
  const terminals = installTerminal();
  stubFetch(answer({ body: [] }));
  await mount();

  assert.equal(terminals.length, 1);
  assert.ok("theme" in terminals[0].options, "Terminal was constructed with no theme option at all");
});

// --- the live terminal -------------------------------------------------------
//
// A held terminal, not a polled picture: one connection and no more, however
// long it stays open; a failure the operator can read; whatever was drawn
// staying drawn. The session's bytes arrive as they happen, and typing goes
// straight in.

test("the terminal opens exactly one socket and arms no timer, however long it runs", async () => {
  installTerminal();
  stubFetch(answer({ body: [] }));
  const panel = await mount();
  ready(sockets[0]);

  for (let tick = 1; tick <= 8; tick += 1) {
    await panel.timers.tick();
  }
  assert.equal(sockets.length, 1, "one socket, not one per tick");
  assert.equal(panel.timers.count(), 0, "a stream needs no timer");
  assert.equal(calls.filter((c) => String(c.url).includes("/screen")).length, 0, "and no screen poll behind it");
});

test("the socket goes to this session's terminal route and asks for the terminal's own size", async () => {
  // The size is the terminal's, not a number of the panel's own: attaching at it
  // resizes the session for everyone watching, so it has to be what is drawn here.
  // Not 80x24 on purpose — that is also the fallback the panel would reach for.
  const terminals = installTerminal({ cols: 132, rows: 41 });
  stubFetch(answer({ body: [] }));
  await mount();

  const url = new URL(sockets[0].url);
  assert.equal(url.protocol, "ws:");
  assert.equal(url.pathname, `/api/sessions/${SHORT}/pty`);
  assert.equal(url.searchParams.get("cols"), String(terminals[0].cols));
  assert.equal(url.searchParams.get("rows"), String(terminals[0].rows));
  assert.equal(sockets[0].binaryType, "arraybuffer", "terminal bytes are read as bytes, not as a Blob");
});

// The bridge attaches to nothing until the socket proves the panel's token
// (internal/server/pty.go). The token goes inside the socket, first; in the URL
// it would land in logs and history.
test("the socket's first frame is the token, and the token is nowhere in its URL", async () => {
  installTerminal();
  stubFetch(answer({ body: [] }));
  await mount();

  assert.equal(tokenCalls.length, 1, "the token was read before the socket was opened");
  assert.equal(sockets.length, 1);
  assert.equal(sockets[0].url.includes(TOKEN), false, "the token is in the socket's URL");
  assert.equal(sockets[0].sent.length, 0, "nothing is sent before the socket is open");

  sockets[0].serverOpen();
  assert.equal(typeof sockets[0].sent[0], "string", "the token is a text frame, not keystrokes");
  assert.deepEqual(JSON.parse(sockets[0].sent[0]), { type: "auth", token: TOKEN });
});

// The token lives as long as the panel's process. Read once per page, it would
// be refused by every terminal opened after the panel restarted.
test("every socket reads the token afresh", async () => {
  installTerminal();
  stubFetch(answer({ body: [] }));
  const panel = await mount();
  sockets[0].serverOpen();
  panel.stop();

  tokenAnswer = () => answer({ body: { token: "the-restarted-panel's-token" } });
  await mount();
  sockets[1].serverOpen();

  assert.equal(tokenCalls.length, 2);
  assert.equal(JSON.parse(sockets[1].sent[0]).token, "the-restarted-panel's-token");
});

// A socket the panel let go of while it was still connecting has nothing to
// prove to anyone: the token is not handed to a connection no panel owns.
test("a socket let go of before it opened never sends the token", async () => {
  installTerminal();
  stubFetch(answer({ body: [] }));
  const panel = await mount();
  const old = sockets[0];
  panel.stop();

  old.readyState = 1;
  old.onopen?.({});
  assert.equal(old.sent.length, 0, "the token went into a socket the panel had already closed");
});

test("a token the panel will not hand over is said on screen, and no socket is opened", async () => {
  installTerminal();
  tokenAnswer = () => answer({ status: 503, body: { error: "this panel is not wired to a terminal token" } });
  stubFetch(answer({ body: [] }));
  const panel = await mount();

  assert.equal(sockets.length, 0, "a socket with nothing to prove would only be refused");
  assert.ok(panel.errorText().includes(t("terminal_token_unavailable")), panel.errorText());
  assert.ok(panel.errorText().includes("not wired to a terminal token"), "and in the panel's own words");
});

// Reading the token takes a round trip, and the operator can close the panel —
// or close it and open another — inside it. A token that arrives for a panel
// nobody is looking at must open nothing: that socket would attach, reshape the
// session, and belong to no panel.
test("a token that arrives after its panel was closed opens nothing", async () => {
  installTerminal();
  const pending = [];
  tokenAnswer = () => new Promise((resolve) => pending.push(resolve));
  stubFetch(answer({ body: [] }));

  const first = await mount();
  first.stop();
  await mount();
  assert.equal(pending.length, 2, "one read per opening");

  pending[0](answer({ body: { token: "first" } }));
  await settle();
  assert.equal(sockets.length, 0, "the closed panel's token opened a socket");

  pending[1](answer({ body: { token: "second" } }));
  await settle();
  assert.equal(sockets.length, 1, "the panel that is open gets its socket");
});

// Attaching sets the session's size for everyone watching it, so the size the
// socket asks for has to be the pane's, measured before the socket exists —
// resizing after the attach would reshape the session twice.
test("the terminal is fitted to its pane before the socket asks for a size", async () => {
  const terminals = installTerminal();
  const fits = installFit({ cols: 173, rows: 52 });
  stubFetch(answer({ body: [] }));
  const panel = await mount();

  assert.equal(fits.length, 1, "one fit addon for the one terminal");
  assert.equal(fits[0].terminal, terminals[0], "loaded into the terminal it measures");
  assert.equal(terminals[0].cols, 173);
  assert.equal(terminals[0].rows, 52);
  const url = new URL(sockets[0].url);
  assert.equal(url.searchParams.get("cols"), "173", "the attach asked for the fitted width");
  assert.equal(url.searchParams.get("rows"), "52", "the attach asked for the fitted height");
  assert.equal(panel.noticeText(), "", "a fitted terminal has nothing to say about its size");
});

// --- following the pane -----------------------------------------------------
//
// The pane changes size under the terminal: the window is resized, the
// orchestrator column's edge is dragged (which moves this pane's edge too), a
// column is folded. The terminal is refitted, and the session — whose size is
// shared by everyone watching it — is told once the pane has stopped moving,
// not on every pointer move of a drag.

const resizes = (socket) =>
  socket.sent
    .filter((data) => typeof data === "string")
    .map((data) => JSON.parse(data))
    .filter((msg) => msg.type === "resize");

test("a pane that changes size refits the terminal and tells the session once, when it settles", async () => {
  const terminals = installTerminal();
  const pane = { cols: 120, rows: 40 };
  installFit(pane);
  const observers = installObserver();
  stubFetch(answer({ body: [] }));
  const panel = await mount();
  ready(sockets[0]);

  // A drag: the pane moves many times in a row.
  for (const cols of [110, 100, 90, 80, 70]) {
    pane.cols = cols;
    observers[0].resize();
  }
  assert.deepEqual(resizes(sockets[0]), [], "nothing is sent while the pane is still moving");
  assert.equal(panel.timers.count(), 1, "one settle timer, however many times the pane moved");

  await panel.timers.tick();
  assert.equal(terminals[0].cols, 70, "the terminal is refitted to where the pane stopped");
  assert.deepEqual(resizes(sockets[0]), [{ type: "resize", cols: 70, rows: 40 }], "and the session is told exactly once");

  // The observer fires again at the same size — a layout pass, a sibling moving.
  observers[0].resize();
  await panel.timers.tick();
  assert.equal(resizes(sockets[0]).length, 1, "the size the session was just given is not given again");
});

test("a pane that settles back at the size the session already has tells it nothing", async () => {
  installTerminal();
  const pane = { cols: 120, rows: 40 };
  installFit(pane);
  const observers = installObserver();
  stubFetch(answer({ body: [] }));
  const panel = await mount();
  ready(sockets[0]);

  pane.cols = 90;
  observers[0].resize();
  pane.cols = 120;
  observers[0].resize();
  await panel.timers.tick();
  assert.deepEqual(resizes(sockets[0]), [], "a resize to the size it already has still reshapes the session for everyone");
});

// The attach went out at the size the pane had when the panel opened. If the
// pane moved before the bridge was ready, the session is still at that size.
test("a pane that moved before the stream was ready is told when it is", async () => {
  installTerminal();
  const pane = { cols: 120, rows: 40 };
  installFit(pane);
  const observers = installObserver();
  stubFetch(answer({ body: [] }));
  const panel = await mount();
  assert.equal(new URL(sockets[0].url).searchParams.get("cols"), "120");

  pane.cols = 100;
  observers[0].resize();
  await panel.timers.tick();
  assert.deepEqual(resizes(sockets[0]), [], "a socket still connecting cannot carry it");

  ready(sockets[0]);
  assert.deepEqual(resizes(sockets[0]), [{ type: "resize", cols: 100, rows: 40 }]);
});

// The bridge reads the token first and everything else once it has attached, so
// a resize sent into an open socket before the bridge says ready is fine — as
// long as it goes after the token.
test("a pane that settles while the bridge is attaching is told behind the token", async () => {
  installTerminal();
  const pane = { cols: 120, rows: 40 };
  installFit(pane);
  const observers = installObserver();
  stubFetch(answer({ body: [] }));
  const panel = await mount();
  sockets[0].serverOpen();

  pane.cols = 100;
  observers[0].resize();
  await panel.timers.tick();
  assert.equal(JSON.parse(sockets[0].sent[0]).type, "auth", "the token went first");
  assert.deepEqual(resizes(sockets[0]), [{ type: "resize", cols: 100, rows: 40 }]);

  sockets[0].serverSend(JSON.stringify({ type: "ready", writable: true }));
  assert.equal(resizes(sockets[0]).length, 1, "and ready does not send it again");
});

test("a pane that can no longer be measured says so and tells the session nothing", async () => {
  installTerminal();
  const pane = { cols: 120, rows: 40 };
  installFit(pane);
  const observers = installObserver();
  stubFetch(answer({ body: [] }));
  const panel = await mount();
  ready(sockets[0]);

  pane.hidden = true;
  observers[0].resize();
  await panel.timers.tick();
  assert.deepEqual(resizes(sockets[0]), []);
  assert.ok(panel.noticeText().includes(t("terminal_not_fitted")), "a terminal that stopped following its pane says so");

  pane.hidden = false;
  pane.cols = 100;
  observers[0].resize();
  await panel.timers.tick();
  assert.equal(panel.noticeText(), "", "and stops saying so once it follows again");
  assert.deepEqual(resizes(sockets[0]), [{ type: "resize", cols: 100, rows: 40 }]);
});

// Nothing outlives the panel: an observer left watching, or a settle timer left
// armed, would refit a disposed terminal and send into a socket that is gone.
test("a pane nobody is looking at any more is not watched, and a stream that ended is not told", async () => {
  installTerminal();
  const pane = { cols: 120, rows: 40 };
  installFit(pane);
  const observers = installObserver();
  stubFetch(answer({ body: [] }));

  const panel = await mount();
  ready(sockets[0]);
  sockets[0].serverClose(4000);
  pane.cols = 90;
  observers[0].resize();
  await panel.timers.tick();
  assert.deepEqual(resizes(sockets[0]), [], "a stream that has ended was told about its pane");

  pane.cols = 80;
  observers[0].resize();
  panel.stop();
  assert.equal(observers[0].disconnected, true, "the observer outlived the panel");
  assert.equal(panel.timers.count(), 0, "a settle timer outlived the panel");
});

test("a terminal the fit addon cannot size is drawn at its own size, says so, and still connects", async () => {
  for (const [label, setup] of [
    ["addon missing", () => delete globalThis.FitAddon],
    ["nothing to measure", () => installFit(null)],
  ]) {
    sockets.length = 0;
    const terminals = installTerminal({ cols: 80, rows: 24 });
    setup();
    stubFetch(answer({ body: [] }));
    const panel = await mount();

    assert.equal(sockets.length, 1, `${label}: the terminal still connects`);
    const url = new URL(sockets[0].url);
    assert.equal(url.searchParams.get("cols"), String(terminals[0].cols), `${label}: the attach asked for the size drawn`);
    assert.ok(panel.noticeText().includes(t("terminal_not_fitted")), `${label}: and it says why`);
    panel.stop();
  }
});

test("a terminal that can neither type nor fit says both, for as long as it holds", async () => {
  installTerminal();
  installFit(null);
  stubFetch(answer({ body: [] }));
  const panel = await mount();
  ready(sockets[0], false);

  const text = panel.noticeText();
  assert.ok(text.includes(t("terminal_read_only")), text);
  assert.ok(text.includes(t("terminal_not_fitted")), text);
});

test("a stream that has ended no longer says it cannot type", async () => {
  installTerminal();
  stubFetch(answer({ body: [] }));
  const panel = await mount();
  ready(sockets[0], false);
  assert.equal(panel.noticeText(), t("terminal_read_only"));

  sockets[0].serverClose(4000);
  assert.equal(panel.noticeText(), "", "there is no stream left to be read-only");
  assert.equal(panel.errorText(), t("terminal_session_ended"), "the ending is what is said instead");
});

test("the session's bytes are written to the terminal as they arrive, never by redrawing it", async () => {
  const terminals = installTerminal();
  stubFetch(answer({ body: [] }));
  await mount();
  ready(sockets[0]);

  sockets[0].serverSend(frame("[1mfirst"));
  sockets[0].serverSend(frame(" second"));

  const written = terminals[0].writes.map((w) => asText(w)).join("");
  assert.equal(written, "[1mfirst second");
  assert.equal(terminals[0].resets, 0, "a stream is appended to, not reset per frame");
});

test("what the operator types into the terminal goes into the socket as bytes", async () => {
  const terminals = installTerminal();
  stubFetch(answer({ body: [] }));
  await mount();
  ready(sockets[0]);

  terminals[0].type("ls -la\r");

  const typed = sockets[0].sent.slice(1);
  assert.equal(typed.length, 1, "one frame after the token");
  assert.equal(typeof typed[0], "object", "a keystroke is a binary frame; text frames are control messages");
  assert.equal(asText(typed[0]), "ls -la\r");
});

test("every key button sends its escape sequence into the socket, and nothing through POST .../keys", async () => {
  // POST .../keys opens an attach of its own on every press, and an attach
  // resizes the session: with a live terminal open that would be a resize per
  // key. The buttons go through the one connection the terminal already holds.
  installTerminal();
  stubFetch(answer({ body: [] }));
  const panel = await mount();
  ready(sockets[0]);

  // transcript is Ctrl+O: Claude Code's own view of the whole conversation, and
  // the same key again takes it back to the prompt.
  const expected = { escape: "", up: "[A", down: "[B", enter: "\r", transcript: "" };
  // Every button, and no fewer: a row lost from KEYS would otherwise shrink this
  // loop rather than fail it.
  assert.deepEqual(KEYS.map((k) => k.id).sort(), Object.keys(expected).sort(), "the key buttons are not the four this checks");
  for (const key of KEYS) {
    await panel.click(`[data-key=${key.id}]`);
    const sent = asText(sockets[0].sent[sockets[0].sent.length - 1]);
    assert.equal(sent, expected[key.id], `${key.id} must send its bytes`);
    assert.notEqual(sent, key.id);
    assert.notEqual(sent, key.label);
  }
  assert.equal(calls.filter((c) => String(c.url).includes("/keys")).length, 0, "no key went out as a request");
});

test("a terminal that cannot type says so as soon as it opens, and typing sends nothing", async () => {
  const terminals = installTerminal();
  stubFetch(answer({ body: [] }));
  const panel = await mount();
  ready(sockets[0], false);

  const notice = panel.root.querySelector(".s-notice");
  assert.equal(notice.hidden, false, "read-only is said before anybody types");
  assert.equal(notice.textContent, t("terminal_read_only"));

  terminals[0].type("x");
  await panel.click("[data-key=enter]");
  assert.equal(keystrokes(sockets[0]).length, 0, "nothing went into a session through a terminal without a key");
  assert.equal(panel.errorText(), t("terminal_read_only"), "and the refusal is on screen");
});

test("a key pressed before the terminal is connected is refused where it can be seen", async () => {
  installTerminal();
  stubFetch(answer({ body: [] }));
  const panel = await mount();

  await panel.click("[data-key=enter]");
  assert.equal(sockets[0].sent.length, 0, "not even the token: the socket never opened");
  assert.equal(panel.errorText(), t("terminal_not_connected"));
});

test("an error the bridge reports is shown", async () => {
  installTerminal();
  stubFetch(answer({ body: [] }));
  const panel = await mount();
  ready(sockets[0]);

  sockets[0].serverSend(JSON.stringify({ type: "error", error: "resize failed: session is gone" }));
  assert.equal(panel.errorText(), "resize failed: session is gone");
});

test("a stream that ends says why, for every way it can end", async () => {
  const cases = [
    [4000, "", t("terminal_session_ended")],
    [4001, "kicked: Session opened in another window", `${t("terminal_kicked")}: Session opened in another window`],
    // A stream the daemon closed while the session kept running is not the
    // session ending, and one nobody could explain is neither.
    [4002, "", t("terminal_stream_dropped")],
    [4003, "", t("terminal_stream_unexplained")],
    [4403, "the terminal token was refused", t("terminal_token_refused")],
    [4404, "no such session", t("terminal_no_session")],
    [4401, "", t("terminal_key_refused")],
    [4503, "", t("terminal_daemon_unavailable")],
    [1006, "", t("terminal_connection_lost")],
    [1011, "attach failed: boom", `${t("terminal_connection_lost")}: attach failed: boom`],
  ];
  for (const [code, reason, want] of cases) {
    sockets.length = 0;
    installTerminal();
    stubFetch(answer({ body: [] }));
    const panel = await mount();
    ready(sockets[0]);
    sockets[0].serverClose(code, reason);
    assert.equal(panel.errorText(), want, `close ${code}`);
    panel.stop();
  }
});

// A stream must not reopen by itself: on a daemon that evicts the previous
// attacher (Windows), reconnecting on its own would take the operator's terminal
// back from them, again and again. Opening the panel again is the reconnect.
test("an ended stream is not reopened by itself, and opening the panel again opens a new one", async () => {
  installTerminal();
  stubFetch(answer({ body: [] }));
  const panel = await mount();
  ready(sockets[0]);
  sockets[0].serverClose(4001, "kicked: Session opened in another window");

  await panel.timers.tick();
  await settle();
  assert.equal(sockets.length, 1, "no socket was opened behind the operator's back");

  panel.stop();
  await mount();
  assert.equal(sockets.length, 2, "opening the panel again is the reconnect");
});

test("what the stream drew before it ended stays on screen", async () => {
  const terminals = installTerminal();
  stubFetch(answer({ body: [] }));
  await mount();
  ready(sockets[0]);
  sockets[0].serverSend(frame("the last thing the session said"));
  sockets[0].serverClose(4000);

  assert.equal(terminals[0].disposed, 0, "the terminal is kept, not thrown away with the stream");
  assert.equal(terminals[0].resets, 0);
  assert.equal(terminals[0].writes.map((w) => asText(w)).join(""), "the last thing the session said");
});

test("closing the panel or stopping it closes the socket and disposes the terminal", async () => {
  // A socket left open is an attach left open on the daemon, with this panel's
  // geometry still set on somebody's session.
  const terminals = installTerminal();
  stubFetch(answer({ body: [] }));

  let panel = await mount();
  await panel.click(".s-close");
  assert.notEqual(sockets[0].closedWith, null, "close button");
  // xterm holds a renderer, listeners and an observer; dropping the reference
  // without disposing leaks all three for the life of the page.
  assert.equal(terminals[0].disposed, 1);

  panel = await mount();
  panel.stop();
  assert.notEqual(sockets[1].closedWith, null, "stop");
});

test("a panel's own close is not reported as a lost connection", async () => {
  installTerminal();
  stubFetch(answer({ body: [] }));
  const panel = await mount();
  ready(sockets[0]);
  const socket = sockets[0];
  panel.stop();
  // A browser delivers onclose for a close the page asked for, too.
  socket.serverClose(1000, "panel closed");
  assert.equal(panel.errorText(), "", "closing the panel is not a failure");
});

// A socket the panel has let go of can still deliver what was already in
// flight. None of it may reach a terminal: the one it was drawing into has been
// disposed, and writing into a disposed xterm is an error in a real browser.
test("bytes still in flight on a socket the panel has let go of go nowhere", async () => {
  const terminals = installTerminal();
  stubFetch(answer({ body: [] }));
  const panel = await mount();
  ready(sockets[0]);
  const old = sockets[0];
  panel.stop();

  old.onmessage?.({ data: frame("late") });
  assert.equal(terminals[0].writes.length, 0, "a late frame was written into a disposed terminal");
});

// The other end of the card panel's Escape rule (web/tests/card.test.js): that
// test builds its own marked element, so it cannot tell whether the terminal
// this panel draws is marked at all. Without the mark, Escape typed into the
// session closes a card on its way to interrupting the session.
test("the terminal is drawn inside an element marked as a terminal", async () => {
  const terminals = installTerminal();
  stubFetch(answer({ body: [] }));
  await mount();

  assert.notEqual(terminals[0].host, null, "the terminal was never opened into the page");
  assert.notEqual(terminals[0].host.closest("[data-terminal]"), null, "what the terminal is drawn into carries no data-terminal mark");
});

test("a missing WebSocket or terminal library is a visible error, and opens nothing", async () => {
  delete globalThis.Terminal;
  stubFetch(answer({ body: [] }));
  let panel = await mount();
  assert.notEqual(panel.errorText(), "");
  assert.equal(sockets.length, 0, "no socket for a terminal that could not be built");
  panel.stop();

  installTerminal();
  delete globalThis.WebSocket;
  panel = await mount();
  assert.notEqual(panel.errorText(), "");
});

// --- the two classes of control, and which is which ------------------------
//
// The operator's first look at this panel produced one sentence: "непонятно
// зачем нужны эти кнопки" — it is not clear what these buttons are for, about a
// bare `Esc ↑ ↓ Enter ✕` row in the top-right corner of the header. That corner
// is where every window on his machine puts controls that act on the window,
// and four of those five buttons do not: they press a key inside a Claude Code
// session running somewhere else, which no undo reaches. The tests below are
// what stops that row from coming back.

test("the keys carry a label saying where the press lands", async () => {
  // Four bare glyphs say nothing about their destination. The label is the only
  // thing on screen that does, so it is not allowed to be absent, empty, or the
  // untranslated name of its own dictionary key.
  installTerminal();
  stubFetch(answer({ body: [] }));
  const panel = await mount();

  assert.equal(panel.root.querySelectorAll("[data-key]").length, KEYS.length, "every key is drawn");
  const label = panel.root.querySelector(".s-keys-label");
  assert.notEqual(label, null, "the key row has no label at all");
  assert.equal(label.textContent, t("keys_to_session"), "the label is not the panel's own string");
  assert.notEqual(label.textContent, "keys_to_session", "the label fell through to its own key name");
  assert.notEqual(label.textContent.trim(), "");
});

test("the transcript key is named in words and says what it opens", async () => {
  // Every other key's glyph says which key it is. This one is a control
  // character with no glyph, and what it does — the whole conversation, not only
  // the screen — is the reason it is there.
  installTerminal();
  stubFetch(answer({ body: [] }));
  const panel = await mount();

  const button = panel.root.querySelector("[data-key=transcript]");
  assert.notEqual(button, null, "there is no transcript key");
  assert.equal(button.textContent, t("key_transcript"));
  assert.notEqual(button.textContent, "key_transcript", "the label fell through to its own key name");
  assert.equal(button.title, t("key_transcript_hint"));
  assert.ok(panel.root.querySelector(".s-keys").contains(button), "the transcript key is not with the other keys");
});

test("the close button is not one of the keys, and the keys are not in the header", async () => {
  // ✕ closes this panel and nothing leaves the machine; the four keys land in
  // somebody's running work. Drawn as one row of five they read as one set, and
  // the operator read them as exactly that.
  installTerminal();
  stubFetch(answer({ body: [] }));
  const panel = await mount();

  const head = panel.root.querySelector(".s-head");
  const keys = panel.root.querySelector(".s-keys");
  assert.notEqual(head.querySelector(".s-close"), null, "the close button belongs to the header");
  assert.equal(head.querySelectorAll("[data-key]").length, 0, "no session key sits in the header row");
  assert.notEqual(keys, null, "the keys have no group of their own");
  assert.equal(keys.querySelector(".s-close"), null, "the close button is inside the key group");
  assert.equal(head.contains(keys), false, "the key group is still nested in the header");
});

// --- which session this panel is pointing at -------------------------------

test("the header names the session the panel has open", async () => {
  installTerminal();
  stubFetch(answer({ body: [] }));
  const panel = await mount({ store: fakeStore(listed({ name: "fleetdeck server" })) });

  const who = panel.root.querySelector(".s-who");
  assert.notEqual(who, null, "nothing in the header names the session");
  assert.equal(who.textContent, "fleetdeck server");
  // The short id is the identity the operator can match against the session
  // list, and two sessions may carry the same name.
  // Set as a property, the way every other title in this panel is: the fake DOM
  // reflects neither direction, and a browser reflects both.
  assert.equal(who.title, SHORT);

  // Resolved on every snapshot, but written only when it changed: a header
  // rewritten once a second drops a selection inside it and costs work for
  // nothing.
  const writes = who.textWrites;
  await panel.store.emit(listed({ name: "fleetdeck server" }));
  await panel.store.emit(listed({ name: "fleetdeck server" }));
  assert.equal(who.textWrites, writes, "the header is rewritten on every snapshot");
});

test("a session with no name yet is named by its short id, never by nothing", async () => {
  // A blank space says the panel does not know where it points. The short id is
  // what the session list shows when a session has no name of its own.
  installTerminal();
  stubFetch(answer({ body: [] }));
  const panel = await mount();

  assert.equal(panel.root.querySelector(".s-who").textContent, SHORT);
});

test("a panel opened before the first snapshot names the session once it appears", async () => {
  // Captured when the panel opens, the name of a session that was not in the
  // snapshot yet stays missing for as long as the panel stays open — and the
  // panel is opened from a list that is itself drawn from that snapshot, so the
  // race is ordinary.
  installTerminal();
  stubFetch(answer({ body: [] }));
  const panel = await mount({ store: fakeStore(null) });

  assert.equal(panel.root.querySelector(".s-who").textContent, SHORT, "before the snapshot: the short id");

  await panel.store.emit(listed({ name: "fleetdeck server" }));

  assert.equal(
    panel.root.querySelector(".s-who").textContent,
    "fleetdeck server",
    "after the snapshot: named, without being reopened",
  );
});

// A [[link]] a session prints on the screen opens the card it names, the same as
// in the orchestrator column: the panel hands its terminal the page's links,
// which web/js/main.js builds.
test("the terminal is given the page's links", async () => {
  const terminals = installTerminal();
  stubFetch(answer({ body: [] }));
  const links = { resolve: () => null, open: () => {} };
  await mount({ links });

  assert.equal(terminals.at(-1).linkProviders?.length, 1, "the terminal links nothing");
});

// --- the font buttons -------------------------------------------------------
//
// The same three buttons as the orchestrator column, from the same builder, in
// the header. The header is built before the terminal is, and holds the buttons
// from then on, so they never make the terminal's pane shorter after it has been
// fitted.

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

// Looked for inside the header, so buttons anywhere else are not found.
const headFontButtons = (root) => {
  const head = root.querySelector(".s-head");
  return {
    group: head?.querySelector(".term-font") ?? null,
    smaller: head?.querySelector(".term-font-smaller") ?? null,
    reset: head?.querySelector(".term-font-reset") ?? null,
    bigger: head?.querySelector(".term-font-bigger") ?? null,
  };
};

test("the header has the font buttons, ahead of the session's name", async () => {
  installTerminal();
  stubFetch(answer({ body: [] }));
  const panel = await mount();

  const head = panel.root.querySelector(".s-head");
  const { group } = headFontButtons(panel.root);
  assert.ok(group, "the header has no font buttons");
  assert.ok(head.children.indexOf(group) < head.children.indexOf(head.querySelector(".s-who")), "the buttons are not ahead of the name");
  for (const b of group.children) assert.ok(String(b.className).split(" ").includes("s-font-btn"));
});

test("the font buttons are in the page before the terminal is built", async () => {
  const terminals = installTerminal();
  const Built = globalThis.Terminal;
  let buttonsAtBuild = null;
  globalThis.Terminal = class extends Built {
    constructor(options) {
      super(options);
      buttonsAtBuild = Boolean(headFontButtons(dom.document.body).group);
    }
  };
  stubFetch(answer({ body: [] }));
  await mount();

  assert.equal(terminals.length, 1, "the panel built no terminal");
  assert.equal(buttonsAtBuild, true, "the buttons came after the terminal was built, and push its pane down after it was fitted");
});

test("the font buttons show the panel's own size and press its terminal", async () => {
  await withFontStorage({ "fleetdeck-terminal-font-orchestrator": "20", "fleetdeck-terminal-font-screen": "10" }, async (map) => {
    const terminals = installTerminal();
    stubFetch(answer({ body: [] }));
    const panel = await mount();
    const terminal = terminals.at(-1);
    const { smaller, reset, bigger } = headFontButtons(panel.root);
    assert.equal(reset.textContent, "10 px");

    fireEvent(bigger, "click");
    assert.equal(terminal.options.fontSize, 11);
    assert.equal(map.get("fleetdeck-terminal-font-screen"), "11");
    assert.equal(map.get("fleetdeck-terminal-font-orchestrator"), "20", "the panel's button changed the column's size");
    assert.equal(reset.textContent, "11 px");

    fireEvent(smaller, "click");
    fireEvent(smaller, "click");
    assert.equal(terminal.options.fontSize, 9);
    assert.equal(smaller.disabled, true, "A− at 9 px looked like it would do something");
  });
});

// The panel keeps the size of its type under its own key, not the orchestrator
// column's: the two are different widths, and what a bigger type costs is
// columns. The column's entry is set too, so reading it shows.
test("the panel's terminal is the size remembered for it, and Cmd+- changes that one", async () => {
  await withFontStorage({ "fleetdeck-terminal-font-orchestrator": "20", "fleetdeck-terminal-font-screen": "14" }, async (map) => {
    const terminals = installTerminal();
    stubFetch(answer({ body: [] }));
    await mount();
    const terminal = terminals.at(-1);
    assert.equal(terminal.options.fontSize, 14, "the panel's terminal did not start at its own size");

    let prevented = false;
    const passed = terminal.keyHandler({ type: "keydown", key: "-", metaKey: true, preventDefault: () => (prevented = true) });

    assert.equal(passed, false);
    assert.equal(prevented, true);
    assert.equal(map.get("fleetdeck-terminal-font-screen"), "13");
    assert.equal(map.get("fleetdeck-terminal-font-orchestrator"), "20", "the panel's key changed the column's size");
  });
});
