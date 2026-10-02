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
import { createCardDock } from "./carddock.js";
import { buildFontControls } from "./fontcontrols.js";
import { t } from "./i18n.js";
import { closeCrossHTML } from "./icon.js";
import { subscribe as storeSubscribe } from "./store.js";
import { clampFontSize, rememberFontSize, storedFontSize } from "./terminalfont.js";

// The code's size is the review's own, not a terminal's: the diff is read, not
// typed into, and its column is as wide as the sheet.
export const REVIEW_FONT_KEY = "fleetdeck-review-font";
// What the code was drawn at before it could be changed: .review-line's
// --fs-sm × 1.18 off the 14 px root, rounded.
export const REVIEW_DEFAULT_FONT_SIZE = 16;

function el(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}

// How many unchanged lines one press of ↑ or ↓ reveals around a hunk.
const EXPAND_STEP = 20;

// The context lines around a change in the diff the page is given: git's
// --unified=3 in internal/review.Git.RawDiff.
const DIFF_CONTEXT = 3;

// A hunk's first line and the line past it on one side. A side with no lines
// (count 0) sits after its start line, so both are start + 1 there.
const firstOf = (start, count) => (count === 0 ? start + 1 : start);
const pastOf = (start, count) => (count === 0 ? start + 1 : start + count);

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
    const b = el("button", "btn btn-sm review-comment-action", t(label));
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
 *
 * options.onBack(cardPath) goes back to the card, from the header's button and
 * from Escape; without it both close the review. The card's session is drawn
 * beside the diff by the card's own dock (web/js/carddock.js), and
 * options.subscribe, links, toOrchestrator, terminal, observe, storage and
 * resume reach it the way renderCard's do.
 */
