// Starting a card from the panel.
//
// A title and a zone, and nothing else (the orchestrator's decision,
// 2026-09-11): the card is written further by the agent or the person who takes
// the task on, and the panel only has to be able to start one. Without this, a
// person with no editor open on the board had no way to put a first card on it.
//
// The control sits in the centre column's tab row, not in #board: the board is
// redrawn whole from every snapshot, and a form inside it would lose what was
// being typed. The form opens over the column (position: absolute, app.css), so
// opening it moves nothing — see docs/engineering/live-terminal.md section 6 on
// why a row that appears and goes away is not free in this page.

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
 * createNewCard appends the "new card" button, its form and its note to host.
 * `create` is the write, api.createCard unless a test replaces it.
 */
export function createNewCard(host, { create = createCard } = {}) {
  const openButton = el("button", "newcard-open", t("new_card"));
  openButton.setAttribute("type", "button");

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

  host.append(openButton, form, note);

  let busy = false;

  const close = () => {
    form.hidden = true;
    error.textContent = "";
  };

  const open = () => {
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

  openButton.addEventListener("click", () => (form.hidden ? open() : close()));
  createButton.addEventListener("click", submit);
  note.addEventListener("click", () => {
    note.hidden = true;
  });
  cancelButton.addEventListener("click", close);
  title.addEventListener("keydown", (ev) => {
    if (ev.key === "Enter") {
      ev.preventDefault?.();
      submit();
    } else if (ev.key === "Escape") {
      // Kept to the form: an Escape that reached the page would close an open
      // card, and one that reached a terminal would interrupt a session.
      ev.preventDefault?.();
      ev.stopPropagation?.();
      close();
      openButton.focus();
    }
  });
}
