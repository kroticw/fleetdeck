// Dragging a card between the board's columns, and the "+" over the new one.
//
// The board is drawn as an HTML string and this fake DOM stores innerHTML
// without parsing it, so the markup is asserted as a string and the delegated
// handlers are driven against a tree built by hand — which is all they touch:
// each one reads its way up from the event's target with closest().

import { test, beforeEach, afterEach } from "node:test";
import assert from "node:assert/strict";

import { installDOM, fireEvent } from "./fake-dom.js";

let dom;
let board;
let boardModule;
let opened;
let added;
let moves;
let answer;

function cardNode(column, { path, stage, session, mark }) {
  const card = dom.element("article");
  card.className = mark ? `kcard ${mark}` : "kcard";
  card.dataset.path = path;
  card.dataset.stage = stage;
  if (session) card.dataset.session = session;
  column.appendChild(card);
  return card;
}

function drag(card, to) {
  fireEvent(card, "dragstart", { dataTransfer: transfer() });
  fireEvent(to, "drop", { dataTransfer: transfer() });
}

function columnNode(stage) {
  const column = dom.element("div");
  column.className = "kcol";
  column.dataset.stage = stage;
  board.appendChild(column);
  return column;
}

function transfer() {
  const data = {};
  return {
    setData(type, value) {
      data[type] = value;
    },
    getData(type) {
      return data[type];
    },
  };
}

beforeEach(async () => {
  dom = installDOM();
  board = dom.element("div");
  dom.document.body.appendChild(board);
  opened = [];
  added = 0;
  moves = [];
  answer = "review";
  boardModule = await import("../js/board.js");
  boardModule.renderBoard(board, (path) => opened.push(path), {
    onAddCard: () => {
      added += 1;
    },
    onMove: (move) => {
      moves.push(move);
      return answer;
    },
  });
});

afterEach(() => {
  dom.restore();
});

test("board: the new column carries the add button and no other column does", () => {
  const { columnHTML } = boardModule;
  const html = columnHTML("new", "new", [], new Set());
  assert.match(html, /class="[^"]*\bkcol-add\b[^"]*"/);
  // A row of its own under the heading, not a square inside it: the head keeps
  // the stage and its count and nothing else, and the button says what it does.
  assert.match(html, /<\/h5>\s*<button type="button" class="[^"]*\bkcol-add\b[^"]*"[^>]*>[^<]+<\/button>/);
  assert.doesNotMatch(html.slice(0, html.indexOf("</h5>")), /kcol-add/);
  for (const stage of ["active", "review", "blocked", "done", "other"]) {
    assert.ok(!/kcol-add/.test(columnHTML(stage, stage, [], new Set())), `${stage} must not offer to start a card`);
  }
});

test("board: a card is draggable and carries the stage a drop writes against", () => {
  const html = boardModule.columnHTML("active", "active", [{ path: "/b/c.md", title: "x", stage: "active", session: "abc12345" }], new Set());
  assert.match(html, /draggable="true"/);
  assert.match(html, /data-stage="active"/);
  assert.match(html, /data-session="abc12345"/);
});

test("board: a card's session is escaped in the attribute as it is in the text", () => {
  const cards = [{ path: "/b/c.md", title: "x", stage: "active", session: 'a"><script>' }];
  const html = boardModule.columnHTML("active", "active", cards, new Set());
  assert.ok(!html.includes("<script>"), "a card file is untrusted input, its session included");
});

test("board: pressing the add button opens the new card form and opens no card", () => {
  const column = columnNode("new");
  const add = dom.element("button");
  add.className = "kcol-add";
  column.appendChild(add);
  fireEvent(add, "click");
  assert.equal(added, 1);
  assert.deepEqual(opened, []);
});

// A snapshot mid-drag would replace the card the pointer is holding, and the
// browser's drag is bound to the element it started on: the gesture ends there
// and the card is left where it was, with nothing saying why.
test("board: a snapshot arriving mid-drag does not redraw the board under the pointer", () => {
  const from = columnNode("new");
  const card = cardNode(from, { path: "/b/c.md", stage: "new" });
  const snap = { cards: [{ path: "/b/c.md", stage: "new", title: "x" }] };

  boardModule.render(board, snap);
  const before = board.htmlWrites;
  assert.ok(before > 0, "a board that never draws would pass this test by doing nothing");

  fireEvent(card, "dragstart", { dataTransfer: transfer() });
  boardModule.render(board, snap);
  assert.equal(board.htmlWrites, before, "a frame during the gesture must be dropped");

  fireEvent(card, "dragend");
  assert.ok(board.htmlWrites > before, "the board must be drawn again once the gesture is over");
  boardModule.render(board, snap);
  assert.ok(board.htmlWrites > before + 1, "and drawn by every frame after it");
});

test("board: a card dropped into another column is reported with where it came from", () => {
  const from = columnNode("new");
  const to = columnNode("active");
  const card = cardNode(from, { path: "/b/c.md", stage: "new" });

  fireEvent(card, "dragstart", { dataTransfer: transfer() });
  fireEvent(to, "dragover", { dataTransfer: transfer() });
  fireEvent(to, "drop", { dataTransfer: transfer() });

  assert.deepEqual(moves, [{ path: "/b/c.md", from: "new", session: "", live: false, to: "active" }]);
});

