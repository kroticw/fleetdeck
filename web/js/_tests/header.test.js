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
import {
  isWaiting,
  isStalled,
  stallReason,
  escapeHTML,
  stalledList,
  createStalledTracker,
  BLOCKED_SETTLE_MS,
  createUsageErrorTracker,
  USAGE_ERROR_STALE_MS,
  alarmHTML,
  usageProblemHTML,
  gauge,
  isUsageStale,
  RATE_LIMITS_AGE_WORTH_SHOWING_MS,
  fleetMenuHTML,
  nextMenuState,
  headerCounts,
} from "../header.js";
import { t } from "../i18n.js";

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

// --- the counter showed a whole tag where it meant to show a reason ---

test("a stalled reason wrapped in an envelope is shown without the tag", () => {
  const wrapped = '<agent-message id="m-3" from="06a1f607" at="2026-09-10T14:00:00+05:00">waiting on a decision</agent-message>';
  const html = stalledList([{ needs: "", state: "blocked", detail: wrapped }]);
  // Only what is on screen: the title deliberately still holds the envelope, so
  // asserting on the whole markup would be asserting the opposite of the rule.
  const [shown] = shownReasons(html);

  assert.ok(!shown.includes("&lt;agent-message"), "the counter was printing the envelope");
  assert.ok(shown.includes("waiting on a decision"), "and not the reason inside it");
  assert.ok(shown.includes("06a1f607"), "who is waiting on whom is part of the reason");
});

test("the counter's title keeps the reason exactly as it arrived", () => {
  const wrapped = '<agent-message id="m-3" from="x" at="t">waiting</agent-message>';
  const html = stalledList([{ needs: "", state: "blocked", detail: wrapped }]);
  const title = html.match(/title="([^"]*)"/)?.[1];
  assert.ok(title.includes("&lt;agent-message"), "nothing is put out of reach");
});

test("a reason that is not an envelope is untouched", () => {
  const html = stalledList([{ needs: "usage limit reached" }]);
  assert.ok(html.includes("usage limit reached"));
});

// --- createStalledTracker ---------------------------------------------
//
// A flag-only stall (empty needs, state/tempo === "blocked") is exactly
// what a session looks like for the length of one message delivery too, so
// the counter must not promote one to "stalled" until it has held for at
// least BLOCKED_SETTLE_MS. A needs-based stall carries the daemon's own
// words and is real the instant it appears -- unaffected by the threshold.
// Time is always supplied as an explicit nowMs, never read from a real
// clock, so these are deterministic.

test("a needs-based stall counts immediately, with no threshold to wait out", () => {
  const tracker = createStalledTracker();
  const s = { short: "a", needs: "usage limit reached" };
  const result = tracker.update([s], 1000);
  assert.deepEqual(result, [s]);
});

test("a flag-only stall does not count on first sight", () => {
  const tracker = createStalledTracker();
  const s = { short: "a", needs: "", state: "blocked", detail: "" };
  const result = tracker.update([s], 1000);
  assert.deepEqual(result, []);
});

test("a flag-only stall counts once it has held for BLOCKED_SETTLE_MS", () => {
  const tracker = createStalledTracker();
  const s = { short: "a", needs: "", state: "blocked", detail: "" };
  tracker.update([s], 0);
  const stillFresh = tracker.update([s], BLOCKED_SETTLE_MS - 1);
  const nowStale = tracker.update([s], BLOCKED_SETTLE_MS);
  assert.deepEqual(stillFresh, [], "one millisecond short of the threshold must not count yet");
  assert.deepEqual(nowStale, [s], "having held for exactly the threshold must count");
});

