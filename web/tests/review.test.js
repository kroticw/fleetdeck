// The review overlay, driven from stubbed API functions.
//
// What it is easy to get wrong: putting diff text into the page as markup,
// drawing a comment on a line it no longer belongs to, losing the operator's
// draft to a stale revision, and repeating a send the operator did not ask for.

import { test, beforeEach, afterEach } from "node:test";
import assert from "node:assert/strict";

import { installDOM, fireEvent, fireDocumentEvent, settle } from "./fake-dom.js";
import { t } from "../js/i18n.js";

let dom;
beforeEach(() => {
  dom = installDOM();
});
afterEach(() => dom.restore());

const HEAD = "9ab41d7000000000000000000000000000000000";

function view(overrides = {}) {
  return {
    workdir: "/w",
    head: HEAD,
    base: { commit: "b".repeat(40), ref: "origin/master" },
    files: [
      {
        oldPath: "a.go",
        newPath: "a.go",
        binary: false,
        truncated: false,
        hunks: [
          {
            oldStart: 1, oldCount: 1, newStart: 1, newCount: 2,
            lines: [
              { kind: " ", old: 1, new: 1, text: "package a" },
              { kind: "+", old: 0, new: 2, text: "<img src=x onerror=alert(1)>" },
            ],
          },
        ],
      },
    ],
    uncommitted: [],
    comments: [],
    replies: [],
    rounds: [],
    rev: 0,
    ...overrides,
  };
}

function api(v, calls = []) {
  return {
    fetchReview: async () => v,
    addReviewComment: async (...args) => {
      calls.push(["add", ...args]);
      return { rev: 1, comments: [] };
    },
    sendReviewRound: async (...args) => {
      calls.push(["send", ...args]);
      return { round: 1, delivery: "", comments: { rev: 2, comments: [] } };
    },
    editReviewComment: async (...args) => {
      calls.push(["edit", ...args]);
      return {};
    },
    deleteReviewComment: async () => ({}),
    resolveReviewComment: async () => ({}),
  };
}

// The label on a comment's action bar (edit/delete/resolve/reopen/reply) is
// the only thing that tells two buttons apart in this fake DOM.
function actionButton(scope, label) {
  return [...scope.querySelectorAll(".review-comment-action")].find((b) => b.textContent === label);
}

test("diff text reaches the page as text, never as markup", async () => {
  const { renderReview } = await import("../js/review.js");
  const root = document.createElement("div");
  renderReview(root, "/b/cards/T-057.md", () => {}, { api: api(view()) });
  await settle();
  assert.equal(root.querySelectorAll("img").length, 0);
  assert.ok(root.textContent.includes("<img src=x onerror=alert(1)>"));
});

test("a comment is drawn at its traced position with its state", async () => {
  const { renderReview } = await import("../js/review.js");
  const root = document.createElement("div");
  const v = view({
    comments: [
      {
        id: "c1", round: 1, body: "wrap it", resolved: false,
        anchor: { commit: "c", path: "a.go", side: "new", start: 1, end: 1, text: ["return err"], before: [], after: [] },
        position: { path: "a.go", start: 2, end: 2, state: "changed" },
      },
    ],
  });
  renderReview(root, "/b/cards/T-057.md", () => {}, { api: api(v) });
  await settle();
  const note = root.querySelector('[data-comment="c1"]');
  assert.ok(note, "the comment is on the page");
  assert.equal(note.closest("[data-new-line]").dataset.newLine, "2");
  assert.ok(note.textContent.includes(t("review_state_changed")));
  assert.ok(note.textContent.includes("return err"), "a changed comment shows the code it was left on");
});

test("a comment whose place is unknown is listed apart, never on a line", async () => {
  const { renderReview } = await import("../js/review.js");
  const root = document.createElement("div");
  const v = view({
    comments: [
      {
        id: "c1", round: 1, body: "x", resolved: false,
        anchor: { commit: "c", path: "gone.go", side: "new", start: 3, end: 3, text: ["old"], before: [], after: [] },
        position: { path: "gone.go", start: 3, end: 3, state: "unavailable" },
      },
    ],
  });
  renderReview(root, "/b/cards/T-057.md", () => {}, { api: api(v) });
  await settle();
  const note = root.querySelector('[data-comment="c1"]');
  assert.equal(note.closest("[data-new-line]"), null);
  assert.ok(note.closest(".review-detached"));
});

test("a new comment is anchored to the head the page read", async () => {
  const { renderReview } = await import("../js/review.js");
  const root = document.createElement("div");
  const calls = [];
  renderReview(root, "/b/cards/T-057.md", () => {}, { api: api(view(), calls) });
  await settle();
  fireEvent(root.querySelector('[data-new-line="2"] .review-line-add'), "click");
  const form = root.querySelector(".review-form");
  form.querySelector("textarea").value = "name it";
  fireEvent(form, "submit");
  await settle();
  const [, card, rev, anchor, body] = calls.find((c) => c[0] === "add");
  assert.equal(card, "/b/cards/T-057.md");
  assert.equal(rev, 0);
  assert.deepEqual(anchor, { commit: HEAD, path: "a.go", side: "new", start: 2, end: 2 });
  assert.equal(body, "name it");
});

test("send is pressed once and sent once, and a failed delivery is shown, not retried", async () => {
  const { renderReview } = await import("../js/review.js");
  const root = document.createElement("div");
  const calls = [];
  const a = api(view({ comments: [{ id: "c1", round: 0, body: "x", resolved: false, anchor: { commit: HEAD, path: "a.go", side: "new", start: 2, end: 2, text: ["y"], before: [], after: [] }, position: { path: "a.go", start: 2, end: 2, state: "in_place" } }] }), calls);
  a.sendReviewRound = async (...args) => {
    calls.push(["send", ...args]);
    return { round: 1, delivery: "daemon gone", comments: { rev: 2, comments: [] } };
  };
  renderReview(root, "/b/cards/T-057.md", () => {}, { api: a });
  await settle();
  fireEvent(root.querySelector(".review-send"), "click");
  await settle();
  assert.equal(calls.filter((c) => c[0] === "send").length, 1);
  assert.ok(root.textContent.includes("daemon gone"));
});

