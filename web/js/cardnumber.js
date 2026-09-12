// web/js/cardnumber.js
//
// The card's number (frontmatter `id`, "T-NNN"), which is the one identifier a
// person reads off the screen and says out loud — and then hands to
// card_path.py to reach the file again. Drawn in two places: on the card in its
// column (board.js), and on the button that opens a session's card in the
// session list (sessions.js), where the question is "what is this one busy
// with".
//
// In both it has to stay distinguishable from the session's short id: the two
// are different identifiers with different lifetimes (the card's is permanent
// and spoken, the session's changes with every run), and an operator naming the
// wrong one gets nowhere. So the number is not merely another piece of small
// grey text — it carries its own class, its own shape in app.css (.knum), and a
// title saying which of the two it is. One module draws it for both places so
// that one shape keeps meaning one kind of thing.
//
// A card with no number gets a word saying so rather than an empty space: a
// gap reads as a panel that lost the number, and inventing one is worse than
// either. A card that does not parse never reaches this: the board handles it
// first, and it names no session to be listed under.

import { t } from "./i18n.js";

function escapeHTML(value) {
  return String(value)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#39;");
}

export function cardNumberHTML(id) {
  if (!id) {
    return `<span class="knum knum-none" title="${escapeHTML(t("card_no_number_hint"))}">${escapeHTML(t("card_no_number"))}</span>`;
  }
  return `<span class="knum" title="${escapeHTML(t("card_number_hint"))}">${escapeHTML(id)}</span>`;
}
