// web/js/_tests/boardstate.test.js
//
// What a card says about its session at a glance: a badge with the state's
// word (working, waiting for an answer, stalled, stopped, dead) in its signal's
// colour, the progress as a number beside it, and the zone as a small dot
// before the card's number rather than a stripe down its side.

import test from "node:test";
import assert from "node:assert/strict";
import { columnHTML, liveStates } from "../board.js";
import { t } from "../i18n.js";

const card = (extra = {}) => ({ path: "/b/T-018-x.md", id: "T-018", title: "work", session: "bb22", zone: "planned", progress: 60, ...extra });

test("a card whose session works says so, with its progress as a number", () => {
  const html = columnHTML("active", "active", [card()], new Set(), new Set(), [], new Map([["bb22", "working"]]));
  assert.match(html, new RegExp(`class="kbadge kbadge-working">${t("dock_state_working")}<`));
  assert.match(html, /class="kpct">60%</);
});

test("a card whose session waits for an answer says that instead", () => {
  const html = columnHTML("review", "review", [card()], new Set(), new Set(), [], new Map([["bb22", "waiting"]]));
  assert.match(html, new RegExp(`class="kbadge kbadge-waiting">${t("dock_state_waiting")}<`));
});

test("a stalled session is said as stalled", () => {
  const html = columnHTML("active", "active", [card()], new Set(), new Set(), [], new Map([["bb22", "stalled"]]));
  assert.match(html, new RegExp(`class="kbadge kbadge-stalled">${t("dock_state_stalled")}<`));
});

test("a dead or stopped session is a badge, and the short id stays an id", () => {
  const dead = columnHTML("blocked", "blocked", [card()], new Set(["/b/T-018-x.md"]));
  assert.match(dead, new RegExp(`class="kbadge kbadge-dead">${t("session_dead")}<`));
  assert.match(dead, /class="ksession ksession-dead">bb22</);
  const stopped = columnHTML("review", "review", [card()], new Set(), new Set(["/b/T-018-x.md"]));
  assert.match(stopped, new RegExp(`class="kbadge kbadge-stopped">${t("dock_state_stopped")}<`));
  assert.match(stopped, /class="ksession ksession-stopped">bb22</);
});

test("a card with no session has no badge and no bar until it has progress", () => {
  const idle = columnHTML("new", "new", [card({ session: "", progress: 0 })], new Set());
  assert.doesNotMatch(idle, /kbadge|kprog/);
  const started = columnHTML("new", "new", [card({ session: "", progress: 20 })], new Set());
  assert.match(started, /class="kprog"/);
});

test("the zone is a dot before the number", () => {
  const html = columnHTML("new", "new", [card({ zone: "urgent" })], new Set());
  assert.match(html, /class="kmeta"><span class="kzone" aria-hidden="true"><\/span><span class="knum"/);
  assert.match(html, /class="kcard zone-urgent/);
});

test("live states come from the snapshot's sessions: waiting before stalled before working", () => {
  const live = liveStates({
    sessions: [
      { short: "a1", needs: "Which one?" },
      { short: "b2", needs: "", state: "blocked" },
      { short: "c3", needs: "" },
    ],
  });
  assert.equal(live.get("a1"), "waiting");
  assert.equal(live.get("b2"), "stalled");
  assert.equal(live.get("c3"), "working");
  assert.equal(liveStates(null).size, 0);
});
