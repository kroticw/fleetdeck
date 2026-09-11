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

// The two field vocabularies, exactly as internal/board/write.go accepts them.
// Progress is a list of strings because that is what the write route takes and
// what a <select> holds; the snapshot carries it as a number.
const STAGES = ["new", "active", "review", "blocked", "done"];
const PROGRESS = ["0", "10", "20", "40", "60", "80", "100"];

function baseName(path) {
  return String(path ?? "")
    .split("/")
    .pop()
    .replace(/\.md$/, "");
}

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
 * hands the id back; with nothing passed the id renders as plain text, which is
 * what it does until the session panel exists.
 *
 * options.subscribe replaces the module's store subscription. It exists so the
 * panel can be driven from a sequence of snapshots in a test without a server,
 * a socket or a browser; nothing in the application passes it.
 */
export function renderCard(root, path, onClose, options = {}) {
  const subscribe = options.subscribe ?? storeSubscribe;
  const onOpenSession = options.onOpenSession ?? null;

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
    const close = el("button", "card-close", "✕");
    close.setAttribute("type", "button");
    close.setAttribute("aria-label", t("card_close"));
    close.addEventListener("click", onClose);
    box.append(close);
    return box;
  };

  const build = (snap, card, known, orphan, backlinks) => {
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
    if (card.session) {
      if (onOpenSession) {
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
    if (meta.children.length > 0) nodes.push(meta);

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
    body.innerHTML = renderMarkdown(card.body, new Set(known));
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
    const backlinks = cards.filter(
      (c) => c.path !== current && (c.links ?? []).includes(baseName(current)),
    );

    const signature = JSON.stringify({
      hasSnapshot: latest !== null,
      current,
      card,
      known,
      orphan,
      backlinks: backlinks.map((c) => [c.path, c.title]),
      pending: [...pending],
      outcomes: [...outcomes],
    });
    if (signature === painted) return;
    painted = signature;

    root.hidden = false;
    root.replaceChildren(...build(latest, card, known, orphan, backlinks));
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
    const link = event.target?.closest?.("[data-link]");
    if (!link || !root.contains(link)) return;
    const target = cardPathForLink(latest?.cards, link.dataset.link);
    if (!target) return;
    event.preventDefault?.();
    current = target;
    // None of this belongs to the card being opened. Answers still in flight
    // find `current` changed and discard themselves.
    //
    // `writes` is deliberately NOT cleared: it holds edit identities, not
    // display state, and its tokens are unique for the life of the panel.
    // Clearing it here would make every in-flight answer stale for the token
    // reason as well, which would leave the "is this still the same card?" half
    // of the guard below covering nothing and untestable — true today and
    // silently untrue the moment this line moved.
    pending.clear();
    outcomes.clear();
    painted = null;
    draw(latest);
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

  return () => {
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
    open(path) {
      close();
      dispose = renderCard(panel, path, close, options);
    },
    close,
  };
}
