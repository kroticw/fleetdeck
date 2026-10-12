// The board's failed commits, said where the operator looks (T-161).
//
// A card write answers once the file is written, and the board's queue
// commits it afterwards. A commit that fails — the signing PIN not entered,
// most likely — leaves the write on disk and uncommitted, and nothing on the
// board would say so: this note does, with the reason and a way to try again.

import { test, beforeEach, afterEach } from "node:test";
import assert from "node:assert/strict";

import { installDOM, fireEvent, settle } from "./fake-dom.js";

globalThis.navigator ??= { language: "en" };

class NoteSocket {
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
globalThis.WebSocket = NoteSocket;
globalThis.location = { protocol: "http:", host: "127.0.0.1:7777" };

let dom;
let host;
let retried;
let retryAnswer;
let t;
let tabs;
let board;

beforeEach(async () => {
  dom = installDOM();
  // The centre column as index.html lays it out: the tab row, then the board.
  host = dom.element("section");
  tabs = dom.element("nav");
  board = dom.element("div");
  host.append(tabs, board);
  dom.document.body.appendChild(host);
  retried = 0;
  retryAnswer = undefined;
  ({ t } = await import("../js/i18n.js"));
  const store = await import("../js/store.js");
  store.connect();
  const { renderCommitNote } = await import("../js/commitnote.js");
  renderCommitNote(host, {
    before: board,
    retry: async () => {
      retried += 1;
      if (retryAnswer instanceof Error) throw retryAnswer;
    },
  });
});

afterEach(() => {
  dom.restore();
});

const note = () => host.querySelector("div.commit-note");

// The note is a row of its own between the tab row and the board, in the flow
// of the column: the board moves down while it shows, and nothing is drawn
// over the columns' heads (the operator's call, 12.10). Not in the tab row,
// where it hung over the board.
test("commit note: it is a row of the column above the board, not part of the tab row", () => {
  const children = host.children;
  const at = children.indexOf(note());
  assert.ok(at >= 0, "the note is not a row of the column");
  assert.equal(children[at + 1], board, "the note is not right above the board");
  assert.equal(tabs.querySelector("div.commit-note"), null);
});

test("commit note: nothing is said while the board's commits keep up", () => {
  socket.push({ cards: [] });
  assert.equal(note().hidden, true);
  socket.push({ cards: [], boardCommits: { queued: 3 } });
  assert.equal(note().hidden, true, "commits on their way are no news");
});

test("commit note: failed commits are said with their count and the reason", () => {
  socket.push({ cards: [], boardCommits: { failed: 2, error: "git commit timed out after 30s, likely a commit-signing passphrase prompt" } });
  assert.equal(note().hidden, false);
  const text = note().querySelector("span.commit-note-text").textContent;
  assert.match(text, new RegExp(t("commits_failed")));
  assert.match(text, /2/);
  assert.match(text, /passphrase/);
  assert.equal(note().getAttribute("role"), "status");
});

test("commit note: Retry asks the board's queue, and the note goes once the debt is paid", async () => {
  socket.push({ cards: [], boardCommits: { failed: 1, error: "no PIN" } });
  const button = note().querySelector("button.commit-retry");
  assert.ok(String(button.className).split(" ").includes("btn"), "the button is not drawn by the button system");
  fireEvent(button, "click");
  await settle();
  assert.equal(retried, 1);
  socket.push({ cards: [] });
  assert.equal(note().hidden, true);
});

test("commit note: a retry the panel refused says why", async () => {
  retryAnswer = new Error("this panel has no board commit queue");
  socket.push({ cards: [], boardCommits: { failed: 1, error: "no PIN" } });
  fireEvent(note().querySelector("button.commit-retry"), "click");
  await settle();
  assert.match(note().querySelector("span.commit-note-text").textContent, /no board commit queue/);
  assert.equal(note().querySelector("button.commit-retry").disabled, false, "the operator cannot try again");
});

test("commit note: a refused retry gives way to the queue's next word", async () => {
  retryAnswer = new Error("this panel has no board commit queue");
  socket.push({ cards: [], boardCommits: { failed: 1, error: "no PIN" } });
  fireEvent(note().querySelector("button.commit-retry"), "click");
  await settle();
  socket.push({ cards: [], boardCommits: { failed: 2, error: "index.lock held" } });
  const text = note().querySelector("span.commit-note-text").textContent;
  assert.match(text, /index\.lock held/, "an old refusal hides what the queue says now");
  assert.doesNotMatch(text, /no board commit queue/);
});
