// The centre column's section switcher.
//
// index.html has carried <nav id="tabs"> and a hidden #docs since the shell was
// laid down, and nothing ever filled them: the documentation section existed in
// the markup and could not be reached. These tests pin the three things that
// switcher has to get right — one section visible at a time, the tab saying
// which one, and a section built once rather than on every visit.

import { test, beforeEach, afterEach } from "node:test";
import assert from "node:assert/strict";

import { installDOM, fireEvent } from "./fake-dom.js";

let dom;
let createSections;
let tabs;
let board;
let docs;

beforeEach(async () => {
  dom = installDOM();
  tabs = dom.element("nav");
  board = dom.element("div");
  docs = dom.element("div");
  ({ createSections } = await import("../js/sections.js"));
});

afterEach(() => {
  dom.restore();
});

function sections(onShowDocs = () => {}) {
  return createSections(tabs, [
    { id: "board", label: "Board", root: board },
    { id: "docs", label: "Docs", root: docs, onFirstShow: onShowDocs },
  ]);
}

test("the first section is the one showing", () => {
  sections();

  assert.equal(board.hidden, false);
  assert.equal(docs.hidden, true);
});

test("one tab per section, labelled", () => {
  sections();

  assert.deepEqual(
    tabs.querySelectorAll("[data-section]").map((node) => node.textContent),
    ["Board", "Docs"],
  );
});

test("choosing a section shows it and hides the others", () => {
  sections();

  fireEvent(tabs.querySelector('[data-section="docs"]'), "click");

  assert.equal(docs.hidden, false);
  assert.equal(board.hidden, true);
});

// Which tab is current has to be readable by a screen reader, not only visible
// as a colour: aria-selected is what a tab control is required to carry.
test("the current tab says so, in the markup and not only in a class", () => {
  sections();
  fireEvent(tabs.querySelector('[data-section="docs"]'), "click");

  const [boardTab, docsTab] = tabs.querySelectorAll("[data-section]");
  assert.equal(docsTab.getAttribute("aria-selected"), "true");
  assert.equal(boardTab.getAttribute("aria-selected"), "false");
  assert.match(docsTab.className, /\bon\b/);
  assert.doesNotMatch(boardTab.className, /\bon\b/);
});

// The documentation section fetches when it is built. Building it at startup
// would make a panel with no documentation directories ask for them — and get a
// 404 — before the operator ever looks at that section.
test("a section is built when it is first shown, not at startup", () => {
  let built = 0;
  sections(() => {
    built += 1;
  });

  assert.equal(built, 0, "the section was built before it was ever shown");

  fireEvent(tabs.querySelector('[data-section="docs"]'), "click");
  assert.equal(built, 1);
});

test("and built once, however often it is revisited", () => {
  let built = 0;
  sections(() => {
    built += 1;
  });

  fireEvent(tabs.querySelector('[data-section="docs"]'), "click");
  fireEvent(tabs.querySelector('[data-section="board"]'), "click");
  fireEvent(tabs.querySelector('[data-section="docs"]'), "click");

  assert.equal(built, 1, "the section was rebuilt on a return visit");
});

test("the section already showing at startup is built too", () => {
  let built = 0;
  createSections(tabs, [
    { id: "board", label: "Board", root: board, onFirstShow: () => (built += 1) },
    { id: "docs", label: "Docs", root: docs },
  ]);

  assert.equal(built, 1, "the first section is visible, so it must have been built");
});

// A page whose markup lost an element must not take the rest of the switcher
// with it: the remaining sections stay usable and the missing one is reported.
test("a section whose element is missing is skipped, not fatal", () => {
  const errors = [];
  const realError = console.error;
  console.error = (...args) => errors.push(args.join(" "));
  try {
    createSections(tabs, [
      { id: "board", label: "Board", root: board },
      { id: "docs", label: "Docs", root: null },
    ]);
  } finally {
    console.error = realError;
  }

  assert.equal(tabs.querySelectorAll("[data-section]").length, 1);
  assert.equal(board.hidden, false);
  assert.match(errors.join(" "), /docs/);
});