// Fix round 1 — an old-side anchor traces onto the base, so its position is
// an old-side line of today's diff (file.oldPath / line.old), not the new
// side: keying every comment by path:line alone let an old-side comment land
// under a same-numbered new-side row of other code, with no mark at all.
test("an old-side comment sits under its removed line, not a same-numbered new-side row", async () => {
  const { renderReview } = await import("../js/review.js");
  const root = document.createElement("div");
  const v = view({
    files: [
      {
        oldPath: "a.go", newPath: "a.go", binary: false, truncated: false,
        hunks: [
          {
            oldStart: 4, oldCount: 2, newStart: 4, newCount: 1,
            lines: [
              { kind: " ", old: 4, new: 4, text: "package a" },
              { kind: "-", old: 5, new: 0, text: "return err" },
              { kind: "+", old: 0, new: 5, text: "return nil" },
            ],
          },
        ],
      },
    ],
    comments: [
      {
        id: "c1", round: 1, body: "old note", resolved: false,
        anchor: { commit: "b".repeat(40), path: "a.go", side: "old", start: 5, end: 5, text: ["return err"], before: [], after: [] },
        position: { path: "a.go", start: 5, end: 5, state: "in_place" },
      },
    ],
  });
  renderReview(root, "/b/cards/T-057.md", () => {}, { api: api(v) });
  await settle();
  const note = root.querySelector('[data-comment="c1"]');
  assert.ok(note, "the comment is on the page");
  assert.equal(note.closest("[data-old-line]").dataset.oldLine, "5");
  assert.equal(note.closest("[data-new-line]"), null, "an old-side comment must not attach to the same-numbered new-side row");
});

// Go encodes a nil slice as JSON null: a clean tree (no uncommitted files), a
// review nobody has commented on yet, and an agent that has not replied are
// all ordinary, not exceptional.
test("null files, uncommitted, comments, replies and rounds do not crash the overlay", async () => {
  const { renderReview } = await import("../js/review.js");
  const root = document.createElement("div");
  const v = view({ files: null, uncommitted: null, comments: null, replies: null, rounds: null });
  renderReview(root, "/b/cards/T-057.md", () => {}, { api: api(v) });
  await settle();
  assert.ok(root.querySelector(".review-base"), "the overlay finished painting instead of crashing on a null array");
});

// Spec §4: base == head is an empty diff, and the page says so rather than
// showing blank space that reads as a bug. It is said neutrally: the same
// picture is a merged branch and a session that has not committed yet, and
// "already merged" was false for the second (the operator's first use).
test("a branch with no commits on its base says so neutrally, not that it was merged", async () => {
  const { renderReview } = await import("../js/review.js");
  const root = document.createElement("div");
  const v = view({ base: { commit: HEAD, ref: "origin/master" }, head: HEAD, files: [] });
  renderReview(root, "/b/cards/T-057.md", () => {}, { api: api(v) });
  await settle();
  assert.ok(root.textContent.includes(t("review_no_commits")));
  assert.ok(!/merged|влит/.test(root.textContent));
});

test("uncommitted files bring the hint that only committed code is reviewed", async () => {
  const { renderReview } = await import("../js/review.js");
  const dirty = document.createElement("div");
  renderReview(dirty, "/b/cards/T-057.md", () => {}, { api: api(view({ uncommitted: ["x.go"] })) });
  const clean = document.createElement("div");
  renderReview(clean, "/b/cards/T-057.md", () => {}, { api: api(view()) });
  await settle();
  assert.equal(dirty.querySelector(".review-commit-hint").textContent, t("review_commit_hint"));
  assert.equal(clean.querySelector(".review-commit-hint"), null);
});

test("the commit hint and the empty-branch line are written in both languages", async () => {
  const { readFileSync } = await import("node:fs");
  const source = readFileSync(new URL("../js/i18n.js", import.meta.url), "utf8");
  assert.match(source, /review_commit_hint: "[^"]*commit[^"]*"/);
  assert.match(source, /review_commit_hint: "[^"]*закоммит[^"]*"/);
  assert.match(source, /review_no_commits: "the branch has no commits on top of its base"/);
  assert.match(source, /review_no_commits: "в ветке нет коммитов поверх базы"/);
  assert.doesNotMatch(source, /review_merged/);
});

test("a failed add keeps the operator's text and reopens the form on the same line", async () => {
  const { renderReview } = await import("../js/review.js");
  const root = document.createElement("div");
  const calls = [];
  const a = api(view(), calls);
  a.addReviewComment = async (...args) => {
    calls.push(["add", ...args]);
    throw new Error("stale revision");
  };
  renderReview(root, "/b/cards/T-057.md", () => {}, { api: a });
  await settle();
  fireEvent(root.querySelector('[data-new-line="2"] .review-line-add'), "click");
  const form1 = root.querySelector(".review-form");
  form1.querySelector("textarea").value = "kept text";
  fireEvent(form1, "submit");
  await settle();
  const form2 = root.querySelector(".review-form");
  assert.ok(form2, "the form reopens after a failed write");
  assert.equal(form2.querySelector("textarea").value, "kept text");
});

test("editing a draft comment sends its new body", async () => {
  const { renderReview } = await import("../js/review.js");
  const root = document.createElement("div");
  const calls = [];
  const v = view({
    comments: [{
      id: "c1", round: 0, body: "old text", resolved: false,
      anchor: { commit: HEAD, path: "a.go", side: "new", start: 2, end: 2, text: ["x"], before: [], after: [] },
      position: { path: "a.go", start: 2, end: 2, state: "in_place" },
    }],
  });
  renderReview(root, "/b/cards/T-057.md", () => {}, { api: api(v, calls) });
  await settle();
  const note = root.querySelector('[data-comment="c1"]');
  fireEvent(actionButton(note, t("review_edit")), "click");
  const form = root.querySelector(".review-form");
  assert.equal(form.querySelector("textarea").value, "old text");
  form.querySelector("textarea").value = "new text";
  fireEvent(form, "submit");
  await settle();
  const [, card, rev, id, body] = calls.find((c) => c[0] === "edit");
  assert.equal(card, "/b/cards/T-057.md");
  assert.equal(rev, 0);
  assert.equal(id, "c1");
  assert.equal(body, "new text");
});

test("shift-click on a second line's + anchors the range between them, ordered", async () => {
  const { renderReview } = await import("../js/review.js");
  const root = document.createElement("div");
  const calls = [];
  const v = view({
    files: [
      {
        oldPath: "a.go", newPath: "a.go", binary: false, truncated: false,
        hunks: [{
          oldStart: 1, oldCount: 1, newStart: 1, newCount: 3,
          lines: [
            { kind: " ", old: 1, new: 1, text: "package a" },
            { kind: "+", old: 0, new: 2, text: "one" },
            { kind: "+", old: 0, new: 3, text: "two" },
          ],
        }],
      },
    ],
  });
  renderReview(root, "/b/cards/T-057.md", () => {}, { api: api(v, calls) });
  await settle();
  // Clicked out of order — line 3 first, then a shift-click on line 2 — to
  // prove the range is ordered by file position, not by click order.
  fireEvent(root.querySelector('[data-new-line="3"] .review-line-add'), "click");
  fireEvent(root.querySelector('[data-new-line="2"] .review-line-add'), "click", { shiftKey: true });
  // The shift-click's form opens right after the row it was clicked on (line
  // 2), which sits earlier in the diff than line 3's own form.
  const form = root.querySelector('[data-new-line="2"]').nextSibling;
  form.querySelector("textarea").value = "range note";
  fireEvent(form, "submit");
  await settle();
  const [, , , anchor] = calls.find((c) => c[0] === "add");
  assert.ok(anchor.start < anchor.end);
  assert.deepEqual(anchor, { commit: HEAD, path: "a.go", side: "new", start: 2, end: 3 });
});

