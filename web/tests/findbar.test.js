// The find bar over a document (Cmd+F in the docs tab and the card's document
// tab): what matches, the count it says, and Enter, Shift+Enter and Escape.

import { test, beforeEach, afterEach } from "node:test";
import assert from "node:assert/strict";

import { installDOM, fireEvent } from "./fake-dom.js";

let dom;
let findbar;

beforeEach(async () => {
  dom = installDOM();
  findbar = await import("../js/findbar.js");
});

afterEach(() => dom.restore());

test("matches are found whatever their case, and never overlap", () => {
  assert.deepEqual(findbar.matchOffsets("Ядро и ядра, ЯДР", "ядр"), [[0, 3], [7, 10], [13, 16]]);
  assert.deepEqual(findbar.matchOffsets("aaaa", "aa"), [[0, 2], [2, 4]]);
  assert.deepEqual(findbar.matchOffsets("text", ""), []);
});

// A highlighter that records what the bar asks of it, standing in for the
// CSS highlights a browser draws.
function recorder(count) {
  const calls = [];
  return {
    calls,
    search: (root, query) => {
      calls.push(["search", query]);
      return query ? count : 0;
    },
    show: (index) => calls.push(["show", index]),
    clear: () => calls.push(["clear"]),
  };
}

function typeInto(input, value) {
  input.value = value;
  fireEvent(input, "input");
}

test("typing counts the matches and shows the first; Enter and Shift+Enter walk them round", () => {
  const highlighter = recorder(3);
  const bar = findbar.createFindBar({ target: () => dom.element("article"), highlighter });
  bar.open();
  const input = bar.element.querySelector(".find-input");
  typeInto(input, "ядр");
  assert.equal(bar.element.querySelector(".find-count").textContent.startsWith("1"), true);
  assert.deepEqual(highlighter.calls.at(-1), ["show", 0]);

  fireEvent(input, "keydown", { key: "Enter" });
  assert.deepEqual(highlighter.calls.at(-1), ["show", 1]);
  fireEvent(input, "keydown", { key: "Enter", shiftKey: true });
  fireEvent(input, "keydown", { key: "Enter", shiftKey: true });
  assert.deepEqual(highlighter.calls.at(-1), ["show", 2], "Shift+Enter from the first goes round to the last");
});

test("nothing found says so, and Escape closes the bar and clears the marks", () => {
  const highlighter = recorder(0);
  const bar = findbar.createFindBar({ target: () => dom.element("article"), highlighter });
  bar.open();
  const input = bar.element.querySelector(".find-input");
  typeInto(input, "zzz");
  assert.equal(bar.element.querySelector(".find-count").textContent, "0");
  fireEvent(input, "keydown", { key: "Escape" });
  assert.equal(bar.element.hidden, true);
  assert.deepEqual(highlighter.calls.at(-1), ["clear"]);
});

test("Cmd+F opens the bar only while its document is on screen", () => {
  const highlighter = recorder(1);
  let visible = false;
  const bar = findbar.createFindBar({ target: () => dom.element("article"), highlighter, active: () => visible });
  let prevented = false;
  const key = { key: "f", metaKey: true, preventDefault: () => (prevented = true) };
  bar.onKey(key);
  assert.equal(bar.element.hidden, true);
  assert.equal(prevented, false, "a hidden document must leave Cmd+F alone");
  visible = true;
  bar.onKey(key);
  assert.equal(bar.element.hidden, false);
  assert.equal(prevented, true);
});
