// The card panel: one card, opened over the board, with its two writable fields.
//
// Three things about this module are decisions, not accidents.
//
// It builds its DOM with createElement and textContent instead of assembling an
// HTML string. Every value it shows — title, session id, stage, parse error, the
// basename of a path — comes out of a markdown file an autonomous agent wrote,
// and textContent cannot be escaped wrongly the way a template can. The one
// exception is the card body, which has to arrive as markup and goes through
// markdown.js, which escapes its whole input before it builds anything.
//
// It redraws only when what it shows has actually changed. A snapshot arrives
// once a second, and rebuilding this panel on each one would collapse an open
// <select> under the operator's cursor every second.
//
// It keeps its notices in the closure rather than writing them straight into the
// DOM. A notice written into the DOM by an event handler is erased by the next
// redraw, which is at most a second away — the operator would see the message
// about their edit appear and vanish before reading it.

import { subscribe as storeSubscribe } from "./store.js";
import { setCardField } from "./api.js";
import { renderMarkdown } from "./markdown.js";
import { markScrollablesWithin, watchScrollables } from "./scrollable.js";
import { t } from "./i18n.js";
import { listDocs as serverDocs, fetchDoc as serverDoc } from "./docs.js";
import { brokenLinksOf, cardsLinkingTo, docForLink, docTitle, documentsOf, noteName } from "./docnames.js";
import { docCardsRow } from "./doccards.js";
import { CARD_TAB, renderTabs, tabsOf } from "./cardtabs.js";
import { authorOf, authorState } from "./docauthor.js";
import { createCardDock } from "./carddock.js";
import { closeCrossHTML } from "./icon.js";

// The two field vocabularies, exactly as internal/board/write.go accepts them.
// Progress is a list of strings because that is what the write route takes and
// what a <select> holds; the snapshot carries it as a number.
const STAGES = ["new", "active", "review", "blocked", "done"];
const PROGRESS = ["0", "10", "20", "40", "60", "80", "100"];

// The stages at which a card's session is worth jumping to. A new card has not
// been taken up yet and a done one no longer needs its session, so a jump there
// would be a control that leads nowhere useful — worse than no control.
const JUMP_STAGES = new Set(["active", "review", "blocked"]);

// A card's note name: what a [[link]] to it spells (web/js/docnames.js).
const baseName = noteName;

// cardPathForLink is which card a wiki link names: the one whose file name,
// without its directory and ".md", is the link's note name — or null when no
// card is called that. Exported for the live terminal's links
// (web/js/terminallinks.js, wired in web/js/main.js), so that a [[link]] in a
// session's output opens the same card it would open on a card.
export function cardPathForLink(cards, name) {
  return (cards ?? []).find((c) => baseName(c.path) === name)?.path ?? null;
}

function el(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}

/**
 * renderCard draws the panel for one card into `root` and keeps it up to date
 * until the returned function is called.
 *
 * options.onOpenSession, when given, makes the card's session id a control that
 * hands the id back — the jump from a card to its session. It is offered only
 * where there is somewhere to go: a card whose work is under way (JUMP_STAGES)
 * and whose session is live. A card whose session is dead or stopped says which
 * instead; a new or finished card shows the id as plain text. With nothing
 * passed the id is always plain text.
 *
 * options.onOpenDoc, when given, is called with a document's path when one of
 * the card's documents is followed — from the list under its number or from a
 * link in its body.
 *
 * options.listDocs replaces the documentation list route, for the reason
 * options.subscribe below exists.
 *
 * options.subscribe replaces the module's store subscription. It exists so the
 * panel can be driven from a sequence of snapshots in a test without a server,
 * a socket or a browser; nothing in the application passes it.
 */
