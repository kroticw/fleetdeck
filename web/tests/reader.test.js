// The document reader: one document, opened over the board from a card.
//
// What it is easy to get wrong: painting the body of a document the operator
// has already left, putting a file name from disk into the page as markup, and
// losing the way back to the card. The reader exists because a document is
// read in the context of the work it belongs to, so the cards linking to it are
// as much a part of it as its body.

import { test, beforeEach, afterEach } from "node:test";
import assert from "node:assert/strict";

import { installDOM, fireEvent, fireDocumentEvent, settle } from "./fake-dom.js";
import { t } from "../js/i18n.js";

const REPORT = {
  path: "/board/docs/reports/2026-09-12-report.md",
  title: "reports/2026-09-12-report.md",
  root: "/board/docs",
};
const LONELY = { path: "/board/docs/reports/lonely.md", title: "reports/lonely.md", root: "/board/docs" };
const DOCS = [REPORT, LONELY];

function snapshot() {
  return {
    cards: [
      { path: "/board/cards/T-004-fleet-ui.md", id: "T-004", title: "Fleet UI", links: ["2026-09-12-report"] },
      { path: "/board/cards/T-011-keeping.md", id: "T-011", title: "Card keeping", links: ["T-004-fleet-ui"] },
    ],
  };
}

function fakeStore(initial) {
  const listeners = new Set();
  return {
    subscribe(fn) {
      listeners.add(fn);
      fn(initial, true);
      return () => listeners.delete(fn);
    },
    push(snap) {
      for (const fn of [...listeners]) fn(snap, true);
    },
    get count() {
      return listeners.size;
    },
  };
}

function deferred() {
  let release;
  const promise = new Promise((resolve) => {
    release = resolve;
  });
  return { promise, release: (value) => release(value) };
}

let dom;
let renderReader;
let createReader;

beforeEach(async () => {
  dom = installDOM();
  ({ renderReader, createReader } = await import("../js/reader.js"));
});

afterEach(() => {
  dom.restore();
});

function open(path = REPORT.path, overrides = {}) {
  const root = dom.element("div");
  dom.document.body.appendChild(root);
  const store = fakeStore(overrides.snapshot ?? snapshot());
  const closed = [];
  const opened = [];
  const dispose = renderReader(root, path, () => closed.push(true), {
    subscribe: store.subscribe,
    listDocs: async () => DOCS,
    fetchDoc: async () => "# Report\n\nBody.\n",
    onOpenCard: (card) => opened.push(card),
    ...overrides,
  });
  return { root, store, closed, opened, dispose };
}

// A control put into the body by hand. The fake DOM stores innerHTML without
// parsing it, so the links markdown.js renders are not nodes here; the click
// delegation they rely on is the same for a node appended like this one.
function linkInBody(root, attribute, value) {
  const link = dom.element("button");
  link.dataset[attribute] = value;
  root.querySelector(".reader-body").appendChild(link);
  return link;
}

test("the document says it is opening, then shows its body", async () => {
  const gate = deferred();
  const { root } = open(REPORT.path, { fetchDoc: () => gate.promise });
  assert.match(root.textContent, new RegExp(t("doc_opening")));

  gate.release("# Report\n\nBody.\n");
  await settle();

  assert.match(root.querySelector(".reader-body").innerHTML, /<h2>Report<\/h2>/);
});

test("the cards the document belongs to are listed by number and open with one click", async () => {
  const { root, opened } = open();
  await settle();

  const cards = root.querySelectorAll(".doc-card");
  assert.equal(cards.length, 1);
  assert.match(cards[0].textContent, /T-004/);
  assert.match(cards[0].textContent, /Fleet UI/);

  fireEvent(cards[0], "click");
  assert.deepEqual(opened, ["/board/cards/T-004-fleet-ui.md"]);
});

test("a document no card links to shows no cards row at all", async () => {
  const { root } = open(LONELY.path);
  await settle();

  assert.equal(root.querySelectorAll(".doc-card").length, 0);
  assert.ok(!root.textContent.includes(t("doc_cards")), "a heading over nothing reads as a panel that lost something");
});

