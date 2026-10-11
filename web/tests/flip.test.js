// Motion for a box rewritten whole (T-159, PR2).
//
// The board and the session list replace their innerHTML on every snapshot, so
// a card that changes column is a new node in a new place with nothing to
// transition from. flip.js reads where every keyed node was before the rewrite
// and plays each one from there to where it is now — but only a node whose
// place in its group changed: a card pushed down by a taller neighbour has not
// moved anywhere the operator should follow, and one that moved out of sight
// has nothing to show.
//
// These drive it with plain objects: a rect, a key, a group, and a record of
// what was animated.

import { test } from "node:test";
import assert from "node:assert/strict";

import { capture, play, MOVE_MS, ENTER_MS } from "../js/flip.js";

function node(key, group, rect) {
  const played = [];
  return {
    key,
    group,
    rect,
    played,
    getBoundingClientRect() {
      return { ...this.rect, right: this.rect.left + this.rect.width, bottom: this.rect.top + this.rect.height };
    },
    animate(frames, options) {
      played.push({ frames, options });
      return {};
    },
  };
}

const opts = (extra = {}) => ({
  keyOf: (n) => n.key,
  groupOf: (n) => n.group,
  viewport: { left: 0, top: 0, right: 1000, bottom: 800 },
  reduced: false,
  ...extra,
});

const box = (left, top) => ({ left, top, width: 100, height: 40 });

test("flip: a card that changed column is played from where it was", () => {
  const before = capture([node("a", "new", box(0, 100)), node("b", "new", box(0, 150))], opts());
  const moved = node("a", "active", box(200, 100));
  const stayed = node("b", "new", box(0, 150));
  play(before, [moved, stayed], opts());
  assert.equal(moved.played.length, 1);
  assert.deepEqual(moved.played[0].frames[0], { transform: "translate(-200px, 0px)" });
  assert.deepEqual(moved.played[0].frames[1], { transform: "none" });
  assert.equal(moved.played[0].options.duration, MOVE_MS);
  assert.deepEqual(stayed.played, [], "a card that kept its place was animated");
});

test("flip: a card whose order in its column changed is played", () => {
  const before = capture([node("a", "s", box(0, 0)), node("b", "s", box(0, 50))], opts());
  const a = node("a", "s", box(0, 50));
  const b = node("b", "s", box(0, 0));
  play(before, [b, a], opts());
  assert.equal(a.played.length, 1);
  assert.equal(b.played.length, 1);
});

test("flip: a card pushed by the layout, in the same place in its column, is not played", () => {
  const before = capture([node("a", "new", box(0, 100))], opts());
  // A taller card above, a narrower window: the same card, second in the same
  // column, somewhere else on screen.
  const shifted = node("a", "new", box(0, 180));
  play(before, [shifted], opts());
  assert.deepEqual(shifted.played, []);
});

test("flip: a move that starts or ends out of sight is not played", () => {
  const before = capture([node("a", "new", box(0, 900)), node("b", "new", box(0, 100))], opts());
  const fromBelow = node("a", "active", box(200, 100));
  const toBelow = node("b", "active", box(200, 1200));
  play(before, [fromBelow, toBelow], opts());
  assert.deepEqual(fromBelow.played, [], "a card flew in from below the fold");
  assert.deepEqual(toBelow.played, [], "a card was animated into a place nobody can see");
});

test("flip: a box that clips its nodes counts as their edge of sight", () => {
  const before = capture([node("a", "new", box(0, 100))], opts());
  const moved = node("a", "active", box(200, 600));
  play(before, [moved], opts({ clipOf: () => ({ left: 0, top: 0, right: 1000, bottom: 500 }) }));
  assert.deepEqual(moved.played, [], "the column scrolled the card out of its own view");
});

// Each column clips its own cards: where a card was is judged by the column it
// was in, where it is by the column it is in. Judged by the new column alone,
// every move between columns starts "out of sight" and none is played.
test("flip: a card moving between two clipping columns is played", () => {
  const columns = { new: { left: 0, top: 0, right: 150, bottom: 800 }, active: { left: 200, top: 0, right: 350, bottom: 800 } };
  const clipOf = (n) => columns[n.group];
  const before = capture([node("a", "new", box(0, 100))], opts({ clipOf }));
  const moved = node("a", "active", box(200, 300));
  play(before, [moved], opts({ clipOf }));
  assert.equal(moved.played.length, 1, "a move between columns was taken for one out of sight");
});

test("flip: a node that is new fades in, but not on the first drawing", () => {
  const first = node("a", "new", box(0, 0));
  play(capture([], opts()), [first], opts());
  assert.deepEqual(first.played, [], "the whole board faded in when the page opened");

  const before = capture([node("a", "new", box(0, 0))], opts());
  const fresh = node("b", "new", box(0, 50));
  play(before, [node("a", "new", box(0, 0)), fresh], opts());
  assert.equal(fresh.played.length, 1);
  assert.equal(fresh.played[0].options.duration, ENTER_MS);
  assert.equal(fresh.played[0].frames[0].opacity, 0);
});

test("flip: nothing moves for a person who asked for less motion", () => {
  const before = capture([node("a", "new", box(0, 0)), node("b", "new", box(0, 50))], opts());
  const moved = node("a", "active", box(200, 0));
  const fresh = node("c", "new", box(0, 100));
  play(before, [moved, fresh], opts({ reduced: true }));
  assert.deepEqual(moved.played, []);
  assert.deepEqual(fresh.played, []);
});

// The board is drawn again right after a drop (dragend), and again by every
// snapshot: a card still on its way is replaced by a node already in its new
// place. Read where it is on screen — mid-flight — it is carried on from
// there rather than jumping to the end.
test("flip: a card redrawn while still moving goes on from where it is on screen", () => {
  const flying = node("a", "active", box(120, 100));
  flying.getAnimations = () => [{ id: "flip", playState: "running" }];
  const before = capture([flying], opts());
  const redrawn = node("a", "active", box(200, 100));
  play(before, [redrawn], opts());
  assert.equal(redrawn.played.length, 1, "the card jumped to the end of its move");
  assert.deepEqual(redrawn.played[0].frames[0], { transform: "translate(-80px, 0px)" });
});

test("flip: a move is played under its own name, so a redraw can tell it from the page's own animations", () => {
  const before = capture([node("a", "new", box(0, 100))], opts());
  const moved = node("a", "active", box(200, 100));
  play(before, [moved], opts());
  assert.equal(moved.played[0].options.id, "flip");
  const pulsing = node("b", "active", box(0, 0));
  pulsing.getAnimations = () => [{ id: "", playState: "running" }];
  const again = capture([pulsing], opts());
  const same = node("b", "active", box(0, 20));
  play(again, [same], opts());
  assert.deepEqual(same.played, [], "a card with only a CSS animation running was taken for a moving one");
});

test("flip: a node without the means to animate is left alone", () => {
  const before = capture([{ key: "a", group: "new" }], opts());
  assert.doesNotThrow(() => play(before, [{ key: "a", group: "active" }], opts()));
});
