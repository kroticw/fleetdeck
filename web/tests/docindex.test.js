// The docs list asks, for every document, which card it belongs to. Asked one
// document at a time that is documents x cards x links x documents, which on
// a real board (169 documents) kept the page busy 3.5 s out of every 3.5 s.
// The index answers the same question for all of them at once.

import { test } from "node:test";
import assert from "node:assert/strict";

import { cardsByDocument, cardsLinkingTo } from "../js/docnames.js";

const docs = [
  { path: "/d/brief.md", title: "brief.md", root: "/d" },
  { path: "/d/log.md", title: "log.md", root: "/d" },
  { path: "/d/loose.md", title: "loose.md", root: "/d" },
];
const cards = [
  { path: "/c/T-1-a.md", id: "T-1", title: "A", links: ["brief", "log"] },
  { path: "/c/T-2-b.md", id: "T-2", title: "B", links: ["log", "T-1-a"] },
];

test("the index gives each document the first card linking it, as cardsLinkingTo does", () => {
  const index = cardsByDocument(cards, docs);
  for (const doc of docs) {
    assert.equal(index.get(doc.path) ?? null, cardsLinkingTo(doc, cards, docs)[0] ?? null, doc.path);
  }
  assert.equal(index.get("/d/log.md").id, "T-1");
  assert.equal(index.has("/d/loose.md"), false);
});

test("the index is built in time a page can afford on a board of real size", () => {
  const many = Array.from({ length: 400 }, (_, i) => ({ path: `/d/doc-${i}.md`, title: `doc-${i}.md`, root: "/d" }));
  const board = Array.from({ length: 120 }, (_, i) => ({ path: `/c/T-${i}.md`, id: `T-${i}`, title: `${i}`, links: [`doc-${i}`, `doc-${i + 1}`, `doc-${i + 2}`] }));
  const start = performance.now();
  cardsByDocument(board, many);
  assert.ok(performance.now() - start < 250, "indexing took longer than a frame budget allows");
});
