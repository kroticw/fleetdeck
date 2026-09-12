// The update button, as the page sees it: what one press does, what each
// thing the window reports turns it into, and what the person reads.
//
// The rules the operator set for it, each pinned below: the button changes on
// the first press; a second update cannot start while one runs; any wait over
// two seconds is shown with its time; and text typed and not sent is not lost
// without a word.

import { test } from "node:test";
import assert from "node:assert/strict";

import {
  UPDATE_BINDING,
  WAY_BINDING,
  WAIT_SHOWN_AFTER_MS,
  UPDATE_REPAINT_MS,
  initialState,
  onPress,
  onProgress,
  reasonKey,
  updateHTML,
} from "../js/update.js";
import { t } from "../js/i18n.js";

// Texts are compared through t(), in whatever language this machine's node
// reports: a test that reads English passes on one machine and fails on the
// next.
const has = (html, key, values = {}) =>
  html.includes(t(key).replace(/\{(\w+)\}/g, (_, name) => values[name] ?? ""));

test("the binding name is the one cmd/fleetdeck-window gives the page", () => {
  assert.equal(UPDATE_BINDING, "fleetdeckUpdate");
});

test("the first press starts the update and changes the button at once", () => {
  const { state, start } = onPress(initialState(), { unsent: false, now: 1000 });
  assert.equal(start, true);
  assert.equal(state.phase, "running");
  const html = updateHTML(state, 1000);
  assert.match(html, /disabled/, "a running update's button does not take a second press");
  assert.ok(has(html, "update_step_press"), "the press is not answered in words at once");
});

test("a press while an update runs starts nothing", () => {
  const running = onPress(initialState(), { unsent: false, now: 0 }).state;
  const { state, start } = onPress(running, { unsent: false, now: 10 });
  assert.equal(start, false);
  assert.equal(state.phase, "running");
});

test("with unsent text the first press asks, and only the second starts", () => {
  const first = onPress(initialState(), { unsent: true, now: 0 });
  assert.equal(first.start, false);
  assert.equal(first.state.phase, "confirm");
  assert.ok(has(updateHTML(first.state, 0), "update_confirm_unsent"), "the question names the unsent text");
  const second = onPress(first.state, { unsent: true, now: 5 });
  assert.equal(second.start, true);
  assert.equal(second.state.phase, "running");
});

test("a wait is shown with its time once it passes two seconds, and not before", () => {
  assert.equal(WAIT_SHOWN_AFTER_MS, 2000);
  let state = onPress(initialState(), { unsent: false, now: 0 }).state;
  state = onProgress(state, { step: "build", detail: "abc1234def" }, 10_000);
  assert.ok(!has(updateHTML(state, 10_000 + 1999), "update_elapsed", { n: "1" }));
  assert.ok(!has(updateHTML(state, 10_000 + 1999), "update_elapsed", { n: "0" }));
  assert.ok(has(updateHTML(state, 10_000 + 2000), "update_elapsed", { n: "2" }));
  assert.ok(has(updateHTML(state, 10_000 + 7400), "update_elapsed", { n: "7" }));
});

// The header repaints a running update on this cadence, so the time of a wait
// appears at most this long after the wait passes two seconds. At once a
// second it appeared up to a second late -- seen on a live page.
test("a running update is repainted often enough for the time to appear on time", () => {
  assert.ok(UPDATE_REPAINT_MS <= 250, `UPDATE_REPAINT_MS = ${UPDATE_REPAINT_MS}`);
});

test("each step says what is happening, with the build it is about", () => {
  let state = onPress(initialState(), { unsent: false, now: 0 }).state;
  state = onProgress(state, { step: "build", detail: "abc1234def5678" }, 1);
  assert.match(updateHTML(state, 1), /abc1234/);
  assert.doesNotMatch(updateHTML(state, 1), /abc1234def/, "a commit is shown short, as the header shows it");
  state = onProgress(state, { step: "handover:swapped", detail: "/x/fleetdeck.app" }, 2);
  assert.equal(state.phase, "running");
  assert.ok(has(updateHTML(state, 2), "update_step_swapped"));
});

test("the ends: done, already current, busy, failed -- each in words, each pressable again but done", () => {
  const running = onPress(initialState(), { unsent: false, now: 0 }).state;

  const done = onProgress(running, { step: "done", detail: "abc1234def" }, 1);
  assert.equal(done.phase, "done");
  assert.ok(has(updateHTML(done, 1), "update_done", { rev: "abc1234" }));

  const current = onProgress(running, { step: "current", detail: "abc1234def" }, 1);
  assert.equal(current.phase, "current");
  assert.ok(has(updateHTML(current, 1), "update_current", { rev: "abc1234" }));
  assert.equal(onPress(current, { unsent: false, now: 2 }).start, true);

  const busy = onProgress(running, { step: "busy", detail: "" }, 1);
  assert.ok(has(updateHTML(busy, 1), "update_busy"));

  const failed = onProgress(running, { step: "failed", detail: "the source tree has uncommitted edits (a.go)" }, 1);
  assert.equal(failed.phase, "failed");
  assert.ok(has(updateHTML(failed, 1), "update_failed", { detail: "the source tree has uncommitted edits (a.go)" }));
  assert.equal(onPress(failed, { unsent: false, now: 2 }).start, true);
});