test("replying to a sent, unresolved comment adds a comment with its anchor and replyTo", async () => {
  const { renderReview } = await import("../js/review.js");
  const root = document.createElement("div");
  const calls = [];
  const v = view({
    comments: [{
      id: "c1", round: 1, body: "please fix", resolved: false,
      anchor: { commit: "b".repeat(40), path: "a.go", side: "new", start: 2, end: 2, text: ["x"], before: [], after: [] },
      position: { path: "a.go", start: 2, end: 2, state: "in_place" },
    }],
  });
  renderReview(root, "/b/cards/T-057.md", () => {}, { api: api(v, calls) });
  await settle();
  const note = root.querySelector('[data-comment="c1"]');
  fireEvent(actionButton(note, t("review_reply")), "click");
  const form = root.querySelector(".review-form");
  form.querySelector("textarea").value = "thanks";
  fireEvent(form, "submit");
  await settle();
  const [, , , anchor, body, replyTo] = calls.find((c) => c[0] === "add");
  assert.deepEqual(anchor, { commit: "b".repeat(40), path: "a.go", side: "new", start: 2, end: 2 });
  assert.equal(body, "thanks");
  assert.equal(replyTo, "c1");
});

test("a resolved comment offers no reply action", async () => {
  const { renderReview } = await import("../js/review.js");
  const root = document.createElement("div");
  const v = view({
    comments: [{
      id: "c1", round: 1, body: "fixed already", resolved: true,
      anchor: { commit: "b".repeat(40), path: "a.go", side: "new", start: 2, end: 2, text: ["x"], before: [], after: [] },
      position: { path: "a.go", start: 2, end: 2, state: "in_place" },
    }],
  });
  renderReview(root, "/b/cards/T-057.md", () => {}, { api: api(v) });
  await settle();
  const note = root.querySelector('[data-comment="c1"]');
  assert.equal(actionButton(note, t("review_reply")), undefined);
});

// A sent comment of round n, placed in place on line 2.
function sent(id, round) {
  return {
    id, round, body: `note ${id}`, resolved: false,
    anchor: { commit: HEAD, path: "a.go", side: "new", start: 2, end: 2, text: ["x"], before: [], after: [] },
    position: { path: "a.go", start: 2, end: 2, state: "in_place" },
  };
}

const ROUND_HEAD = "4e1d0aa9" + "0".repeat(32);

test("every round is listed with its time, head and failed delivery, and an unanswered one says so", async () => {
  const { renderReview } = await import("../js/review.js");
  const root = document.createElement("div");
  const v = view({
    comments: [sent("c1", 1), sent("c2", 2)],
    replies: [{ id: "c1", status: "fixed", commit: "", text: "done", raw: "", parsed: true, partial: false }],
    rounds: [
      { n: 1, sent: "2026-09-28T10:00:00+03:00", head: ROUND_HEAD, positions: {}, delivery: "" },
      { n: 2, sent: "2026-09-28T11:00:00+03:00", head: ROUND_HEAD, positions: {}, delivery: "daemon gone" },
    ],
  });
  renderReview(root, "/b/cards/T-057.md", () => {}, { api: api(v) });
  await settle();
  const one = root.querySelector('[data-round="1"]');
  const two = root.querySelector('[data-round="2"]');
  assert.ok(one && two, "both rounds are listed");
  assert.ok(one.textContent.includes("2026-09-28T10:00:00+03:00"));
  assert.ok(one.textContent.includes("4e1d0aa9"));
  assert.ok(!one.textContent.includes(ROUND_HEAD), "the head is shortened");
  assert.ok(two.textContent.includes("daemon gone"));
  assert.ok(two.textContent.includes(t("review_round_unanswered").replace("{n}", "2")));
  assert.ok(!one.textContent.includes(t("review_round_unanswered").replace("{n}", "1")));
});

test("a round not delivered or not answered offers a resend, an answered delivered one does not", async () => {
  const { renderReview } = await import("../js/review.js");
  const root = document.createElement("div");
  const v = view({
    comments: [sent("c1", 1), sent("c2", 2), sent("c3", 3)],
    replies: [
      { id: "c1", status: "fixed", commit: "", text: "done", raw: "", parsed: true, partial: false },
      { id: "c2", status: "fixed", commit: "", text: "done", raw: "", parsed: true, partial: false },
    ],
    rounds: [
      { n: 1, sent: "s", head: ROUND_HEAD, positions: {}, delivery: "" },
      { n: 2, sent: "s", head: ROUND_HEAD, positions: {}, delivery: "daemon gone" },
      { n: 3, sent: "s", head: ROUND_HEAD, positions: {}, delivery: "" },
    ],
  });
  renderReview(root, "/b/cards/T-057.md", () => {}, { api: api(v) });
  await settle();
  assert.equal(root.querySelector('[data-round="1"] .review-round-notify'), null);
  assert.ok(root.querySelector('[data-round="2"] .review-round-notify'), "an undelivered round");
  assert.ok(root.querySelector('[data-round="3"] .review-round-notify'), "a round nobody answered");
  assert.equal(root.querySelector('[data-round="2"] .review-round-notify').textContent, t("review_notify_again"));
});

test("a resend is one request per press, the button held while it is in flight", async () => {
  const { renderReview } = await import("../js/review.js");
  const root = document.createElement("div");
  const calls = [];
  const a = api(view({
    comments: [sent("c1", 1)],
    rounds: [{ n: 1, sent: "s", head: ROUND_HEAD, positions: {}, delivery: "daemon gone" }],
  }), calls);
  let answer;
  a.notifyReviewRound = (...args) => {
    calls.push(["notify", ...args]);
    return new Promise((resolve) => {
      answer = resolve;
    });
  };
  renderReview(root, "/b/cards/T-057.md", () => {}, { api: a });
  await settle();
  const button = root.querySelector('[data-round="1"] .review-round-notify');
  fireEvent(button, "click");
  assert.equal(button.disabled, true, "held while in flight");
  fireEvent(button, "click");
  answer({ round: 1, delivery: "still gone", comments: {} });
  await settle();
  const notifies = calls.filter((c) => c[0] === "notify");
  assert.equal(notifies.length, 1);
  assert.deepEqual(notifies[0], ["notify", "/b/cards/T-057.md", 0, 1]);
  assert.ok(root.textContent.includes("still gone"), "the result is shown");
});