export function renderCard(root, path, onClose, options = {}) {
  const subscribe = options.subscribe ?? storeSubscribe;
  const onOpenSession = options.onOpenSession ?? null;
  const onOpenDoc = options.onOpenDoc ?? null;
  const listDocs = options.listDocs ?? serverDocs;
  const fetchBody = options.fetchDoc ?? serverDoc;

  // Which card the panel is showing. A wiki link or a backlink moves it, which
  // is why this is not simply the `path` argument everywhere below.
  let current = path;
  // The last snapshot seen, so a click handler can resolve a link without
  // reaching back into the store.
  let latest = null;
  // Everything a write leaves behind is keyed by field, and every write carries
  // a token, because a panel that can be re-targeted mid-request has two ways to
  // draw an answer onto the wrong thing: the operator follows a link while a
  // request is in flight and the message about card A lands on card B, or two
  // fields are edited in a row and the slower answer overwrites the faster one's
  // message and reverts a control whose write in fact succeeded. A single
  // notice slot and a single pending slot cannot tell any of those apart.

  // field -> the value the operator chose, held until a snapshot shows the card
  // holding it. It is what keeps the control on the operator's choice across the
  // round trip instead of flickering back to the file's old value.
  const pending = new Map();
  // field -> {kind: "notice"|"error", reason}. "notice" is "the field was
  // written and the commit did not happen" — a success with a caveat, never an
  // error and never a retry, because repeating a progress edit applies it twice.
  // "error" is "nothing was written", and the control goes back to the file.
  const outcomes = new Map();
  // field -> the token of the newest write started for that field. An answer
  // whose token is no longer the newest belongs to an edit the operator has
  // already replaced.
  const writes = new Map();
  let nextToken = 0;
  // Signature of what is currently on screen, so an unchanged snapshot redraws
  // nothing.
  let painted = null;
  // The same for the row of tabs, which the authors' states also move.
  let paintedTabs = null;
  // The documentation list, fetched once per opened panel and null until it
  // arrives. A card's documents are the links in it that name a document there.
  let docs = null;
  let disposed = false;
  // The open tab: the card itself, or one of its documents by path (T-091).
  // Back to the card whenever the panel moves to another card.
  let active = CARD_TAB;
  // A document the sheet was asked to open on (options.doc, a link name or a
  // path), taken up once the documents are listed and forgotten after.
  let wanted = options.doc ?? null;
  // path -> {text} | {error} | {loading}: a document's body, fetched the first
  // time its tab opens and kept for the life of the sheet, so neither a snapshot
  // nor going back to the tab asks for it again.
  const bodies = new Map();

  // The sheet's frame, built once: the head, the row of tabs, and the stage
  // holding the open tab's pane and the place the author's session lives in
  // (T-091). Snapshots redraw the head and the pane and leave the rest alone: a
  // live terminal in the session's place would otherwise be torn down and
  // reconnected once a second.
  const headHost = el("div", "card-sheet-head");
  const tabsHost = el("div", "card-tabs");
  const stage = el("div", "card-stage");
  const pane = el("div", "card-pane");
  const grip = el("div", "card-dock-grip");
  const dock = el("div", "card-dock");
  grip.hidden = true;
  dock.hidden = true;
  stage.dataset.dock = "bottom";
  stage.append(pane, grip, dock);
  root.replaceChildren(headHost, tabsHost, stage);
  // The author's session next to the open tab (web/js/carddock.js). The panel's
  // options reach it as they are: how to go to the orchestrator
  // (toOrchestrator), a terminal's links and font key, and a stand's place and
  // open session (dock, expand); a test's terminal, width and storage too.
  const sessionPlace = createCardDock(dock, {
    stage,
    grip,
    toOrchestrator: options.toOrchestrator,
    links: options.links,
    fontKey: options.fontKey,
    place: options.dock,
    expand: options.expand,
    terminal: options.terminal,
    observe: options.observe,
    storage: options.storage,
    resume: options.resume,
  });

  const shownValue = (card, field) =>
    pending.has(field) ? pending.get(field) : String(card?.[field] ?? "");

  const onFieldChange = async (select) => {
    const field = select.dataset.field;
    const value = select.value;
    // Both captured before the await: which card is being written, and which
    // edit of this field this is.
    const card = current;
    const token = (nextToken += 1);

    writes.set(field, token);
    outcomes.delete(field);
    pending.set(field, value);
    repaint(field);

    let outcome = null;
    let written = true;
    try {
      const result = await setCardField(card, field, value);
      if (!result.committed) outcome = { kind: "notice", reason: result.reason };
    } catch (err) {
      outcome = { kind: "error", reason: err.message };
      written = false;
    }

    // The panel may have moved to another card while this was in flight, and
    // this field may have been edited again. Either way the answer is no longer
    // about what is on screen, and drawing it there would attach a message about
    // one file to another.
    if (current !== card || writes.get(field) !== token) return;

    // Nothing reached the file, so the control must stop showing a value the
    // card does not have: a control left on the operator's choice is a lie about
    // the state of the board.
    if (!written) pending.delete(field);
    if (outcome) outcomes.set(field, outcome);
    repaint(field);
  };

  const fieldControl = (field, values, value) => {
    const wrap = el("label", "card-field");
    wrap.append(el("span", "card-field-name", field));
    const select = document.createElement("select");
    select.dataset.field = field;
    // A card holding a value the board does not allow still has to be shown as
    // it is. Offering only the legal values would silently present one of them
    // as the card's own.
    const options = values.includes(value) ? values : [value, ...values];
    for (const option of options) {
      const node = document.createElement("option");
      node.value = option;
      node.textContent = option;
      node.disabled = !values.includes(option);
      node.selected = option === value;
      select.append(node);
    }
    select.addEventListener("change", () => onFieldChange(select));
    wrap.append(select);
    return wrap;
  };

  const head = (title) => {
    const box = el("div", "card-head");
    box.append(el("h3", "card-title", title));
    const close = el("button", "card-close");
    close.innerHTML = closeCrossHTML;
    close.setAttribute("type", "button");
    close.setAttribute("aria-label", t("card_close"));
    close.addEventListener("click", onClose);
    box.append(close);
    return box;
  };

  // pick opens a tab: the card's, or one of its documents' by path.
  const pick = (key) => {
    if (key === active) return;
    active = key;
    // A document that could not be fetched is asked for again when its tab is
    // picked again: the reason it failed -- a panel restarting -- may be gone.
    if (bodies.get(key)?.error !== undefined) bodies.delete(key);
    if (key !== CARD_TAB) load(key);
    painted = null;
    draw(latest);
    pane.scrollTop = 0;
  };

  const load = (docPath) => {
    if (bodies.has(docPath)) return;
    bodies.set(docPath, { loading: true });
    Promise.resolve()
      .then(() => fetchBody(docPath))
      .then(
        (text) => ({ text: String(text ?? "") }),
        (err) => ({ error: err?.message ?? String(err) }),
      )
      .then((result) => {
        if (disposed) return;
        bodies.set(docPath, result);
        painted = null;
        draw(latest);
      });
  };

  // goToCard moves the panel to another card, on that card's own tab. Answers
  // still in flight find `current` changed and discard themselves.
  //
  // `writes` is deliberately NOT cleared: it holds edit identities, not
  // display state, and its tokens are unique for the life of the panel.
  // Clearing it here would make every in-flight answer stale for the token
  // reason as well, which would leave the "is this still the same card?" half
  // of the guard in onFieldChange covering nothing and untestable — true today
  // and silently untrue the moment this line moved.
  const goToCard = (target) => {
    current = target;
    active = CARD_TAB;
    pending.clear();
    outcomes.clear();
    painted = null;
    draw(latest);
  };

  // The line naming who wrote the document, and where the panel learnt it: the
  // document's own frontmatter, or, failing that, the card it is opened from.
  const authorLine = (author) => {
    const from = author.from === "document" ? t("card_doc_author_from_document") : t("card_doc_author_from_card");
    return el("p", "card-doc-author", `${t("card_doc_author").replace("{short}", author.short)} · ${from}`);
  };

  // docPane is a document's tab: its title, the cards linking it, who wrote it,
  // and its body once fetched.
  const docPane = (doc, card, cards, known) => {
    const head = el("div", "card-doc-head");
    head.append(el("h3", "card-doc-title", docTitle(doc)));
    const row = docCardsRow(cardsLinkingTo(doc, cards, docs), goToCard);
    if (row) head.append(row);
    const author = authorOf(doc, card);
    if (author) head.append(authorLine(author));

    const state = bodies.get(doc.path);
    if (!state || state.loading) return [head, el("p", "card-doc-loading", t("card_doc_loading"))];
    if (state.error !== undefined) return [head, el("p", "card-error", `${t("card_doc_failed")}: ${state.error}`)];
    const body = el("article", "card-doc-body");
    body.innerHTML = renderMarkdown(
      state.text,
      new Set(known),
      { has: (name) => docForLink(docs, name) !== null },
      { missingTitle: t("card_doc_missing") },
    );
    return [head, body];
  };

  const build = (snap, card, known, orphan, stopped, backlinks, documents, broken) => {
    if (!snap) {
      return [head(baseName(current)), el("p", "card-empty", t("card_waiting"))];
    }
    if (!card) {
      return [head(baseName(current)), el("p", "card-empty", t("card_gone"))];
    }

    const nodes = [head(card.title || baseName(current))];

    if (card.parseError) {
      // The server answers 422 to a write into a card whose frontmatter does not
      // parse, so the panel does not offer controls that could only fail.
      nodes.push(el("p", "card-parse-error", `${t("card_parse_error")}: ${card.parseError}`));
    } else {
      const fields = el("div", "card-fields");
      fields.append(fieldControl("stage", STAGES, shownValue(card, "stage")));
      fields.append(fieldControl("progress", PROGRESS, shownValue(card, "progress")));
      nodes.push(fields);
    }

    const meta = el("div", "card-meta");
    // The card's number, first in the row and right under stage and progress:
    // this is the identifier an operator reads off the screen and says out
    // loud, and card_path.py is what they hand it to. It shares the row with
    // the session's short id and must not share its look — the session's
    // identifier is temporary and changes with every run, so naming it instead
    // leads nowhere. Hence a class of its own, a shape of its own in app.css,
    // and a title saying which of the two this is.
    //
    // Skipped entirely for a card that does not parse: nothing is known about
    // the number of a card whose frontmatter could not be read, and "no
    // number" would be an invented fact stacked on a real failure.
    if (!card.parseError) {
      const numbered = Boolean(card.id);
      const number = el(
        "span",
        numbered ? "card-num" : "card-num card-num-none",
        numbered ? card.id : t("card_no_number"),
      );
      // A card written around the board's new_card.py has no number at all.
      // The word says so; an empty space would read as a panel that lost it.
      number.setAttribute("title", numbered ? t("card_number_hint") : t("card_no_number_hint"));
      meta.append(number);
    }
    if (card.session) {
      // Whether the session is live comes from the snapshot's own two lists —
      // the ones the board marks its cards by — not from a second look at the
      // session list: two answers to "is it live" would sooner or later differ.
      // The stage is the file's, not a pending edit's: the jump is about the
      // work the card records, and the next snapshot brings any edit in.
      const live = !orphan && !stopped;
      if (onOpenSession && live && JUMP_STAGES.has(card.stage)) {
        // A button, not an <a> with no href: an anchor without one is not
        // focusable, is not in the tab order and is not announced as a link, so
        // it would look like a link and work only for a mouse.
        const link = el("button", "card-session card-session-link", card.session);
        link.setAttribute("type", "button");
        link.addEventListener("click", () => onOpenSession(card.session));
        meta.append(link);
      } else {
        meta.append(el("span", "card-session", card.session));
      }
    }
    if (orphan) {
      // Shown, and nothing more: the panel writes stage and progress and no
      // other field, so it has no honest "unlink" to offer.
      meta.append(el("span", "card-session-dead", t("session_dead")));
    }
    if (stopped) {
      // No terminal to open either, so no jump — the words say why there is
      // none, instead of a control that would open nothing.
      meta.append(el("span", "card-session-stopped", t("session_stopped")));
    }
    if (meta.children.length > 0) nodes.push(meta);

    // The card's documents, right under its number, where they are seen without
    // scrolling the body: a report can run to a thousand lines, and the card is
    // where the operator stands when they go looking for it. Its [[links]] are
    // the only source (docnames.js), so the list cannot disagree with the text.
    //
    // No documents, no block. Most cards on a board have none, and a heading
    // over nothing reads as a panel that lost them.
    //
    // A link that opens nothing is listed as well, as text with the reason and
    // not as a control: hidden, it is a broken link nobody fixes, and drawn like
    // the others, a click on it would lead nowhere without a word.
    if (documents.length > 0 || broken.length > 0) {
      const box = el("div", "card-docs");
      box.append(el("h4", "card-docs-title", t("card_docs")));
      for (const doc of documents) {
        const entry = el("button", "card-doc", docTitle(doc));
        entry.setAttribute("type", "button");
        // The card's own document: its tab, not the reader over the board.
        entry.addEventListener("click", () => pick(doc.path));
        box.append(entry);
      }
      for (const name of broken) {
        const entry = el("span", "card-doc-missing", name);
        entry.append(el("span", "card-doc-missing-why", ` — ${t("card_doc_missing")}`));
        box.append(entry);
      }
      nodes.push(box);
    }

    // One line per field that has something to say, in the order the controls
    // are in, and each names its field: with two writable fields there can be
    // two answers on screen at once, and an unlabelled message would not say
    // which edit it is about.
    for (const field of ["stage", "progress"]) {
      const outcome = outcomes.get(field);
      if (!outcome) continue;
      const what = outcome.kind === "error" ? t("card_write_refused") : t("card_not_committed");
      const text = outcome.reason ? `${field}: ${what}: ${outcome.reason}` : `${field}: ${what}`;
      nodes.push(el("p", outcome.kind === "error" ? "card-error" : "card-notice", text));
    }

    const body = el("div", "card-body");
    body.innerHTML = renderMarkdown(
      card.body,
      new Set(known),
      { has: (name) => docForLink(docs, name) !== null },
      // Said only once the documents are known: until then a link to one
      // opens nothing for a reason that is not the one this names.
      docs === null ? {} : { missingTitle: t("card_doc_missing") },
    );
    nodes.push(body);

    if (backlinks.length > 0) {
      const box = el("div", "card-backlinks");
      box.append(el("h4", "card-backlinks-title", t("backlinks")));
      for (const back of backlinks) {
        const link = el("button", "card-backlink", back.title || baseName(back.path));
        link.setAttribute("type", "button");
        link.dataset.link = baseName(back.path);
        box.append(link);
      }
      nodes.push(box);
    }

    return nodes;
  };

  const draw = (snap, focusField) => {
    latest = snap ?? null;
    const cards = latest?.cards ?? [];
    const card = cards.find((c) => c.path === current) ?? null;
    for (const [field, value] of pending) {
      // The snapshot has caught up with this edit; the card itself is the source
      // of the value again.
      if (card && String(card[field] ?? "") === value) pending.delete(field);
    }
    const known = cards.map((c) => baseName(c.path));
    const orphan = (latest?.orphanCards ?? []).includes(current);
    // Kept apart from orphan for the reason the server keeps the two lists
    // apart (internal/state/snapshot.go): a stopped session is paused work, not
    // lost work, and the card says which of the two it is.
    const stopped = !orphan && (latest?.stoppedCards ?? []).includes(current);
    const backlinks = cards.filter(
      (c) => c.path !== current && (c.links ?? []).includes(baseName(current)),
    );
    const documents = card ? documentsOf(card, cards, docs) : [];
    const broken = card ? brokenLinksOf(card, cards, docs) : [];

    // The tabs, the one asked for once the documents are known, and the open
    // one -- the card's, if the open document is no longer the card's.
    const tabs = card ? tabsOf(card, cards, docs) : [{ key: CARD_TAB }];
    if (wanted !== null && docs !== null) {
      const doc = docForLink(docs, wanted) ?? docs.find((d) => d.path === wanted) ?? null;
      wanted = null;
      if (doc && tabs.some((tab) => tab.key === doc.path)) {
        active = doc.path;
        load(doc.path);
      }
    }
    if (!tabs.some((tab) => tab.key === active)) active = CARD_TAB;
    const open = tabs.find((tab) => tab.key === active);
    const states = new Map(
      tabs
        .filter((tab) => tab.doc)
        .map((tab) => {
          const author = authorOf(tab.doc, card);
          return [tab.key, author ? authorState(author.short, latest) : null];
        }),
    );

    // The open tab's author, on every snapshot and not only on a changed one:
    // what the session is doing -- the question it waits on, a stop -- moves
    // without the card changing at all.
    sessionPlace.show(card ? authorOf(open?.doc ?? null, card) : null, latest);

    const signature = JSON.stringify({
      hasSnapshot: latest !== null,
      current,
      card,
      known,
      orphan,
      stopped,
      backlinks: backlinks.map((c) => [c.path, c.title]),
      documents: docs === null ? null : documents.map((d) => d.path),
      broken,
      pending: [...pending],
      outcomes: [...outcomes],
      tabs: tabs.map((tab) => tab.key),
      active,
      body: open?.doc ? bodies.get(open.doc.path) ?? null : null,
      author: open?.doc ? [open.doc.session ?? "", cardsLinkingTo(open.doc, cards, docs).map((c) => [c.path, c.title, c.id])] : null,
    });
    // The authors' states move every turn and are drawn only on the tabs: a
    // change in them draws the row again and leaves the document being read --
    // its selection, an open <details> -- as it is.
    const tabsSignature = JSON.stringify({ tabs: tabs.map((tab) => tab.key), states: [...states], active });
    const paintTabs = () => {
      paintedTabs = tabsSignature;
      renderTabs(tabsHost, tabs, { active, stateOf: (tab) => states.get(tab.key) ?? null, onPick: pick });
    };
    if (signature === painted) {
      if (tabsSignature !== paintedTabs) paintTabs();
      return;
    }
    painted = signature;

    root.hidden = false;
    const [headNode, ...cardNodes] = build(latest, card, known, orphan, stopped, backlinks, documents, broken);
    headHost.replaceChildren(headNode);
    paintTabs();
    pane.replaceChildren(...(open?.doc ? docPane(open.doc, card, cards, known) : cardNodes));
    // After the panel is in the page, never while it is being built: a node
    // outside the document has no layout, so both widths read zero and every
    // box "fits". Measured there, the mark never appeared at all — and looked
    // correct, because nothing on screen said it was missing.
    markScrollablesWithin(root, ".md-table, pre");
    if (focusField) {
      // The control the operator was using has just been replaced by the
      // rebuild. Putting the focus back is the difference between a panel that
      // can be driven from the keyboard and one that drops out from under it on
      // every edit.
      root.querySelector(`select[data-field="${focusField}"]`)?.focus?.();
    }
  };

  const repaint = (focusField) => draw(latest, focusField);

  const onLinkClick = (event) => {
    const docLink = event.target?.closest?.("[data-doc]");
    if (docLink && root.contains(docLink)) {
      const doc = docForLink(docs, docLink.dataset.doc);
      if (!doc) return;
      event.preventDefault?.();
      // One of this card's documents opens on its tab; any other in the reader.
      const cards = latest?.cards ?? [];
      const card = cards.find((c) => c.path === current);
      const own = card ? documentsOf(card, cards, docs).some((d) => d.path === doc.path) : false;
      if (own) pick(doc.path);
      else onOpenDoc?.(doc.path);
      return;
    }
    const link = event.target?.closest?.("[data-link]");
    if (!link || !root.contains(link)) return;
    const target = cardPathForLink(latest?.cards, link.dataset.link);
    if (!target) return;
    event.preventDefault?.();
    goToCard(target);
  };

  // Escape closes the panel, and while the panel is open that is all it does —
  // even pressed inside a live terminal, where it would otherwise go to the
  // session and, in a Claude Code session, interrupt the turn.
  //
  // A terminal is where the focus is left by the one way to have a card open
  // and the focus in a terminal at once: a [[link]] clicked in the terminal
  // (web/js/terminallinks.js), which opens the card without moving the focus.
  // A click into a terminal otherwise closes an open card first (onOutside,
  // below). Measured, the Escape that followed interrupted the session's turn
  // and left the card open — one keystroke, the wrong one of two things.
  //
  // So an Escape from a terminal (its host carries data-terminal) is taken
  // before the terminal sees it: this listener is on the capture phase, ahead
  // of xterm's own handler on its textarea, which is where the keystroke turns
  // into bytes for the session. With the card closed, the next Escape reaches
  // the session as it always did. An Escape from anywhere else closes the card
  // and goes on to its own target, as it did before.
  const onKey = (event) => {
    if (event.key !== "Escape") return;
    // The card's own docked terminal (T-091) is where the operator answers the
    // document's author: Escape there is the session's -- AskUserQuestion's
    // "Esc to cancel", or interrupting a turn -- and the card stays open.
    if (dock.contains(event.target)) return;
    if (event.target?.closest?.("[data-terminal]")) {
      event.stopPropagation?.();
      event.preventDefault?.();
    }
    onClose();
  };

  // mousedown rather than click: the click that opens this panel is still on its
  // way up the tree while this listener is being added, and a click listener
  // would catch that very event and close the panel in the same gesture that
  // opened it. That gesture's mousedown is already over.
  const onOutside = (event) => {
    if (!root.contains(event.target)) onClose();
  };

  root.addEventListener("click", onLinkClick);

  // Watched on the panel, which outlives every card drawn into it, rather than
  // on the body, which is replaced whole on each render: a resize listener per
  // render would accumulate one per card ever opened, each holding a body that
  // left the document long ago.
  watchScrollables(root, ".md-table, pre");
  document.addEventListener("keydown", onKey, true);
  document.addEventListener("mousedown", onOutside);
  // Wrapped, not passed straight in: subscribe calls its listener with
  // (snapshot, connected), and draw's second parameter is a field name.
  const unsubscribe = subscribe((snap) => draw(snap));

  (async () => {
    let list = null;
    try {
      const answer = await listDocs();
      list = Array.isArray(answer) ? answer : null;
    } catch {
      // No documentation roots, or none readable: the card is drawn exactly as
      // it was before documents could be linked, its document links shown as
      // links that do not work. The documentation section is where the
      // server's reason is said. The list stays unknown rather than empty: an
      // empty list would call every document link the card has broken.
      list = null;
    }
    if (disposed) return;
    docs = list;
    draw(latest);
  })();

  return () => {
    disposed = true;
    sessionPlace.dispose();
    unsubscribe();
    root.removeEventListener("click", onLinkClick);
    document.removeEventListener("keydown", onKey, true);
    document.removeEventListener("mousedown", onOutside);
  };
}

/**
 * createCardPanel owns the one panel the page has: which card it is showing, and
 * that only one is ever alive.
 *
 * `open` is exactly what renderBoard's onOpenCard parameter takes, so the board
 * keeps its own click delegation and this module never learns how a card is
 * drawn. A second delegation of our own on the same element would fire on the
 * same click as the board's, which is two open paths for one gesture.
 *
 * `open` disposes the panel already showing before drawing the next one. Without
 * that, every card the operator opens leaves another store subscription and
 * another pair of document listeners behind — invisibly, because the new panel
 * draws over the old one.
 *
 * It lives here rather than in main.js because main.js cannot be tested: it
 * connects a socket the moment it is imported.
 */
export function createCardPanel(panel, options = {}) {
  if (!panel) {
    // Silence here would look exactly like a board whose cards do nothing.
    console.error("fleetdeck: the card panel is not wired — the page has no #card-panel");
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
    // open(path, {doc, dock, expand}): doc opens the sheet on that document's
    // tab (a link name or a path); dock and expand place the author's session
    // and open it, for a stand, without storing the choice. Nothing of one
    // opening is carried into the next.
    open(path, how = {}) {
      close();
      dispose = renderCard(panel, path, close, { ...options, ...how });
    },
    close,
  };
}
