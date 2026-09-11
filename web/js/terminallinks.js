// web/js/terminallinks.js
//
// Wiki links in a live terminal. A session prints [[card]] as plain text, and
// the operator reads it in the orchestrator column and on the screen tab. Each
// such link that names a card becomes something to click, opening that card
// the way the board does.
//
// Which text is a link is markdown.js's rule, the one the card panel renders
// by, so a link is clickable in a terminal exactly where it is clickable on a
// card. What this module adds is where a link sits on a terminal row, which is
// counted in cells, not characters: a wide character — an emoji, a CJK letter —
// takes two cells and one character, and a range counted in characters would
// put every link after it in the wrong place.
//
// Only links on one row are found. Claude Code wraps its own lines to the
// terminal's width, so in a narrow column a long link is split across two rows
// with nothing to join them by — the break is the session's, not the
// terminal's, and the rows do not say they belong together. Such a link is not
// a link at all, rather than half a name opening the wrong card or none.
//
// Plain web addresses are not made links here, deliberately. The desktop
// window's WebKit view has no handler for a new window, so window.open does
// nothing there: an address underlined in the column would look clickable and
// do nothing in the one place the operator works.

import { WIKILINK, linkParts } from "./markdown.js";

// rowText reads one terminal row — an xterm IBufferLine — as text, and which
// cell each UTF-16 unit of that text sits in. The right half of a wide
// character is a cell of width 0 with no text of its own, and is skipped.
export function rowText(line, cols) {
  let text = "";
  const cells = [];
  for (let x = 0; x < cols; x += 1) {
    const cell = line.getCell(x);
    if (!cell) break;
    if (cell.getWidth() === 0) continue;
    const chars = cell.getChars() || " ";
    for (let i = 0; i < chars.length; i += 1) cells.push(x);
    text += chars;
  }
  return { text, cells };
}

// wikiLinksIn finds every whole [[link]] in a row's text: where it starts and
// ends in the text, and the note name it points at.
export function wikiLinksIn(text) {
  const found = [];
  for (const match of text.matchAll(WIKILINK)) {
    const { target } = linkParts(match[1]);
    if (target) found.push({ start: match.index, end: match.index + match[0].length, target });
  }
  return found;
}

// wikiLinkProvider is the xterm link provider for one terminal. `resolve`
// turns a note name into the path of the card it names, or null when no card
// has it; `open` opens a card by its path.
//
// xterm asks for one buffer row at a time, counting rows from 1, and wants
// each link's cells counted from 1 with the last one included.
export function wikiLinkProvider(terminal, { resolve, open }) {
  return {
    provideLinks(y, callback) {
      const line = terminal.buffer.active.getLine(y - 1);
      if (!line) {
        callback(undefined);
        return;
      }
      const { text, cells } = rowText(line, terminal.cols);
      const links = [];
      for (const found of wikiLinksIn(text)) {
        const path = resolve(found.target);
        if (!path) continue;
        const last = cells[found.end - 1];
        links.push({
          range: {
            start: { x: cells[found.start] + 1, y },
            end: { x: last + (line.getCell(last)?.getWidth() || 1), y },
          },
          text: text.slice(found.start, found.end),
          decorations: { pointerCursor: true, underline: true },
          activate: () => open(path),
        });
      }
      callback(links.length > 0 ? links : undefined);
    },
  };
}
