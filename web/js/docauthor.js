// web/js/docauthor.js — who wrote a card's document, and what that session is
// doing now.
//
// The author is the session a document names in its own frontmatter
// (docs/en/board-convention.md, "Documents a card links to"; the panel reads it
// off /api/docs). A document that names none falls back to the card it was
// opened from, and says so: that card's session is who is working on it now,
// which is not the same as who wrote it. The card's own tab is authored by the
// card's session.
import { DEAD, STOPPED } from "./lifecycle.js";
import { isStalled, waiting, WAITING_YES } from "./needs.js";

// authorOf is {short, from} -- from "document" or "card" -- or null when neither
// names a session and there is no one to answer.
export function authorOf(doc, card) {
  if (doc?.session) return { short: doc.session, from: "document" };
  if (card?.session) return { short: card.session, from: "card" };
  return null;
}

export function sessionOf(short, snap) {
  return (snap?.sessions ?? []).find((s) => s.short === short) ?? null;
}

// authorState is what the session short is doing, as the card's document tab
// shows it. The orchestrator comes first: its terminal lives in a panel of its
// own and is never opened a second time in a card, whatever it is doing. Then
// where the session is in its life, and only for a live one what it last said.
export function authorState(short, snap) {
  if (short && short === snap?.orchestratorSession) return "orchestrator";
  const s = sessionOf(short, snap);
  if (!s) return "unknown";
  if (s.lifecycle === DEAD) return "dead";
  if (s.lifecycle === STOPPED) return "stopped";
  if (isStalled(s)) return "stalled";
  if (waiting(s) === WAITING_YES) return "waiting";
  return "working";
}