test("board: a card dropped back into its own column is not a move", () => {
  const from = columnNode("new");
  const card = cardNode(from, { path: "/b/c.md", stage: "new" });
  fireEvent(card, "dragstart", { dataTransfer: transfer() });
  fireEvent(from, "drop", { dataTransfer: transfer() });
  assert.deepEqual(moves, []);
});

// A column that does not cancel dragover is not a drop target at all, and the
// browser refuses the drop without saying so.
test("board: a column takes the drop by cancelling dragover", () => {
  const from = columnNode("new");
  const to = columnNode("active");
  const card = cardNode(from, { path: "/b/c.md", stage: "new" });
  fireEvent(card, "dragstart", { dataTransfer: transfer() });
  const over = fireEvent(to, "dragover", { dataTransfer: transfer() });
  assert.equal(over.defaultPrevented, true);
});

test("board: dragover outside a drag is left alone", () => {
  const to = columnNode("active");
  const over = fireEvent(to, "dragover", { dataTransfer: transfer() });
  assert.equal(over.defaultPrevented, false, "a page-wide drag of something else must not be captured");
});

// Whether an agent is keeping the card decides whether the drop asks before
// writing. It is read off the marks the card was drawn with — a session that
// is dead or stopped is named on the card and is nobody's to lose.
test("board: a card an agent is keeping is reported as held", () => {
  const to = columnNode("review");
  const card = cardNode(columnNode("active"), { path: "/b/c.md", stage: "active", session: "abc12345" });
  drag(card, to);
  assert.deepEqual(moves[0], { path: "/b/c.md", from: "active", session: "abc12345", live: true, to: "review" });
});

test("board: a card whose session is dead or stopped is not reported as held", () => {
  for (const mark of ["kcard-orphan", "kcard-stopped"]) {
    moves = [];
    const to = columnNode("review");
    const card = cardNode(columnNode("active"), { path: "/b/c.md", stage: "active", session: "abc12345", mark });
    drag(card, to);
    assert.equal(moves[0].live, false, `${mark} names a session nobody is keeping`);
    assert.equal(moves[0].session, "abc12345", "the id is still said, so the operator can see whose it was");
  }
});

test("board: a card with no session at all is not reported as held", () => {
  const to = columnNode("active");
  const card = cardNode(columnNode("new"), { path: "/b/c.md", stage: "new" });
  drag(card, to);
  assert.equal(moves[0].live, false);
});

// The reason the form is not in the column the button is in: the board is
// redrawn whole from every snapshot, and a form inside #board would be
// replaced mid-sentence. This is the check that the two really are apart.
test("board: a snapshot arriving while a card is being typed does not take the text", async () => {
  const tabs = dom.element("nav");
  dom.document.body.appendChild(tabs);
  const { createNewCard } = await import("../js/newcard.js");
  const form = createNewCard(tabs, { create: async () => ({ path: "/b/c.md", committed: true }) });
  form.open();
  const title = tabs.querySelector("input.newcard-title");
  title.value = "half a thought";

  boardModule.render(board, { cards: [{ path: "/b/c.md", stage: "new", title: "x" }] });

  assert.equal(tabs.querySelector("input.newcard-title").value, "half a thought");
  assert.equal(tabs.querySelector("div.newcard").hidden, false, "the form must still be open");
});

// --- the snapshot that arrives mid-move ---

test("board: a move the panel has asked for is drawn until the snapshot agrees", () => {
  const { pendingView } = boardModule;
  const pending = new Map([["/b/c.md", "active"]]);
  const stale = { cards: [{ path: "/b/c.md", stage: "new" }] };
  assert.equal(pendingView(stale, pending).cards[0].stage, "active", "the card would jump back for a second");
  assert.equal(stale.cards[0].stage, "new", "the snapshot itself must not be rewritten");
  assert.equal(pending.size, 1);
});

test("board: a move the snapshot has caught up with is forgotten", () => {
  const { pendingView } = boardModule;
  const pending = new Map([["/b/c.md", "active"]]);
  const fresh = { cards: [{ path: "/b/c.md", stage: "active" }] };
  assert.equal(pendingView(fresh, pending).cards[0].stage, "active");
  assert.equal(pending.size, 0, "an entry the snapshot agrees with is over");
});

test("board: a move of a card that has left the board is forgotten too", () => {
  const { pendingView } = boardModule;
  const pending = new Map([["/b/gone.md", "done"]]);
  pendingView({ cards: [{ path: "/b/c.md", stage: "new" }] }, pending);
  assert.equal(pending.size, 0, "an archived card would keep its entry for ever");
});

test("board: with nothing pending the snapshot is handed on as it came", () => {
  const { pendingView } = boardModule;
  const snap = { cards: [{ path: "/b/c.md", stage: "new" }] };
  assert.equal(pendingView(snap, new Map()), snap);
  assert.equal(pendingView(null, new Map()), null);
});
