// The board's failed commits, said where the operator looks (T-161).
//
// A card write answers once the file is written; the panel's queue commits it
// afterwards (internal/board.Committer), and every snapshot carries what the
// queue has to say (boardCommits). A commit that failed — the signing PIN not
// entered, most likely — left the write on disk and uncommitted, and nothing
// else on the page says so. This note does: how many, why, and a button that
// tries them again with the operator there to answer a PIN prompt. Commits on
// their way are no news, and say nothing.
//
// It is a row of the centre column of its own, right above #board — not inside
// it, which every snapshot replaces whole, and not hung over it from the tab
// row, where it covered the columns' heads: while it shows, the board moves
// down (the operator's call, 12.10).

import { retryCommits } from "./api.js";
import { subscribe } from "./store.js";
import { t } from "./i18n.js";

function el(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}

/**
 * renderCommitNote puts the note into host, right before `before` (the board),
 * and keeps it to the snapshot. retry is the API call unless a test replaces
 * it.
 */
export function renderCommitNote(host, { before = null, retry = retryCommits } = {}) {
  const note = el("div", "commit-note");
  note.hidden = true;
  note.setAttribute("role", "status");
  const text = el("span", "commit-note-text");
  const button = el("button", "btn btn-sm commit-retry", t("commits_retry"));
  button.setAttribute("type", "button");
  note.append(text, button);
  if (before) host.insertBefore(note, before);
  else host.append(note);

  // A refusal of the retry itself, shown while the queue still says what it
  // said when the retry was refused; its next word replaces it.
  let refused = "";
  let refusedOver = "";
  let last = null;
  const said = (snap) => `${snap?.boardCommits?.failed ?? 0}\u0000${snap?.boardCommits?.error ?? ""}`;

  const draw = () => {
    const failed = last?.boardCommits?.failed ?? 0;
    note.hidden = failed === 0;
    if (failed === 0) return;
    const why = refused || last.boardCommits.error || "";
    text.textContent = `${t("commits_failed")}: ${failed}${why ? ` — ${why}` : ""}`;
    text.setAttribute("title", why);
  };

  button.addEventListener("click", async () => {
    button.disabled = true;
    refused = "";
    try {
      await retry();
    } catch (err) {
      refused = String(err?.message ?? err);
      refusedOver = said(last);
    } finally {
      button.disabled = false;
      draw();
    }
  });

  subscribe((snap) => {
    last = snap;
    if (refused && said(snap) !== refusedOver) refused = "";
    draw();
  });
}
