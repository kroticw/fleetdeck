// Starting a card from the panel.
//
// A title and a zone, and nothing else (the orchestrator's decision,
// 2026-09-11): the card is written further by the agent or the person who takes
// the task on, and the panel only has to be able to start one. Without this, a
// person with no editor open on the board had no way to put a first card on it.
//
// The button that opens this is drawn in the board's new column (board.js) and
// the form is not: the board is redrawn whole from every snapshot, and a form
// inside #board would lose what was being typed. It lives in the centre
// column's tab row instead and opens over the column (position: absolute,
// app.css), so opening it moves nothing — see docs/engineering/live-terminal.md
// section 6 on why a row that appears and goes away is not free in this page.
//
// The button is in the new column and in no other because a card cannot be
// started anywhere else: every stage but new needs a session on the card
// first (internal/board/write.go).

import { createCard } from "./api.js";
import { t } from "./i18n.js";

// The board's vocabulary of zones, in the order the operator's board lists them.
const ZONES = ["urgent", "unplanned", "planned", "niceToHave"];
const DEFAULT_ZONE = "unplanned";

function el(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}

/**
 * createNewCard appends the "new card" form and its note to host, and hands
 * back the two ways it is opened. `create` is the write, api.createCard unless
 * a test replaces it.
 */
export function createNewCard(host, { create = createCard } = {}) {
  const form = el("div", "newcard");
  form.hidden = true;
  form.setAttribute("role", "dialog");
  form.setAttribute("aria-label", t("new_card"));

  const title = el("input", "newcard-title");
  title.setAttribute("type", "text");
  title.setAttribute("placeholder", t("new_card_title"));
  title.setAttribute("aria-label", t("new_card_title"));

  const zone = el("select", "newcard-zone");
  zone.setAttribute("aria-label", t("new_card_zone"));
  for (const name of ZONES) {
    const option = el("option", "", name);
    option.value = name;
    zone.appendChild(option);
  }
  zone.value = DEFAULT_ZONE;

  const createButton = el("button", "newcard-create", t("new_card_create"));
  createButton.setAttribute("type", "button");
  const cancelButton = el("button", "newcard-cancel", t("new_card_cancel"));
  cancelButton.setAttribute("type", "button");
  const error = el("div", "newcard-error");

  form.append(title, zone, createButton, cancelButton, error);

  // What stays said after the form has closed: a card that reached the board
  // and not its history.
  const note = el("span", "newcard-note");
  note.hidden = true;

  host.append(form, note);

  let busy = false;
  // What had the focus when the form opened, to give it back when it closes:
  // the button that opened it is drawn inside the board and replaced by the
  // next snapshot, so it cannot be focused again by name.
  let opener = null;

  const close = () => {
    form.hidden = true;
    error.textContent = "";
    opener?.focus?.();
    opener = null;
  };

  const open = () => {
    opener = document.activeElement ?? null;
    form.hidden = false;
    error.textContent = "";
    note.hidden = true;
    title.focus();
  };

  const submit = async () => {
    if (busy) return;
    const text = title.value.trim();
    if (text === "") {
      error.textContent = t("new_card_title_required");
      return;
    }
    busy = true;
    createButton.disabled = true;
    error.textContent = "";
    try {
      const result = await create(text, zone.value);
      title.value = "";
      close();
      if (!result.committed) {
        note.textContent = `${t("new_card_not_committed")}: ${result.reason}`;
        note.hidden = false;
      }
    } catch (err) {
      // What was typed stays: the operator fixes it, not retypes it.
      error.textContent = String(err?.message ?? err);
    } finally {
      busy = false;
      createButton.disabled = false;
    }
  };

  const toggle = () => (form.hidden ? open() : close());

  createButton.addEventListener("click", submit);
  note.addEventListener("click", () => {
    note.hidden = true;
  });
  cancelButton.addEventListener("click", close);
  title.addEventListener("keydown", (ev) => {
    if (ev.key === "Enter") {
      ev.preventDefault?.();
      submit();
    }
  });
  // Escape from anywhere in the form, not only its title: the zone and the
  // buttons take focus too.
  form.addEventListener("keydown", (ev) => {
    if (ev.key !== "Escape") return;
    // Kept to the form: an Escape that reached the page would close an open
    // card, and one that reached a terminal would interrupt a session.
    ev.preventDefault?.();
    ev.stopPropagation?.();
    close();
  });

  // open for the board's button, and toggle for the fleetdeck window's new
  // card capsule, where a second press closes the form the first opened.
  return { open, toggle };
}