export function renderReview(root, cardPath, onClose, options = {}) {
  const api = options.api ?? serverApi;
  const subscribe = options.subscribe ?? storeSubscribe;
  const onBack = options.onBack ?? (() => onClose());
  let v = null;
  let disposed = false;
  let sending = false;
  let notice = "";

  const header = el("div", "review-header");
  const back = el("button", "btn btn-md review-back", `← ${t("review_back")}`);
  back.setAttribute("type", "button");
  back.addEventListener("click", () => onBack(cardPath));
  const title = el("h2", "review-title", t("review_title"));
  const close = el("button", "btn btn-icon btn-md review-close");
  close.setAttribute("type", "button");
  close.setAttribute("aria-label", t("review_close"));
  close.innerHTML = closeCrossHTML;
  close.addEventListener("click", () => onClose());
  const send = el("button", "btn btn-primary review-send", t("review_send"));
  send.setAttribute("type", "button");
  const status = el("p", "review-status");
  const body = el("div", "review-body");

  let fontSize = storedFontSize(REVIEW_FONT_KEY, REVIEW_DEFAULT_FONT_SIZE);
  const font = buildFontControls({
    buttonClass: "review-font",
    defaultSize: REVIEW_DEFAULT_FONT_SIZE,
    onStep: (step) => {
      fontSize = step === 0 ? REVIEW_DEFAULT_FONT_SIZE : clampFontSize(fontSize + step);
      rememberFontSize(REVIEW_FONT_KEY, fontSize, REVIEW_DEFAULT_FONT_SIZE);
      paintFont();
    },
  });
  const paintFont = () => {
    body.style.setProperty("--review-code-size", `${fontSize}px`);
    font.paint(fontSize);
  };
  paintFont();

  header.append(back, title, font.node, send, close);

  // The diff and the card's session on one stage, laid out by the card
  // sheet's rules (.card-stage, .card-dock): the session is read and answered
  // while the diff stays on screen.
  const stage = el("div", "card-stage review-stage");
  const grip = el("div", "card-dock-grip");
  const dock = el("div", "card-dock");
  grip.hidden = true;
  dock.hidden = true;
  stage.dataset.dock = "bottom";
  stage.append(body, grip, dock);
  root.replaceChildren(header, status, stage);
  root.hidden = false;

  const sessionPlace = createCardDock(dock, {
    stage,
    grip,
    expand: true,
    links: options.links,
    toOrchestrator: options.toOrchestrator,
    terminal: options.terminal,
    observe: options.observe,
    storage: options.storage,
    resume: options.resume,
  });
  const unsubscribe = subscribe((snap) => {
    if (disposed) return;
    const card = (snap?.cards ?? []).find((c) => c.path === cardPath);
    sessionPlace.show(card?.session ? { short: card.session, from: "card" } : null, snap);
  });

  // Escape inside the session is the session's, and one a comment form took
  // (it prevents the default) closes only the form.
  const onKey = (event) => {
    if (event.key !== "Escape" || event.defaultPrevented) return;
    if (dock.contains(event.target)) return;
    onBack(cardPath);
  };
  document.addEventListener("keydown", onKey);

  const repliesFor = (id) => (v?.replies ?? []).filter((r) => r.id === id);

  // The last line a "+" was clicked on, for a shift-click on a second line of
  // the same file and side: the range between the two, ordered by file
  // position rather than by which one was clicked first.
  let lastAdd = null;

  // Every commentable row drawn, with where a comment on it is anchored: what
  // a drag reads to tell which rows it covers. Rebuilt by each paint.
  let rows = new Map();
  // A drag from a line's "+": the anchor it started on and the line it is
  // over now. A range never leaves its file and side.
  let drag = null;
  // The range a released drag picked, marked for as long as its form is open.
  let picked = null;
  const sameTarget = (a, b) => a.commit === b.commit && a.path === b.path && a.side === b.side;
  const markDrag = () => {
    const range = drag ?? picked;
    const lo = range && Math.min(range.start, range.end);
    const hi = range && Math.max(range.start, range.end);
    for (const [row, at] of rows) {
      row.classList.toggle("review-line-picked", range !== null && sameTarget(at, range) && at.n >= lo && at.n <= hi);
    }
  };
  const cancelDrag = () => {
    if (!drag) return;
    drag = null;
    markDrag();
  };
  // The release opens the range form under the range's last line. A press and
  // release on one line is a click, and the "+"'s own click handles it.
  const onRelease = () => {
    if (!drag) return;
    const d = drag;
    drag = null;
    const anchor = { commit: d.commit, path: d.path, side: d.side, start: Math.min(d.start, d.end), end: Math.max(d.start, d.end) };
    const last = d.start === d.end ? null : [...rows].find(([, at]) => sameTarget(at, d) && at.n === anchor.end)?.[0];
    if (last) {
      lastAdd = { commit: d.commit, path: d.path, side: d.side, start: d.start };
      picked = anchor;
      const unpick = () => {
        picked = null;
        markDrag();
      };
      if (!openForm(last, "", (words) => submitAdd(anchor, words), "review_add", { onClose: unpick })) picked = null;
    }
    markDrag();
  };
  // A release outside the window never reaches the page: the drag ends when
  // the pointer leaves the page (a mouseleave with nowhere to go) or comes
  // back over a row with no button held (the row's mouseover, below).
  const onLeave = (event) => {
    if (event.relatedTarget == null) cancelDrag();
  };
  document.addEventListener("mouseup", onRelease);
  document.addEventListener("mouseleave", onLeave, true);

  // Unchanged lines revealed around the hunks, per file, at the head they were
  // read from: kept across repaints (every write repaints), dropped when the
  // head moves. total is the file's length once a read has said it.
  let context = { head: "", files: new Map() };
  const contextOf = (path) => {
    if (!context.files.has(path)) context.files.set(path, { lines: new Map(), total: Infinity });
    return context.files.get(path);
  };

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

  // What each open form is about — the line it is under, or the comment — and
  // what it does, so a repaint can put it back with the words in it.
  const formOf = new WeakMap();

  // A form already open under the row gets the focus instead of a second one,
  // and null comes back. Cancel and Escape drop the form and write nothing; a
  // submit drops it too, and a failed write brings it back through `restore`.
  const openForm = (row, initialText, onSubmit, okLabel = "review_add", { onClose, focus = true } = {}) => {
    const open = row.nextSibling;
    if (open?.classList?.contains("review-form")) {
      open.querySelector("textarea")?.focus();
      return null;
    }
    const form = el("form", "review-form");
    const text = el("textarea", "review-form-text");
    text.value = initialText;
    const close = () => {
      form.remove();
      onClose?.();
    };
    text.addEventListener("keydown", (event) => {
      if (event.key !== "Escape") return;
      event.preventDefault?.();
      event.stopPropagation?.();
      close();
    });
    const cancel = el("button", "btn review-form-cancel", t("review_cancel"));
    cancel.setAttribute("type", "button");
    cancel.addEventListener("click", close);
    const ok = el("button", "btn btn-primary review-form-ok", t(okLabel));
    ok.setAttribute("type", "submit");
    const bar = el("div", "review-form-actions");
    bar.append(cancel, ok);
    form.append(text, bar);
    form.addEventListener("submit", (event) => {
      event.preventDefault?.();
      const words = text.value.trim();
      if (words === "") return;
      close();
      onSubmit(words);
    });
    const line = rows.get(row);
    formOf.set(form, { line, comment: line ? undefined : row.dataset.comment, onSubmit, okLabel, onClose });
    row.after(form);
    if (focus) text.focus();
    return form;
  };

  // Every form open in the body, with its words and whether it has the focus:
  // a repaint rebuilds the body whole, and an unsent comment must not go
  // with it.
  const openForms = () =>
    [...body.querySelectorAll(".review-form")].flatMap((form) => {
      const about = formOf.get(form);
      const text = form.querySelector("textarea");
      return about ? [{ ...about, text: text.value, focused: document.activeElement === text }] : [];
    });

  // Puts the forms openForms saw back under their line or comment. One whose
  // place is gone keeps its words on the page, as a failed write's do.
  const reopen = (saved) => {
    for (const s of saved) {
      const at = s.line
        ? [...rows].find(([, a]) => sameTarget(a, s.line) && a.n === s.line.n)?.[0]
        : [...body.querySelectorAll("[data-comment]")].find((box) => box.dataset.comment === s.comment);
      if (at) {
        openForm(at, s.text, s.onSubmit, s.okLabel, { onClose: s.onClose, focus: s.focused });
        continue;
      }
      s.onClose?.();
      const box = el("div", "review-unsent");
      box.append(el("p", "review-unsent-title", t("review_unsent_text")));
      const text = el("textarea", "review-unsent-text");
      text.value = s.text;
      text.setAttribute("readonly", "");
      box.append(text);
      body.insertBefore(box, body.firstChild);
    }
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
    // Pressed without a text selection starting under the drag.
    add.addEventListener("mousedown", (event) => {
      if (event.button !== 0) return;
      event.preventDefault?.();
      drag = { commit, path, side, start: n, end: n };
      markDrag();
    });
    row.addEventListener("mouseover", (event) => {
      if (drag && event.buttons === 0) {
        cancelDrag();
        return;
      }
      if (!drag || !sameTarget(drag, { commit, path, side })) return;
      drag.end = n;
      markDrag();
    });
    rows.set(row, { commit, path, side, n });
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
        const again = el("button", "btn btn-sm review-round-notify", t("review_notify_again"));
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

  // Reads new-side lines from..to of file at the head on screen, keeps them,
  // and repaints: the revealed lines are then drawn, and commented on, like
  // any other context line.
  const expand = async (file, from, to) => {
    const head = v.head;
    try {
      const got = await api.fetchReviewLines(cardPath, head, file.newPath, from, to);
      if (disposed || context.head !== head) return;
      const known = contextOf(file.newPath);
      known.total = got.total;
      got.lines.forEach((text, i) => known.lines.set(got.from + i, text));
    } catch (err) {
      if (disposed) return;
      notice = `${t("review_expand_failed")}: ${err.message}`;
    }
    paint();
  };

  // The controls of one gap: ↓ the lines under the hunk above, ↑ the lines
  // over the hunk below, or all of them; a short gap is one press.
  const expander = (file, from, to, { up, down }) => {
    const node = el("div", "review-expand");
    const count = to - from + 1;
    const offer = (className, label, a, b) => {
      const button = el("button", className, label);
      button.setAttribute("type", "button");
      button.addEventListener("click", () => {
        button.disabled = true;
        expand(file, a, b);
      });
      node.append(button);
    };
    if (count <= EXPAND_STEP) {
      offer("review-expand-all", t("review_expand_all").replace("{n}", String(count)), from, to);
      return node;
    }
    const step = t("review_expand_step").replace("{n}", String(EXPAND_STEP));
    if (down) offer("review-expand-down", `↓ ${step}`, from, from + EXPAND_STEP - 1);
    if (up) offer("review-expand-up", `↑ ${step}`, to - EXPAND_STEP + 1, to);
    if (Number.isFinite(count)) offer("review-expand-all", t("review_expand_all").replace("{n}", String(count)), from, to);
    return node;
  };

  const paint = () => {
    if (!v) return;
    const saved = openForms();
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
    // Review reads commits only: work the session has not committed is named
    // here and nowhere else, with what it takes to bring it in.
    if (v.uncommitted.length > 0) {
      const box = el("div", "review-uncommitted");
      box.append(el("p", "review-commit-hint", t("review_commit_hint")));
      box.append(el("p", "review-uncommitted-title", t("review_uncommitted")));
      for (const p of v.uncommitted) box.append(el("code", "review-uncommitted-path", p));
      nodes.push(box);
    }
    // base == head is an empty diff, and blank space would read as a defect.
    // Said neutrally: a merged branch and a session that has not committed
    // yet look the same from here.
    if (v.base.commit === v.head) {
      nodes.push(el("p", "review-no-commits", t("review_no_commits")));
    }
    rows = new Map();
    if (context.head !== v.head) context = { head: v.head, files: new Map() };
    const shownLines = new Set();

    // One diff or context line, the comments traced onto it, and the form a
    // failed add left on it.
    const appendLine = (box, file, line) => {
      const { row, matchesRestore } = lineRow(file, line);
      box.append(row);
      if (matchesRestore) {
        const savedAnchor = { commit: restore.commit, path: restore.path, side: restore.side, start: restore.start, end: restore.end };
        const text = restore.text;
        restore = null;
        openForm(row, text, (words) => submitAdd(savedAnchor, words));
      }
      for (const [side, path, n] of [["new", file.newPath, line.new], ["old", file.oldPath, line.old]]) {
        if (!n) continue;
        const key = `${side}:${path}:${n}`;
        shownLines.add(key);
        for (const c of placedOn.get(key) ?? []) {
          const holder = el("div", "review-line-comments");
          holder.dataset[side === "new" ? "newLine" : "oldLine"] = String(n);
          placeComment(holder, c);
          box.append(holder);
        }
      }
    };

    // The unchanged new-side lines from..to (to Infinity: up to the file's
    // end, not yet known), old = new + offset. What was revealed is drawn at
    // the gap's edges; what is left is one row of expand controls.
    const gap = (box, file, from, to, offset, { up, down }) => {
      const known = contextOf(file.newPath);
      const end = Math.min(to, known.total);
      const drawn = (n) => ({ kind: " ", old: n + offset, new: n, text: known.lines.get(n) });
      let top = from;
      while (top <= end && known.lines.has(top)) appendLine(box, file, drawn(top++));
      if (!Number.isFinite(end)) {
        box.append(expander(file, top, end, { up: false, down }));
        return;
      }
      let bottom = end;
      while (bottom >= top && known.lines.has(bottom)) bottom -= 1;
      if (top <= bottom) box.append(expander(file, top, bottom, { up, down }));
      for (let n = bottom + 1; n <= end; n += 1) appendLine(box, file, drawn(n));
    };

    const fileNodes = [];
    for (const file of v.files) {
      const box = el("section", "review-file");
      const name = file.oldPath && file.newPath && file.oldPath !== file.newPath ? `${file.oldPath} → ${file.newPath}` : file.newPath || file.oldPath;
      box.append(el("h3", "review-file-name", name));
      if (file.binary) box.append(el("p", "review-file-note", t("review_binary")));
      if (file.truncated) box.append(el("p", "review-file-note", t("review_truncated")));
      const hunks = file.hunks ?? [];
      // Only a file on both sides has unchanged code around its hunks: a new
      // file's hunk is all of it, a deleted one has nothing at head to read.
      const expandable = Boolean(file.oldPath && file.newPath) && hunks.length > 0;
      hunks.forEach((hunk, i) => {
        if (expandable) {
          const prev = hunks[i - 1];
          const first = firstOf(hunk.newStart, hunk.newCount);
          gap(box, file, prev ? pastOf(prev.newStart, prev.newCount) : 1, first - 1, firstOf(hunk.oldStart, hunk.oldCount) - first, { up: true, down: Boolean(prev) });
        }
        for (const line of hunk.lines) appendLine(box, file, line);
      });
      const last = hunks.at(-1);
      // The shown diff has DIFF_CONTEXT lines of context after a change
      // wherever the file has them: fewer after the last one means the file
      // ends there, and there is nothing below to expand.
      const trailing = expandable ? last.lines.length - 1 - last.lines.findLastIndex((l) => l.kind !== " ") : 0;
      if (expandable && trailing >= DIFF_CONTEXT) {
        const past = pastOf(last.newStart, last.newCount);
        gap(box, file, past, Infinity, pastOf(last.oldStart, last.oldCount) - past, { up: false, down: true });
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
    reopen(saved);
    markDrag();
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
    unsubscribe();
    sessionPlace.dispose();
    document.removeEventListener("keydown", onKey);
    document.removeEventListener("mouseup", onRelease);
    document.removeEventListener("mouseleave", onLeave, true);
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
    // open(cardPath, how): how is the card sheet's own opening (its tab), handed
    // back with the card to options.onBack so the way back lands where the
    // review was opened from.
    open(cardPath, how = {}) {
      close();
      const onBack = options.onBack ? (path) => options.onBack(path, how) : undefined;
      dispose = renderReview(panel, cardPath, close, { ...options, onBack });
    },
    close,
  };
}
