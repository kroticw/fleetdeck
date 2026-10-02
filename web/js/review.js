// The review overlay: a session's branch as a diff, the operator's comments on
// its lines, and the agent's answers under them.
//
// Every piece of text here is the agent's — the diff, the file names, the
// replies — so none of it is ever markup: every node is built with
// createElement and filled with textContent.
//
// A comment is drawn where the server traced it, never where it was left: the
// line it was left on may hold other code now. A comment whose place is not a
// line of today's diff — its file gone, its commit gone, or its line outside
// the hunks shown — is listed above the diff with the code it was left on.

import * as serverApi from "./api.js";
import { t } from "./i18n.js";
import { closeCrossHTML } from "./icon.js";

function el(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}

const STATE_WORDS = {
  in_place: "",
  changed: "review_state_changed",
  deleted: "review_state_deleted",
  file_gone: "review_state_file_gone",
  unavailable: "review_state_unavailable",
};

function commentNode(c, reply, actions) {
  const box = el("div", `review-comment review-comment-${c.position.state}`);
  box.dataset.comment = c.id;
  const head = el("div", "review-comment-head");
  head.append(el("span", "review-comment-id", c.id));
  head.append(el("span", "review-comment-round", c.round === 0 ? t("review_draft") : `${t("review_round")} ${c.round}`));
  if (STATE_WORDS[c.position.state]) head.append(el("span", "review-comment-state", t(STATE_WORDS[c.position.state])));
  if (c.resolved) head.append(el("span", "review-comment-resolved", t("review_resolved")));
  box.append(head);
  if (c.position.state !== "in_place") {
    // The code the comment was left on: the only honest context for a comment
    // whose line was rewritten, deleted, or cannot be found.
    box.append(el("pre", "review-comment-was", c.anchor.text.join("\n")));
  }
  box.append(el("p", "review-comment-body", c.body));
  for (const r of reply) {
    const answer = el("div", r.parsed ? `review-reply review-reply-${r.status}` : "review-reply review-reply-raw");
    if (r.parsed) {
      answer.append(el("span", "review-reply-status", t(`review_reply_${r.status}`)));
      if (r.commit) answer.append(el("code", "review-reply-commit", r.commit));
      answer.append(el("p", "review-reply-text", r.text));
    } else {
      answer.append(el("pre", "review-reply-text", r.raw));
    }
    if (r.partial) answer.append(el("span", "review-reply-partial", t("review_reply_partial")));
    box.append(answer);
  }
  const bar = el("div", "review-comment-actions");
  // actions gets the comment's own box: editing and replying open a form
  // right under it, the way a line's "+" opens one under the line.
  for (const [label, run] of actions(c, box)) {
    const b = el("button", "review-comment-action", t(label));
    b.setAttribute("type", "button");
    b.addEventListener("click", run);
    bar.append(b);
  }
  if (bar.children.length > 0) box.append(bar);
  return box;
}

/**
 * renderReview draws the review of the card at cardPath into root and returns
 * its dispose function. options.api replaces the review calls of api.js,
 * for a test.
 */