test("a reply is shown parsed, raw, or marked as possibly still being written", async () => {
  const { renderReview } = await import("../js/review.js");
  const root = document.createElement("div");
  const v = view({
    comments: [sent("c1", 1), sent("c2", 1), sent("c3", 1)],
    replies: [
      { id: "c1", status: "fixed", commit: "4e1d0aa", text: "wrapped it", raw: "## c1\n...", parsed: true, partial: false },
      { id: "c2", status: "done-ish", commit: "", text: "", raw: "## c2\nstatus: done-ish\n\n<b>raw</b>", parsed: false, partial: false },
      { id: "c3", status: "fixed", commit: "", text: "half", raw: "## c3", parsed: true, partial: true },
    ],
  });
  renderReview(root, "/b/cards/T-057.md", () => {}, { api: api(v) });
  await settle();
  const parsed = root.querySelector('[data-comment="c1"] .review-reply');
  assert.ok(parsed.textContent.includes(t("review_reply_fixed")));
  assert.ok(parsed.textContent.includes("4e1d0aa"));
  assert.ok(parsed.textContent.includes("wrapped it"));
  const raw = root.querySelector('[data-comment="c2"] .review-reply');
  assert.ok(raw.textContent.includes("## c2\nstatus: done-ish\n\n<b>raw</b>"), "an unparsed section is shown as it is");
  assert.equal(root.querySelectorAll("b").length, 0);
  assert.ok(root.querySelector('[data-comment="c3"] .review-reply').textContent.includes(t("review_reply_partial")));
  assert.ok(!parsed.textContent.includes(t("review_reply_partial")));
});

test("text typed on a line the agent committed over meanwhile is kept on the page", async () => {
  const { renderReview } = await import("../js/review.js");
  const root = document.createElement("div");
  let current = view();
  const a = api(current);
  a.fetchReview = async () => current;
  a.addReviewComment = async () => {
    current = view({ head: "f".repeat(40), files: [] });
    throw new Error("stale revision");
  };
  renderReview(root, "/b/cards/T-057.md", () => {}, { api: a });
  await settle();
  fireEvent(root.querySelector('[data-new-line="2"] .review-line-add'), "click");
  const form = root.querySelector(".review-form");
  form.querySelector("textarea").value = "kept across a new head";
  fireEvent(form, "submit");
  await settle();
  const unsent = root.querySelector(".review-unsent");
  assert.ok(unsent, "the text has a place of its own");
  assert.ok(unsent.textContent.includes(t("review_unsent_text")));
  assert.equal(unsent.querySelector("textarea").value, "kept across a new head");
});

test("the base line names the working tree it was read from", async () => {
  const { renderReview } = await import("../js/review.js");
  const root = document.createElement("div");
  renderReview(root, "/b/cards/T-057.md", () => {}, { api: api(view({ workdir: "/work/tree-7" })) });
  await settle();
  assert.ok(root.querySelector(".review-base").textContent.includes("/work/tree-7"));
});

test("a comment not on a line shown here is listed with where it was", async () => {
  const { renderReview } = await import("../js/review.js");
  const root = document.createElement("div");
  const gone = (id, start, end) => ({
    id, round: 1, body: "x", resolved: false,
    anchor: { commit: "c", path: "gone.go", side: "new", start, end, text: ["old"], before: [], after: [] },
    position: { path: "gone.go", start, end, state: "unavailable" },
  });
  renderReview(root, "/b/cards/T-057.md", () => {}, { api: api(view({ comments: [gone("c1", 3, 3), gone("c2", 5, 7)] })) });
  await settle();
  const list = root.querySelector(".review-detached");
  assert.ok(list.textContent.includes("gone.go:3"));
  assert.ok(list.textContent.includes("gone.go:5–7"));
});

test("a comment form is cancelled by its button and by Escape, and nothing is written", async () => {
  const { renderReview } = await import("../js/review.js");
  const root = document.createElement("div");
  const calls = [];
  renderReview(root, "/b/cards/T-057.md", () => {}, { api: api(view(), calls) });
  await settle();
  const plus = root.querySelector('[data-new-line="2"] .review-line-add');
  fireEvent(plus, "click");
  const cancel = root.querySelector(".review-form .review-form-cancel");
  assert.ok(cancel, "the form offers a cancel");
  assert.equal(cancel.getAttribute("type"), "button", "cancel must not submit");
  root.querySelector(".review-form textarea").value = "never mind";
  fireEvent(cancel, "click");
  assert.equal(root.querySelector(".review-form"), null, "cancel closes the form");

  fireEvent(plus, "click");
  const escape = fireEvent(root.querySelector(".review-form textarea"), "keydown", { key: "Escape" });
  assert.equal(root.querySelector(".review-form"), null, "Escape in the textarea closes the form");
  assert.equal(escape.defaultPrevented, true);
  await settle();
  assert.equal(calls.filter((c) => c[0] === "add").length, 0, "a cancelled form writes nothing");
});

test("a second + on a line whose form is open focuses that form instead of stacking another", async () => {
  const { renderReview } = await import("../js/review.js");
  const root = document.createElement("div");
  renderReview(root, "/b/cards/T-057.md", () => {}, { api: api(view()) });
  await settle();
  const plus = root.querySelector('[data-new-line="2"] .review-line-add');
  fireEvent(plus, "click");
  const text = root.querySelector(".review-form textarea");
  text.focused = false;
  fireEvent(plus, "click");
  assert.equal(root.querySelectorAll(".review-form").length, 1);
  assert.equal(text.focused, true, "the open form gets the focus");
});

test("the form's buttons are cancel and add, side by side in one row under the text", async () => {
  const { renderReview } = await import("../js/review.js");
  const root = document.createElement("div");
  renderReview(root, "/b/cards/T-057.md", () => {}, { api: api(view()) });
  await settle();
  fireEvent(root.querySelector('[data-new-line="2"] .review-line-add'), "click");
  const form = root.querySelector(".review-form");
  const bar = form.querySelector(".review-form-actions");
  assert.ok(bar, "the buttons sit in a row of their own");
  assert.equal(form.children[1], bar, "the row is under the textarea");
  assert.deepEqual([...bar.children].map((b) => b.className), ["btn review-form-cancel", "btn btn-primary review-form-ok"]);
});

// Drag: press on a line's "+", move over the lines below, release — the range
// form opens under the last line, anchored to the whole range.
function threeAdded() {
  return view({
    files: [
      {
        oldPath: "a.go", newPath: "a.go", binary: false, truncated: false,
        hunks: [{
          oldStart: 1, oldCount: 2, newStart: 1, newCount: 4,
          lines: [
            { kind: " ", old: 1, new: 1, text: "package a" },
            { kind: "-", old: 2, new: 0, text: "gone" },
            { kind: "+", old: 0, new: 2, text: "one" },
            { kind: "+", old: 0, new: 3, text: "two" },
            { kind: "+", old: 0, new: 4, text: "three" },
          ],
        }],
      },
      {
        oldPath: "", newPath: "b.go", binary: false, truncated: false,
        hunks: [{ oldStart: 0, oldCount: 0, newStart: 1, newCount: 1, lines: [{ kind: "+", old: 0, new: 1, text: "other" }] }],
      },
    ],
  });
}

