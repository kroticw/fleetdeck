// Links from a card to a document, and back from the document to its cards.
//
// The rules are small and every one of them is about not showing the wrong
// thing: a link that names a card stays the card's, a name two documents share
// opens neither of them, and a card lists each of its documents once.

import { test } from "node:test";
import assert from "node:assert/strict";

import { brokenLinksOf, docForLink, documentsOf, cardsLinkingTo } from "../js/docnames.js";
import { renderMarkdown } from "../js/markdown.js";

const REPORT = {
  path: "/board/docs/reports/2026-09-12-report.md",
  title: "reports/2026-09-12-report.md",
  root: "/board/docs",
};
const OTHER = {
  path: "/board/docs/reports/2026-09-13-other.md",
  title: "reports/2026-09-13-other.md",
  root: "/board/docs",
};
const OLD_OTHER = {
  path: "/board/docs/old/2026-09-13-other.md",
  title: "old/2026-09-13-other.md",
  root: "/board/docs",
};
const DOCS = [REPORT, OTHER, OLD_OTHER];

const CARD_A = {
  path: "/board/cards/T-001-a.md",
  id: "T-001",
  title: "A",
  links: ["2026-09-12-report", "T-002-b", "reports/2026-09-13-other", "2026-09-12-report", "nowhere"],
};
const CARD_B = {
  path: "/board/cards/T-002-b.md",
  id: "T-002",
  title: "B",
  links: ["docs/reports/2026-09-12-report"],
};
const CARD_C = { path: "/board/cards/T-003-c.md", id: "T-003", title: "C", links: null };
const CARDS = [CARD_A, CARD_B, CARD_C];

test("a document is found by its name, by a longer tail of its path, and through its root", () => {
  assert.equal(docForLink(DOCS, "2026-09-12-report"), REPORT);
  assert.equal(docForLink(DOCS, "reports/2026-09-12-report"), REPORT);
  assert.equal(docForLink(DOCS, "docs/reports/2026-09-12-report"), REPORT);
});

test("a name that is not a document's is not one", () => {
  assert.equal(docForLink(DOCS, "nowhere"), null);
  assert.equal(docForLink(DOCS, "12-report"), null, "a tail is whole path segments, not the end of a name");
  assert.equal(docForLink(DOCS, ""), null);
  assert.equal(docForLink(null, "2026-09-12-report"), null);
});

test("a name two documents share opens neither of them", () => {
  assert.equal(docForLink(DOCS, "2026-09-13-other"), null);
  assert.equal(docForLink(DOCS, "old/2026-09-13-other"), OLD_OTHER);
});

test("a card's documents come in the order it links them, each once, without its card links", () => {
  assert.deepEqual(documentsOf(CARD_A, CARDS, DOCS), [REPORT, OTHER]);
});

test("a link naming a card stays the card's even when a document has the same name", () => {
  const clash = { path: "/board/docs/T-002-b.md", title: "T-002-b.md", root: "/board/docs" };
  assert.deepEqual(documentsOf(CARD_A, CARDS, [...DOCS, clash]), [REPORT, OTHER]);
});

test("a card without links, or without a documentation list, has no documents", () => {
  assert.deepEqual(documentsOf(CARD_C, CARDS, DOCS), []);
  assert.deepEqual(documentsOf(CARD_A, CARDS, null), []);
});

test("a link that opens nothing is named, once, and a card link is never one", () => {
  // "2026-09-13-other" fits two documents, so it opens neither: to the reader
  // that is the same link that goes nowhere.
  const card = { ...CARD_A, links: ["T-002-b", "nowhere", "2026-09-13-other", "2026-09-12-report", "nowhere"] };
  assert.deepEqual(brokenLinksOf(card, CARDS, DOCS), ["nowhere", "2026-09-13-other"]);
});

test("without a documentation list no link is called broken", () => {
  // Not knowing the documents is not knowing a link is broken.
  assert.deepEqual(brokenLinksOf(CARD_A, CARDS, null), []);
  assert.deepEqual(brokenLinksOf(CARD_C, CARDS, DOCS), []);
});

test("a document lists every card that links it, however the link is written", () => {
  assert.deepEqual(cardsLinkingTo(REPORT, CARDS, DOCS), [CARD_A, CARD_B]);
  assert.deepEqual(cardsLinkingTo(OLD_OTHER, CARDS, DOCS), []);
});

test("a link to a document renders as a control carrying its name", () => {
  const html = renderMarkdown("See [[2026-09-12-report|the report]].", new Set(), new Set(["2026-09-12-report"]));
  assert.match(
    html,
    /<button type="button" class="wikilink wikilink-doc" data-doc="2026-09-12-report">the report<\/button>/,
  );
});

test("a card link wins over a document link of the same name", () => {
  const html = renderMarkdown("[[same]]", new Set(["same"]), new Set(["same"]));
  assert.match(html, /data-link="same"/);
  assert.ok(!html.includes("data-doc"), html);
});

test("a name that is neither a card nor a document stays a link that does not work", () => {
  const html = renderMarkdown("[[nowhere]]", new Set(), new Set(["2026-09-12-report"]));
  assert.match(html, /wikilink-missing/);
  assert.ok(!html.includes("data-doc"), html);
});

test("anything with has() decides which names are documents", () => {
  const html = renderMarkdown("[[a]] [[b]]", new Set(), { has: (name) => name === "b" });
  assert.match(html, /data-doc="b"/);
  assert.ok(!html.includes('data-doc="a"'), html);
});

test("a document name cannot break out of the attribute it lands in", () => {
  const html = renderMarkdown('[[x" onclick="alert(1)]]', new Set(), { has: () => true });
  assert.ok(!html.includes('" onclick="'), html);
});
