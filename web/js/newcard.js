// Starting a card from the panel.
//
// A title, a description, a zone and the repository, and nothing else: the
// card is written further by the agent or the person who takes the task on,
// and the panel only has to be able to start one. Without this, a person with
// no editor open on the board had no way to put a first card on it. The
// repository is asked here because a worker is started in it; a card without
// one is worked in the home directory (T-134).
//
// The button that opens this is drawn in the board's new column (board.js) and
// the form is not: the board is redrawn whole from every snapshot, and a form
// inside #board would lose what was being typed. It lives in the centre
// column's tab row instead and opens as a window over the board (position:
// fixed, app.css), so opening it moves nothing — see
// docs/engineering/live-terminal.md section 6 on why a row that appears and
// goes away is not free in this page.
//
// The button is in the new column and in no other because a card cannot be
// started anywhere else: every stage but new needs a session on the card
// first (internal/board/write.go).

import { createCard, pickDirectory } from "./api.js";
import { get } from "./store.js";
import { t, reasonText } from "./i18n.js";

// The board's vocabulary of zones, in the order the operator's board lists them.
const ZONES = ["urgent", "unplanned", "planned", "niceToHave"];
const DEFAULT_ZONE = "unplanned";

function el(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}

// The repositories on the board, latest card first, each once: the history the
// repo field suggests from. The board is the panel's, so every browser and the
// window see the same list, and a repo a card was started with stays on it.
function boardRepos(cards) {
  const seen = new Set();
  return [...cards]
    .filter((c) => c.repo)
    .sort((a, b) => String(b.created ?? "").localeCompare(String(a.created ?? "")))
    .map((c) => c.repo)
    .filter((repo) => !seen.has(repo) && seen.add(repo));
}

// base64 is a file's bytes as JSON carries them to the server's []byte. In
// slices: spreading a whole screenshot into one call overflows the stack.
async function base64(file) {
  const bytes = new Uint8Array(await file.arrayBuffer());
  let binary = "";
  for (let i = 0; i < bytes.length; i += 0x8000) binary += String.fromCharCode(...bytes.subarray(i, i + 0x8000));
  return btoa(binary);
}

// A datalist is named by id, and a page can hold two forms at once.
let formCount = 0;

/**
 * createNewCard appends the "new card" form and its note to host, and hands
 * back the two ways it is opened. `create` is the write, `pick` the folder
 * dialog and `cards` the board's cards: api and store unless a test replaces
 * them.
 */
