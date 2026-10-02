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
  KNOWN_BINDING,
  WAIT_SHOWN_AFTER_MS,
  UPDATE_REPAINT_MS,
  CHECK_SHOWN_MS,
  initialState,
  onPress,
  onProgress,
  needsRepaint,
  reasonKey,
  settle,
  STAND_UPDATE,
  panelPlace,
  updatePanelReport,
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

test("the ends: done, already current, busy, failed -- each in words, pressable again only where there is something to do", () => {
  const running = onPress(initialState(), { unsent: false, now: 0 }).state;

  const done = onProgress(running, { step: "done", detail: "abc1234def" }, 1);
  assert.equal(done.phase, "done");
  assert.ok(has(updateHTML(done, 1), "update_done", { rev: "abc1234" }));

  const current = onProgress(running, { step: "current", detail: "abc1234def" }, 1);
  assert.equal(current.phase, "current");
  assert.ok(has(updateHTML(current, 1), "update_current", { rev: "abc1234" }));
  assert.doesNotMatch(updateHTML(current, 1), /update-button/, "already up to date, and still a button to update with");

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

// The button is on screen only while there is something to update to: its
// appearing is the notice that a newer version exists. A button that stood
// there always meant "press and I will go and look", was pressed for nothing,
// and changed nothing on screen on the day a release came out.

test("the known binding is the one cmd/fleetdeck-window gives the page", () => {
  assert.equal(KNOWN_BINDING, "fleetdeckUpdateKnown");
});

test("with nothing to update to there is no button at all, not even a disabled one", () => {
  const html = updateHTML(initialState(), 0);

  assert.doesNotMatch(html, /update-button/, `a button with nothing to update to: ${html}`);
  assert.equal(html, '<span class="update-control"></span>', "the empty control must be empty, so CSS can take it out of the row");
});

test("a window that knows of nothing newer leaves no button", () => {
  const state = onProgress(initialState(), { step: "none" }, 0);

  assert.equal(state.phase, "idle");
  assert.doesNotMatch(updateHTML(state, 0), /update-button/);
});

test("a version the window found takes the button back off when the window says there is none", () => {
  const offered = onProgress(initialState(), { step: "available", detail: "v0.8.0" }, 0);

  const withdrawn = onProgress(offered, { step: "none" }, 1);

  assert.doesNotMatch(updateHTML(withdrawn, 1), /update-button/);
});

// The window looks on its own schedule, so what it finds can arrive in the
// middle of an update, or while the page is asking about unsent text. Neither
// may be knocked back to an offer.
test("what the window finds on its own does not interrupt a press", () => {
  const confirm = onPress(onProgress(initialState(), { step: "available", detail: "v0.8.0" }, 0), { unsent: true, now: 1 }).state;
  const running = onPress(initialState(), { unsent: false, now: 0 }).state;
  const done = onProgress(running, { step: "done", detail: "abc1234" }, 1);
  for (const state of [confirm, running, done]) {
    for (const report of [{ step: "available", detail: "v0.9.0" }, { step: "none" }]) {
      assert.equal(onProgress(state, report, 2), state, `${report.step} knocked ${state.phase} out of the way`);
    }
  }
});

test("a failed or refused update keeps its button, so it can be tried again", () => {
  const running = onPress(initialState(), { unsent: false, now: 0 }).state;
  const failed = onProgress(running, { step: "failed", reason: "offline", detail: "no route" }, 1);
  const busy = onProgress(running, { step: "busy" }, 1);

  assert.match(updateHTML(failed, 1), /class="btn btn-sm update-button"/);
  assert.match(updateHTML(busy, 1), /class="btn btn-sm update-button"/);
});

// A dev app, opened beside the installed app, says it does not update rather
// than leaving a code on screen.
test("a dev app says it does not update, and offers no button", () => {
  const state = onProgress(initialState(), { step: "cannot", reason: "dev" }, 0);
  const html = updateHTML(state, 0);

  assert.doesNotMatch(html, /update-button/, `a button for an update a dev app cannot do: ${html}`);
  assert.ok(has(html, "update_cannot_dev"), `no reason on screen: ${html}`);
});

// A build that cannot update itself never finds anything to update to, so it
// has no button to press; this answer only arrives if something calls the
// update binding anyway. It is said in words, and still offers no button.
test("a build that cannot update says why, and offers no button", () => {
  const state = onProgress(initialState(), { step: "cannot", reason: "built-here" }, 0);
  const html = updateHTML(state, 0);

  assert.doesNotMatch(html, /update-button/, `a button for an update this build cannot do: ${html}`);
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
// The window looks for a newer version by itself while it runs, and says
// nothing unless there is something to say. When there is, it has to be
// visible without anybody having pressed anything -- that is the whole point.
test("a version the window found is shown with a button that installs it", () => {
  const state = onProgress(initialState(), { step: "available", detail: "v0.4.0" }, 0);
  const html = updateHTML(state, 0);

  assert.ok(has(html, "update_available", { version: "v0.4.0" }), `nothing on screen: ${html}`);
  assert.match(html, /class="btn btn-sm update-button"/);
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

// Check for Updates… in the app menu asks the releases page now. The answer
// appears where the Update button does, in the reader's language: that it is
// being checked, what was found, that nothing newer is out, or why there was
// no answer. "Nothing newer" and a failure go away by themselves after
// CHECK_SHOWN_MS; a found version stays, with its button.

test("checking is said at once, with no button, and its wait is shown after two seconds", () => {
  const state = onProgress(initialState(), { step: "checking" }, 1000);

  assert.equal(state.phase, "checking");
  const html = updateHTML(state, 1000);
  assert.ok(has(html, "update_checking"), `nothing on screen: ${html}`);
  assert.doesNotMatch(html, /update-button/);
  assert.ok(has(updateHTML(state, 3500), "update_elapsed", { n: "2" }));
});

test("nothing newer is said with the running version, and goes after ten seconds", () => {
  assert.equal(CHECK_SHOWN_MS, 10_000);
  const checking = onProgress(initialState(), { step: "checking" }, 0);
  const latest = onProgress(checking, { step: "latest", detail: "v1.0.0" }, 500);

  const html = updateHTML(latest, 500);
  assert.ok(has(html, "update_latest", { version: "v1.0.0" }), `nothing on screen: ${html}`);
  assert.doesNotMatch(html, /update-button/);
  assert.equal(settle(latest, 500 + CHECK_SHOWN_MS - 1), latest);
  assert.equal(settle(latest, 500 + CHECK_SHOWN_MS).phase, "idle");
});

test("a found version stays, with the button that installs it", () => {
  const checking = onProgress(initialState(), { step: "checking" }, 0);
  const found = onProgress(checking, { step: "available", detail: "v1.1.0" }, 1);

  assert.equal(found.phase, "available");
  assert.equal(settle(found, 1 + 60 * CHECK_SHOWN_MS), found);
  assert.match(updateHTML(found, 2), /class="btn btn-sm update-button"/);
});

test("a check that got no answer says why in the reader's language, and goes after ten seconds", () => {
  const checking = onProgress(initialState(), { step: "checking" }, 0);
  const failed = onProgress(checking, { step: "check-failed", reason: "offline", detail: "dial tcp: no route to host" }, 1);

  const html = updateHTML(failed, 1);
  assert.ok(has(html, "update_check_failed_because", { why: t("update_check_reason_offline"), detail: "dial tcp: no route to host" }), `not said: ${html}`);
  assert.match(html, /update-problem/);
  assert.equal(settle(failed, 1 + CHECK_SHOWN_MS).phase, "idle");
});

test("a check that failed for a reason nobody foresaw still shows the particulars", () => {
  const failed = onProgress(initialState(), { step: "check-failed", reason: "other", detail: "something odd" }, 0);

  assert.ok(has(updateHTML(failed, 0), "update_check_failed", { detail: "something odd" }));
});

// A version found before the check stays found: a failed check is no answer,
// and the window keeps the version too (watch.go).
test("a check while a version is on offer keeps the offer through a failure", () => {
  const offered = onProgress(initialState(), { step: "available", detail: "v1.1.0" }, 0);
  const checking = onProgress(offered, { step: "checking" }, 1);
  const failed = onProgress(checking, { step: "check-failed", reason: "offline", detail: "x" }, 2);

  assert.match(updateHTML(failed, 2), /class="btn btn-sm update-button"/, "the button for the version found went away");
  assert.equal(onPress(failed, { unsent: false, now: 3 }).start, true);
  const after = settle(failed, 2 + CHECK_SHOWN_MS);
  assert.equal(after.phase, "available");
  assert.equal(after.detail, "v1.1.0");
});

test("a check never interrupts an update, the question about unsent text, or an update just done", () => {
  const confirm = onPress(onProgress(initialState(), { step: "available", detail: "v1.1.0" }, 0), { unsent: true, now: 1 }).state;
  const running = onPress(initialState(), { unsent: false, now: 0 }).state;
  const done = onProgress(running, { step: "done", detail: "abc1234" }, 1);
  for (const state of [confirm, running, done]) {
    for (const report of [{ step: "checking" }, { step: "latest", detail: "v1.0.0" }, { step: "check-failed", reason: "offline", detail: "x" }]) {
      assert.equal(onProgress(state, report, 2), state, `${report.step} knocked ${state.phase} out of the way`);
    }
  }
});

test("the header repaints while there is a wait to count or an answer to take away", () => {
  assert.equal(needsRepaint(initialState()), false);
  assert.equal(needsRepaint(onProgress(initialState(), { step: "available", detail: "v1.1.0" }, 0)), false);
  assert.equal(needsRepaint(onPress(initialState(), { unsent: false, now: 0 }).state), true);
  assert.equal(needsRepaint(onProgress(initialState(), { step: "checking" }, 0)), true);
  assert.equal(needsRepaint(onProgress(initialState(), { step: "latest", detail: "v1.0.0" }, 0)), true);
  assert.equal(needsRepaint(onProgress(initialState(), { step: "check-failed", reason: "offline", detail: "x" }, 0)), true);
});

test("what a check reports is escaped before it reaches the page", () => {
  const failed = onProgress(initialState(), { step: "check-failed", reason: "other", detail: "<img src=x onerror=alert(1)>" }, 0);
  assert.doesNotMatch(updateHTML(failed, 0), /<img/);
  const latest = onProgress(initialState(), { step: "latest", detail: "<b>v1</b>" }, 0);
  assert.doesNotMatch(updateHTML(latest, 0), /<b>/);
});

// A stand's frames of Check for Updates… (FLEETDECK_STAND_OPEN check-*): the
// header is held in one state, and says what it shows in the window's log for
// scripts/standcheck (updatecontrol.go there) to hold the frame to.
test("every state a stand can open has a report to be held in, and only those", () => {
  assert.deepEqual(Object.keys(STAND_UPDATE).sort(), ["check-available", "check-checking", "check-failed", "check-latest"]);
  assert.equal(onProgress(initialState(), STAND_UPDATE["check-checking"], 0).phase, "checking");
  assert.equal(onProgress(initialState(), STAND_UPDATE["check-latest"], 0).phase, "latest");
  assert.equal(onProgress(initialState(), STAND_UPDATE["check-failed"], 0).phase, "checkFailed");
  assert.equal(onProgress(initialState(), STAND_UPDATE["check-available"], 0).phase, "available");
});

// The panel as the DOM would hand it over: its box, and the elements in it by
// class, the status with how much of its text fits.
const rect = (top, left, width, height) => ({ top, left, width, height, right: left + width, bottom: top + height });
function panelOf(html, { box = rect(150, 8, 297, 60), hidden = false, status = {} } = {}) {
  const fits = { scrollWidth: 270, clientWidth: 270, scrollHeight: 32, clientHeight: 32, rect: rect(160, 16, 270, 32), ...status };
  return {
    hidden,
    getBoundingClientRect: () => box,
    querySelector(selector) {
      const name = selector.slice(1);
      const m = html.match(new RegExp(`class="[^"]*\\b${name}\\b[^"]*"[^>]*>([^<]*)<`));
      if (!m) return null;
      return { textContent: m[1], ...fits, getBoundingClientRect: () => fits.rect };
    },
  };
}
const panelHTML = (open) => updateHTML(onProgress(initialState(), STAND_UPDATE[open], 0), 0);
const term = { getBoundingClientRect: () => rect(140, 0, 313, 500) };
const page = { innerWidth: 313, document: { documentElement: { scrollWidth: 313 } } };

test("the update panel reports its words, its button and whether it is a problem", () => {
  const failed = updatePanelReport(page, panelOf(panelHTML("check-failed")), term);
  assert.equal(failed.report, "update");
  assert.match(failed.text, /no such host/);
  assert.equal(failed.button, false);
  assert.equal(failed.problem, true);
  assert.equal(failed.shown, true);

  const found = updatePanelReport(page, panelOf(panelHTML("check-available")), term);
  assert.equal(found.button, true);
  assert.match(found.text, /v1\.1\.0/);
  assert.equal(found.problem, false);
});

// What a screenshot cannot settle to the point: where the panel is against
// the terminal it lies over and the page it is in, and whether its words fit
// their box.
test("the update panel reports where it lies and whether its words fit", () => {
  const r = updatePanelReport(page, panelOf(panelHTML("check-latest")), term);
  assert.deepEqual(r.box, { top: 150, left: 8, right: 305, bottom: 210 });
  assert.equal(r.termTop, 140);
  assert.equal(r.pageWidth, 313);
  assert.equal(r.clipped, false);

  const wider = updatePanelReport(page, panelOf(panelHTML("check-failed"), { status: { scrollWidth: 400 } }), term);
  assert.equal(wider.clipped, true, "words wider than their box were not seen");
  const taller = updatePanelReport(page, panelOf(panelHTML("check-failed"), { status: { scrollHeight: 50 } }), term);
  assert.equal(taller.clipped, true, "words taller than their box were not seen");
  const outside = updatePanelReport(page, panelOf(panelHTML("check-failed"), { status: { rect: rect(160, 16, 300, 32) } }), term);
  assert.equal(outside.clipped, true, "words running past the panel's edge were not seen");
});

test("a hidden panel, or none, reports nothing shown", () => {
  assert.equal(updatePanelReport(page, panelOf(panelHTML("check-latest"), { hidden: true }), term).shown, false);
  const none = updatePanelReport(page, null, null);
  assert.equal(none.shown, false);
  assert.equal(none.text, "");
});

// The panel lies over the top of the orchestrator's terminal, column-wide,
// and never over the brand row, the window's buttons or the island's head
// above the terminal.
test("the panel is placed over the top of the terminal, inset from the column's sides", () => {
  assert.deepEqual(panelPlace(rect(40, 0, 313, 660), rect(140, 0, 313, 500)), { top: 148, left: 8, width: 297 });
  assert.deepEqual(panelPlace(rect(40, 100, 500, 660), rect(140, 100, 500, 500)), { top: 148, left: 108, width: 484 });
});

test("a folded column, or one with no terminal yet, has no room for the panel", () => {
  assert.equal(panelPlace(rect(40, 0, 48, 660), rect(140, 0, 48, 500)), null);
  assert.equal(panelPlace(rect(40, 0, 313, 660), null), null);
  assert.equal(panelPlace(rect(40, 0, 313, 660), rect(140, 0, 313, 0)), null);
  assert.equal(panelPlace(null, rect(140, 0, 313, 500)), null);
});
