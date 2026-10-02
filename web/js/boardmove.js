// What it means to drag a card into another column.
//
// board.js knows only that a card was dropped somewhere else; the rules are
// here, and there are three of them.
//
// One: every write carries the stage the board drew the card in. The board is
// drawn from a snapshot up to a second old and the card's own agent writes the
// same file, so without that the hand would put back the stage the agent has
// just moved on from — and both writes would succeed.
//
// Two: a card whose session is alive is being kept by that session. It is
// still moved when the operator says so — the board is theirs — but not
// silently, because the agent goes on writing the card it thinks it is keeping.
//
// Three: every stage but new needs a session on the card (internal/board), so
// a card with none dropped into active is offered one rather than refused.
// Dropped into review, blocked or done it is refused, in the board's own words.

import { createDialog } from "./dialog.js";
import { setCardField, startWork } from "./api.js";
import { t, reasonText } from "./i18n.js";
import { get } from "./store.js";

function el(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}

function button(className, label) {
  const node = el("button", className, label);
  node.setAttribute("type", "button");
  return node;
}

/**
 * createBoardMove builds the dialogs a move can need and hands back the
 * board's onMove: it answers the stage the board should draw until the next
 * snapshot agrees, or null when the card did not move.
 *
 * host is where the dialogs are appended — anywhere outside #board, which is
 * replaced whole by every snapshot. patch, start and snapshot are the reaches
 * outside, replaced by a test.
 */
export function createBoardMove(host, { patch = setCardField, start = startWork, snapshot = get } = {}) {
  // Built once and reused: they live outside the board, and a dialog rebuilt
  // per drop would lose the focus the old one was about to give back.
  let answer = null;
  const settle = (value) => {
    const pending = answer;
    answer = null;
    pending?.(value);
  };

  const heldText = el("div", "bmove-held-text");
  const heldGo = button("btn btn-primary bmove-go bmove-held-go", t("move_held_go"));
  const heldCancel = button("btn bmove-cancel bmove-held-cancel", t("move_cancel"));
  const heldDialog = createDialog({ title: t("move_held_title"), onClose: () => settle(false) });
  heldDialog.body.append(heldText);
  heldDialog.foot.append(heldCancel, heldGo);

  const startText = el("div", "bmove-start-text");
  const startGo = button("btn btn-primary bmove-go bmove-start-go", t("start_session_go"));
  const startCancel = button("btn bmove-cancel bmove-start-cancel", t("move_cancel"));
  const startDialog = createDialog({ title: t("start_session_title"), onClose: () => settle(false) });
  startDialog.body.append(startText);
  startDialog.foot.append(startCancel, startGo);

  // Accepting a card is the one move with a consequence outside the board: the
  // session behind it is put out. It is asked about instead of the held
  // question above — this one says everything that one does and more — and it
  // is asked only where the accident is possible, which is the drag. The same
  // stage chosen in the open card's select is two deliberate clicks on a named
  // value, and a question there would be a question on every card the operator
  // closes by hand.
  const doneText = el("div", "bmove-done-text");
  const doneGo = button("btn btn-primary bmove-go bmove-done-go", t("move_done_go"));
  const doneCancel = button("btn bmove-cancel bmove-done-cancel", t("move_cancel"));
  const doneDialog = createDialog({ title: t("move_done_title"), onClose: () => settle(false) });
  doneDialog.body.append(doneText);
  doneDialog.foot.append(doneCancel, doneGo);

  // What happened around a move that did happen. The card is in its new column
  // either way, and a cleanup that stopped halfway leaves a session running
  // that the operator has every reason to believe is out — the board draws
  // nothing about sessions, so this window is the only place it can be said.
  const afterText = el("div", "bmove-after-text");
  const afterDialog = createDialog({ title: t("move_after_title") });
  afterDialog.body.append(afterText);
  const afterClose = button("btn bmove-cancel bmove-after-close", t("move_close"));
  afterDialog.foot.append(afterClose);
  afterClose.addEventListener("click", () => afterDialog.close());

  // A refusal is its own window rather than a line inside the one that asked:
  // the question is over, and what is left is the board's own sentence saying
  // which rule refused — the detail the operator acts on.
  const refusedText = el("div", "bmove-error");
  const refusedDialog = createDialog({ title: t("move_refused_title") });
  refusedDialog.body.append(refusedText);
  const refusedClose = button("btn bmove-cancel bmove-refused-close", t("move_close"));
  refusedDialog.foot.append(refusedClose);
  refusedClose.addEventListener("click", () => refusedDialog.close());

  heldCancel.addEventListener("click", () => heldDialog.close());
  startCancel.addEventListener("click", () => startDialog.close());
  doneCancel.addEventListener("click", () => doneDialog.close());
  heldGo.addEventListener("click", () => {
    settle(true);
    heldDialog.close();
  });
  startGo.addEventListener("click", () => {
    settle(true);
    startDialog.close();
  });
  doneGo.addEventListener("click", () => {
    settle(true);
    doneDialog.close();
  });

  host.append(heldDialog.element, startDialog.element, doneDialog.element, afterDialog.element, refusedDialog.element);

  const ask = (dialog) =>
    new Promise((resolve) => {
      answer = resolve;
      dialog.open();
    });

  // By the code when the board sent one and this bundle has words for it, the
  // way the open card reads the same refusal (card.js): the board's sentence
  // is English and names the rule without the way out. The words are the
  // fallback, never nothing — a dispatch that failed carries no code at all.
  const refuse = (err) => {
    refusedText.textContent = reasonText("card_refused", err?.code) || String(err?.message ?? err);
    refusedDialog.open();
    return null;
  };

  return async function onMove({ path, from, to, session, live }) {
    // A card with no session dropped into active is offered one, because the
    // board will not take the stage without it. The session is started, its
    // short id written into the card and the task sent, in that order and by
    // the server (POST /api/sessions). A panel that starts no sessions says so
    // in the same window and offers nothing to press but the way out: the
    // snapshot tells the page, so it does not offer a start that would fail.
    if (to === "active" && !session) {
      const canStart = Boolean(snapshot()?.canStartWork);
      startText.textContent = canStart ? t("start_session_ask") : t("start_session_none");
      startGo.disabled = !canStart;
      if (!(await ask(startDialog))) return null;
      try {
        const result = await start(path);
        // A dispatch that got part of the way answers a success status with
        // ok false: the session may be running and the card may already name
        // it. The step that failed is what the operator has to read, and the
        // card is left where it is rather than drawn as moved.
        if (result?.ok === false) {
          const failed = (result.steps ?? []).find((step) => step.error);
          return refuse(new Error(failed?.error || t("start_session_failed")));
        }
        return to;
      } catch (err) {
        return refuse(err);
      }
    }

    if (to === "done" && live) {
      doneText.textContent = `${t("move_done_ask")} ${session}`;
      if (!(await ask(doneDialog))) return null;
    } else if (live) {
      heldText.textContent = `${t("move_held_ask")} ${session}`;
      if (!(await ask(heldDialog))) return null;
    }

    try {
      report(await patch(path, "stage", to, from));
      return to;
    } catch (err) {
      return refuse(err);
    }
  };

  // report shows what went wrong around a move that happened. Only the steps
  // that failed: a list of everything that worked is a window the operator
  // learns to dismiss without reading, and then the one line that mattered goes
  // with it.
  function report(result) {
    const failed = (result?.steps ?? []).filter((step) => step.error);
    if (failed.length === 0) return;
    afterText.textContent = failed.map((step) => `${step.name}: ${step.error}`).join("\n");
    afterDialog.open();
  }
}