test("control case: a session that clears before the threshold never counts, one held past it does", () => {
  const tracker = createStalledTracker();
  const clearsQuickly = { short: "quick", needs: "", state: "blocked", detail: "" };
  const staysBlocked = { short: "slow", needs: "", state: "blocked", detail: "" };

  tracker.update([clearsQuickly, staysBlocked], 0);
  // "quick" resolves well before the threshold -- gone from the next snapshot.
  const midway = tracker.update([staysBlocked], BLOCKED_SETTLE_MS / 2);
  const atThreshold = tracker.update([staysBlocked], BLOCKED_SETTLE_MS);

  assert.deepEqual(midway, [], "the session that resolved quickly must never have counted");
  assert.deepEqual(
    atThreshold,
    [staysBlocked],
    "the session that stayed blocked the whole time must count once the threshold passes",
  );
});

test("a session that clears and re-blocks starts a fresh clock, not a stale one", () => {
  const tracker = createStalledTracker();
  const s = { short: "a", needs: "", state: "blocked", detail: "" };
  tracker.update([s], 0);
  tracker.update([], BLOCKED_SETTLE_MS); // resolved: absent from this snapshot
  // Re-enters blocked well past what the old timestamp would have needed.
  const rightAfterReentry = tracker.update([s], BLOCKED_SETTLE_MS + 10);
  assert.deepEqual(rightAfterReentry, [], "re-entering blocked must not reuse the old clock");
});

test("a session that gains needs text stops being flag-only and is judged by the needs rule instead", () => {
  const tracker = createStalledTracker();
  const s = { short: "a", needs: "", state: "blocked", detail: "" };
  tracker.update([s], 0);
  const withQuestion = { ...s, needs: "answer: pick one" };
  const result = tracker.update([withQuestion], BLOCKED_SETTLE_MS / 2);
  assert.deepEqual(result, [], "a question in needs is Waiting, not Stalled, at any age");
});

test("no exception for any particular session identity, orchestrator included", () => {
  const tracker = createStalledTracker();
  const orchestrator = { short: "06a1f607", needs: "", state: "blocked", detail: "" };
  const ordinary = { short: "abc123", needs: "", state: "blocked", detail: "" };
  tracker.update([orchestrator, ordinary], 0);
  const result = tracker.update([orchestrator, ordinary], BLOCKED_SETTLE_MS);
  assert.deepEqual(
    result.map((s) => s.short).sort(),
    ["06a1f607", "abc123"],
    "the threshold applies the same way regardless of which session it is",
  );
});

// --- the silence condition on a flag-only stall -------------------------
//
// A blocked flag on its own is not evidence that a session has stopped:
// state is set by a mechanism the session does not control and has been
// observed holding "blocked" while the session was demonstrably working.
// silentFor -- the age of the last write to the session's transcript -- is
// the measurement that answers "is it alive right now", and it travels in
// the same snapshot, on the same row, beside the flag that contradicts it.
//
// So a flag-only stall counts only once the session has also been silent for
// BLOCKED_SETTLE_MS. silentFor is a Go time.Duration on the wire, so it
// arrives in nanoseconds; NS below keeps the fixtures readable.
const NS = 1e6; // nanoseconds in a millisecond

test("the operator's case: a stuck blocked flag with a fresh transcript is not stalled", () => {
  // Observed live on 2026-09-12, fleet session 512ed1ad: tempo=active,
  // state=blocked, needs="" held through 55% of a sampling window while
  // silentFor never passed 35.5s -- the session was writing to its
  // transcript the whole time. Before the silence condition the badge lit
  // anyway: the tracker's own clock had run past the threshold during an
  // earlier, real stall, and the sticky flag never let it reset.
  const tracker = createStalledTracker();
  const s = {
    short: "512ed1ad",
    needs: "",
    tempo: "active",
    state: "blocked",
    detail: "",
    silentFor: 23 * 1000 * NS,
  };
  tracker.update([s], 0);
  const wellPastTheThreshold = tracker.update([s], BLOCKED_SETTLE_MS * 4);
  assert.deepEqual(
    wellPastTheThreshold,
    [],
    "a session that wrote to its transcript 23 seconds ago has not stalled, whatever state says",
  );
});