test("dragging from a line's + over the lines below opens the range form under the last one", async () => {
  const { renderReview } = await import("../js/review.js");
  const root = document.createElement("div");
  const calls = [];
  renderReview(root, "/b/cards/T-057.md", () => {}, { api: api(threeAdded(), calls) });
  await settle();
  const row = (n) => root.querySelector(`[data-new-line="${n}"]`);
  const press = fireEvent(row(2).querySelector(".review-line-add"), "mousedown", { button: 0 });
  assert.equal(press.defaultPrevented, true, "the press does not start a text selection");
  fireEvent(row(3), "mouseover");
  fireEvent(row(4).querySelector(".review-line-text"), "mouseover");
  assert.deepEqual(
    [...root.querySelectorAll(".review-line-picked")].map((r) => r.dataset.newLine),
    ["2", "3", "4"],
    "the covered lines are marked while the drag goes on",
  );
  fireDocumentEvent(document, "mouseup");
  const form = row(4).nextSibling;
  assert.ok(form.classList.contains("review-form"), "the form opens under the last line of the range");
  form.querySelector("textarea").value = "range";
  fireEvent(form, "submit");
  await settle();
  const [, , , anchor] = calls.find((c) => c[0] === "add");
  assert.deepEqual(anchor, { commit: HEAD, path: "a.go", side: "new", start: 2, end: 4 });
});

test("a drag never spans two sides or two files", async () => {
  const { renderReview } = await import("../js/review.js");
  const root = document.createElement("div");
  const calls = [];
  renderReview(root, "/b/cards/T-057.md", () => {}, { api: api(threeAdded(), calls) });
  await settle();
  const files = root.querySelectorAll("section.review-file");
  fireEvent(files[0].querySelector('[data-new-line="3"] .review-line-add'), "mousedown", { button: 0 });
  fireEvent(files[0].querySelector('[data-old-line="2"]'), "mouseover");
  fireEvent(files[1].querySelector('[data-new-line="1"]'), "mouseover");
  fireEvent(files[0].querySelector('[data-new-line="4"]'), "mouseover");
  fireEvent(files[1].querySelector('[data-new-line="1"]'), "mouseover");
  fireDocumentEvent(document, "mouseup");
  assert.equal(files[1].querySelector(".review-form"), null, "nothing opens in the other file");
  const form = files[0].querySelector('[data-new-line="4"]').nextSibling;
  form.querySelector("textarea").value = "x";
  fireEvent(form, "submit");
  await settle();
  const [, , , anchor] = calls.find((c) => c[0] === "add");
  assert.deepEqual(anchor, { commit: HEAD, path: "a.go", side: "new", start: 3, end: 4 });
});

test("a press and release on one line leaves the form to the click", async () => {
  const { renderReview } = await import("../js/review.js");
  const root = document.createElement("div");
  renderReview(root, "/b/cards/T-057.md", () => {}, { api: api(threeAdded()) });
  await settle();
  fireEvent(root.querySelector('[data-new-line="3"] .review-line-add'), "mousedown", { button: 0 });
  fireDocumentEvent(document, "mouseup");
  assert.equal(root.querySelector(".review-form"), null);
});

// Two hunks with unchanged code between them: new 4..41 is hidden, and the
// old side runs two lines behind (the first hunk added two lines).
function twoHunks() {
  return view({
    files: [
      {
        oldPath: "a.go", newPath: "a.go", binary: false, truncated: false,
        hunks: [
          {
            oldStart: 1, oldCount: 1, newStart: 1, newCount: 3,
            lines: [
              { kind: " ", old: 1, new: 1, text: "package a" },
              { kind: "+", old: 0, new: 2, text: "one" },
              { kind: "+", old: 0, new: 3, text: "two" },
            ],
          },
          // Three lines of context after the last change: the diff's full
          // --unified=3, so the file goes on below.
          {
            oldStart: 40, oldCount: 4, newStart: 42, newCount: 5,
            lines: [
              { kind: " ", old: 40, new: 42, text: "end" },
              { kind: "+", old: 0, new: 43, text: "three" },
              { kind: " ", old: 41, new: 44, text: "a" },
              { kind: " ", old: 42, new: 45, text: "b" },
              { kind: " ", old: 43, new: 46, text: "c" },
            ],
          },
        ],
      },
    ],
  });
}

// fetchReviewLines as the route answers it: the lines asked for, clamped to
// a file of total lines.
function withLines(a, calls, total = 50) {
  a.fetchReviewLines = async (...args) => {
    calls.push(["lines", ...args]);
    const [, , , from, to] = args;
    const lines = [];
    for (let n = from; n <= Math.min(to, total); n += 1) lines.push(`line ${n}`);
    return { from, total, lines };
  };
  return a;
}

test("the unchanged lines between two hunks are expanded a page at a time, then whole", async () => {
  const { renderReview } = await import("../js/review.js");
  const root = document.createElement("div");
  const calls = [];
  renderReview(root, "/b/cards/T-057.md", () => {}, { api: withLines(api(twoHunks(), calls), calls) });
  await settle();
  const gap = () => root.querySelector(".review-expand");
  assert.ok(gap(), "the gap between the hunks offers to expand");
  assert.ok(gap().querySelector(".review-expand-all").textContent.includes("38"), "the gap says how many lines it hides");
  assert.ok(gap().querySelector(".review-expand-down"), "a gap between hunks opens downward");

  fireEvent(gap().querySelector(".review-expand-up"), "click");
  await settle();
  assert.deepEqual(calls.find((c) => c[0] === "lines"), ["lines", "/b/cards/T-057.md", HEAD, "a.go", 22, 41]);
  const last = root.querySelector('[data-new-line="41"]');
  assert.ok(last, "the lines above the second hunk are drawn");
  assert.equal(last.dataset.oldLine, "39", "with the old side's number filled in");
  assert.ok(last.textContent.includes("line 41"));
  assert.ok(last.classList.contains("review-line-ctx"));

  // 4..21 is left: short enough to be shown in one press.
  assert.equal(gap().querySelector(".review-expand-up"), null);
  fireEvent(gap().querySelector(".review-expand-all"), "click");
  await settle();
  assert.deepEqual(calls.filter((c) => c[0] === "lines")[1], ["lines", "/b/cards/T-057.md", HEAD, "a.go", 4, 21]);
  assert.equal(root.querySelector('[data-new-line="4"]').dataset.oldLine, "2");
  const left = root.querySelectorAll(".review-expand");
  assert.equal(left.length, 1, "a gap fully shown is gone");
  // The reads said the file has 50 lines, so its end, 47..50, is known too.
  assert.ok(left[0].querySelector(".review-expand-all").textContent.includes("4"), "only the file's end is left to expand");
  const order = [...root.querySelectorAll(".review-line")].map((r) => Number(r.dataset.newLine || 0)).filter(Boolean);
  assert.deepEqual(order, [...order].sort((a, b) => a - b), "the lines are drawn in file order");
});