export function createNewCard(host, { create = createCard, pick = pickDirectory, cards = () => get()?.cards ?? [] } = {}) {
  const form = el("div", "newcard");
  form.hidden = true;
  form.setAttribute("role", "dialog");
  form.setAttribute("aria-label", t("new_card"));

  const title = el("input", "newcard-title");
  title.setAttribute("type", "text");
  title.setAttribute("placeholder", t("new_card_title"));
  title.setAttribute("aria-label", t("new_card_title"));

  const description = el("textarea", "newcard-desc");
  description.setAttribute("placeholder", t("new_card_desc"));
  description.setAttribute("aria-label", t("new_card_desc"));

  // The zone is the card's urgency, not its stage. Labelled in words and
  // wrapped in its label rather than named by aria-label alone: the operator
  // read the bare identifiers as stages and looked for the board's columns
  // among them. Each option shows the dictionary's word and sends the
  // schema's identifier, which is what the card's zone field must hold.
  const zoneLabel = el("label", "newcard-zone-label");
  zoneLabel.appendChild(el("span", "newcard-zone-caption", t("new_card_zone")));
  const zone = el("select", "newcard-zone");
  for (const name of ZONES) {
    const option = el("option", "", t(`zone_${name}`));
    option.value = name;
    zone.appendChild(option);
  }
  zone.value = DEFAULT_ZONE;
  zoneLabel.appendChild(zone);

  // Kept after a card is made: the next one is usually for the same checkout.
  const repo = el("input", "newcard-repo");
  repo.setAttribute("type", "text");
  repo.setAttribute("placeholder", t("new_card_repo"));
  repo.setAttribute("aria-label", t("new_card_repo"));
  const repos = el("datalist");
  repos.setAttribute("id", `newcard-repos-${++formCount}`);
  repo.setAttribute("list", repos.getAttribute("id"));

  // The browser never tells a page where a folder lives, so the panel shows the
  // Finder's dialog itself and answers the folder as a repo (api.pickDirectory).
  const pickButton = el("button", "newcard-pick btn", t("new_card_pick"));
  pickButton.setAttribute("type", "button");

  const repoRow = el("div", "newcard-repo-row");
  repoRow.append(zoneLabel, repo, repos, pickButton);

  const createButton = el("button", "newcard-create btn btn-primary", t("new_card_create"));
  createButton.setAttribute("type", "button");
  const closeButton = el("button", "newcard-close btn btn-icon", "×");
  closeButton.setAttribute("type", "button");
  closeButton.setAttribute("aria-label", t("new_card_cancel"));
  const head = el("div", "newcard-head");
  head.append(title, closeButton);

  // Claude Code refuses to start in a folder it was never trusted in, and the
  // panel cannot see that trust: it says what to do instead.
  const hint = el("p", "newcard-hint", t("new_card_pick_trust"));
  hint.hidden = true;

  // Pasted screenshots, dropped or chosen files: kept here until the card is
  // made, then written beside the board with it (internal/board).
  let attached = [];
  const fileList = el("ul", "newcard-files");
  const renderFiles = () => {
    fileList.replaceChildren(
      ...attached.map((file, index) => {
        const item = el("li", "newcard-file");
        const remove = el("button", "newcard-file-remove", "×");
        remove.setAttribute("type", "button");
        remove.setAttribute("aria-label", `${t("new_card_detach")} ${file.name}`);
        remove.addEventListener("click", () => {
          attached = attached.filter((_, i) => i !== index);
          renderFiles();
        });
        item.append(el("span", "newcard-file-name", file.name), remove);
        return item;
      }),
    );
  };
  const attach = (files) => {
    attached = [...attached, ...files];
    renderFiles();
  };
  const fileInput = el("input", "newcard-files-input");
  fileInput.setAttribute("type", "file");
  fileInput.setAttribute("multiple", "");
  fileInput.hidden = true;
  const attachButton = el("button", "newcard-attach btn", t("new_card_attach"));
  attachButton.setAttribute("type", "button");
  const filesRow = el("div", "newcard-files-row");
  filesRow.append(attachButton, fileInput, fileList);

  const error = el("div", "newcard-error");
  const actions = el("div", "newcard-actions");
  actions.append(error, createButton);

  form.append(head, description, filesRow, repoRow, hint, actions);

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
    repos.replaceChildren(
      ...boardRepos(cards()).map((name) => {
        const option = el("option");
        option.value = name;
        return option;
      }),
    );
    form.hidden = false;
    error.textContent = "";
    note.hidden = true;
    hint.hidden = true;
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
      const where = repo.value.trim();
      const files = await Promise.all(attached.map(async (file) => ({ name: file.name, data: await base64(file) })));
      const result = await create(text, zone.value, where, description.value.trim(), files);
      title.value = "";
      description.value = "";
      attached = [];
      renderFiles();
      repo.value = where;
      close();
      if (!result.committed) {
        note.textContent = `${t("new_card_not_committed")}: ${result.reason}`;
        note.hidden = false;
      }
    } catch (err) {
      // What was typed stays: the operator fixes it, not retypes it. A repo
      // the board refused is said by its code, in the page's words: it is the
      // one field here whose rule the server's English does not explain.
      const rule = String(err?.code ?? "").startsWith("repo_") ? reasonText("card_refused", err.code) : "";
      error.textContent = rule ? `repo: ${rule}` : String(err?.message ?? err);
    } finally {
      busy = false;
      createButton.disabled = false;
    }
  };

  const choose = async () => {
    pickButton.disabled = true;
    error.textContent = "";
    try {
      const chosen = await pick(t("new_card_pick_prompt"));
      if (chosen) {
        repo.value = chosen;
        hint.hidden = false;
      }
    } catch (err) {
      error.textContent = String(err?.message ?? err);
    } finally {
      pickButton.disabled = false;
    }
  };

  const toggle = () => (form.hidden ? open() : close());

  createButton.addEventListener("click", submit);
  pickButton.addEventListener("click", choose);
  attachButton.addEventListener("click", () => fileInput.click?.());
  fileInput.addEventListener("change", () => {
    attach([...(fileInput.files ?? [])]);
    fileInput.value = "";
  });
  // A paste that carries files attaches them; one that carries only text is
  // left to the field it lands in.
  form.addEventListener("paste", (ev) => {
    const files = [...(ev.clipboardData?.files ?? [])];
    if (files.length === 0) return;
    ev.preventDefault?.();
    attach(files);
  });
  form.addEventListener("dragover", (ev) => {
    if ([...(ev.dataTransfer?.types ?? [])].includes("Files")) ev.preventDefault?.();
  });
  form.addEventListener("drop", (ev) => {
    const files = [...(ev.dataTransfer?.files ?? [])];
    if (files.length === 0) return;
    ev.preventDefault?.();
    attach(files);
  });
  note.addEventListener("click", () => {
    note.hidden = true;
  });
  closeButton.addEventListener("click", close);
  const submitOnEnter = (ev) => {
    if (ev.key === "Enter") {
      ev.preventDefault?.();
      submit();
    }
  };
  title.addEventListener("keydown", submitOnEnter);
  repo.addEventListener("keydown", submitOnEnter);
  // In the description Enter is a new line; Cmd or Ctrl with it creates.
  description.addEventListener("keydown", (ev) => {
    if (ev.metaKey || ev.ctrlKey) submitOnEnter(ev);
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