test("control case e4fa5037: an hour-long real stall with tempo=active is still counted", () => {
  // internal/daemon/testdata/list_sessions.json's record "e4fa5037" is an
  // attested hour-long stall with exactly this flag shape (tempo=active,
  // state=blocked, needs=""). Dropping state in favour of tempo alone was
  // tried and rejected once, in commit 96ba6d4, because it loses this case;
  // the silence condition must not lose it either. A session parked for an
  // hour has written nothing for an hour, so silentFor is what tells it
  // apart from the case above -- the flags are identical in both.
  const tracker = createStalledTracker();
  const s = {
    short: "e4fa5037",
    needs: "",
    tempo: "active",
    state: "blocked",
    detail: "awaiting user decision on a dependency version",
    silentFor: 60 * 60 * 1000 * NS,
  };
  assert.deepEqual(
    tracker.update([s], 0),
    [s],
    "an hour of silence behind a blocked flag is a stall, and counts on first sight",
  );
});

test("the silence threshold is BLOCKED_SETTLE_MS, to the millisecond", () => {
  const tracker = createStalledTracker();
  const at = { short: "at", needs: "", state: "blocked", detail: "", silentFor: BLOCKED_SETTLE_MS * NS };
  const under = { short: "under", needs: "", state: "blocked", detail: "", silentFor: (BLOCKED_SETTLE_MS - 1) * NS };
  const counted = tracker.update([at, under], 0).map((s) => s.short);
  assert.deepEqual(counted, ["at"], "exactly the threshold counts; one millisecond short does not");
});

test("a session that goes quiet, is counted, then writes again leaves the count at once", () => {
  // The stall clock must reset when the session comes back rather than
  // accumulate across the resumption. Nothing here resets anything:
  // silentFor is an age, not an accumulator, so one write to the transcript
  // is the whole reset, and there is no stale timestamp left to carry over.
  const tracker = createStalledTracker();
  const base = { short: "a", needs: "", tempo: "active", state: "blocked", detail: "" };
  const quiet = { ...base, silentFor: (BLOCKED_SETTLE_MS + 1000) * NS };
  const alive = { ...base, silentFor: 2 * 1000 * NS };
  assert.deepEqual(tracker.update([quiet], 0), [quiet], "silent past the threshold: counted");
  assert.deepEqual(
    tracker.update([alive], 1000),
    [],
    "one write to the transcript ends the stall, with no accumulated clock left over",
  );
  assert.deepEqual(
    tracker.update([{ ...base, silentFor: 30 * 1000 * NS }], 2000),
    [],
    "and the next quiet half-minute starts from zero, not from where the old count stood",
  );
});

test("an unmeasured silentFor falls back to the tracker's own clock, not to silence", () => {
  // silentFor === 0 means "no transcript to stat", never "silent for zero
  // time" -- cmd/fleetdeck/collect.go's transcriptState and sessions.js's
  // silentLabel both read it that way. Taking that absence for "not silent
  // enough" would hide a session that stalled before writing anything, so
  // for as long as there is nothing to measure the flag's own age decides,
  // exactly as it did before this rule existed.
  const tracker = createStalledTracker();
  const s = { short: "a", needs: "", state: "blocked", detail: "", silentFor: 0 };
  assert.deepEqual(tracker.update([s], 0), [], "unmeasured and just seen: not yet");
  assert.deepEqual(
    tracker.update([s], BLOCKED_SETTLE_MS),
    [s],
    "unmeasured and blocked for the whole threshold: counted, as before this rule existed",
  );
});

test("a needs-based stall ignores silentFor entirely, however fresh the transcript is", () => {
  // The daemon's own words are real the instant they appear. The silence
  // condition exists only to disambiguate a bare flag, which has no words.
  const tracker = createStalledTracker();
  const s = { short: "a", needs: "usage limit reached", state: "working", silentFor: 1000 * NS };
  assert.deepEqual(tracker.update([s], 0), [s], "a worded stall counts at any silentFor");
});

// --- createUsageErrorTracker -------------------------------------------

test("usageError inactive reads as none", () => {
  const tracker = createUsageErrorTracker();
  assert.equal(tracker.update(false, 1000), "none");
});