test("an expanded context line is commented on like any other, at head", async () => {
  const { renderReview } = await import("../js/review.js");
  const root = document.createElement("div");
  const calls = [];
  renderReview(root, "/b/cards/T-057.md", () => {}, { api: withLines(api(twoHunks(), calls), calls) });
  await settle();
  fireEvent(root.querySelector(".review-expand .review-expand-all"), "click");
  await settle();
  fireEvent(root.querySelector('[data-new-line="12"] .review-line-add'), "click");
  const form = root.querySelector(".review-form");
  form.querySelector("textarea").value = "here";
  fireEvent(form, "submit");
  await settle();
  const [, , , anchor] = calls.find((c) => c[0] === "add");
  assert.deepEqual(anchor, { commit: HEAD, path: "a.go", side: "new", start: 12, end: 12 });
  assert.ok(root.querySelector('[data-new-line="12"]'), "the expanded lines survive the repaint after a write");
});

test("above the first hunk expands upward only, and below the last one downward until the file ends", async () => {
  const { renderReview } = await import("../js/review.js");
  const root = document.createElement("div");
  const calls = [];
  const v = view({
    files: [{
      oldPath: "a.go", newPath: "a.go", binary: false, truncated: false,
      hunks: [{
        oldStart: 30, oldCount: 3, newStart: 30, newCount: 4,
        lines: [
          { kind: "+", old: 0, new: 30, text: "mid" },
          { kind: " ", old: 30, new: 31, text: "a" },
          { kind: " ", old: 31, new: 32, text: "b" },
          { kind: " ", old: 32, new: 33, text: "c" },
        ],
      }],
    }],
  });
  renderReview(root, "/b/cards/T-057.md", () => {}, { api: withLines(api(v, calls), calls, 35) });
  await settle();
  const [above, below] = root.querySelectorAll(".review-expand");
  assert.equal(above.querySelector(".review-expand-down"), null, "nothing is above the top of the file");
  assert.ok(above.querySelector(".review-expand-up"));
  assert.equal(below.querySelector(".review-expand-up"), null, "the end of the file is not known yet");
  fireEvent(below.querySelector(".review-expand-down"), "click");
  await settle();
  assert.deepEqual(calls.find((c) => c[0] === "lines"), ["lines", "/b/cards/T-057.md", HEAD, "a.go", 34, 53]);
  assert.ok(root.querySelector('[data-new-line="35"]'));
  assert.equal(root.querySelectorAll(".review-expand").length, 1, "the file's end reached, the lower control is gone");
});

test("a new file and a deleted file offer no expansion", async () => {
  const { renderReview } = await import("../js/review.js");
  const root = document.createElement("div");
  const v = view({
    files: [
      { oldPath: "", newPath: "n.go", binary: false, truncated: false, hunks: [{ oldStart: 0, oldCount: 0, newStart: 1, newCount: 1, lines: [{ kind: "+", old: 0, new: 1, text: "x" }] }] },
      { oldPath: "d.go", newPath: "", binary: false, truncated: false, hunks: [{ oldStart: 1, oldCount: 1, newStart: 0, newCount: 0, lines: [{ kind: "-", old: 1, new: 0, text: "x" }] }] },
    ],
  });
  renderReview(root, "/b/cards/T-057.md", () => {}, { api: api(v) });
  await settle();
  assert.equal(root.querySelectorAll(".review-expand").length, 0);
});

// Every review button that is shaped like a button is a .btn: the comment's
// actions and a round's resend are small ones, peers in their row. The "+"
// in the gutter and the expand bands are not capsules by design.
test("the review's buttons are .btn capsules, the gutter + and the expand bands are not", async () => {
  const { renderReview } = await import("../js/review.js");
  const root = document.createElement("div");
  const v = view({
    comments: [sent("c1", 1), { ...sent("c2", 0) }],
    rounds: [{ n: 1, sent: "s", head: ROUND_HEAD, positions: {}, delivery: "daemon gone" }],
  });
  renderReview(root, "/b/cards/T-057.md", () => {}, { api: withLines(api(twoHunks()), []) });
  const other = document.createElement("div");
  renderReview(other, "/b/cards/T-057.md", () => {}, { api: api(v) });
  await settle();
  const actions = other.querySelectorAll(".review-comment-action");
  assert.ok(actions.length >= 3);
  for (const b of actions) assert.equal(b.className, "btn btn-sm review-comment-action", b.textContent);
  assert.equal(other.querySelector(".review-round-notify").className, "btn btn-sm review-round-notify");
  assert.ok(!root.querySelector(".review-line-add").classList.contains("btn"));
  assert.ok(!root.querySelector(".review-expand button").classList.contains("btn"));
});

// ---- fix round after the independent review ----

// The shown diff has --unified=3: a last hunk with fewer than three lines of
// context after its last change ends where the file does.
test("a last hunk that reaches the end of the file offers nothing below it", async () => {
  const { renderReview } = await import("../js/review.js");
  const root = document.createElement("div");
  const v = view({
    files: [{
      oldPath: "a.go", newPath: "a.go", binary: false, truncated: false,
      hunks: [{
        oldStart: 30, oldCount: 2, newStart: 30, newCount: 3,
        lines: [
          { kind: " ", old: 30, new: 30, text: "a" },
          { kind: "+", old: 0, new: 31, text: "added" },
          { kind: " ", old: 31, new: 32, text: "}" },
        ],
      }],
    }],
  });
  renderReview(root, "/b/cards/T-057.md", () => {}, { api: api(v) });
  await settle();
  const strips = root.querySelectorAll(".review-expand");
  assert.equal(strips.length, 1, "only the lines above the hunk are hidden");
  assert.ok(strips[0].querySelector(".review-expand-up"));
});

