// A board document's body without its frontmatter: what the card's document
// tab, the reader and the documentation section draw (T-091). The frontmatter
// names the session that wrote the document; it is read for that
// (internal/board DocSession) and is not text for the operator to read.

import { test } from "node:test";
import assert from "node:assert/strict";

import { documentBody } from "../js/docnames.js";

test("a document's frontmatter is not part of its body", () => {
  const text = "---\nsession: e62e1d58\ncards: [T-090]\n---\n\n# Report\n\nThe questions.\n";
  assert.equal(documentBody(text), "\n# Report\n\nThe questions.\n");
});

test("a document without a frontmatter keeps every line, its first one too", () => {
  for (const text of ["# Report\n\nText.\n", "First line\n---\nnot a frontmatter\n---\n", ""]) {
    assert.equal(documentBody(text), text);
  }
});

test("a rule longer than three dashes at the top opens no frontmatter", () => {
  const text = "----\nsession: e62e1d58\n----\n# Report\n";
  assert.equal(documentBody(text), text);
});

test("a frontmatter never closed is left in, for the document to show what is wrong", () => {
  const text = "---\nsession: e62e1d58\n# Report\n";
  assert.equal(documentBody(text), text);
});

test("CRLF line ends and a frontmatter that ends the file are cut too", () => {
  assert.equal(documentBody("---\r\nsession: e62e1d58\r\n---\r\n# Report\r\n"), "# Report\r\n");
  assert.equal(documentBody("---\nsession: e62e1d58\n---"), "");
});

test("only the frontmatter at the very top is cut, not a rule pair further down", () => {
  const text = "# Report\n\n---\nsession: e62e1d58\n---\n";
  assert.equal(documentBody(text), text);
});

test("anything that is not text is an empty body", () => {
  assert.equal(documentBody(undefined), "");
  assert.equal(documentBody(null), "");
});