test("usageError active reads as fresh under the threshold", () => {
  const tracker = createUsageErrorTracker();
  tracker.update(true, 0);
  assert.equal(tracker.update(true, USAGE_ERROR_STALE_MS - 1), "fresh");
});

test("control case: usageError past the threshold reads as stale, distinct from fresh", () => {
  const tracker = createUsageErrorTracker();
  tracker.update(true, 0);
  const fresh = tracker.update(true, USAGE_ERROR_STALE_MS - 1);
  const stale = tracker.update(true, USAGE_ERROR_STALE_MS);
  assert.equal(fresh, "fresh");
  assert.equal(stale, "stale");
  assert.notEqual(fresh, stale, "the two ages must render differently or the threshold is dead code");
});

test("usageError clearing and reappearing starts a fresh clock", () => {
  const tracker = createUsageErrorTracker();
  tracker.update(true, 0);
  tracker.update(false, USAGE_ERROR_STALE_MS); // recovered
  const rightAfter = tracker.update(true, USAGE_ERROR_STALE_MS + 10);
  assert.equal(rightAfter, "fresh", "a fresh failure must not inherit the old clock");
});

// --- alarmHTML / usageProblemHTML ---------------------------------------

test("offline and daemon_down still render as the red .problem span", () => {
  const html = alarmHTML(false, {});
  assert.equal(html.includes('class="problem"'), true);
  assert.equal(html.includes(t("offline")), true);
});

test("usage_down never joins the red .problem span, at any severity", () => {
  assert.equal(usageProblemHTML("none"), "");
  assert.equal(usageProblemHTML("fresh").includes("problem-quiet"), true);
  assert.equal(usageProblemHTML("stale", "auth").includes("problem-notice"), true);
  assert.equal(usageProblemHTML("fresh").includes('class="problem"'), false);
  assert.equal(usageProblemHTML("stale", "auth").includes('class="problem"'), false);
});

// usageProblemHTML's wording must depend on cmd/fleetdeck/collect.go's
// classifyUsageError, not only on how long the failure has lasted: the bug
// this card exists for was exactly "sign-in needed" shown for a cause
// sign-in cannot fix (a rate limit). Only the "stale" (worded) severity
// varies by kind -- "fresh" stays deliberately generic, see the function's
// own comment.
test("stale wording depends on the error kind, never defaults to sign-in", () => {
  assert.equal(usageProblemHTML("stale", "auth").includes(t("usage_down_auth")), true);
  assert.equal(usageProblemHTML("stale", "rate_limit").includes(t("usage_down_rate_limited")), true);
  assert.equal(usageProblemHTML("stale", "rate_limit").includes(t("usage_down_auth")), false);
  // "other", and an old snapshot with no kind at all, must not claim sign-in
  // fixes it -- that claim is only ever true for kind "auth". Pinned as a
  // positive fact (equals the generic notice), not only as an absence of
  // the auth text: a version that rendered "" or dropped the notice
  // entirely would still pass a not-equal-to-auth-text check, so that
  // alone does not prove this branch renders anything at all.
  assert.equal(usageProblemHTML("stale", "other"), `<span class="problem-notice">${t("usage_down")}</span>`);
  assert.equal(usageProblemHTML("stale", undefined), `<span class="problem-notice">${t("usage_down")}</span>`);
});

// --- gauge: the flicker fix's visible half -------------------------------
//
// cmd/fleetdeck/collect.go's usage.Fetcher now falls back to its own cache
// on a failed refresh, so snap.limits stays populated (aged) instead of
// going nil for one poll cycle -- see internal/usage's own tests for that
// half. gauge() is the other half: a last-known value must read calm
// (.gauge-stale), never the hot/warm/cool severity coloring a fresh reading
// gets, since a severity color on an aged number asserts a freshness it
// does not have.

test("no data at all is the existing gauge-off dash, unaffected by staleness", () => {
  const html = gauge("5h", null, false, undefined);
  assert.equal(html.includes("gauge-off"), true);
  assert.equal(html.includes("—"), true);
});

