// The documentation section: the markdown under the directories configured in
// docs.paths, listed on the left and rendered on the right.
//
// Three things about this module are decisions, not accidents.
//
// It is the one part of the panel that fetches its own reads. Everything else
// renders from the snapshot the socket pushes, but documentation is not fleet
// state: it does not change once a second, it can be megabytes, and putting it
// in the snapshot would send every document to every browser on every tick.
//
// It builds its list with createElement and textContent rather than assembling
// an HTML string. A document's title is a file name off the operator's disk, and
// a name is not markup. The one place markup is produced is the document body,
// which goes through markdown.js — the module that escapes its whole input
// before it builds anything.
//
// It repeats the server's refusals verbatim instead of flattening them into "no
// documentation". The server deliberately distinguishes "no directories are
// configured" from "the configured directories cannot be read" from "they hold
// no markdown", and a section that showed an empty list for all three would undo
// that on the last hop.

import { renderMarkdown } from "./markdown.js";
import { t } from "./i18n.js";

// The documentation section has no cards to resolve wiki links against, so every
// link in a document renders as one that does not work. Shared and frozen: it is
// read on every render and never written.
const NO_CARDS = Object.freeze(new Set());

function el(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}

// message turns a failed response into the sentence the server wrote. The
// fallbacks matter: a route that fails without a JSON body still has to say
// something an operator can act on, and an empty string is not that.
async function refusal(response) {
  let body = null;
  try {
    body = await response.json();
  } catch {
    body = null;
  }
  const detail = typeof body?.error === "string" && body.error !== "" ? body.error : response.statusText;
  return new Error(detail || `HTTP ${response.status}`);
}

/**
 * renderDocs draws the documentation section into `root`.
 *
 * It fetches the list once, when it is called; the section is built the first
 * time the operator opens it (see sections.js) rather than at startup, so a
 * panel with no documentation directories does not ask for them before anyone
 * has looked.
 */
export function renderDocs(root) {
  // The list and the body are two elements with two lifetimes, and that is the
  // point. The list is rebuilt only when the set of documents changes, so
  // opening one does not tear down and re-create the entry the operator's cursor
  // is on; the body is replaced on every open.
  const nav = el("nav", "docs-list");
  const article = el("article", "docs-body");
  const box = el("div", "docs");
  box.append(nav, article);
  root.replaceChildren(box);

  let selected = null;
  // Which open is the newest. An answer carrying an older token belongs to a
  // document the operator has already navigated away from, and painting it would
  // put one document's text under another one's highlighted entry.
  let token = 0;

  const markSelected = () => {
    for (const entry of nav.querySelectorAll("[data-path]")) {
      entry.className = entry.dataset.path === selected ? "docs-entry on" : "docs-entry";
    }
  };

  // Either the rendered document or a line of plain text. The two are separate
  // paths because only one of them is markup: everything that is not the
  // document body reaches the page as text and can never be anything else.
  const paintBody = (content) => {
    if (content?.html !== undefined) {
      article.innerHTML = content.html;
      return;
    }
    article.replaceChildren(el("p", "docs-empty", content?.text ?? t("pick_doc")));
  };

  const paintList = (docs, listError) => {
    if (listError) {
      // Replaces the list, and only the list: one document refusing to open says
      // nothing about the others, so that failure goes to the body instead.
      nav.replaceChildren(el("p", "docs-error", listError));
      return;
    }
    if (docs.length === 0) {
      nav.replaceChildren(el("p", "docs-empty", t("docs_empty")));
      return;
    }
    nav.replaceChildren(
      ...docs.map((doc) => {
        // A button rather than an href-less <a>: an anchor with no href is not
        // focusable, is not in the tab order and is not announced as a link, so
        // it would look like a link and work only for a mouse.
        const entry = el("button", "docs-entry", doc.title);
        entry.setAttribute("type", "button");
        entry.dataset.path = doc.path;
        return entry;
      }),
    );
    markSelected();
  };

  const open = async (path) => {
    const mine = (token += 1);
    selected = path;
    markSelected();
    paintBody({ text: t("doc_opening") });
    try {
      const response = await fetch(`/api/docs/content?path=${encodeURIComponent(path)}`);
      if (!response.ok) throw await refusal(response);
      const { body } = await response.json();
      if (token !== mine) return;
      paintBody({ html: renderMarkdown(body, NO_CARDS) });
    } catch (err) {
      if (token !== mine) return;
      paintBody({ text: `${t("doc_open_failed")}: ${err.message}` });
    }
  };

  // One delegated listener on the section rather than one per entry: the list is
  // rebuilt whenever the set of documents changes, and per-entry listeners would
  // have to be re-attached each time — the shape where one rebuild silently
  // leaves a dead list behind.
  root.addEventListener("click", (event) => {
    const entry = event.target?.closest?.("[data-path]");
    if (entry && root.contains(entry)) open(entry.dataset.path);
  });

  paintBody(null);

  (async () => {
    try {
      const response = await fetch("/api/docs");
      if (!response.ok) throw await refusal(response);
      const body = await response.json();
      paintList(Array.isArray(body) ? body : [], "");
    } catch (err) {
      // Verbatim, with a line saying what failed: the server's sentence already
      // names the directory it could not read or the rule that refused the path,
      // and rewording it here would cost the operator the one detail they act on.
      paintList([], `${t("docs_list_failed")}: ${err.message}`);
    }
  })();
}
