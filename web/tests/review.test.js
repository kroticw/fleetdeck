// The review overlay, driven from stubbed API functions.
//
// What it is easy to get wrong: putting diff text into the page as markup,
// drawing a comment on a line it no longer belongs to, losing the operator's
// draft to a stale revision, and repeating a send the operator did not ask for.

import { test, beforeEach, afterEach } from "node:test";
import assert from "node:assert/strict";

import { installDOM, fireEvent, settle } from "./fake-dom.js";
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

// Spec §4: the base is the fork point, and once the branch is merged the fork
// point is HEAD itself — the diff is empty, and the page says so rather than
// showing blank space that reads as a bug.
test("a branch already merged into its base says so instead of an empty diff", async () => {
  const { renderReview } = await import("../js/review.js");
  const root = document.createElement("div");
  const v = view({ base: { commit: HEAD, ref: "origin/master" }, head: HEAD, files: [] });
  renderReview(root, "/b/cards/T-057.md", () => {}, { api: api(v) });
  await settle();
  assert.ok(root.textContent.includes(t("review_merged")));
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
