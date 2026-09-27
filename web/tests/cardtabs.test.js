// A card's tabs: the card itself, then every document it links to, each with the
// state of the session that wrote it (T-091).

import { test, beforeEach, afterEach } from "node:test";
import assert from "node:assert/strict";

import { installDOM, fireEvent } from "./fake-dom.js";
import { t } from "../js/i18n.js";

const DOCS = [
  { path: "/d/reports/doc-a.md", title: "reports/doc-a.md", root: "/d", session: "a41c09d2" },
  { path: "/d/reports/doc-b.md", title: "reports/doc-b.md", root: "/d" },
  { path: "/d/reports/doc-c.md", title: "reports/doc-c.md", root: "/d" },
];
const CARD = { path: "/b/cards/T-085.md", session: "909bf9b2", links: ["doc-b", "missing", "doc-a", "doc-b", "T-001"] };
const CARDS = [CARD, { path: "/b/cards/T-001.md", id: "T-001", links: [] }];

let dom;
let tabsOf;
let renderTabs;

beforeEach(async () => {
  dom = installDOM();
  ({ tabsOf, renderTabs } = await import("../js/cardtabs.js"));
});

afterEach(() => dom.restore());

test("the card comes first, then its documents in the order the card links them, once each", () => {
  const tabs = tabsOf(CARD, CARDS, DOCS);
  assert.deepEqual(
    tabs.map((tab) => tab.key),
    ["card", "/d/reports/doc-b.md", "/d/reports/doc-a.md"],
  );
  assert.equal(tabs[1].doc, DOCS[1]);
});

test("a link to nothing, and a link to another card, is not a tab", () => {
  const keys = tabsOf(CARD, CARDS, DOCS).map((tab) => tab.key);
  assert.ok(!keys.some((k) => k.includes("missing")));
  assert.ok(!keys.some((k) => k.includes("T-001")));
});

test("before the documents are listed the card is the only tab", () => {
  assert.deepEqual(tabsOf(CARD, CARDS, null).map((tab) => tab.key), ["card"]);
});

function draw(options = {}) {
  const root = dom.element("div");
  const picked = [];
  const handle = renderTabs(root, tabsOf(CARD, CARDS, DOCS), {
    active: "card",
    stateOf: (tab) => (tab.doc === DOCS[0] ? "waiting" : "working"),
    onPick: (key) => picked.push(key),
    ...options,
  });
  return { root, picked, handle };
}

test("the tabs are a tab list, one selected", () => {
  const { root } = draw({ active: "/d/reports/doc-a.md" });
  assert.equal(root.getAttribute("role"), "tablist");
  const tabs = root.querySelectorAll(".card-tab");
  assert.equal(tabs.length, 3);
  assert.deepEqual(
    tabs.map((b) => b.getAttribute("aria-selected")),
    ["false", "false", "true"],
  );
  assert.ok(tabs.every((b) => b.getAttribute("role") === "tab"));
  assert.equal(tabs[0].textContent, t("card_tab_card"));
});

test("a document's tab carries its author's state on a dot, and says it in words", () => {
  const { root } = draw();
  const tabs = root.querySelectorAll(".card-tab");
  const dot = tabs[2].querySelector(".card-tab-dot");
  assert.equal(dot.dataset.state, "waiting");
  assert.match(tabs[2].getAttribute("aria-label"), new RegExp(t("author_state_waiting")));
  assert.equal(tabs[0].querySelector(".card-tab-dot"), null, "the card's own tab has no dot");
});

test("a document's tab is named after the document, the full name in its title", () => {
  const { root } = draw();
  const tab = root.querySelectorAll(".card-tab")[1];
  assert.match(tab.textContent, /doc-b/);
  assert.equal(tab.getAttribute("title"), "reports/doc-b");
});

test("a press on a tab picks it", () => {
  const { root, picked } = draw();
  fireEvent(root.querySelectorAll(".card-tab")[2], "click");
  assert.deepEqual(picked, ["/d/reports/doc-a.md"]);
});

test("the arrow keys pick the neighbouring tab and stop at the ends", () => {
  const { root, picked } = draw({ active: "/d/reports/doc-b.md" });
  fireEvent(root, "keydown", { key: "ArrowRight" });
  fireEvent(root, "keydown", { key: "ArrowLeft" });
  assert.deepEqual(picked, ["/d/reports/doc-a.md", "card"]);
  const last = draw({ active: "/d/reports/doc-a.md" });
  fireEvent(last.root, "keydown", { key: "ArrowRight" });
  const first = draw({ active: "card" });
  fireEvent(first.root, "keydown", { key: "ArrowLeft" });
  assert.deepEqual([...last.picked, ...first.picked], []);
});

test("a row drawn again answers an arrow key once, from where the latest drawing stands", () => {
  const root = dom.element("div");
  const picked = [];
  const tabs = tabsOf(CARD, CARDS, DOCS);
  renderTabs(root, tabs, { active: "card", onPick: (key) => picked.push(key) });
  renderTabs(root, tabs, { active: "/d/reports/doc-b.md", onPick: (key) => picked.push(key) });
  fireEvent(root, "keydown", { key: "ArrowRight" });
  assert.deepEqual(picked, ["/d/reports/doc-a.md"]);
});

test("tabs past the row's right edge are counted on a button that lists every tab", () => {
  // The row is 300 wide: the card's tab and the first document's fit, the last does not.
  const { root, picked } = draw({ measure: () => ({ row: 300, tabs: [{ right: 90 }, { right: 250 }, { right: 410 }] }) });
  const more = root.querySelector(".card-tabs-more");
  assert.ok(more, "a button for the tabs out of sight");
  assert.equal(more.textContent, t("card_tabs_more").replace("{n}", "1"));
  fireEvent(more, "click");
  const menu = root.querySelector(".card-tabs-menu");
  assert.ok(menu && !menu.hidden);
  const items = menu.querySelectorAll(".card-tabs-item");
  assert.equal(items.length, 3);
  fireEvent(items[2], "click");
  assert.deepEqual(picked, ["/d/reports/doc-a.md"]);
});

test("no such button when every tab fits", () => {
  const { root } = draw({ measure: () => ({ row: 600, tabs: [{ right: 90 }, { right: 250 }, { right: 410 }] }) });
  assert.equal(root.querySelector(".card-tabs-more"), null);
});
