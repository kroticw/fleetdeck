// web/js/_tests/boardhead.test.js
//
// The board island's head (T-226): beside the tabs, "Find card" and "+ Card".
//
// Run with: node --test web/js/_tests/boardhead.test.js

import test from "node:test";
import assert from "node:assert/strict";
import { installDOM, fireEvent, fireDocumentEvent } from "../../tests/fake-dom.js";
import { mountBoardHead } from "../boardhead.js";

globalThis.navigator ??= { language: "en" };

function mounted() {
  const dom = installDOM();
  const row = dom.element("nav");
  dom.document.body.appendChild(row);
  const calls = [];
  const head = mountBoardHead(row, { onFind: (q) => calls.push(["find", q]), onNewCard: () => calls.push(["newCard"]) });
  return { dom, row, calls, head };
}

test("the head adds a search field and a new card button to the row", () => {
  const { dom, row, head } = mounted();
  assert.equal(head.find.tagName, "INPUT");
  assert.equal(head.find.type, "search");
  assert.ok(head.find.getAttribute("aria-label"), "the field is named for VoiceOver");
  assert.ok(row.contains(head.find) && row.contains(head.newCard));
  dom.restore();
});

test("typing finds, and the button opens the new card form", () => {
  const { dom, calls, head } = mounted();
  head.find.value = "диплом";
  fireEvent(head.find, "input");
  fireEvent(head.newCard, "click");
  assert.deepEqual(calls, [["find", "диплом"], ["newCard"]]);
  dom.restore();
});

test("Escape in the field clears what was typed", () => {
  const { dom, calls, head } = mounted();
  head.find.value = "диплом";
  fireEvent(head.find, "keydown", { key: "Escape" });
  assert.equal(head.find.value, "");
  assert.deepEqual(calls, [["find", ""]]);
  dom.restore();
});

// "/" puts the keyboard in the field, unless it is typing somewhere already: a
// terminal, a form, the field itself.
test("slash goes to the field, but not from where text is being typed", () => {
  const { dom, head } = mounted();
  const plain = fireDocumentEvent(dom.document, "keydown", { key: "/", target: dom.document.body });
  assert.equal(dom.document.activeElement, head.find);
  assert.equal(plain.defaultPrevented, true, "the slash is not typed into the field");

  dom.document.activeElement = null;
  const textarea = dom.element("textarea");
  const typed = fireDocumentEvent(dom.document, "keydown", { key: "/", target: textarea });
  assert.equal(dom.document.activeElement, null, "a slash typed into a textarea stays there");
  assert.equal(typed.defaultPrevented, false);

  const withCommand = fireDocumentEvent(dom.document, "keydown", { key: "/", metaKey: true, target: dom.document.body });
  assert.equal(withCommand.defaultPrevented, false, "a shortcut with a modifier is someone else's");
  dom.restore();
});
