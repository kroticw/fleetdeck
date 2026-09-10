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
  // The edit the operator has made that the snapshot has not caught up with
  // yet. It keeps the control showing what the operator chose during the round
  // trip and, on a written-but-not-committed answer, until the next snapshot
  // confirms it. Cleared the moment a write is refused.
  let pending = null;
  // "The field was written and the commit did not happen" — a success with a
  // caveat, never an error, and never a retry: repeating a progress edit would
  // apply it twice.
  let notice = null;
  // "Nothing was written." The control goes back to what the file holds.
  let failure = null;
  // Signature of what is currently on screen, so an unchanged snapshot redraws
  // nothing.
  let painted = null;

  const shownValue = (card, field) =>
    pending?.field === field ? pending.value : String(card?.[field] ?? "");

  const onFieldChange = async (select) => {
    const field = select.dataset.field;
    const value = select.value;
    notice = null;
    failure = null;
    pending = { field, value };
    repaint(field);
    try {
      const result = await setCardField(current, field, value);
      if (!result.committed) {
        notice = { field, reason: result.reason };
      }
    } catch (err) {
      // Nothing reached the file, so the control must stop showing a value the
      // card does not have: a control left on the operator's choice is a lie
      // about the state of the board.
      pending = null;
      failure = err.message;
    }
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
        const link = el("a", "card-session", card.session);
        link.addEventListener("click", () => onOpenSession(card.session));
        meta.append(link);
      } else {
        meta.append(el("span", "card-session", card.session));
      }
    }
    if (orphan) {
      // Shown, and nothing more: the panel writes stage and progress and no
      // other field, so it has no honest "unlink" to offer.
      meta.append(el("span", "card-session-dead", t("card_session_dead")));
    }
    if (meta.children.length > 0) nodes.push(meta);

    if (failure) {
      nodes.push(el("p", "card-error", `${t("card_write_refused")}: ${failure}`));
    }
    if (notice) {
      const text = notice.reason
        ? `${t("card_not_committed")}: ${notice.reason}`
        : t("card_not_committed");
      nodes.push(el("p", "card-notice", text));
    }

    const body = el("div", "card-body");
    body.innerHTML = renderMarkdown(card.body, new Set(known));
    nodes.push(body);

    if (backlinks.length > 0) {
      const box = el("div", "card-backlinks");
      box.append(el("h4", "card-backlinks-title", t("backlinks")));
      for (const back of backlinks) {
        const link = el("a", "card-backlink", back.title || baseName(back.path));
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
    if (pending && card && String(card[pending.field] ?? "") === pending.value) {
      // The snapshot has caught up with the edit; the card itself is the source
      // of the value again.
      pending = null;
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
      pending,
      notice,
      failure,
    });
    if (signature === painted) return;
    painted = signature;

    root.hidden = false;
    root.replaceChildren(...build(latest, card, known, orphan, backlinks));
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
    const target = (latest?.cards ?? []).find((c) => baseName(c.path) === link.dataset.link);
    if (!target) return;
    event.preventDefault?.();
    current = target.path;
    pending = null;
    notice = null;
    failure = null;
    painted = null;
    draw(latest);
  };

  const onKey = (event) => {
    if (event.key === "Escape") onClose();
  };

  // mousedown rather than click: the click that opens this panel is still on its
  // way up the tree while this listener is being added, and a click listener
  // would catch that very event and close the panel in the same gesture that
  // opened it. That gesture's mousedown is already over.
  const onOutside = (event) => {
    if (!root.contains(event.target)) onClose();
  };

  root.addEventListener("click", onLinkClick);
  document.addEventListener("keydown", onKey);
  document.addEventListener("mousedown", onOutside);
  // Wrapped, not passed straight in: subscribe calls its listener with
  // (snapshot, connected), and draw's second parameter is a field name.
  const unsubscribe = subscribe((snap) => draw(snap));

  return () => {
    unsubscribe();
    root.removeEventListener("click", onLinkClick);
    document.removeEventListener("keydown", onKey);
    document.removeEventListener("mousedown", onOutside);
  };
}
