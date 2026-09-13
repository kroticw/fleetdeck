// The document reader: one document, opened over the board from a card.
//
// Three things about this module are decisions, not accidents.
//
// It is an overlay over the centre column, like the card panel and the session
// panel, and not a jump to the documentation section. A report is read in the
// context of the card it belongs to; the section would put it back among every
// other document, with the card gone from the screen. Over the board, the way
// back is one click on the card's number at the top.
//
// The cards at the top are worked out, not read from anywhere: a card's
// [[links]] are what ties it to its documents (docnames.js), so the row cannot
// disagree with the cards.
//
// The body and the cards row have two lifetimes. The body is fetched once per
// document and never again for a snapshot; the row is redrawn when a snapshot
// changes who links here. Re-rendering the body once a second would throw the
// operator's place in a long report away once a second.

import { subscribe as storeSubscribe } from "./store.js";
import { renderMarkdown } from "./markdown.js";
import { markScrollablesWithin, watchScrollables } from "./scrollable.js";
import { listDocs as serverDocs, fetchDoc as serverDoc } from "./docs.js";
import { cardPathForLink } from "./card.js";
import { cardsLinkingTo, docForLink, docTitle, noteName } from "./docnames.js";
import { docCardsRow } from "./doccards.js";
import { t } from "./i18n.js";

function el(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}

/**
 * renderReader draws one document into `root` and keeps its cards row up to
 * date until the returned function is called.
 *
 * options.onOpenCard is called with a card's path when one of the cards linking
 * here, or a card link inside the document, is followed.
 *
 * options.subscribe, options.listDocs and options.fetchDoc replace the store
 * and the two documentation routes, so the reader can be driven in a test
 * without a server; nothing in the application passes them.
 */
export function renderReader(root, path, onClose, options = {}) {
  const subscribe = options.subscribe ?? storeSubscribe;
  const listDocs = options.listDocs ?? serverDocs;
  const fetchDoc = options.fetchDoc ?? serverDoc;
  const onOpenCard = options.onOpenCard ?? null;

  let current = path;
  // null until the list arrives. A document link can only be resolved against
  // it, and until then the reader knows the document by its path alone.
  let docs = null;
  let cards = [];
  // The markdown of the document on screen, once it has arrived: kept so the
  // body can be rendered again when the list arrives and its links resolve.
  let text = null;
  // Which load is the newest. An answer carrying an older token belongs to a
  // document the operator has already left.
  let token = 0;
  let paintedCards = null;
  let disposed = false;

  const title = el("h3", "reader-title");
  const close = el("button", "reader-close", "✕");
  close.setAttribute("type", "button");
  close.setAttribute("aria-label", t("reader_close"));
  close.addEventListener("click", onClose);
  const head = el("div", "reader-head");
  head.append(title, close);
  // The row lives in a slot of its own, so replacing it never touches the body
  // under it. An empty slot has no size: no cards is no row, not a gap.
  const slot = el("div", "reader-cards");
  const body = el("article", "reader-body");
  root.replaceChildren(head, slot, body);
  root.hidden = false;

  const shown = () => (docs ?? []).find((doc) => doc.path === current) ?? null;

  const paintTitle = () => {
    const doc = shown();
    title.textContent = doc ? docTitle(doc) : noteName(current);
  };

  const paintCards = () => {
    const linking = cardsLinkingTo(shown(), cards, docs);
    const signature = JSON.stringify([current, linking.map((card) => [card.path, card.id, card.title])]);
    if (signature === paintedCards) return;
    paintedCards = signature;
    const row = docCardsRow(linking, onOpenCard);
    slot.replaceChildren(...(row ? [row] : []));
  };

  const paintBody = () => {
    if (text === null) return;
    const cardNames = new Set(cards.map((card) => noteName(card.path)));
    body.innerHTML = renderMarkdown(text, cardNames, { has: (name) => docForLink(docs, name) !== null });
    markScrollablesWithin(body, ".md-table, pre");
  };

  const load = async (target) => {
    const mine = (token += 1);
    current = target;
    text = null;
    paintTitle();
    paintCards();
    body.replaceChildren(el("p", "reader-empty", t("doc_opening")));
    try {
      const answer = await fetchDoc(target);
      if (disposed || token !== mine) return;
      text = String(answer ?? "");
      paintBody();
    } catch (err) {
      if (disposed || token !== mine) return;
      // Verbatim: the server's sentence names the rule that refused the path,
      // and that is the one detail the operator acts on.
      body.replaceChildren(el("p", "reader-error", `${t("doc_open_failed")}: ${err.message}`));
    }
  };

  const onClick = (event) => {
    const link = event.target?.closest?.("[data-link],[data-doc]");
    if (!link || !root.contains(link)) return;
    if (link.dataset.link !== undefined) {
      const card = cardPathForLink(cards, link.dataset.link);
      if (!card) return;
      event.preventDefault?.();
      onOpenCard?.(card);
      return;
    }
    const doc = docForLink(docs, link.dataset.doc);
    if (!doc) return;
    event.preventDefault?.();
    load(doc.path);
  };

  const onKey = (event) => {
    if (event.key === "Escape") onClose();
  };

  // mousedown rather than click, for the reason card.js gives: the click that
  // opened the reader is still on its way up while this listener is added.
  const onOutside = (event) => {
    if (!root.contains(event.target)) onClose();
  };

  root.addEventListener("click", onClick);
  watchScrollables(root, ".md-table, pre");
  document.addEventListener("keydown", onKey);
  document.addEventListener("mousedown", onOutside);
  const unsubscribe = subscribe((snap) => {
    cards = snap?.cards ?? [];
    paintCards();
  });

  load(current);

  (async () => {
    let list = [];
    try {
      const answer = await listDocs();
      list = Array.isArray(answer) ? answer : [];
    } catch {
      // The document itself is still fetched by its path. What is lost is only
      // what the list is for — its title and the cards linking to it — and the
      // reader then shows neither rather than guessing.
      list = [];
    }
    if (disposed) return;
    docs = list;
    paintTitle();
    paintCards();
    paintBody();
  })();

  return () => {
    disposed = true;
    unsubscribe();
    root.removeEventListener("click", onClick);
    document.removeEventListener("keydown", onKey);
    document.removeEventListener("mousedown", onOutside);
  };
}

/**
 * createReader owns the one reader the page has, the way createCardPanel owns
 * the card panel: opening a document disposes the one already shown, so no
 * store subscription or document listener is left behind by a reader nobody
 * can see.
 */
export function createReader(panel, options = {}) {
  if (!panel) {
    console.error("fleetdeck: the document reader is not wired — the page has no #reader-panel");
    return { open() {}, close() {} };
  }

  let dispose = null;

  const close = () => {
    if (dispose) {
      dispose();
      dispose = null;
    }
    panel.replaceChildren();
    panel.hidden = true;
  };

  return {
    open(path) {
      close();
      dispose = renderReader(panel, path, close, options);
    },
    close,
  };
}
