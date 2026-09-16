// The band the fleetdeck window is dragged by, measured over this page's own
// markup and rules rather than over a made-up one (web/js/_tests/topband.test.js
// is that unit).
//
// v0.12.0 on the operator's machine: with a session open the window could not be
// moved at all. The session view itself was not what took the band away -- it
// begins below the board's top inset -- but #sheet-scrim did: the dimmed board
// under an open sheet is an element covering the whole centre column from its
// very top, and freeTopHeight stops at the first row holding anything that is
// not ground. One point at y = 0 took the band from the whole width, including
// the room beside the sheet where nothing is drawn at all.
//
// The test builds the top of the board page as the window sees it -- the boxes
// index.html puts in the centre column, placed by the rules web/app.css gives
// them under :root[data-surface="board"] -- and runs the page's own
// freeTopHeight over it.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { TOP_BAND_MAX, freeTopHeight, isGround } from "../js/topband.js";

const html = readFileSync(new URL("../index.html", import.meta.url), "utf8");
const css = readFileSync(new URL("../app.css", import.meta.url), "utf8").replace(/\/\*[\s\S]*?\*\//g, "");

const BOARD = ':root[data-surface="board"]';
// The window's own size on the operator's machine, and the insets it sends for
// it are the ones web/app.css names as its fallbacks: the same numbers a page
// lays itself out with before the window's first message arrives.
const WIDTH = 1512;

// The body of the rule whose selector list holds selector, as
// web/tests/board-columns-css.test.js reads one.
function ruleBody(selector) {
  for (let from = 0; ; ) {
    const at = css.indexOf(selector, from);
    if (at < 0) throw new Error(`web/app.css has no rule for ${selector}`);
    const open = css.indexOf("{", at);
    const tail = css.slice(at + selector.length, open).trim();
    const before = css.slice(css.lastIndexOf("}", at) + 1, at).trim();
    if ((tail === "" || tail.startsWith(",")) && (before === "" || before.endsWith(","))) {
      return css.slice(open + 1, css.indexOf("}", open));
    }
    from = at + selector.length;
  }
}

// A length as the rules write it: a plain px, a var() with its fallback, or a
// calc of the two. Only the forms the rules read here actually use.
function px(value) {
  const text = value.trim();
  const calc = /^calc\((.+)\)$/.exec(text);
  if (calc) {
    const [, left, sign, right] = /^(.+?)\s([-+])\s(\S+)$/.exec(calc[1]);
    return px(left) + (sign === "-" ? -1 : 1) * px(right);
  }
  const fallback = /^var\(--[\w-]+,\s*(.+)\)$/.exec(text);
  if (fallback) return px(fallback[1]);
  if (text === "0") return 0;
  const number = /^(-?[\d.]+)px$/.exec(text);
  if (!number) throw new Error(`not a length this test can read: ${value}`);
  return Number(number[1]);
}

function declaration(body, name) {
  const found = new RegExp(`(?:^|;)\\s*${name}\\s*:\\s*([^;]+)`).exec(body);
  if (!found) throw new Error(`no ${name} in ${body.trim()}`);
  return found[1].trim();
}

// The centre column's children, in the order index.html lays them out, each with
// the id it carries and whether it is marked as the page's ground. The order is
// the paint order among boxes of the same z-index, so the last one at a point
// wins.
function centreColumnChildren() {
  const column = html.slice(html.indexOf('<section id="center"'), html.indexOf("</section>", html.indexOf('<section id="center"')));
  return [...column.matchAll(/<(\w+) id="([\w-]+)"([^>]*)>/g)]
    .slice(1)
    .map(([, tagName, id, rest]) => ({ tagName: tagName.toUpperCase(), id, ground: rest.includes("data-window-ground") }));
}

const children = centreColumnChildren();
const child = (id) => {
  const found = children.find((c) => c.id === id);
  if (!found) throw new Error(`web/index.html has no #${id} in the centre column`);
  return { ...found, hasAttribute: (name) => found.ground && name === "data-window-ground" };
};

// The centre column itself: the page's ground, and in the window the whole of it
// (web/js/main.js removes the side columns from the board's surface).
const CENTRE = { tagName: "SECTION", id: "center", hasAttribute: (name) => name === "data-window-ground" };

// What the window sees at a point of the board page with the named sheet open,
// or none. The boxes are placed by web/app.css's own numbers: the scrim over the
// whole column, the sheet below the board's top inset less 4 pt.
function pageWithOpen(sheet) {
  const scrim = child("sheet-scrim");
  const scrimInset = px(declaration(ruleBody(`${BOARD} #sheet-scrim`), "inset"));
  const sheetRule = ruleBody(`${BOARD} #card-panel`);
  const sheetTop = px(declaration(sheetRule, "top"));
  const sheetLeft = px(declaration(sheetRule, "left"));
  return (x, y) => {
    if (sheet && y >= sheetTop && x >= sheetLeft) return child(sheet);
    if (sheet && y >= scrimInset) return scrim;
    // No sheet: the board's own box, which carries the padding of the top inset,
    // and the column beside it.
    return y >= 0 && x >= 0 ? (sheet ? scrim : child("board")) : CENTRE;
  };
}

const SHEETS = ["session-panel", "card-panel", "reader-panel"];

test("with nothing open the band is the whole of the board's top inset", () => {
  assert.equal(freeTopHeight(pageWithOpen(null), WIDTH), TOP_BAND_MAX);
});

// The defect. The band has to be there, and it has to be there across the whole
// width: the window is given one height for all of it.
test("with a sheet open the window still has a band to be dragged by", () => {
  for (const sheet of SHEETS) {
    const band = freeTopHeight(pageWithOpen(sheet), WIDTH);
    assert.ok(band > 0, `with #${sheet} open the band is ${band} pt tall: the window cannot be moved`);
    // The sheet's own top, 4 pt above the board's inset: the band reaches down
    // to the sheet and no further.
    assert.equal(band, TOP_BAND_MAX - 4, `with #${sheet} open`);
  }
});

// The other half: nothing of a sheet may end up under the band, or a press meant
// for the session's close button drags the window instead.
test("the band never reaches the sheet itself", () => {
  for (const sheet of SHEETS) {
    const at = pageWithOpen(sheet);
    const band = freeTopHeight(at, WIDTH);
    const sheetLeft = px(declaration(ruleBody(`${BOARD} #card-panel`), "left"));
    assert.equal(isGround(at(sheetLeft + 1, band)), false, `#${sheet} does not begin where the band ends`);
    for (let y = 0; y < band; y += 4) {
      assert.notEqual(at(sheetLeft + 1, y).id, sheet, `#${sheet} is under the band at y = ${y}`);
    }
  }
});

// Why there is a band at all: the dimmed board is the board, drawn darker. It
// takes a press and does nothing with it, so it is ground, and index.html marks
// it so. If the scrim is ever given something to do with a press -- closing the
// sheet, say -- this mark has to go and the band has to be found another way.
test("the dimmed board under a sheet is the page's ground", () => {
  assert.equal(child("sheet-scrim").ground, true, "#sheet-scrim in web/index.html is not marked data-window-ground");
  assert.equal(isGround(child("sheet-scrim")), true);
});

// Everything above is the window's. In a browser tab no script measures a band
// and the scrim is not drawn at all: the mark is an attribute nothing there
// reads.
test("a browser tab is left as it was", () => {
  assert.match(ruleBody("#sheet-scrim"), /display:\s*none/);
  assert.match(ruleBody(`${BOARD} #sheet-scrim`), /position:\s*absolute/);
});
