// web/js/_tests/folded-column.test.js
//
// The sessions column folded away. It used to be a bare strip with one button
// on it and nothing else for its whole height: a person folded the column to
// free the space and lost sight of the fleet, with no way to learn who was in
// it but to unfold again.
//
// Under _tests/ for the same reason the neighbouring files are: web/embed.go's
// plain (non "all:") directory pattern excludes any directory whose name starts
// with "_", so this subtree never reaches the binary.
//
// Run with: node --test web/js/_tests/folded-column.test.js

import test from "node:test";
import assert from "node:assert/strict";
import { foldedStripHtml } from "../sessions.js";

globalThis.navigator ??= { language: "en" };

const live = (short, name, extra = {}) => ({ short, name, lifecycle: "live", ...extra });

// --- what it says ---

test("the strip carries one mark per live session, and the count", () => {
  const html = foldedStripHtml([
    live("aa11", "fleetdeck: колонка сессий"),
    live("bb22", "fleetdeck: обновление из выпуска"),
    live("cc33", "оркестр"),
  ], new Set());

  assert.match(html, /class="sfold-count"[^>]*>3</, "the count is how many are running");
  assert.equal((html.match(/class="sfold-mark/g) ?? []).length, 3);
  assert.ok(html.includes(">КС<") && html.includes(">ОИ<") && html.includes(">ОР<"),
    "each mark is the session's own, not a repeat of the project they share");
});

// The acceptance in one test: the fleet this was built for names nearly every
// session "fleetdeck: …", and a strip of identical squares would satisfy the
// letter of "show icons" while telling a person nothing.
test("sessions that share a project prefix still get marks that differ", () => {
  const html = foldedStripHtml([
    live("aa11", "fleetdeck: колонка сессий"),
    live("bb22", "fleetdeck: обновление из выпуска"),
    live("cc33", "fleetdeck: разведка по разным агентам"),
  ], new Set());
  const marks = [...html.matchAll(/class="sfold-mark[^"]*"[^>]*>([^<]+)</g)].map((m) => m[1]);
  assert.equal(new Set(marks).size, 3, `marks must differ, got ${marks}`);
});

test("a session's full name and state are on the mark for hovering", () => {
  const html = foldedStripHtml([live("aa11", "fleetdeck: колонка сессий")], new Set());
  assert.match(html, /title="fleetdeck: колонка сессий"/);
});

// --- state, the thing worth folding a column to keep ---

test("a session waiting for a person is marked as waiting on the strip", () => {
  const html = foldedStripHtml([
    live("aa11", "one", { needs: "answer: which branch? (a · b)" }),
    live("bb22", "two"),
  ], new Set());
  assert.match(html, /class="sfold-mark sfold-waiting"[^>]*data-session="aa11"/);
  assert.ok(!/data-session="bb22"[^>]*sfold-waiting/.test(html));
});

test("a stalled session is marked stalled, and never as waiting", () => {
  const html = foldedStripHtml([live("aa11", "one")], new Set(["aa11"]));
  assert.match(html, /class="sfold-mark sfold-stalled"/);
  assert.ok(!html.includes("sfold-waiting"), "stalled is not waiting: the two counters are separate");
});

// The same order as the unfolded column, for the same reason: whoever is
// waiting is who a person needs to see first, and a strip that ordered itself
// differently would put them somewhere else every time the column was folded.
test("a waiting session is first on the strip, as it is first in the list", () => {
  const html = foldedStripHtml([
    live("aa11", "one"),
    live("bb22", "two", { needs: "answer: anything?" }),
  ], new Set());
  assert.ok(html.indexOf('data-session="bb22"') < html.indexOf('data-session="aa11"'));
});

// --- what it does not say ---

// A fleet's stopped sessions outnumber its running ones several times over on
// this machine. Marking them here would bury the handful that are actually
// working, and the folded strip is about who is running now.
test("a stopped session is not on the strip", () => {
  const html = foldedStripHtml([
    live("aa11", "running one"),
    { short: "bb22", name: "stopped one", lifecycle: "stopped" },
  ], new Set());
  assert.ok(!html.includes('data-session="bb22"'));
  assert.match(html, /class="sfold-count"[^>]*>1</);
});

test("a fleet with nothing running says so rather than showing an empty strip", () => {
  const html = foldedStripHtml([{ short: "bb22", lifecycle: "stopped" }], new Set());
  assert.match(html, /class="sfold-count"[^>]*>0</);
  assert.ok(!html.includes("sfold-mark"));
});

// --- pressing it ---

// The marks are buttons because pressing one opens that session, the same as
// pressing its row when the column is open. The paid lesson in the stylesheet
// runs both ways: what can be pressed has to look it, and what cannot must not
// — so a mark being a real <button> is the markup half of keeping that promise.
test("every mark is a real button carrying the session it opens", () => {
  const html = foldedStripHtml([live("aa11", "one"), live("bb22", "two")], new Set());
  assert.equal((html.match(/<button[^>]*class="sfold-mark/g) ?? []).length, 2);
  assert.match(html, /<button[^>]*data-session="aa11"/);
});

// --- hostile input ---

test("a name that looks like markup reaches the strip as text", () => {
  const html = foldedStripHtml([live("aa11", `<img src=x onerror="alert(1)">`)], new Set());
  assert.ok(!html.includes("<img"));
  assert.ok(html.includes("&lt;img"));
});
