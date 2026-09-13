// web/js/session.js
//
// The session panel: one screen over one session. The session's live terminal,
// the keys that press into it, and the cards the session has worked on.
//
// It used to be two tabs and a box. The digest tab was the session's
// conversation rebuilt from its transcript — the same conversation the terminal
// shows, only behind it — and the box under both was a second way into the same
// session that put text there differently from typing it. Both are gone: the
// terminal is where a session is read and where it is written to, the same way a
// session is talked to everywhere else.
//
// What the terminal cannot show is the session's path: which cards it took, in
// what order, the finished ones included. That is the one thing about a session
// its own output does not contain, so it is what the panel adds above the
// terminal.
//
// Nothing here is built out of an HTML string. Every node is created and every
// piece of text is assigned as .textContent, which means a session's own words
// and a card's title — and sessions we did not write can join this fleet (spec
// 3.1) — have no path into markup at all. It also makes the panel drivable under
// node's test runner against the stand-in document in web/tests/fake-dom.js. The
// clock and the snapshot store are reached through parameters; the document,
// fetch, WebSocket and the terminal constructor are read from the global object,
// which is where the browser puts them and where a test can put its own.

import { fetchSessionCards } from "./api.js";
import { subscribe as subscribeToStore } from "./store.js";
import { t } from "./i18n.js";
import { createLiveTerminal } from "./liveterminal.js";
import { FONT_KEYS } from "./terminalfont.js";
import { buildFontControls } from "./fontcontrols.js";

// The key buttons send bytes, not names.
//
// A button's bytes go into the live terminal's socket exactly as a keystroke
// does, and from there into the session's PTY (internal/server/pty.go). There is
// no translation layer anywhere between this table and the terminal, so sending
// "up" would type the letters u and p into a live session instead of moving its
// selection.
export const KEYS = [
  { id: "escape", label: "Esc", bytes: "" },
  { id: "up", label: "↑", bytes: "[A" },
  { id: "down", label: "↓", bytes: "[B" },
  { id: "enter", label: "Enter", bytes: "\r" },
];

// historyKey is what tells the panel the session's cards may have changed: the
// cards of this session the snapshot holds, with what can change about them.
//
// The history itself is not in the snapshot — closed cards go to the board's
// archive, which the snapshot never reads — so it is asked for from the server.
// Asking once a second, with every snapshot, would read the whole board and its
// archive for nothing almost every time. What does change the history always
// shows here first: a card taken puts this session's id on a card, a card
// closed changes its stage, and a card archived leaves the board.
function historyKey(snapshot, short) {
  const cards = (snapshot?.cards ?? []).filter((c) => String(c.session ?? "").trim() === short);
  return JSON.stringify(cards.map((c) => [c.path, c.stage, c.title]));
}

