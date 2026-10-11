// The board between a gesture and the server's answer.
//
// A card write takes about 0.4 s on a signed board and a start several
// seconds (T-159), and the board used to show nothing in that time: a card
// sat in the column it was dropped out of, and a new card's form closed onto
// an empty column. These pin what is drawn instead — the result at once,
// marked as not yet written, and taken back with a word when it was refused.
//
// The fake DOM stores innerHTML without parsing it, so what is drawn is read
// off the markup string, column by column.

import { test, beforeEach, afterEach } from "node:test";
import assert from "node:assert/strict";

import { installDOM, fireEvent, settle } from "./fake-dom.js";

globalThis.navigator ??= { language: "en" };

class BoardSocket {
  constructor() {
    this.onmessage = null;
    this.onclose = null;
    this.onerror = null;
    socket = this;
  }
  close() {}
  push(snapshot) {
    this.onmessage?.({ data: JSON.stringify(snapshot) });
  }
}

let socket = null;
globalThis.WebSocket = BoardSocket;
globalThis.location = { protocol: "http:", host: "127.0.0.1:7777" };

let dom;
let board;
let boardModule;
let handle;
let answer;
let moves;
let t;

const CARD = "/b/cards/T-001.md";
const snap = (stage, extra = []) => ({ cards: [{ path: CARD, id: "T-001", title: "first", stage, zone: "planned", progress: 0 }, ...extra] });