test("a fresh reading is colored by severity, not marked stale", () => {
  const html = gauge("5h", { utilization: 95, resetsAt: "2026-01-01T00:00:00Z" }, false, undefined);
  assert.equal(html.includes("gauge-hot"), true);
  assert.equal(html.includes("gauge-stale"), false);
});

// The control case: the same window value, stale vs fresh, must render
// visibly differently -- otherwise the flag exists in code but changes
// nothing a person can see.
test("control case: the same window renders differently stale vs fresh", () => {
  const window_ = { utilization: 95, resetsAt: "2026-01-01T00:00:00Z" };
  const fresh = gauge("5h", window_, false, undefined);
  const stale = gauge("5h", window_, true, new Date(Date.now() - 3 * 60000).toISOString());
  assert.notEqual(fresh, stale, "a stale reading must render differently from a fresh one");
  assert.equal(fresh.includes("gauge-hot"), true);
  assert.equal(stale.includes("gauge-hot"), false, "a stale reading must not carry the fresh severity color");
  assert.equal(stale.includes("gauge-stale"), true);
});

test("a stale reading carries its age, not the reset countdown", () => {
  const threeMinutesAgo = new Date(Date.now() - 3 * 60000).toISOString();
  const html = gauge("5h", { utilization: 40, resetsAt: "2026-01-01T00:00:00Z" }, true, threeMinutesAgo);
  assert.equal(html.includes("3m"), true);
  assert.equal(html.includes(t("last_known")), true);
});

// --- isUsageStale: the local-file age half of the same flicker fix -------
//
// The local rate-limits file (cmd/fleetdeck-status) can go stale with no
// usageError at all -- no session has ticked its statusline in a while,
// nothing failed. isUsageStale is what tells the gauges to read calm for
// that case too, not just the network-error case createUsageErrorTracker
// already covered.

test("no limits at all is not stale -- gauge-off, not gauge-stale", () => {
  assert.equal(isUsageStale({}, Date.now()), false);
});

test("an active usageError makes limits stale regardless of age", () => {
  const nowMs = Date.now();
  const snap = { limits: { fetchedAt: new Date(nowMs).toISOString() }, usageError: "boom" };
  assert.equal(isUsageStale(snap, nowMs), true);
});

// The control case: same fetchedAt, only the elapsed time differs.
test("control case: age alone flips stale once past the threshold, with no usageError", () => {
  const fetchedAt = new Date(0).toISOString();
  const justUnder = { limits: { fetchedAt } };
  const justOver = { limits: { fetchedAt } };
  assert.equal(isUsageStale(justUnder, RATE_LIMITS_AGE_WORTH_SHOWING_MS - 1), false);
  assert.equal(isUsageStale(justOver, RATE_LIMITS_AGE_WORTH_SHOWING_MS + 1), true);
});

// --- the fleet menu ---
//
// The row of fleet buttons became one menu, and the menu is shown with one
// fleet too. That is a deliberate change to what shipped: a control that
// appeared only once a second fleet existed was a second fleet nobody could
// find out about, and the way to make one now lives in this menu.

test("the menu names the fleet this tab shows, and is closed until it is opened", () => {
  const html = fleetMenuHTML([{ name: "A", current: false, waiting: 0 }, { name: "B", current: true, waiting: 0 }], false);
  assert.match(html, /class="fleet-menu-name">B</, "the trigger says which fleet is on screen");
  assert.match(html, /aria-expanded="false"/);
  assert.doesNotMatch(html, /fleet-menu-list/, "a closed menu has no list in the page at all");
});

test("one fleet still has a menu: it is where a second fleet comes from", () => {
  const html = fleetMenuHTML([{ name: "A", current: true, waiting: 0 }], false);
  assert.match(html, /class="fleet-menu-name">A</);
  assert.match(fleetMenuHTML([{ name: "A", current: true, waiting: 0 }], true), /fleet-menu-new/);
});