test("a comment being written survives an expand, on its line, with its text and the focus", async () => {
  const { renderReview } = await import("../js/review.js");
  const root = document.createElement("div");
  document.body.append(root);
  const calls = [];
  renderReview(root, "/b/cards/T-057.md", () => {}, { api: withLines(api(twoHunks(), calls), calls) });
  await settle();
  fireEvent(root.querySelector('[data-new-line="2"] .review-line-add'), "click");
  root.querySelector(".review-form textarea").value = "half written";
  root.querySelector(".review-form textarea").focus();
  fireEvent(root.querySelector(".review-expand .review-expand-up"), "click");
  await settle();
  assert.ok(root.querySelector('[data-new-line="41"]'), "the expand happened");
  const form = root.querySelector('[data-new-line="2"]').nextSibling;
  assert.ok(form?.classList.contains("review-form"), "the form is open on the same line");
  assert.equal(form.querySelector("textarea").value, "half written");
  assert.equal(document.activeElement, form.querySelector("textarea"), "and keeps the focus");
  form.querySelector("textarea").value = "done";
  fireEvent(form, "submit");
  await settle();
  const [, , , anchor, body] = calls.find((c) => c[0] === "add");
  assert.deepEqual(anchor, { commit: HEAD, path: "a.go", side: "new", start: 2, end: 2 });
  assert.equal(body, "done");
});

test("an open reply survives another comment's successful write, and the written form does not come back", async () => {
  const { renderReview } = await import("../js/review.js");
  const root = document.createElement("div");
  const calls = [];
  const v = view({ comments: [sent("c1", 1)] });
  renderReview(root, "/b/cards/T-057.md", () => {}, { api: api(v, calls) });
  await settle();
  fireEvent(actionButton(root.querySelector('[data-comment="c1"]'), t("review_reply")), "click");
  root.querySelector('[data-comment="c1"]').nextSibling.querySelector("textarea").value = "a reply in progress";
  fireEvent(root.querySelector('[data-new-line="1"] .review-line-add'), "click");
  const other = root.querySelector('[data-new-line="1"]').nextSibling;
  other.querySelector("textarea").value = "new note";
  fireEvent(other, "submit");
  await settle();
  assert.equal(calls.filter((c) => c[0] === "add").length, 1);
  const reply = root.querySelector('[data-comment="c1"]').nextSibling;
  assert.ok(reply?.classList.contains("review-form"), "the reply form is still open under its comment");
  assert.equal(reply.querySelector("textarea").value, "a reply in progress");
  assert.equal(root.querySelectorAll(".review-form").length, 1, "the form that was sent is not reopened");
});

test("a dragged range stays marked while its form is open, and the marks go with cancel or submit", async () => {
  const { renderReview } = await import("../js/review.js");
  const root = document.createElement("div");
  renderReview(root, "/b/cards/T-057.md", () => {}, { api: api(threeAdded()) });
  await settle();
  const row = (n) => root.querySelector(`[data-new-line="${n}"]`);
  const picked = () => [...root.querySelectorAll(".review-line-picked")].map((r) => r.dataset.newLine);
  const drag = () => {
    fireEvent(row(2).querySelector(".review-line-add"), "mousedown", { button: 0 });
    fireEvent(row(4), "mouseover", { buttons: 1 });
    fireDocumentEvent(document, "mouseup");
  };
  drag();
  assert.deepEqual(picked(), ["2", "3", "4"], "the range is marked under its open form");
  fireEvent(root.querySelector(".review-form-cancel"), "click");
  assert.deepEqual(picked(), [], "cancel clears the marks");
  drag();
  fireEvent(root.querySelector(".review-form textarea"), "keydown", { key: "Escape" });
  assert.deepEqual(picked(), [], "Escape clears the marks");
  drag();
  root.querySelector(".review-form textarea").value = "x";
  fireEvent(root.querySelector(".review-form"), "submit");
  await settle();
  assert.deepEqual(picked(), [], "a submit clears the marks");
});

test("a drag released outside the window is forgotten, not turned into a form by the next release", async () => {
  const { renderReview } = await import("../js/review.js");
  const root = document.createElement("div");
  renderReview(root, "/b/cards/T-057.md", () => {}, { api: api(threeAdded()) });
  await settle();
  const row = (n) => root.querySelector(`[data-new-line="${n}"]`);
  fireEvent(row(2).querySelector(".review-line-add"), "mousedown", { button: 0 });
  fireEvent(row(3), "mouseover", { buttons: 1 });
  // Released outside: the page never heard the mouseup. Back over a row, no
  // button is held any more.
  fireEvent(row(4), "mouseover", { buttons: 0 });
  assert.equal(root.querySelectorAll(".review-line-picked").length, 0);
  fireDocumentEvent(document, "mouseup");
  assert.equal(root.querySelector(".review-form"), null);

  fireEvent(row(2).querySelector(".review-line-add"), "mousedown", { button: 0 });
  fireEvent(row(3), "mouseover", { buttons: 1 });
  fireDocumentEvent(document, "mouseleave");
  fireDocumentEvent(document, "mouseup");
  assert.equal(root.querySelector(".review-form"), null, "leaving the page ends the drag too");
});

// --- the way back, the card's session beside the diff, and the code's size ---

const CARD = "/b/cards/T-057.md";
const SHORT = "a41c09d2";

function reviewSnap(overrides = {}) {
  return {
    orchestratorSession: "0c7e1a2b",
    cards: [{ path: CARD, id: "T-057", session: SHORT }],
    sessions: [{ short: SHORT, name: "T-057 review", needs: "answer: which base?", lifecycle: "live" }],
    ...overrides,
  };
}

// The overlay's live parts faked the way web/tests/carddock.test.js fakes the
// card's: a snapshot handed over once, a terminal that records, a fixed width.
function withSession(extra = {}) {
  const made = [];
  let unsubscribed = 0;
  const options = {
    subscribe: (fn) => {
      fn(extra.snap ?? reviewSnap());
      return () => {
        unsubscribed += 1;
      };
    },
    terminal: (host, short) => {
      const term = {
        host,
        short,
        stopped: 0,
        typed: [],
        open() {},
        stop() {
          this.stopped += 1;
        },
        type(bytes) {
          this.typed.push(bytes);
        },
        stepFont() {},
      };
      made.push(term);
      return term;
    },
    observe: (_, fn) => {
      fn(1000);
      return () => {};
    },
    storage: { getItem: () => null, setItem() {}, removeItem() {} },
  };
  return {
    options,
    made,
    get unsubscribed() {
      return unsubscribed;
    },
  };
}

test("the way back returns to the card, from its button and from Escape", async () => {
  const { renderReview } = await import("../js/review.js");
  const root = document.createElement("div");
  const back = [];
  const closed = [];
  const dispose = renderReview(root, CARD, () => closed.push(true), { api: api(view()), onBack: (p) => back.push(p) });
  await settle();
  const button = root.querySelector(".review-back");
  assert.ok(button, "the header offers the way back");
  assert.ok(button.className.split(" ").includes("btn"));
  fireEvent(button, "click");
  assert.deepEqual(back, [CARD]);

  fireDocumentEvent(document, "keydown", { key: "Escape", target: root.querySelector(".review-body") });
  assert.deepEqual(back, [CARD, CARD], "Escape goes back to the card too");
  assert.deepEqual(closed, [], "neither closes the review onto the board");

  dispose();
  fireDocumentEvent(document, "keydown", { key: "Escape", target: document.body });
  assert.equal(back.length, 2, "a disposed review hears no more keys");
});

