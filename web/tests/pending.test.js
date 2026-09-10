// Drawing a message before the server has it, and the three ways that goes
// wrong. Each case names what would have to break for it to fail.

import { test } from "node:test";
import assert from "node:assert/strict";

import { createPending } from "../js/pending.js";

const answer = (text) => ({ role: "assistant", text });
const said = (text) => ({ role: "user", text });

test("a sent message is drawn at once, after everything the server sent", () => {
  const pending = createPending();
  pending.add("перезапусти панель");

  const drawn = pending.merge([answer("готово")]);

  assert.equal(drawn.length, 2);
  assert.equal(drawn[0].text, "готово", "the server's own steps were reordered");
  assert.equal(drawn[1].text, "перезапусти панель");
  assert.equal(drawn[1].role, "user", "the person is speaking, so it is a user step");
});

// The whole reason it is appended rather than inserted: a pending message is by
// definition the newest thing in the thread, and the real one lands in the same
// place, so nothing moves when one replaces the other.
test("it lands where the real one will, so nothing jumps at the swap", () => {
  const pending = createPending();
  pending.add("вторая");

  const before = pending.merge([said("первая"), answer("ок")]);
  assert.equal(before.at(-1).text, "вторая");

  const after = pending.merge([said("первая"), answer("ок"), said("вторая")]);
  assert.equal(after.at(-1).text, "вторая", "the real message is in the position the drawn one held");
  assert.equal(after.length, 3, "and it is there once");
});

// Break it by returning the incoming steps with every pending message appended
// unconditionally and this test fails with the message twice.
test("the drawn message goes away when the real one arrives", () => {
  const pending = createPending();
  pending.add("перезапусти панель");

  const first = pending.merge([answer("готово")]);
  assert.equal(first.length, 2, "precondition: it was drawn ahead");

  const second = pending.merge([answer("готово"), said("перезапусти панель")]);

  assert.equal(second.length, 2, `the message is on screen twice: ${JSON.stringify(second)}`);
  assert.equal(second.filter((s) => s.pending).length, 0, "the drawn copy outlived the real one");
});

// And it stays gone: a later poll that no longer carries it must not bring the
// drawn copy back from the dead.
test("once matched it is forgotten, not re-drawn by the next poll", () => {
  const pending = createPending();
  pending.add("перезапусти панель");
  pending.merge([said("перезапусти панель")]);

  const later = pending.merge([answer("готово")]);

  assert.deepEqual(later.map((s) => s.text), ["готово"]);
});

// The same trap the transcript's own de-duplication had, on this side. Break it
// by remembering a set of texts instead of counting and this test fails with
// one message where two were sent.
test("the same words sent twice are two messages, not one", () => {
  const pending = createPending();
  pending.add("да");
  pending.add("да");

  const drawn = pending.merge([said("да")]);

  assert.equal(drawn.filter((s) => s.text === "да").length, 2, "one of the two was eaten by the other");
  assert.equal(drawn.filter((s) => s.pending).length, 1, "exactly one is still waiting");
});

// A failed send means the message is in nobody's hands. Leaving it drawn would
// tell a person their words reached the session.
test("a message whose send failed is taken off the screen", () => {
  const pending = createPending();
  const handle = pending.add("перезапусти панель");

  pending.drop(handle);

  assert.deepEqual(pending.merge([answer("готово")]).map((s) => s.text), ["готово"]);
});

// The handle exists for exactly this: the same words twice, one of which failed.
test("dropping one of two identical messages leaves the other", () => {
  const pending = createPending();
  const first = pending.add("да");
  pending.add("да");

  pending.drop(first);

  const drawn = pending.merge([]);
  assert.equal(drawn.length, 1, `wrong number left: ${JSON.stringify(drawn)}`);
});

// Both panes trim before sending, so the transcript holds the trimmed form.
// Comparing untrimmed would never match, and the drawn copy would sit beside
// the real one forever — worse than the wait this exists to remove.
test("a message is recognised through the trimming the panes do before sending", () => {
  const pending = createPending();
  pending.add("  перезапусти панель\n");

  const drawn = pending.merge([said("перезапусти панель")]);

  assert.equal(drawn.length, 1, "the trimmed message was not recognised as the same one");
});

// An answer that happens to repeat the words is not the message coming back.
test("an answer with the same words does not count as the message arriving", () => {
  const pending = createPending();
  pending.add("готово");

  const drawn = pending.merge([answer("готово")]);

  assert.equal(drawn.length, 2, "an assistant step was mistaken for the person's own message");
});

test("with nothing waiting the steps come back untouched", () => {
  const pending = createPending();
  const steps = [answer("готово"), said("привет")];

  const drawn = pending.merge(steps);

  assert.equal(drawn, steps, "the list was rebuilt for no reason");
});

// A failed digest poll hands the pane an empty list, and losing what a person
// just typed because a background request failed would be the same defect this
// panel already refuses for the text in the box.
test("a poll that returned nothing does not lose what is waiting", () => {
  const pending = createPending();
  pending.add("перезапусти панель");

  assert.equal(pending.merge([]).length, 1);
  assert.equal(pending.merge(undefined).length, 1, "an absent list is not an empty transcript");
});