// renderSession draws the panel for one session into `root` and opens its
// terminal. It returns a stop function; calling it, or the panel's own close
// button, leaves no terminal and no subscription behind.
//
// The panel is opened with the daemon's short id, because that is the identity
// the session list hands over, the one the daemon answers the terminal to, and
// the one a card's session field holds.
//
// timers and subscribe exist for the tests, which cannot wait on real clocks or
// drive a live socket. Both default to the real thing, so nothing in the shipped
// path is a stand-in.
export function renderSession(
  root,
  short,
  onClose,
  {
    timers = globalThis,
    subscribe = subscribeToStore,
    // Handed to the terminal as it is (see createLiveTerminal).
    links = null,
    // Opens a card on the board from the history. Absent, the history is text.
    onOpenCard = null,
  } = {},
) {
  let live = null;
  let fontButtons = null;
  let nameLine = null;
  let cardsRow = null;
  let errorLine = null;
  let noticeLine = null;
  let unsubscribe = null;
  let latest = null;

  const el = (tag, className, text) => {
    const node = document.createElement(tag);
    if (className) node.className = className;
    if (text !== undefined) node.textContent = text;
    return node;
  };

  // The name of the session this panel is pointing at, and the short id when the
  // snapshot does not name it. The short id is never nothing: it is the identity
  // the session list shows for an unnamed session and the one an operator can
  // match against that list, whereas a blank header says only that the panel
  // does not know where it points — under a key row that promises to press keys
  // in "the live session".
  const currentName = () => (latest?.sessions ?? []).find((s) => s.short === short)?.name || short;

  // Resolved on every snapshot rather than captured when the panel opens: a
  // panel opened before the first snapshot lands would otherwise hold whatever
  // was known then — nothing — for as long as it stays open, while the session
  // sits named in the list beside it.
  //
  // Written only when it actually changed. A header rewritten once a second
  // drops any selection inside it and costs the work for no visible difference,
  // which is invisible in the resulting tree and therefore a counted assertion
  // in session.test.js rather than a comment here alone.
  const refreshName = () => {
    if (!nameLine) return;
    const name = currentName();
    if (nameLine.textContent !== name) nameLine.textContent = name;
  };

  // The error line holds two states, not one. What the operator's own action
  // reported — a key pressed into a terminal that cannot type — and what the
  // stream reported, which the operator did not cause. What the operator just
  // did wins; neither can erase the other.
  let actionError = "";
  let streamError = "";

  // What the terminal says about itself for as long as it holds: that the
  // stream cannot type, and that the terminal is not the size of its pane.
  const standingNotice = () =>
    [live?.readOnly ? t("terminal_read_only") : "", live?.unfitted ? t("terminal_not_fitted") : ""]
      .filter(Boolean)
      .join("; ");

  // Both lines sit under the terminal rather than replacing it: a transient
  // failure must not cost a person the screen they were reading.
  const paintError = () => {
    if (!errorLine) return;
    const message = actionError || streamError;
    errorLine.textContent = message;
    errorLine.hidden = !message;
  };

  const paintNotice = () => {
    if (!noticeLine) return;
    const message = standingNotice();
    noticeLine.textContent = message;
    noticeLine.hidden = !message;
  };

  const showError = (message) => {
    actionError = message ?? "";
    paintError();
  };

  const showStreamError = (message) => {
    streamError = message ?? "";
    paintError();
  };

  // --- the cards the session worked on -----------------------------------------
  //
  // Drawn into a row of its own that is there from the start and never changes
  // height: a row above a terminal that grows takes the room from the terminal,
  // and every refit resizes the session for everyone watching it
  // (docs/engineering/live-terminal.md). So a history that is loading, empty,
  // failed or long is still one line — the long one scrolls sideways — and the
  // failure is said here rather than on the error line, which would appear.

  // Each request is numbered, and only the latest one is drawn: two requests a
  // moment apart can answer in either order, and the older answer drawn last
  // would put back a history that has already changed.
  let historyRequest = 0;
  let historyFailed = false;
  let knownHistory = null;

  const drawCardsRow = (...contents) => {
    if (!cardsRow) return;
    cardsRow.replaceChildren(el("span", "s-cards-label", t("session_cards")), ...contents);
  };

  const cardNode = (card) => {
    const archived = card.archived === true;
    const openable = !archived && typeof onOpenCard === "function";
    // A card still on the board opens in the card panel. An archived one does
    // not: the card panel reads the board, and a click that opens an empty panel
    // is worse than text that does not pretend to be a control.
    const node = el(openable ? "button" : "span", archived ? "s-card s-card-archived" : "s-card");
    if (openable) {
      node.type = "button";
      node.addEventListener("click", () => onOpenCard(card.path));
    }
    node.dataset.path = String(card.path ?? "");
    if (card.id) node.appendChild(el("span", "s-card-id", String(card.id)));
    node.appendChild(el("span", "s-card-title", String(card.title || card.path || "")));
    node.appendChild(el("span", "s-card-stage", String(card.stage ?? "")));
    // Properties, never interpolated into markup.
    node.title = [
      [card.id, card.title].filter(Boolean).join(" "),
      [card.stage, card.created].filter(Boolean).join(", "),
      archived ? t("session_card_archived") : "",
    ]
      .filter(Boolean)
      .join(" — ");
    return node;
  };

  const drawHistory = (cards) => {
    if (cards.length === 0) {
      drawCardsRow(el("span", "s-cards-none", t("session_cards_none")));
      return;
    }
    const nodes = [];
    // Oldest first, as the server orders them, with the order spelled out
    // between them: the point of the row is the path, not the set.
    cards.forEach((card, i) => {
      if (i > 0) nodes.push(el("span", "s-cards-sep", "→"));
      nodes.push(cardNode(card));
    });
    drawCardsRow(...nodes);
  };

  const loadHistory = async () => {
    const request = ++historyRequest;
    try {
      const cards = await fetchSessionCards(short);
      if (request !== historyRequest) return;
      historyFailed = false;
      knownHistory = cards;
      drawHistory(cards);
    } catch (err) {
      if (request !== historyRequest) return;
      historyFailed = true;
      // A history drawn before stays readable only in the sentence's absence;
      // a failure replaces it, because a list that may be stale is not what the
      // row claims to be.
      knownHistory = null;
      drawCardsRow(el("span", "s-cards-error", `${t("session_cards_failed")}: ${err.message}`));
    }
  };

  let lastKey = null;
  const onSnapshot = (snapshot) => {
    latest = snapshot ?? null;
    refreshName();
    const key = historyKey(latest, short);
    // A failed history is asked for again with the next snapshot, which is
    // what retries it: a board that was briefly unreadable recovers without the
    // panel being reopened.
    if (key === lastKey && !historyFailed) return;
    lastKey = key;
    void loadHistory();
  };

  // --- the terminal ------------------------------------------------------------

  const openTerminal = (host) => {
    live = createLiveTerminal(host, short, {
      timers,
      links,
      fontKey: FONT_KEYS.screen,
      report: {
        streamError: showStreamError,
        actionError: showError,
        standing: paintNotice,
        ready: refreshName,
        fontSize: (size) => fontButtons?.paint(size),
      },
    });
    live.open();
  };

  // Through the stream the terminal already holds, like any keystroke: a
  // connection of its own per press would be an attach per press, and every
  // attach resizes the session.
  const pressKey = (key) => {
    if (live) live.type(key.bytes);
    else showError(t("terminal_not_connected"));
  };

  // Everything that goes with the panel: the subscription, the terminal's
  // socket, its attach on the daemon and everything xterm holds, and any history
  // answer still on its way.
  const stop = () => {
    if (unsubscribe) unsubscribe();
    unsubscribe = null;
    if (live) live.stop();
    live = null;
    historyRequest += 1;
  };

  const drawShell = () => {
    root.hidden = false;

    const head = el("div", "s-head");

    // The size of the terminal's type (web/js/fontcontrols.js, the same buttons
    // as the orchestrator column's). Built here, before the terminal is, and the
    // same height as the rest of the header, so the header does not grow under a
    // terminal that has already been fitted.
    fontButtons = buildFontControls({ onStep: (step) => live?.stepFont(step), buttonClass: "s-font-btn" });

    // The header is where a person looks to find out what they are looking at,
    // and the keys at the foot of the panel say they are pressed in a live
    // session, which is only half an answer until the panel says which one.
    nameLine = el("div", "s-who", currentName());
    // The name is the session list's own, and two sessions may carry the same
    // one; the short id under the pointer tells them apart. A property, never
    // interpolated into markup.
    nameLine.title = short;

    const close = el("button", "s-close", "✕");
    close.type = "button";
    close.title = t("close_session");
    close.addEventListener("click", () => {
      stop();
      onClose();
    });

    head.append(fontButtons.node, nameLine, close);

    cardsRow = el("div", "s-cards");
    drawCardsRow(el("span", "s-cards-loading", t("session_cards_loading")));

    const body = el("div", "s-body");
    const host = el("div", "s-term");
    // Marks the element keys typed into a live session come from, so the rest of
    // the page can leave them alone — Escape above all, which interrupts a Claude
    // Code session's turn (see onKey in web/js/card.js).
    host.dataset.terminal = "";
    body.appendChild(host);

    errorLine = el("div", "s-error");
    errorLine.hidden = true;

    noticeLine = el("div", "s-notice");
    noticeLine.hidden = true;

    // The keys are not in the header, and this is the change the operator's
    // first look at this panel bought. Drawn in the top-right corner beside ✕,
    // they are in the exact place every window on the operator's machine puts
    // controls that act on the window, and he read them as that and asked what
    // they were for. They are not that: each one presses a key inside a Claude
    // Code session running somewhere else, in work that is somebody's, and
    // nothing takes it back. So they sit under the terminal they press into,
    // under a label that names the destination.
    const keys = el("div", "s-keys");
    keys.appendChild(el("span", "s-keys-label", t("keys_to_session")));
    for (const key of KEYS) {
      const button = el("button", "s-key", key.label);
      button.type = "button";
      button.dataset.key = key.id;
      button.addEventListener("click", () => pressKey(key));
      keys.appendChild(button);
    }

    root.replaceChildren(head, cardsRow, body, errorLine, noticeLine, keys);
    return host;
  };

  const host = drawShell();
  unsubscribe = subscribe(onSnapshot);
  openTerminal(host);
  return stop;
}

export default renderSession;