test("an Escape a comment form or the session took is not a way back", async () => {
  const { renderReview } = await import("../js/review.js");
  const root = document.createElement("div");
  const back = [];
  const s = withSession();
  renderReview(root, CARD, () => {}, { api: api(view()), onBack: (p) => back.push(p), ...s.options });
  await settle();
  fireDocumentEvent(document, "keydown", { key: "Escape", target: root.querySelector(".card-dock-term") });
  assert.deepEqual(back, [], "Escape in the session's terminal is the session's");
  fireDocumentEvent(document, "keydown", { key: "Escape", target: root.querySelector(".review-body"), defaultPrevented: true });
  assert.deepEqual(back, [], "an Escape already handled closes the form only");
});

test("the card's session is mounted beside the diff, open, with its terminal and keys", async () => {
  const { renderReview } = await import("../js/review.js");
  const root = document.createElement("div");
  const s = withSession();
  const dispose = renderReview(root, CARD, () => {}, { api: api(view()), ...s.options });
  await settle();
  const stage = root.querySelector(".review-stage");
  assert.ok(stage, "the diff and the session share a stage");
  assert.ok(stage.querySelector(".review-body"), "the diff is on the stage");
  const dock = stage.querySelector(".card-dock");
  assert.ok(dock, "the session is the card's own dock, not a second one");
  assert.equal(dock.hidden, false);
  assert.equal(dock.dataset.open, "true", "open from the start: review and session are read together");
  assert.equal(dock.dataset.short, SHORT);
  assert.ok(dock.textContent.includes("which base?"), "the handle says what the session waits on");
  assert.ok(dock.querySelector(".term-font"), "the terminal's own A−/px/A+");
  assert.deepEqual(
    s.made.map((m) => m.short),
    [SHORT],
    "one terminal, attached to the card's session",
  );
  fireEvent(dock.querySelector(".s-key"), "click");
  assert.equal(s.made[0].typed.length, 1, "the key row types into the session");

  dispose();
  assert.equal(s.made[0].stopped, 1, "closing the review lets go of the terminal");
  assert.equal(s.unsubscribed, 1, "and of the snapshots");
});

test("a card with no session has no session place in its review", async () => {
  const { renderReview } = await import("../js/review.js");
  const root = document.createElement("div");
  const s = withSession({ snap: reviewSnap({ cards: [{ path: CARD, id: "T-057", session: "" }] }) });
  renderReview(root, CARD, () => {}, { api: api(view()), ...s.options });
  await settle();
  assert.equal(root.querySelector(".card-dock").hidden, true);
  assert.equal(s.made.length, 0);
});

function fontStorage(initial = {}) {
  const map = new Map(Object.entries(initial));
  return {
    getItem: (k) => (map.has(k) ? map.get(k) : null),
    setItem: (k, v) => map.set(k, String(v)),
    removeItem: (k) => map.delete(k),
    map,
  };
}

test("the code's size is stepped by A−/px/A+ within the terminal's range and remembered apart from it", async () => {
  const { renderReview, REVIEW_FONT_KEY, REVIEW_DEFAULT_FONT_SIZE } = await import("../js/review.js");
  const { MAX_FONT_SIZE, FONT_KEYS } = await import("../js/terminalfont.js");
  assert.equal(REVIEW_FONT_KEY, "fleetdeck-review-font");
  assert.ok(!Object.values(FONT_KEYS).includes(REVIEW_FONT_KEY), "not a terminal's key");
  assert.equal(REVIEW_DEFAULT_FONT_SIZE, 16, "the size the code had before it could be changed");

  const had = Object.getOwnPropertyDescriptor(globalThis, "localStorage");
  const storage = fontStorage();
  globalThis.localStorage = storage;
  try {
    const root = document.createElement("div");
    const dispose = renderReview(root, CARD, () => {}, { api: api(view()) });
    await settle();
    const body = root.querySelector(".review-body");
    const controls = root.querySelector(".review-header .term-font");
    assert.ok(controls, "the size controls sit in the review's header");
    const [smaller, reset, bigger] = controls.children;
    assert.equal(body.style.getPropertyValue("--review-code-size"), "16px");
    assert.equal(reset.textContent, "16 px");
    assert.equal(reset.disabled, true, "at the default there is nothing to reset");

    fireEvent(bigger, "click");
    fireEvent(bigger, "click");
    assert.equal(body.style.getPropertyValue("--review-code-size"), "18px");
    assert.equal(reset.textContent, "18 px");
    assert.equal(storage.map.get(REVIEW_FONT_KEY), "18");
    fireEvent(smaller, "click");
    assert.equal(storage.map.get(REVIEW_FONT_KEY), "17");
    fireEvent(reset, "click");
    assert.equal(storage.map.has(REVIEW_FONT_KEY), false, "the default is forgotten, not written");
    dispose();

    storage.map.set(REVIEW_FONT_KEY, "99");
    const again = document.createElement("div");
    renderReview(again, CARD, () => {}, { api: api(view()) });
    await settle();
    assert.equal(
      again.querySelector(".review-body").style.getPropertyValue("--review-code-size"),
      `${MAX_FONT_SIZE}px`,
      "a stored size comes back, clamped",
    );
    assert.equal(again.querySelector(".review-header .term-font-bigger").disabled, true);
  } finally {
    if (had) Object.defineProperty(globalThis, "localStorage", had);
    else delete globalThis.localStorage;
  }
});

// A file off screen is not laid out (.review-file's content-visibility), so
// its box is as tall as contain-intrinsic-size says. Every write rebuilds the
// diff, and a rebuilt file starting from a guess instead of the height it was
// drawn at would move everything under it while the operator reads.
test("a repaint keeps every file at the height it was drawn at, and a new one starts from its rows", async () => {
  const { renderReview } = await import("../js/review.js");
  const root = document.createElement("div");
  renderReview(root, "/b/cards/T-057.md", () => {}, { api: api(view()) });
  await settle();
  const first = root.querySelector(".review-file");
  const guess = first.style.getPropertyValue("contain-intrinsic-size");
  assert.match(guess, /^auto \d+px$/, "an undrawn file is sized from its rows, not left at zero");
  assert.ok(Number.parseInt(guess.slice(5), 10) > 0);

  first.clientHeight = 1234;
  fireEvent(root.querySelector('[data-new-line="2"] .review-line-add'), "click");
  const form = root.querySelector(".review-form");
  form.querySelector("textarea").value = "name it";
  fireEvent(form, "submit");
  await settle();
  const rebuilt = root.querySelector(".review-file");
  assert.notEqual(rebuilt, first, "the write repainted the diff");
  assert.equal(rebuilt.style.getPropertyValue("contain-intrinsic-size"), "auto 1234px");
});
