// Wiki links in a live terminal (web/js/terminallinks.js).
//
// A session prints [[card]] as plain text, and the operator reads it in the
// orchestrator column and on the screen tab. Here each such link that names a
// card becomes something to click, opening that card the way the board does.
//
// Three things are pinned: which text is a link — the same rule the card panel
// renders by; where on the row it sits, counted in terminal cells rather than
// characters; and that a name no card has, or half a name the session wrapped
// onto the next row, is not a link at all rather than a link to the wrong place.

import { test } from "node:test";
import assert from "node:assert/strict";

import { rowText, wikiLinksIn, wikiLinkProvider } from "../js/terminallinks.js";

// A terminal row as xterm's IBufferLine hands it out: one cell per column, a
// wide character taking two, the second of which has width 0 and no text.
function line(cells) {
  const out = [];
  for (const c of cells) {
    if (c === "😀") {
      out.push({ chars: c, width: 2 }, { chars: "", width: 0 });
    } else {
      out.push({ chars: c, width: 1 });
    }
  }
  return {
    getCell: (x) => (x < out.length ? { getChars: () => out[x].chars, getWidth: () => out[x].width } : undefined),
  };
}

const row = (s, cols = s.length + 4) => line([...s.padEnd(cols, " ")]);

test("a row is read as its text, with the cell each character sits in", () => {
  const { text, cells } = rowText(line(["a", "😀", "b"]), 4);
  assert.equal(text, "a😀b");
  // "😀" is two UTF-16 units in one cell pair; "b" is in the cell after the pair.
  assert.deepEqual(cells, [0, 1, 1, 3]);
});

test("the links in a row are found by the rule the card panel renders by", () => {
  const found = wikiLinksIn("see [[plan-a]] and [[plan-b|the other one]] and [[plan-c#section]]");
  assert.deepEqual(found.map((f) => f.target), ["plan-a", "plan-b", "plan-c"]);
  assert.deepEqual(found[0], { start: 4, end: 14, target: "plan-a" });
});

// Claude Code wraps its own lines to the terminal's width, so a long link in a
// narrow column is split across two rows with nothing to join them by. Neither
// half is a link.
test("half a link on either row is not a link", () => {
  assert.deepEqual(wikiLinksIn("  [[2026-09-11-fleetdeck-te"), []);
  assert.deepEqual(wikiLinksIn("  rminal-instead-of-feed]]"), []);
  assert.deepEqual(wikiLinksIn("[[]] and [[ | alias]]"), [], "a link with no name is not a link");
});

// What the terminal hands a provider and what it gets back: rows counted from 1,
// cells counted from 1, the end cell included.
function terminal(rows, cols = 40) {
  return { cols, buffer: { active: { getLine: (y) => rows[y] } } };
}

function provide(provider, y) {
  let got = "never answered";
  provider.provideLinks(y, (links) => (got = links));
  return got;
}

const cards = { "plan-a": "/board/cards/plan-a.md", "plan-b": "/board/cards/plan-b.md" };
const resolve = (name) => cards[name] ?? null;

test("a link that names a card covers its own cells and opens that card", () => {
  const opened = [];
  const provider = wikiLinkProvider(terminal([row("  see [[plan-a]] now")]), { resolve, open: (p) => opened.push(p) });

  const links = provide(provider, 1);

  assert.equal(links.length, 1);
  assert.deepEqual(links[0].range, { start: { x: 7, y: 1 }, end: { x: 16, y: 1 } }, "the brackets included, counted from 1");
  assert.equal(links[0].text, "[[plan-a]]");
  links[0].activate(new Event("click"), links[0].text);
  assert.deepEqual(opened, ["/board/cards/plan-a.md"]);
});

test("a wide character before a link moves it by the cells it takes, not by its characters", () => {
  const provider = wikiLinkProvider(terminal([line([..."😀 [[plan-a]]    "])]), { resolve, open: () => {} });

  const [link] = provide(provider, 1);

  // The emoji takes cells 1 and 2, the space cell 3, so the link starts at 4.
  assert.deepEqual(link.range, { start: { x: 4, y: 1 }, end: { x: 13, y: 1 } });
});

// A name no card has would be a link to nowhere. The card panel shows such a
// link as broken; the terminal cannot mark it, so it leaves it plain text.
test("a name no card has is not a link", () => {
  const provider = wikiLinkProvider(terminal([row("[[plan-a]] [[nobody]] [[plan-b]]")]), { resolve, open: () => {} });

  const links = provide(provider, 1);

  assert.deepEqual(links.map((l) => l.text), ["[[plan-a]]", "[[plan-b]]"]);
});

test("a row with no link answers with nothing, and a row that is not there too", () => {
  const provider = wikiLinkProvider(terminal([row("plain text")]), { resolve, open: () => {} });
  assert.equal(provide(provider, 1), undefined);
  assert.equal(provide(provider, 5), undefined);
});

test("the row asked for is the one counted from 1", () => {
  const provider = wikiLinkProvider(terminal([row("first"), row("[[plan-b]]")]), { resolve, open: () => {} });
  assert.equal(provide(provider, 1), undefined);
  assert.equal(provide(provider, 2)[0].range.start.y, 2);
});