export function renderReview(root, cardPath, onClose, options = {}) {
  const api = options.api ?? serverApi;
  let v = null;
  let disposed = false;
  let sending = false;
  let notice = "";

  const header = el("div", "review-header");
  const title = el("h2", "review-title", t("review_title"));
  const close = el("button", "btn btn-icon btn-md review-close");
  close.setAttribute("type", "button");
  close.setAttribute("aria-label", t("review_close"));
  close.innerHTML = closeCrossHTML;
  close.addEventListener("click", () => onClose());
  const send = el("button", "btn btn-primary review-send", t("review_send"));
  send.setAttribute("type", "button");
  const status = el("p", "review-status");
  header.append(title, send, close);
  const body = el("div", "review-body");
  root.replaceChildren(header, status, body);
  root.hidden = false;

  const repliesFor = (id) => (v?.replies ?? []).filter((r) => r.id === id);

  // The last line a "+" was clicked on, for a shift-click on a second line of
  // the same file and side: the range between the two, ordered by file
  // position rather than by which one was clicked first.
  let lastAdd = null;

  // What a failed write leaves behind: the anchor (or comment id) the
  // operator was writing about, and the words they had typed. The next paint
  // reopens the same form with the text still in it — a stale revision is the
  // common case, and the operator's text belongs in the retry, not the error.
  let restore = null;

  const write = async (call, onFailure) => {
    try {
      await call();
      notice = "";
    } catch (err) {
      notice = `${t("review_write_failed")}: ${err.message}`;
      onFailure?.();
    }
    await load();
  };

  const submitAdd = (anchor, words) =>
    write(
      () => api.addReviewComment(cardPath, v.rev, anchor, words),
      () => {
        restore = { kind: "line", text: words, ...anchor };
      },
    );

  const submitEdit = (id, words) =>
    write(
      () => api.editReviewComment(cardPath, v.rev, id, words),
      () => {
        restore = { kind: "edit", id, text: words };
      },
    );

  const submitReply = (c, words) => {
    const anchor = { commit: c.anchor.commit, path: c.anchor.path, side: c.anchor.side, start: c.anchor.start, end: c.anchor.end };
    return write(
      () => api.addReviewComment(cardPath, v.rev, anchor, words, c.id),
      () => {
        restore = { kind: "reply", id: c.id, text: words };
      },
    );
  };

  const openForm = (row, initialText, onSubmit, okLabel = "review_add") => {
    if (row.nextSibling?.classList?.contains("review-form")) return;
    const form = el("form", "review-form");
    const text = el("textarea", "review-form-text");
    text.value = initialText;
    const ok = el("button", "btn btn-primary review-form-ok", t(okLabel));
    ok.setAttribute("type", "submit");
    form.append(text, ok);
    form.addEventListener("submit", (event) => {
      event.preventDefault?.();
      const words = text.value.trim();
      if (words === "") return;
      onSubmit(words);
    });
    row.after(form);
  };

  const actions = (c, box) => {
    const out = [];
    if (c.round === 0) {
      out.push(["review_edit", () => openForm(box, c.body, (words) => submitEdit(c.id, words), "review_save")]);
      out.push(["review_delete", () => write(() => api.deleteReviewComment(cardPath, v.rev, c.id))]);
    } else {
      out.push([c.resolved ? "review_reopen" : "review_resolve", () => write(() => api.resolveReviewComment(cardPath, v.rev, c.id, !c.resolved))]);
      if (!c.resolved) {
        out.push(["review_reply", () => openForm(box, "", (words) => submitReply(c, words))]);
      }
    }
    return out;
  };

  // Builds one comment, appends it, and — if it is the one a failed edit or
  // reply left behind — reopens that form on it with the typed text back.
  const placeComment = (container, c) => {
    const box = commentNode(c, repliesFor(c.id), actions);
    container.append(box);
    if (restore?.kind === "edit" && restore.id === c.id) {
      const text = restore.text;
      restore = null;
      openForm(box, text, (words) => submitEdit(c.id, words), "review_save");
    } else if (restore?.kind === "reply" && restore.id === c.id) {
      const text = restore.text;
      restore = null;
      openForm(box, text, (words) => submitReply(c, words));
    }
  };

  const lineRow = (file, line) => {
    const row = el("div", `review-line review-line-${line.kind === "+" ? "add" : line.kind === "-" ? "del" : "ctx"}`);
    if (line.new) row.dataset.newLine = String(line.new);
    if (line.old) row.dataset.oldLine = String(line.old);
    row.append(el("span", "review-line-old", line.old ? String(line.old) : ""));
    row.append(el("span", "review-line-new", line.new ? String(line.new) : ""));
    const add = el("button", "review-line-add", "+");
    add.setAttribute("type", "button");
    add.setAttribute("aria-label", t("review_comment_line"));
    const side = line.kind === "-" ? "old" : "new";
    const n = side === "old" ? line.old : line.new;
    const commit = side === "old" ? v.base.commit : v.head;
    const path = side === "old" ? file.oldPath : file.newPath;
    add.addEventListener("click", (event) => {
      let start = n;
      let end = n;
      if (event.shiftKey && lastAdd && lastAdd.commit === commit && lastAdd.path === path && lastAdd.side === side) {
        start = Math.min(lastAdd.start, n);
        end = Math.max(lastAdd.start, n);
      }
      lastAdd = { commit, path, side, start: n };
      openForm(row, "", (words) => submitAdd({ commit, path, side, start, end }, words));
    });
    row.append(add);
    row.append(el("code", "review-line-text", line.text));
    // Matched on the range's start line alone: a failed range add is
    // relocated to reopen where the operator first clicked, keeping the
    // range restore below sends (start/end come from `restore`, not from
    // this one line).
    const matchesRestore = restore?.kind === "line" && restore.commit === commit && restore.path === path && restore.side === side && restore.start === n;
    return { row, matchesRestore };
  };

  // Rounds whose resend is in flight: a repaint while one is waiting must not
  // offer a second press.
  const notifying = new Set();

  const notify = async (n) => {
    if (notifying.has(n)) return;
    notifying.add(n);
    try {
      const answer = await api.notifyReviewRound(cardPath, v.rev, n);
      notice = answer.delivery
        ? `${t("review_round")} ${n}: ${t("review_not_delivered")} ${answer.delivery}`
        : `${t("review_round")} ${n}: ${t("review_sent")}`;
    } catch (err) {
      notice = `${t("review_send_failed")}: ${err.message}`;
    }
    notifying.delete(n);
    await load();
    status.textContent = notice;
  };

  // Every round sent, with whether the session was reached and whether the
  // agent has answered it. The resend is offered where either is in doubt —
  // a delivery that failed, or one never recorded (a crash between freezing
  // the round and recording it) that nobody has answered.
  const roundsNode = () => {
    const box = el("div", "review-rounds");
    for (const r of v.rounds) {
      const row = el("div", "review-round");
      row.dataset.round = String(r.n);
      row.append(el("span", "review-round-head", `${t("review_round")} ${r.n} · ${r.sent} · ${r.head.slice(0, 8)}`));
      if (r.delivery) row.append(el("span", "review-round-delivery", `${t("review_not_delivered")} ${r.delivery}`));
      const answered = v.comments.some((c) => c.round === r.n && repliesFor(c.id).length > 0);
      if (!answered) row.append(el("span", "review-round-unanswered", t("review_round_unanswered").replace("{n}", String(r.n))));
      if (r.delivery || !answered) {
        const again = el("button", "review-round-notify", t("review_notify_again"));
        again.setAttribute("type", "button");
        again.disabled = notifying.has(r.n);
        again.addEventListener("click", () => {
          again.disabled = true;
          notify(r.n);
        });
        row.append(again);
      }
      box.append(row);
    }
    return box;
  };

  const paint = () => {
    if (!v) return;
    const placedOn = new Map();
    const detached = [];
    for (const c of v.comments) {
      const p = c.position;
      const onLine = p.state === "in_place" || p.state === "changed" || p.state === "deleted";
      // An old-side anchor traces onto the base, so its position is an
      // old-side line of today's diff — file.oldPath/line.old — never the
      // new side a bare path:line key would otherwise collide with.
      const side = c.anchor.side === "old" ? "old" : "new";
      const key = `${side}:${p.path}:${p.end}`;
      if (onLine) {
        if (!placedOn.has(key)) placedOn.set(key, []);
        placedOn.get(key).push(c);
      } else {
        detached.push(c);
      }
    }
    const nodes = [];
    nodes.push(el("p", "review-base", `${t("review_base")} ${v.base.ref} ${v.base.commit.slice(0, 8)} → ${v.head.slice(0, 8)} · ${v.workdir}`));
    if (v.rounds.length > 0) nodes.push(roundsNode());
    if (v.uncommitted.length > 0) {
      const box = el("div", "review-uncommitted");
      box.append(el("p", "review-uncommitted-title", t("review_uncommitted")));
      for (const p of v.uncommitted) box.append(el("code", "review-uncommitted-path", p));
      nodes.push(box);
    }
    // The base is the fork point; once the branch is merged the fork point is
    // HEAD itself, the diff is empty, and blank space would read as a defect
    // rather than as nothing to review.
    if (v.base.commit === v.head) {
      nodes.push(el("p", "review-merged", t("review_merged")));
    }
    const shownLines = new Set();
    const fileNodes = [];
    for (const file of v.files) {
      const box = el("section", "review-file");
      const name = file.oldPath && file.newPath && file.oldPath !== file.newPath ? `${file.oldPath} → ${file.newPath}` : file.newPath || file.oldPath;
      box.append(el("h3", "review-file-name", name));
      if (file.binary) box.append(el("p", "review-file-note", t("review_binary")));
      if (file.truncated) box.append(el("p", "review-file-note", t("review_truncated")));
      for (const hunk of file.hunks ?? []) {
        for (const line of hunk.lines) {
          const { row, matchesRestore } = lineRow(file, line);
          box.append(row);
          if (matchesRestore) {
            const savedAnchor = { commit: restore.commit, path: restore.path, side: restore.side, start: restore.start, end: restore.end };
            const text = restore.text;
            restore = null;
            openForm(row, text, (words) => submitAdd(savedAnchor, words));
          }
          if (line.new) {
            const key = `new:${file.newPath}:${line.new}`;
            shownLines.add(key);
            for (const c of placedOn.get(key) ?? []) {
              const holder = el("div", "review-line-comments");
              holder.dataset.newLine = String(line.new);
              placeComment(holder, c);
              box.append(holder);
            }
          }
          if (line.old) {
            const key = `old:${file.oldPath}:${line.old}`;
            shownLines.add(key);
            for (const c of placedOn.get(key) ?? []) {
              const holder = el("div", "review-line-comments");
              holder.dataset.oldLine = String(line.old);
              placeComment(holder, c);
              box.append(holder);
            }
          }
        }
      }
      fileNodes.push(box);
    }
    // A comment traced onto a line the diff does not show (unchanged code far
    // from any hunk) has a place, but not on screen: listed apart as well.
    for (const [key, list] of placedOn) if (!shownLines.has(key)) detached.push(...list);
    if (detached.length > 0) {
      const box = el("div", "review-detached");
      box.append(el("p", "review-detached-title", t("review_detached")));
      for (const c of detached) {
        const p = c.position;
        box.append(el("code", "review-detached-where", p.start === p.end ? `${p.path}:${p.start}` : `${p.path}:${p.start}–${p.end}`));
        placeComment(box, c);
      }
      nodes.push(box);
    }
    // A failed write whose form found no place to reopen — the agent committed
    // meanwhile and the line or comment it was about is not drawn any more.
    // The words stay on the page to be copied; nothing is resubmitted.
    if (restore) {
      const box = el("div", "review-unsent");
      box.append(el("p", "review-unsent-title", t("review_unsent_text")));
      const text = el("textarea", "review-unsent-text");
      text.value = restore.text;
      text.setAttribute("readonly", "");
      box.append(text);
      nodes.unshift(box);
      restore = null;
    }
    body.replaceChildren(...nodes, ...fileNodes);
    const drafts = v.comments.filter((c) => c.round === 0).length;
    send.disabled = sending || drafts === 0;
    status.textContent = notice;
  };

  const load = async () => {
    try {
      const next = await api.fetchReview(cardPath);
      if (disposed) return;
      // Go encodes a nil slice as JSON null (a clean tree, a review nobody
      // has commented on, an agent that has not replied); normalized to []
      // once here so nothing downstream has to guard against it again.
      v = {
        ...next,
        files: next.files ?? [],
        uncommitted: next.uncommitted ?? [],
        comments: next.comments ?? [],
        replies: next.replies ?? [],
        rounds: next.rounds ?? [],
      };
    } catch (err) {
      if (disposed) return;
      notice = `${t("review_load_failed")}: ${err.message}`;
      body.replaceChildren();
    }
    paint();
    status.textContent = notice;
  };

  send.addEventListener("click", async () => {
    if (sending || !v) return;
    sending = true;
    send.disabled = true;
    try {
      const answer = await api.sendReviewRound(cardPath, v.rev);
      notice = answer.delivery
        ? `${t("review_round")} ${answer.round}: ${t("review_not_delivered")} ${answer.delivery}`
        : `${t("review_round")} ${answer.round}: ${t("review_sent")}`;
    } catch (err) {
      notice = `${t("review_send_failed")}: ${err.message}`;
    }
    sending = false;
    await load();
    status.textContent = notice;
  });

  load();
  return () => {
    disposed = true;
  };
}

export function createReviewPanel(panel, options = {}) {
  if (!panel) {
    console.error("fleetdeck: the review is not wired — the page has no #review-panel");
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
    open(cardPath) {
      close();
      dispose = renderReview(panel, cardPath, close, options);
    },
    close,
  };
}
