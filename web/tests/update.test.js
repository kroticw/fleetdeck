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
  WAIT_SHOWN_AFTER_MS,
  UPDATE_REPAINT_MS,
  initialState,
  onPress,
  onProgress,
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
