// web/js/doccards.js
//
// The row over an open document naming the cards that link to it. Drawn in
// both places a document is read — the reader over the board (reader.js) and
// the documentation section (docs.js) — by this one builder, so the way back
// from a document to its task looks and works the same wherever the document
// was opened.
//
// Each card is its number and its title. The number keeps the chip it has on
// the board (.knum, see cardnumber.js): it is the identifier a person says out
// loud and will look for.
//
// Built with createElement and textContent: a card's title is text an agent
// wrote, and a title is not markup.

import { noteName } from "./docnames.js";
import { t } from "./i18n.js";

function el(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}

// docCardsRow returns the row, or null when no card links to the document. No
// row rather than a label over nothing: an empty "cards:" reads as a panel that
// lost them, while plenty of documents on a board belong to no card at all.
export function docCardsRow(cards, onOpenCard) {
  if (!cards || cards.length === 0) return null;
  const row = el("div", "doc-cards");
  row.append(el("span", "doc-cards-title", t("doc_cards")));
  for (const card of cards) {
    // A button, not an <a> with no href: an anchor without one is not focusable
    // and not in the tab order, so it would work only for a mouse.
    const button = el("button", "doc-card");
    button.setAttribute("type", "button");
    const number = el("span", card.id ? "knum" : "knum knum-none", card.id || t("card_no_number"));
    number.setAttribute("title", card.id ? t("card_number_hint") : t("card_no_number_hint"));
    button.append(number, el("span", "doc-card-title", card.title || noteName(card.path)));
    button.addEventListener("click", () => onOpenCard?.(card.path));
    row.append(button);
  }
  return row;
}