test("the title comes from disk and is text, never markup", async () => {
  const hostile = { path: "/board/docs/<b>x</b>.md", title: "<b>x</b>.md", root: "/board/docs" };
  const { root } = open(hostile.path, { listDocs: async () => [hostile] });
  await settle();

  const title = root.querySelector(".reader-title");
  assert.equal(title.textContent, "<b>x</b>");
  assert.equal(title.children.length, 0);
});

test("a document that cannot be opened says so, with the server's reason", async () => {
  const { root } = open(REPORT.path, {
    fetchDoc: async () => {
      throw new Error("documents are served only from the configured documentation roots");
    },
  });
  await settle();

  assert.match(root.textContent, new RegExp(t("doc_open_failed")));
  assert.match(root.textContent, /configured documentation roots/);
});

test("a new snapshot redraws the cards and never fetches the document again", async () => {
  let fetches = 0;
  const { root, store } = open(REPORT.path, {
    fetchDoc: async () => {
      fetches += 1;
      return "# Report\n";
    },
  });
  await settle();

  const next = snapshot();
  next.cards[1].links = ["2026-09-12-report"];
  store.push(next);
  await settle();

  assert.equal(fetches, 1);
  assert.equal(root.querySelectorAll(".doc-card").length, 2);
  assert.match(root.querySelector(".reader-body").innerHTML, /<h2>Report<\/h2>/);
});

test("a card link inside the document opens that card", async () => {
  const { root, opened } = open();
  await settle();

  fireEvent(linkInBody(root, "link", "T-011-keeping"), "click");

  assert.deepEqual(opened, ["/board/cards/T-011-keeping.md"]);
});

test("a document link inside the document opens that document in the same reader", async () => {
  const paths = [];
  const { root } = open(REPORT.path, {
    fetchDoc: async (path) => {
      paths.push(path);
      return "# Doc\n";
    },
  });
  await settle();

  fireEvent(linkInBody(root, "doc", "lonely"), "click");
  await settle();

  assert.deepEqual(paths, [REPORT.path, LONELY.path]);
  assert.equal(root.querySelector(".reader-title").textContent, "reports/lonely");
});

test("a slow answer for a document the reader has left never lands", async () => {
  const slow = deferred();
  const { root } = open(REPORT.path, {
    fetchDoc: (path) => (path === REPORT.path ? slow.promise : Promise.resolve("# Lonely\n")),
  });
  await settle();

  fireEvent(linkInBody(root, "doc", "lonely"), "click");
  await settle();
  slow.release("# Report\n");
  await settle();

  assert.match(root.querySelector(".reader-body").innerHTML, /Lonely/);
  assert.ok(!/Report/.test(root.querySelector(".reader-body").innerHTML));
});

test("Escape and the close button both close the reader", () => {
  const first = open();
  fireDocumentEvent(dom.document, "keydown", { key: "Escape" });
  assert.equal(first.closed.length, 1);
  first.dispose();

  const second = open();
  fireEvent(second.root.querySelector(".reader-close"), "click");
  assert.equal(second.closed.length, 1);
});

test("disposing stops the reader listening to anything", () => {
  const { store, closed, dispose } = open();
  dispose();

  assert.equal(store.count, 0);
  fireDocumentEvent(dom.document, "keydown", { key: "Escape" });
  assert.equal(closed.length, 0);
});

test("opening shows the panel, a second open replaces the first, closing hides it", () => {
  const panel = dom.element("div");
  dom.document.body.appendChild(panel);
  panel.hidden = true;
  const store = fakeStore(snapshot());
  const reader = createReader(panel, {
    subscribe: store.subscribe,
    listDocs: async () => DOCS,
    fetchDoc: async () => "# x\n",
  });

  reader.open(REPORT.path);
  assert.equal(panel.hidden, false);
  reader.open(LONELY.path);
  assert.equal(store.count, 1, "the first reader was left subscribed");
  reader.close();
  assert.equal(panel.hidden, true);
  assert.equal(store.count, 0);
});