// columnHolding names the column whose markup holds needle, or null.
function columnHolding(needle) {
  const html = board.innerHTML;
  const heads = [...html.matchAll(/<div class="kcol[^"]*" data-stage="([^"]+)">/g)];
  for (let i = 0; i < heads.length; i++) {
    const end = i + 1 < heads.length ? heads[i + 1].index : html.length;
    if (html.slice(heads[i].index, end).includes(needle)) return heads[i][1];
  }
  return null;
}

function articleHolding(needle) {
  const html = board.innerHTML;
  const at = html.indexOf(needle);
  if (at < 0) return "";
  return html.slice(html.lastIndexOf("<article", at), html.indexOf("</article>", at));
}

const columnOf = (path) => columnHolding(`data-path="${path}"`);
const cardMarkup = (path) => articleHolding(`data-path="${path}"`);

// drop drives the board's own handlers against nodes built by hand, the way
// boarddrag.test.js does: the markup above is a string the fake DOM never
// turns into nodes.
function drop(from, to, session = "") {
  const source = dom.element("div");
  source.className = "kcol";
  source.dataset.stage = from;
  const target = dom.element("div");
  target.className = "kcol";
  target.dataset.stage = to;
  const card = dom.element("article");
  card.className = "kcard";
  card.dataset.path = CARD;
  card.dataset.stage = from;
  if (session) card.dataset.session = session;
  source.appendChild(card);
  board.append(source, target);
  fireEvent(card, "dragstart", { dataTransfer: { setData() {} } });
  fireEvent(target, "drop", { dataTransfer: {} });
}

beforeEach(async () => {
  dom = installDOM();
  board = dom.element("div");
  dom.document.body.appendChild(board);
  moves = [];
  answer = null;
  boardModule = await import("../js/board.js");
  ({ t } = await import("../js/i18n.js"));
  const store = await import("../js/store.js");
  store.connect();
  handle = boardModule.renderBoard(board, () => {}, {
    onAddCard: () => {},
    onMove: (move, hooks) => {
      moves.push(move);
      return answer(hooks);
    },
  });
});

afterEach(() => {
  dom.restore();
});

test("board pending: a card is drawn in its new column the moment the move begins", async () => {
  socket.push(snap("new"));
  let release;
  answer = (hooks) => {
    hooks.begin("move");
    return new Promise((resolve) => (release = resolve));
  };
  drop("new", "blocked", "aa11");
  assert.equal(columnOf(CARD), "blocked", "the card waits in the column it was dropped out of");
  assert.match(cardMarkup(CARD), /\bkcard-pending\b/, "and is not marked as still being written");
  assert.match(cardMarkup(CARD), new RegExp(t("card_saving")));

  release("blocked");
  await settle();
  socket.push(snap("blocked"));
  assert.equal(columnOf(CARD), "blocked");
  assert.doesNotMatch(cardMarkup(CARD), /kcard-pending/, "a written move is an ordinary card again");
});

test("board pending: a card being written cannot be dragged again", async () => {
  socket.push(snap("new"));
  answer = (hooks) => {
    hooks.begin("move");
    return new Promise(() => {});
  };
  drop("new", "blocked", "aa11");
  // Its stage on the server is still the old one: a second drag would write
  // against a stage the board has not got yet, and be refused.
  assert.match(cardMarkup(CARD), /draggable="false"/);
});

test("board pending: a start says it is starting a session", async () => {
  socket.push(snap("new"));
  answer = (hooks) => {
    hooks.begin("start");
    return new Promise(() => {});
  };
  drop("new", "active");
  assert.equal(columnOf(CARD), "active");
  assert.match(cardMarkup(CARD), new RegExp(t("card_starting")));
});

test("board pending: a snapshot that agrees before the answer leaves the card marked as being written", async () => {
  socket.push(snap("new"));
  answer = (hooks) => {
    hooks.begin("move");
    return new Promise(() => {});
  };
  drop("new", "blocked", "aa11");
  // The file is written before the commit: the board sees the new stage while
  // the write has not answered yet, and may still be refused.
  socket.push(snap("blocked"));
  assert.match(cardMarkup(CARD), /\bkcard-pending\b/);
});

test("board pending: a refused move goes back to its column and says so", async () => {
  socket.push(snap("new"));
  answer = (hooks) => {
    hooks.begin("move");
    return Promise.resolve(null);
  };
  drop("new", "blocked", "aa11");
  await settle();
  assert.equal(columnOf(CARD), "new");
  assert.match(cardMarkup(CARD), /\bkcard-failed\b/);
  assert.match(cardMarkup(CARD), new RegExp(t("card_move_failed")));
});

test("board pending: a move that threw goes back too", async () => {
  socket.push(snap("new"));
  answer = (hooks) => {
    hooks.begin("move");
    return Promise.reject(new Error("network"));
  };
  const quiet = console.error;
  console.error = () => {};
  try {
    drop("new", "blocked", "aa11");
    await settle();
  } finally {
    console.error = quiet;
  }
  assert.equal(columnOf(CARD), "new");
  assert.match(cardMarkup(CARD), /\bkcard-failed\b/);
});

test("board pending: a move the operator declined is no failure", async () => {
  socket.push(snap("new"));
  answer = () => Promise.resolve(null);
  drop("new", "active");
  await settle();
  assert.equal(columnOf(CARD), "new");
  assert.doesNotMatch(cardMarkup(CARD), /kcard-failed/, "nothing was refused: the operator said no");
});

// --- the pure view, by the clock ---

test("board pending: a write that takes long says the panel is slow", () => {
  const { pendingView, SLOW_MS } = boardModule;
  const pending = new Map([[CARD, { stage: "blocked", kind: "move", since: 0 }]]);
  assert.equal(pendingView(snap("new"), pending, SLOW_MS - 1).cards[0].slow, false);
  assert.equal(pendingView(snap("new"), pending, SLOW_MS + 1).cards[0].slow, true);
  const html = boardModule.columnHTML("blocked", "blocked", pendingView(snap("new"), pending, SLOW_MS + 1).cards, new Set());
  assert.match(html, /\bkcard-slow\b/);
  assert.match(html, new RegExp(t("card_slow")));
});

test("board pending: an answered move the snapshot never agrees with is let go", () => {
  const { pendingView, SETTLE_MS } = boardModule;
  const pending = new Map([[CARD, { stage: "blocked", kind: "move", since: 0, answeredAt: 0 }]]);
  assert.equal(pendingView(snap("new"), pending, SETTLE_MS - 1).cards[0].stage, "blocked");
  assert.equal(pendingView(snap("new"), pending, SETTLE_MS + 1).cards[0].stage, "new", "a card left for ever in a column the board never wrote");
  assert.equal(pending.size, 0);
});

test("board pending: an unanswered move is never let go by the clock", () => {
  const { pendingView, SETTLE_MS } = boardModule;
  // The server bounds its own waits (a start, up to three minutes): the card
  // is drawn as being written for as long as it is.
  const pending = new Map([[CARD, { stage: "active", kind: "start", since: 0 }]]);
  assert.equal(pendingView(snap("new"), pending, SETTLE_MS * 20).cards[0].stage, "active");
});

// --- a card being created ---

test("board pending: a card being created is drawn in the new column at once", async () => {
  socket.push(snap("active"));
  handle.creating("second card", "urgent");
  assert.equal(columnHolding("second card"), "new", "the card the operator has just created is nowhere, or in the wrong column");
  assert.match(articleHolding("second card"), /\bkcard-ghost\b/);
  assert.match(articleHolding("second card"), /\bzone-urgent\b/);
  assert.match(articleHolding("second card"), new RegExp(t("card_creating")));
});

test("board pending: the placeholder gives way to the card when the snapshot carries it", async () => {
  socket.push(snap("active"));
  const creating = handle.creating("second card", "planned");
  creating.created("/b/cards/T-002.md");
  socket.push(snap("active"));
  assert.match(board.innerHTML, /second card/, "the placeholder went before the card came: a gap on screen");
  socket.push(snap("active", [{ path: "/b/cards/T-002.md", id: "T-002", title: "second card", stage: "new", zone: "planned", progress: 0 }]));
  assert.doesNotMatch(board.innerHTML, /kcard-ghost/);
  assert.equal(columnOf("/b/cards/T-002.md"), "new");
});

test("board pending: a refused card takes its placeholder with it", async () => {
  socket.push(snap("active"));
  const creating = handle.creating("second card", "planned");
  creating.failed();
  assert.doesNotMatch(board.innerHTML, /kcard-ghost/);
});

test("board pending: a placeholder is no column of its own and opens nothing", async () => {
  const opened = [];
  const other = dom.element("div");
  dom.document.body.appendChild(other);
  boardModule.renderBoard(other, (path) => opened.push(path));
  const ghost = dom.element("article");
  ghost.className = "kcard kcard-ghost";
  other.appendChild(ghost);
  fireEvent(ghost, "click");
  assert.deepEqual(opened, []);
});
