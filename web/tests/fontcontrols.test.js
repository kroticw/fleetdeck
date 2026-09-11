// The buttons that size a live terminal's type (web/js/fontcontrols.js): what
// they are, what they ask for, and that the end of the range is visible on
// them rather than a press that does nothing.
//
// What a press DOES is not here: the buttons only ask, and the terminal's own
// stepFont answers them — the same function Cmd+= / Cmd+- / Cmd+0 go through
// (web/tests/liveterminal.test.js pins that).

import { test, beforeEach, afterEach } from "node:test";
import assert from "node:assert/strict";

import { installDOM, fireEvent } from "./fake-dom.js";
import { buildFontControls } from "../js/fontcontrols.js";
import { t } from "../js/i18n.js";

let dom;

beforeEach(() => {
  dom = installDOM();
});

afterEach(() => {
  dom.restore();
});

function controls(buttonClass = "col-size-btn") {
  const asked = [];
  const made = buildFontControls({ onStep: (step) => asked.push(step), buttonClass });
  const smaller = made.node.querySelector(".term-font-smaller");
  const reset = made.node.querySelector(".term-font-reset");
  const bigger = made.node.querySelector(".term-font-bigger");
  return { made, asked, smaller, reset, bigger };
}

test("three buttons in order: smaller, the size itself, bigger", () => {
  const { made, smaller, reset, bigger } = controls();

  assert.ok(String(made.node.className).split(" ").includes("term-font"));
  assert.deepEqual(made.node.children, [smaller, reset, bigger]);
  for (const b of [smaller, reset, bigger]) {
    assert.equal(b.tagName, "BUTTON");
    assert.equal(b.getAttribute("type"), "button");
  }
  assert.equal(smaller.textContent, "A−");
  assert.equal(bigger.textContent, "A+");
});

test("each button says what it does and which key does the same", () => {
  const { smaller, reset, bigger } = controls();

  assert.equal(smaller.getAttribute("title"), t("terminal_font_smaller"));
  assert.equal(smaller.getAttribute("aria-label"), t("terminal_font_smaller"));
  assert.equal(bigger.getAttribute("title"), t("terminal_font_bigger"));
  assert.equal(bigger.getAttribute("aria-label"), t("terminal_font_bigger"));
  assert.equal(reset.getAttribute("title"), t("terminal_font_reset"));
  assert.equal(reset.getAttribute("aria-label"), t("terminal_font_reset"));
});

test("the buttons wear the class of the controls they sit among", () => {
  const strip = controls("col-size-btn");
  const head = controls("s-font-btn");

  for (const b of [strip.smaller, strip.reset, strip.bigger]) assert.ok(String(b.className).split(" ").includes("col-size-btn"));
  for (const b of [head.smaller, head.reset, head.bigger]) assert.ok(String(b.className).split(" ").includes("s-font-btn"));
});

test("a press asks for the same steps the keys do: -1, back to the default, +1", () => {
  const { asked, smaller, reset, bigger } = controls();

  fireEvent(smaller, "click");
  fireEvent(reset, "click");
  fireEvent(bigger, "click");

  assert.deepEqual(asked, [-1, 0, 1]);
});

// Pressing a button must not take the focus from the terminal: the next key a
// person types after making the type bigger belongs to the session, and a
// focused button would take a Space or an Enter as another press.
test("pressing a button leaves the focus where it was", () => {
  const { smaller, reset, bigger } = controls();

  for (const b of [smaller, reset, bigger]) {
    const down = fireEvent(b, "mousedown");
    assert.equal(down.defaultPrevented, true, `${b.className} took the focus on mousedown`);
  }
});

// Found live: a browser sends no mousedown to a disabled button, so a press on
// A+ at 24 px took the focus to the page and the next Cmd+- went nowhere. A
// disabled button lets the pointer through (web/app.css), and the press lands
// on the group around the buttons — which keeps the focus just the same.
test("pressing a button that is off leaves the focus where it was too", () => {
  const { made } = controls();

  const down = fireEvent(made.node, "mousedown");

  assert.equal(down.defaultPrevented, true, "a press on a button that is off took the focus from the terminal");
});

test("the size is shown, and the end of the range turns its button off", () => {
  const { made, smaller, reset, bigger } = controls();

  made.paint(15);
  assert.equal(reset.textContent, "15 px");
  assert.deepEqual([smaller.disabled, reset.disabled, bigger.disabled], [false, false, false]);

  made.paint(24);
  assert.equal(reset.textContent, "24 px");
  assert.deepEqual([smaller.disabled, reset.disabled, bigger.disabled], [false, false, true], "at 24 px");

  made.paint(9);
  assert.deepEqual([smaller.disabled, reset.disabled, bigger.disabled], [true, false, false], "at 9 px");

  made.paint(12);
  assert.deepEqual([smaller.disabled, reset.disabled, bigger.disabled], [false, true, false], "at 12 px there is nothing to go back to");
});

test("with no terminal to size, every button is off and no size is claimed", () => {
  const { made, smaller, reset, bigger } = controls();

  made.paint(null);

  assert.deepEqual([smaller.disabled, reset.disabled, bigger.disabled], [true, true, true]);
  assert.equal(reset.textContent, "— px");
});

test("before anything paints them, the buttons are off", () => {
  const { smaller, reset, bigger } = controls();

  assert.deepEqual([smaller.disabled, reset.disabled, bigger.disabled], [true, true, true]);
});