test("what the window reports is escaped before it reaches the page", () => {
  const running = onPress(initialState(), { unsent: false, now: 0 }).state;
  const failed = onProgress(running, { step: "failed", detail: "<img src=x onerror=alert(1)>" }, 1);
  assert.doesNotMatch(updateHTML(failed, 1), /<img/);
});

// The defect this whole change is about: the page used to decide whether to
// show an update button by whether the window had bound one, so an app
// installed from a release -- which could not update, and said nothing about
// it -- showed no button at all. Nobody could learn that updating existed.
//
// The button is always there now. A build that cannot update says so, in
// words, beside a button that is visibly not pressable.

test("the way binding is the one cmd/fleetdeck-window gives the page", () => {
  assert.equal(WAY_BINDING, "fleetdeckUpdateWay");
});

test("a build that cannot update shows the button and says why", () => {
  const state = onProgress(initialState(), { step: "cannot", reason: "built-here" }, 0);
  const html = updateHTML(state, 0);

  assert.match(html, /class="update-button"/, "no button at all: the silent refusal is back");
  assert.match(html, /disabled/, "the button offers an update this build cannot do");
  assert.ok(has(html, "update_cannot_built_here"), `no reason on screen: ${html}`);
});

test("every reason a build cannot update has words of its own", () => {
  for (const reason of ["not-a-bundle", "built-here", "no-version"]) {
    const html = updateHTML(onProgress(initialState(), { step: "cannot", reason }, 0), 0);
    assert.ok(has(html, reasonKey("update_cannot", reason)), `${reason} has no sentence of its own`);
  }
});

// A refusal arrives as a code and the particulars separately, because the
// sentence has to be in the reader's language and the particulars -- a team
// identifier, a path, how many megabytes -- come from the system in whatever
// language it uses.
test("a refusal is shown in the reader's language, with its particulars beside it", () => {
  const state = onProgress(initialState(), {
    step: "failed",
    reason: "seal:wrong-team",
    detail: "the downloaded app is signed by team ZZZZZZZZZZ, not PTLLPQ8LY4",
  }, 0);
  const html = updateHTML(state, 0);

  assert.ok(has(html, "update_reason_seal_wrong_team"), `no translated reason: ${html}`);
  assert.match(html, /ZZZZZZZZZZ/, "the particulars of the refusal are not shown");
  assert.match(html, /update-problem/, "a refusal is not marked as a problem");
});

test("every refusal the window can send has words of its own", () => {
  const reasons = [
    "offline",
    "no-releases",
    "no-room",
    "other",
    "seal:broken",
    "seal:not-developer-id",
    "seal:wrong-team",
    "seal:no-hardened-runtime",
    "seal:no-timestamp",
    "seal:not-notarized",
  ];
  for (const reason of reasons) {
    const html = updateHTML(onProgress(initialState(), { step: "failed", reason, detail: "x" }, 0), 0);
    assert.ok(has(html, reasonKey("update_reason", reason)), `${reason} has no sentence of its own`);
  }
});

// A refusal with no code -- an older window, or something nobody foresaw --
// must still say something rather than show an empty line where a sentence
// belongs.
test("a refusal with no code still says something", () => {
  const html = updateHTML(onProgress(initialState(), { step: "failed", detail: "something went wrong" }, 0), 0);

  assert.match(html, /something went wrong/, `nothing on screen: ${html}`);
  assert.match(html, /update-problem/);
});

test("downloading and checking are steps the person can see", () => {
  for (const [step, key] of [["download", "update_step_download"], ["verify", "update_step_verify"]]) {
    const html = updateHTML(onProgress(initialState(), { step, detail: "v0.4.0" }, 0), 0);
    assert.ok(has(html, key, { version: "v0.4.0" }), `${step} is not shown: ${html}`);
  }
});

// A build that cannot update must not start one when the button is pressed
// anyway -- by a keyboard, or by a click the browser delivered before the
// answer arrived.
// The window asks GitHub once a day, at startup, and says nothing unless
// there is something to say. When there is, it has to be visible without
// anybody having pressed anything -- that is the whole point of asking.
test("a version found at startup is shown, and the button still works", () => {
  const state = onProgress(initialState(), { step: "available", detail: "v0.4.0" }, 0);
  const html = updateHTML(state, 0);

  assert.ok(has(html, "update_available", { version: "v0.4.0" }), `nothing on screen: ${html}`);
  assert.match(html, /class="update-button"/);
  assert.doesNotMatch(html, /disabled/, "the button that would install it is not pressable");
  assert.equal(onPress(state, { unsent: false, now: 1 }).start, true);
});

// A version offered at startup must not sit on screen once an update has
// started: the state moves on with the update, like every other step.
test("starting the update replaces the offer", () => {
  const offered = onProgress(initialState(), { step: "available", detail: "v0.4.0" }, 0);

  const pressed = onPress(offered, { unsent: false, now: 1 });

  assert.equal(pressed.state.phase, "running");
});

test("pressing a button that cannot update starts nothing", () => {
  const state = onProgress(initialState(), { step: "cannot", reason: "built-here" }, 0);

  const pressed = onPress(state, { unsent: false, now: 0 });

  assert.equal(pressed.start, false);
  assert.equal(pressed.state.phase, "cannot");
});
