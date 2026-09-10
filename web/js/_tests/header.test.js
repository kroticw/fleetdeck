// web/js/_tests/header.test.js
//
// Mirrors internal/daemon/types.go's Session.Waiting()/.Stalled() test cases
// (and the fixtures in internal/state/events_test.go) so the JS restatement
// in header.js cannot silently drift from the Go source of truth.
//
// This file lives under a leading-underscore directory (_tests/) on purpose:
// web/embed.go's `//go:embed index.html app.css js` is a plain (non "all:")
// directory pattern, which Go's embed semantics already exclude any file or
// directory whose name starts with "." or "_" from -- so this whole
// subtree is invisible to that embed and never ships in the binary or gets
// served over HTTP, unlike a test file sitting directly in js/.
//
// Run with: node --test web/js/_tests/header.test.js
// This file is a development-only tool. Node is not a runtime dependency of
// the shipped frontend or of bin/fleetdeck -- nothing here is bundled or
// shipped.

import test from "node:test";
import assert from "node:assert/strict";
import { isWaiting, isStalled, stallReason, escapeHTML, stalledList } from "../header.js";

// The reasons a person actually sees, in order. Each reason is its own
// element so the stylesheet can clip each one independently, so "what is
// shown" is the list of those elements' contents rather than a run of text
// split on a separator.
function shownReasons(html) {
  return [...html.matchAll(/<span class="stall-reason" title="[^"]*">([^<]*)<\/span>/g)].map(
    (m) => m[1],
  );
}

test("a question in needs is waiting, not stalled", () => {
  const s = { needs: "answer: pick one (A · B)" };
  assert.equal(isWaiting(s), true);
  assert.equal(isStalled(s), false);
});

test("a usage limit in needs is stalled, not waiting", () => {
  const s = { needs: "usage limit reached" };
  assert.equal(isWaiting(s), false);
  assert.equal(isStalled(s), true);
});

test("blocked state with empty needs is stalled, and detail is carried verbatim", () => {
  const s = { needs: "", state: "blocked", detail: "waiting on my own subagents" };
  assert.equal(isWaiting(s), false);
  assert.equal(isStalled(s), true);
  assert.equal(stallReason(s), "waiting on my own subagents");
});

test("blocked tempo with empty needs and non-blocked state is stalled", () => {
  const s = { needs: "", tempo: "blocked", state: "" };
  assert.equal(isStalled(s), true);
});

test("no needs, no blocked state or tempo is neither waiting nor stalled", () => {
  const s = { needs: "", state: "", tempo: "" };
  assert.equal(isWaiting(s), false);
  assert.equal(isStalled(s), false);
});

test("a dying session is never in either counter, even with a question pending", () => {
  const s = { needs: "answer: x", dying: true };
  assert.equal(isWaiting(s), false);
  assert.equal(isStalled(s), false);
});

// The fleet is open (spec 3.1): needs/detail come from sessions we did not
// write and must be treated as untrusted content, not developer-controlled
// text, wherever they reach innerHTML.
test("escapeHTML neutralizes the characters that matter in an HTML template literal", () => {
  const input = `usage limit reached<a href="http://evil.example/reauth">Re-authenticate here</a> & 'quoted'`;
  const escaped = escapeHTML(input);
  assert.equal(escaped.includes("<"), false);
  assert.equal(escaped.includes(">"), false);
  assert.equal(escaped.includes('"'), false);
  assert.equal(
    escaped,
    "usage limit reached&lt;a href=&quot;http://evil.example/reauth&quot;&gt;Re-authenticate here&lt;/a&gt; &amp; &#39;quoted&#39;",
  );
});

test("stalledList escapes a stalled session's reason text before it is joined into markup", () => {
  const malicious = { needs: 'usage limit reached<img src=x onerror="alert(1)">' };
  const html = stalledList([malicious]);
  assert.equal(html.includes("<img"), false);
  assert.equal(html.includes("&lt;img"), true);
});

test("stalledList drops reasons that are empty strings instead of leaving a stray separator", () => {
  // Stalled via the flag-only branch (state/tempo blocked) with no detail:
  // stallReason(s) is "" for this session per its own contract.
  const noReason = { needs: "", state: "blocked", detail: "" };
  const withReason = { needs: "usage limit reached" };
  const html = stalledList([withReason, noReason]);
  assert.deepEqual(shownReasons(html), ["usage limit reached"]);
  assert.equal(html.includes("stall-more"), false);
});

