// web/js/_tests/boardfind.test.js
//
// "Find card" in the board's head (T-226): the board shows the cards whose
// number or title has what was typed, whatever its case.
//
// Run with: node --test web/js/_tests/boardfind.test.js

import test from "node:test";
import assert from "node:assert/strict";
import { cardMatches, render } from "../board.js";

const card = { path: "/b/T-226.md", id: "T-226", title: "Редизайн fleetdeck", stage: "active", zone: "planned", progress: 60 };

test("a card is found by its number or its title, whatever the case", () => {
  for (const query of ["T-226", "t-226", "226", "редизайн", "FLEETDECK", "  дизайн  "]) {
    assert.equal(cardMatches(card, query), true, query);
  }
  assert.equal(cardMatches(card, "диплом"), false);
});

test("an empty query finds every card", () => {
  assert.equal(cardMatches(card, ""), true);
  assert.equal(cardMatches(card, "   "), true);
});

test("a card without a number or a title is found only by an empty query", () => {
  assert.equal(cardMatches({ path: "/b/x.md" }, "x"), false);
  assert.equal(cardMatches({ path: "/b/x.md" }, ""), true);
});

// The board drawn with a query holds only the cards it finds.
test("the board drawn with a query shows only the cards found", () => {
  let html = "";
  const root = {
    classList: { toggle: () => {} },
    scrollLeft: 0,
    clientWidth: 800,
    scrollWidth: 800,
    get innerHTML() {
      return html;
    },
    set innerHTML(value) {
      html = value;
    },
    querySelectorAll: () => [],
  };
  const other = { ...card, path: "/b/T-224.md", id: "T-224", title: "Диплом" };
  render(root, { cards: [card, other] }, { query: "диплом" });
  assert.ok(html.includes("T-224"), "the card found is drawn");
  assert.ok(!html.includes("T-226"), "the card not found is not");
});
