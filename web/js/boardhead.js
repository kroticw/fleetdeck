// web/js/boardhead.js — the board island's head beside the tabs (T-226):
// "Find card", which narrows the board to the cards it finds, and "+ Card",
// which opens the new card form. "/" puts the keyboard in the field from
// anywhere text is not being typed.
import { t } from "./i18n.js";

// Where a slash is text, not a shortcut: a field, a form, a terminal.
function typing(target) {
  const tag = target?.tagName ?? "";
  return tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT" || target?.isContentEditable === true;
}

export function mountBoardHead(row, { onFind, onNewCard }) {
  const find = document.createElement("input");
  find.type = "search";
  find.className = "board-find";
  find.placeholder = t("board_find");
  find.setAttribute("aria-label", t("board_find"));
  find.setAttribute("aria-keyshortcuts", "/");
  find.addEventListener("input", () => onFind(find.value));
  find.addEventListener("keydown", (event) => {
    if (event.key !== "Escape" || !find.value) return;
    find.value = "";
    onFind("");
  });

  const newCard = document.createElement("button");
  newCard.type = "button";
  newCard.className = "btn btn-primary board-new";
  newCard.textContent = t("new_card");
  newCard.addEventListener("click", () => onNewCard());

  row.appendChild(find);
  row.appendChild(newCard);

  document.addEventListener("keydown", (event) => {
    if (event.key !== "/" || event.metaKey || event.ctrlKey || event.altKey || typing(event.target)) return;
    event.preventDefault();
    find.focus();
  });
  return { find, newCard };
}