test("stalledList's tail counts non-empty reasons left out, not sessions past a fixed slot", () => {
  // 5 stalled sessions; session 2 sits at a position that would have fallen
  // inside the old MAX_STALL_REASONS=3 slice-then-filter order, and has an
  // empty reason. Sessions 1, 3, 4, 5 all carry real reasons -- 4 real
  // reasons total, cap is 3, so exactly 1 real reason is truly hidden.
  // Filtering empty reasons out BEFORE slicing (not after) is what makes
  // this land on "+1 more" instead of the old code's "+2 more" (which came
  // from slicing 3 raw session slots first, losing one to the empty reason,
  // and never noticing a 4th real reason existed to replace it).
  const sessions = [
    { needs: "usage limit reached" },
    { needs: "", state: "blocked", detail: "" },
    { needs: "login required" },
    { needs: "rate limited" },
    { needs: "API error: timeout" },
  ];
  const html = stalledList(sessions);
  assert.equal(html.includes("+1 more"), true);
  assert.equal(html.includes("+2 more"), false);
  // None of the 3 shown reasons is empty/blank.
  const shown = shownReasons(html);
  assert.equal(shown.length, 3);
  for (const reason of shown) {
    assert.notEqual(reason.trim(), "");
  }
});

test("stalledList shows no tail when an in-slice empty reason is backfilled by a real one outside it", () => {
  // Mirrors the /review-branch reviewer's own 4-session repro: 3 real
  // reasons, 1 empty, with the empty one landing inside what used to be the
  // visible slice (slot 2). The reviewer assumed this should render
  // "+2 more" hidden reasons (2 sessions past slot 3, counted by position).
  // That assumption is exactly the premise this redesign removes: a session
  // that never had a reason to show was never truncated content, it is
  // simply absent. With only 3 real reasons total and a cap of 3, all of
  // them fit -- so the correct, honest tail is no tail at all.
  const sessions = [
    { needs: "usage limit reached" },
    { needs: "", state: "blocked", detail: "" },
    { needs: "login required" },
    { needs: "rate limited" },
  ];
  const html = stalledList(sessions);
  assert.equal(html.includes("more"), false);
  assert.deepEqual(shownReasons(html), [
    "usage limit reached",
    "login required",
    "rate limited",
  ]);
});

// --- Long reasons must not turn the header into a wall of text ---
//
// The daemon puts the text of an incoming message into `detail` verbatim, so
// any session someone has written to carries a paragraph, not a phrase. The
// spec (section 4) requires the row to carry the reason in words; it does not
// require it to carry all of them at once. What it does require is that the
// words stay reachable — the whole argument for "words beat flags" is that a
// person needs to read them.

test("each stalled reason is its own element, so one long reason cannot run into the next", () => {
  const html = stalledList([{ needs: "usage limit reached" }, { needs: "login required" }]);
  const rows = html.match(/class="stall-reason"/g) ?? [];
  assert.equal(rows.length, 2, "each reason needs its own box to be clipped independently");
});

test("a long reason keeps its full text in the title attribute, not only what fits on screen", () => {
  const long = "context deadline exceeded while " + "x".repeat(400);
  const html = stalledList([{ needs: "", state: "blocked", detail: long }]);
  const title = html.match(/title="([^"]*)"/)?.[1];
  assert.ok(title, "a clipped reason must carry its full text somewhere reachable");
  assert.equal(title, long, "the title must hold the whole reason, not a shortened copy");
});

test("a reason's title is escaped, so a quote in detail cannot end the attribute", () => {
  const nasty = 'he said "go" <img src=x onerror=alert(1)>';
  const html = stalledList([{ needs: "", state: "blocked", detail: nasty }]);
  assert.ok(!html.includes('="go"'), "an unescaped quote would close the title attribute early");
  assert.ok(!html.includes("<img"), "the reason must never reach the DOM as markup");
  assert.ok(html.includes("&quot;go&quot;"), "the quote belongs in the title, escaped");
});

test("a multi-line detail is carried as one line's worth of text, newlines and all", () => {
  const multi = "first line\nsecond line\nthird line";
  const html = stalledList([{ needs: "", state: "blocked", detail: multi }]);
  const title = html.match(/title="([^"]*)"/)?.[1];
  assert.equal(title, multi, "the title keeps the reason exactly as the daemon wrote it");
});
