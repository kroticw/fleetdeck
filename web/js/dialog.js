// web/js/dialog.js — the panel's dialog, the one modal primitive.
//
// It came with the board's moves (web/js/boardmove.js): a question asked before
// a card is moved — start a session for it, or move a card its agent is still
// keeping — has to hold the work until it is answered, and a hand-made box that
// does not trap the focus lets Tab walk out of the question and into the board
// behind it.
//
// The new card form deliberately does not move here: it is a popover anchored
// over the column, open over a board that has to stay visible while a card is
// written, and a scrim with a focus trap would break exactly what it is for.
//
// Anatomy: a scrim, the window, a head with the title and a close button, a
// body, and a footer for the buttons. Two widths — narrow for a question, wide
// for a form; a third is for the third case to ask for.

import { t } from "./i18n.js";

// Every dialog open right now, the innermost last. The stack is not here for
// nesting — today there is never more than one — but so that the listeners
// below are on the document only while there is something for them to close.
// A page with nothing open must not carry a handler that swallows Escape: in
// this panel that key belongs to a live terminal, where it interrupts the
// session's turn (web/js/card.js says the rest).
const open = [];

// The name the window addresses its title by. A page can hold more than one
// dialog at a time, and two aria-labelledby pointing at the same id would
// leave a screen reader reading one window's title for the other's.
let ids = 0;

function el(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}

// Everything a Tab can land on in this page's dialogs. The fake DOM the tests
// run against understands this shape of selector and no more, and nothing here
// needs more.
const FOCUSABLE = "button, input, select, textarea, a[href]";

// Spread first: querySelectorAll answers a NodeList, which has forEach and no
// filter. Without this every open() threw on its own first line — after the
// window was already on screen, so the dialog looked fine and the focus never
// moved into it, the trap never armed, and the promise the caller was waiting
// on was rejected instead of answered.
function stops(win) {
  return [...win.querySelectorAll(FOCUSABLE)].filter((node) => !node.disabled && !node.hidden);
}

function top() {
  return open[open.length - 1] ?? null;
}

// On the capture phase, as web/js/card.js's Escape is and for the same reason:
// ahead of xterm's own handler on its textarea, which is where the keystroke
// would otherwise turn into bytes for the session. The event is kept here
// whole — while a dialog is up, Escape is the dialog's alone, and one that went
// on would close an open card or interrupt a turn behind the window.
//
// There is no shared closing order in this panel to join: the drawers each
// handle Escape their own way and never collide, because they are mutually
// exclusive. Inventing a registry for two windows would mean rewriting all of
// them.
function onKey(event) {
  if (event.key !== "Escape") return;
  event.stopPropagation?.();
  event.preventDefault?.();
  top()?.close();
}

// mousedown rather than click: the click that opens a dialog is still on its
// way up the tree while this listener is being added, and a click listener
// would catch that very event and close the window in the same gesture that
// opened it. That gesture's mousedown is already over. The same choice, for the
// same reason, is in web/js/card.js and web/js/reader.js.
function onOutside(event) {
  const dialog = top();
  if (dialog?.dismissOnOutside && !dialog.window.contains(event.target)) dialog.close();
}

/**
 * createDialog builds a closed dialog and hands back its parts.
 *
 * The caller fills `body` and `foot` and puts `element` in the page; open() and
 * close() do the rest. onClose is called however the dialog was closed — the
 * close button, Escape, a press beside it or close() itself — so that what the
 * dialog was asked about is forgotten in one place rather than four.
 */
export function createDialog({ title = "", size = "narrow", dismissOnOutside = true, onClose } = {}) {
  ids += 1;
  const id = `dialog-title-${ids}`;
  const heading = el("h2", "dialog-title", title);
  heading.setAttribute("id", id);

  const closeButton = el("button", "btn btn-icon btn-sm dialog-close", "×");
  closeButton.setAttribute("type", "button");
  closeButton.setAttribute("aria-label", t("dialog_close"));

  const head = el("div", "dialog-head");
  head.append(heading, closeButton);
  const body = el("div", "dialog-body");
  const foot = el("div", "dialog-foot");

  const win = el("div", "dialog");
  win.dataset.size = size;
  win.setAttribute("role", "dialog");
  win.setAttribute("aria-modal", "true");
  win.setAttribute("aria-labelledby", id);
  win.append(head, body, foot);

  const element = el("div", "dialog-scrim");
  element.hidden = true;
  element.append(win);

  // What had the focus when the dialog opened, to give it back when it closes:
  // the window is a detour, and it ends where the person left off.
  let opener = null;

  const dialog = {
    element,
    window: win,
    body,
    foot,
    dismissOnOutside,
    get isOpen() {
      return open.includes(dialog);
    },
    open() {
      if (dialog.isOpen) return;
      opener = document.activeElement ?? null;
      element.hidden = false;
      open.push(dialog);
      // Added on every open, not only on the first: adding the same function
      // twice is a no-op, and this cannot then be wrong about whether it did.
      document.addEventListener("keydown", onKey, true);
      document.addEventListener("mousedown", onOutside);
      // The close button is a way out, not what the dialog is asking, so the
      // focus goes past it to the first control of the question itself.
      const targets = stops(win);
      (targets.find((node) => node !== closeButton) ?? targets[0])?.focus?.();
    },
    close() {
      if (!dialog.isOpen) return;
      element.hidden = true;
      open.splice(open.indexOf(dialog), 1);
      if (open.length === 0) {
        document.removeEventListener("keydown", onKey, true);
        document.removeEventListener("mousedown", onOutside);
      }
      opener?.focus?.();
      opener = null;
      onClose?.();
    },
  };

  closeButton.addEventListener("click", () => dialog.close());

  // The focus trap. Only the two ends are taken: between them the browser's own
  // order is better than anything this could recompute, and a dialog that moved
  // the focus itself on every Tab would fight the reading order it is given.
  win.addEventListener("keydown", (event) => {
    if (event.key !== "Tab") return;
    const targets = stops(win);
    if (targets.length === 0) return;
    const at = targets.indexOf(document.activeElement);
    const next = event.shiftKey ? at - 1 : at + 1;
    if (next >= 0 && next < targets.length) return;
    event.preventDefault?.();
    targets[event.shiftKey ? targets.length - 1 : 0].focus();
  });

  return dialog;
}

export default createDialog;
