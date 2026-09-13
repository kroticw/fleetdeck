// web/js/docnames.js
//
// Which document a wiki link names, which documents a card links to, and which
// cards link to a document.
//
// A card reaches its reports the way it reaches another card, by [[name]]
// (docs/*/board-convention.md), and the board's validator refuses a report
// named by its path instead. So a card's links are the one place the panel
// learns what documents belong to it: there is no second list, in the
// frontmatter or anywhere else, to fall out of step with the text.
//
// A name resolves the way the validator and Obsidian read it: a document's path
// without ".md", or any tail of that path made of whole segments, with the
// documentation root's own directory counted as the first segment. So "x",
// "reports/x" and "docs/reports/x" all name <root>/reports/x.md.
//
// A name that fits two documents opens neither. Picking one would put a
// document on screen that the link may not mean, with nothing saying so; a link
// that does not work at least looks like one.
//
// Pure functions over the documentation list (GET /api/docs) and the snapshot's
// cards, so every rule here is tested without a DOM.

// noteName is a file's name as a wiki link spells it: no directory, no ".md".
export function noteName(path) {
  return String(path ?? "")
    .split("/")
    .pop()
    .replace(/\.md$/, "");
}

// docTitle is how a document is named on screen: its path under its root,
// without ".md". The file name alone is not enough when two directories hold
// one of the same name, and the full path is the operator's disk, not a name.
export function docTitle(doc) {
  return String(doc?.title ?? "").replace(/\.md$/i, "");
}

function namesOf(doc) {
  const root = String(doc.root ?? "")
    .split("/")
    .filter(Boolean)
    .pop();
  const title = docTitle(doc).split("/").filter(Boolean);
  const parts = root ? [root, ...title] : title;
  return parts.map((_, start) => parts.slice(start).join("/"));
}

export function docForLink(docs, name) {
  if (!name || !Array.isArray(docs)) return null;
  const matches = docs.filter((doc) => namesOf(doc).includes(name));
  return matches.length === 1 ? matches[0] : null;
}

// documentsOf is the documents a card links to, in the order it links them and
// each once. A link naming a card stays the card's even when a document happens
// to share the name: that is what the link already meant before documents
// could be linked at all.
export function documentsOf(card, cards, docs) {
  if (!Array.isArray(docs)) return [];
  const cardNames = new Set((cards ?? []).map((c) => noteName(c.path)));
  const seen = new Set();
  const found = [];
  for (const name of card?.links ?? []) {
    if (cardNames.has(name)) continue;
    const doc = docForLink(docs, name);
    if (!doc || seen.has(doc.path)) continue;
    seen.add(doc.path);
    found.push(doc);
  }
  return found;
}

// brokenLinksOf is the links a card has that open nothing: no card has the
// name, and no single document does. Each once, in the order it links them.
// Without a documentation list nothing is called broken — not knowing the
// documents is not knowing a link is broken.
export function brokenLinksOf(card, cards, docs) {
  if (!Array.isArray(docs)) return [];
  const cardNames = new Set((cards ?? []).map((c) => noteName(c.path)));
  const found = [];
  for (const name of card?.links ?? []) {
    if (!name || cardNames.has(name) || found.includes(name) || docForLink(docs, name)) continue;
    found.push(name);
  }
  return found;
}

// cardsLinkingTo is the way back: every card that has this document among its
// documents. Worked out from the cards rather than written anywhere, so it
// cannot disagree with them.
export function cardsLinkingTo(doc, cards, docs) {
  if (!doc) return [];
  return (cards ?? []).filter((card) => documentsOf(card, cards, docs).some((d) => d.path === doc.path));
}