test("an open menu lists every fleet, marks this one and counts the others' waiting", () => {
  const html = fleetMenuHTML([
    { name: "A", current: false, waiting: 2 },
    { name: "B", current: true, waiting: 0 },
  ], true);
  const entries = [...html.matchAll(/<button type="button" class="(fleet-menu-entry[^"]*)" data-fleet-menu="pick" data-fleet="([^"]*)"([^>]*)>(.*?)<\/button>/g)];
  assert.deepEqual(entries.map((m) => m[2]), ["A", "B"]);
  assert.equal(entries[0][1], "fleet-menu-entry");
  assert.match(entries[0][4], /class="fleet-menu-waiting">2</, "a fleet with waiting sessions says how many");
  assert.equal(entries[1][1], "fleet-menu-entry fleet-menu-entry-current");
  assert.match(entries[1][3], /aria-current="page"/);
  assert.doesNotMatch(entries[1][4], /fleet-menu-waiting/, "no count where nothing waits");
});

test("an open menu offers the way back to the start page and the way to a new fleet", () => {
  const html = fleetMenuHTML([{ name: "A", current: true, waiting: 0 }], true);
  assert.match(html, /data-fleet-menu="all"/, "the way back to the start page");
  assert.match(html, /data-fleet-menu="new"/, "the way to a fleet that does not exist yet");
});

test("a fleet's name reaches the menu only as escaped text", () => {
  const html = fleetMenuHTML([
    { name: '"><img src=x>', current: false, waiting: 0 },
    { name: "B", current: true, waiting: 0 },
  ], true);
  assert.ok(!html.includes("<img"), "a name must never reach the DOM as markup");
  assert.ok(html.includes('data-fleet="&quot;&gt;&lt;img src=x&gt;"'));
});

// What a click means is a function of its own, because a handler attached to
// markup built with innerHTML is invisible to these tests: three mutants in
// exactly such handlers survived every unit test once and were only killed on
// a stand (docs/engineering/multiple-fleets.md §7).

test("the trigger opens a closed menu and closes an open one", () => {
  assert.deepEqual(nextMenuState(false, "toggle", ""), { open: true, go: null });
  assert.deepEqual(nextMenuState(true, "toggle", ""), { open: false, go: null });
});

test("choosing the fleet already on screen closes the menu and goes nowhere", () => {
  // It is a full page load, and reloading the panel a person is already
  // looking at would close every terminal they have open.
  assert.deepEqual(nextMenuState(true, "pick", "B", "B"), { open: false, go: null });
});

test("choosing another fleet leaves for it", () => {
  assert.deepEqual(nextMenuState(true, "pick", "A", "B"), { open: false, go: { fleet: "A" } });
});

test("all fleets goes to the start page, and a new fleet goes to its form", () => {
  assert.deepEqual(nextMenuState(true, "all", ""), { open: false, go: { path: "/" } });
  assert.deepEqual(nextMenuState(true, "new", ""), { open: false, go: { path: "/#new" } });
});

test("anything else closes the menu without going anywhere", () => {
  assert.deepEqual(nextMenuState(true, "away", ""), { open: false, go: null });
  assert.deepEqual(nextMenuState(false, "away", ""), { open: false, go: null });
});

test("the counters count this fleet and the unclaimed, never another fleet's", () => {
  const mine = { short: "b1", fleets: ["B"], needs: "answer: ship it?" };
  const nobodys = { short: "n1", needs: "usage limit reached" };
  const theirs = { short: "a1", fleets: ["A"], needs: "answer: go on?" };
  const theirStall = { short: "a2", fleets: ["A"], needs: "rate limited" };
  const snap = { fleet: "B", fleets: ["A", "B"], sessions: [mine, nobodys, theirs, theirStall] };
  const counts = headerCounts(snap, [nobodys, theirStall]);
  assert.deepEqual(counts.waiting.map((s) => s.short), ["b1"]);
  assert.deepEqual(counts.stalled.map((s) => s.short), ["n1"]);
});

