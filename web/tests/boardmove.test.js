// What a card dropped into another column actually does: the question asked
// before an agent's card is moved by hand, the offer of a session for a card
// that has none, and the precondition every write carries.

import { test, beforeEach, afterEach } from "node:test";
import assert from "node:assert/strict";

import { installDOM, fireEvent, settle } from "./fake-dom.js";

let dom;
let host;
let move;
let patched;
let started;
let patchAnswer;
let startAnswer;
let snap;
let t;

async function fakePatch(path, field, value, expect) {
  patched.push({ path, field, value, expect });
  if (patchAnswer instanceof Error) throw patchAnswer;
  return patchAnswer;
}

async function fakeStart(card) {
  started.push({ card });
  if (startAnswer instanceof Error) throw startAnswer;
  return startAnswer;
}

function press(className) {
  const button = host.querySelector(`button.${className}`);
  assert.ok(button, `no ${className} button on screen`);
  fireEvent(button, "click");
}

beforeEach(async () => {
  dom = installDOM();
  host = dom.element("nav");
  dom.document.body.appendChild(host);
  patched = [];
  started = [];
  patchAnswer = { committed: true };
  startAnswer = { ok: true, session: "abc12345", steps: [] };
  snap = { canStartWork: true };
  // Russian, and set before the first import of the dictionary: i18n.js reads
  // navigator.language once, when it loads. On the English dictionary an
  // untranslated sentence is indistinguishable from a translated one, which is
  // how the refusal below stayed English without a test noticing.
  Object.defineProperty(globalThis, "navigator", { value: { language: "ru-RU" }, configurable: true });
  const { createBoardMove } = await import("../js/boardmove.js");
  ({ t } = await import("../js/i18n.js"));
  move = createBoardMove(host, { patch: fakePatch, start: fakeStart, snapshot: () => snap });
});

afterEach(() => {
  dom.restore();
});

// The board draws from a snapshot up to a second old and the card's own agent
// writes the same file. Without the stage the board drew, a hand would put
// back the stage the agent has just moved on from.
test("board move: a plain move writes the stage against the one the board drew", async () => {
  const drawn = await move({ path: "/b/c.md", from: "new", to: "blocked", session: "", live: false });
  assert.deepEqual(patched, [{ path: "/b/c.md", field: "stage", value: "blocked", expect: "new" }]);
  assert.equal(drawn, "blocked", "the board draws the move until the snapshot agrees");
});

test("board move: a refused write says the board's own words and the card stays put", async () => {
  patchAnswer = new Error("cannot set stage to review while session is empty");
  const drawn = await move({ path: "/b/c.md", from: "new", to: "review", session: "", live: false });
  assert.equal(drawn, null);
  assert.match(host.querySelector("div.bmove-error").textContent, /session is empty/);
});

// The same translation the open card does, by the same code. Two paths write a
// stage and only one of them spoke the operator's language: the board's own
// words are English and name the rule without the way out, so a drag that
// showed them left the window half translated — a Russian title over an
// English sentence.
test("board move: a rule refusal is said by its code, in the page's language", async () => {
  patchAnswer = Object.assign(new Error("cannot set stage to review while session is empty: the board requires a session at stage review"), {
    code: "session_required",
  });
  const drawn = await move({ path: "/b/c.md", from: "new", to: "review", session: "", live: false });
  assert.equal(drawn, null);
  const shown = host.querySelector("div.bmove-error").textContent;
  assert.equal(shown, t("card_refused_session_required"));
  assert.match(shown, /[а-яё]/i, "the sentence is not in the page's language");
  assert.doesNotMatch(shown, /session is empty/, "the server's English came through");
});

// A code this build has no sentence for — an older page against a newer panel
// — still shows the server's words rather than an empty window.
test("board move: a refusal with an unknown code falls back to the server's words", async () => {
  patchAnswer = Object.assign(new Error("the board refused this for a reason of its own"), { code: "nobody_knows" });
  const drawn = await move({ path: "/b/c.md", from: "new", to: "review", session: "", live: false });
  assert.equal(drawn, null);
  assert.match(host.querySelector("div.bmove-error").textContent, /reason of its own/);
});

test("board move: a card its session is keeping is not moved until the operator says so", async () => {
  const pending = move({ path: "/b/c.md", from: "active", to: "review", session: "abc12345", live: true });
  await settle();
  assert.deepEqual(patched, [], "the write must wait for the answer");
  assert.match(host.querySelector("div.bmove-held-text").textContent, /abc12345/, "the operator must be told which session");
  press("bmove-held-go");
  assert.equal(await pending, "review");
  assert.deepEqual(patched, [{ path: "/b/c.md", field: "stage", value: "review", expect: "active" }]);
});

test("board move: leaving an agent's card alone writes nothing", async () => {
  const pending = move({ path: "/b/c.md", from: "active", to: "done", session: "abc12345", live: true });
  await settle();
  press("bmove-held-cancel");
  assert.equal(await pending, null);
  assert.deepEqual(patched, []);
});

// A card whose session is dead or stopped is nobody's to lose: no question.
test("board move: a card whose session is not alive moves without a question", async () => {
  const drawn = await move({ path: "/b/c.md", from: "active", to: "blocked", session: "abc12345", live: false });
  assert.equal(drawn, "blocked");
  assert.equal(patched.length, 1);
});

test("board move: a card with no session dropped into active is offered one", async () => {
  const pending = move({ path: "/b/c.md", from: "new", to: "active", session: "", live: false });
  await settle();
  assert.deepEqual(patched, [], "the stage is the dispatch's to write, in its own order");
  assert.equal(host.querySelector("button.bmove-start-go").disabled, false);
  press("bmove-start-go");
  assert.equal(await pending, "active");
  assert.deepEqual(started, [{ card: "/b/c.md" }]);
});

// A panel that starts no sessions (a stand given no claude) would only fail
// the start: it says so in the same window and offers nothing to press but the
// way out.
test("board move: a panel that starts no sessions says so and offers no start", async () => {
  snap = {};
  const pending = move({ path: "/b/c.md", from: "new", to: "active", session: "", live: false });
  await settle();
  assert.equal(host.querySelector("button.bmove-start-go").disabled, true);
  assert.notEqual(host.querySelector("div.bmove-start-text").textContent, "");
  press("bmove-start-cancel");
  assert.equal(await pending, null);
  assert.deepEqual(started, []);
});

// The session may be running and the card may already name it. Drawn as a
// move, the board would say the handover finished.
test("board move: a dispatch that got part of the way is not drawn as a move", async () => {
  startAnswer = { ok: false, session: "abc12345", steps: [{ name: "session", note: "started abc12345" }, { name: "card", error: "the card has moved on" }] };
  const pending = move({ path: "/b/c.md", from: "new", to: "active", session: "", live: false });
  await settle();
  press("bmove-start-go");
  assert.equal(await pending, null);
  assert.match(host.querySelector("div.bmove-error").textContent, /the card has moved on/);
});

test("board move: a dispatch refused outright says why", async () => {
  startAnswer = new Error("this panel is not wired to starting a session for a card");
  const pending = move({ path: "/b/c.md", from: "new", to: "active", session: "", live: false });
  await settle();
  press("bmove-start-go");
  assert.equal(await pending, null);
  assert.match(host.querySelector("div.bmove-error").textContent, /not wired/);
});

// A card that already names a session dropped into active is an ordinary move:
// the stage is writable, and starting a second session for it is not offered.
test("board move: a card that already names a session is moved into active, not given another", async () => {
  const drawn = await move({ path: "/b/c.md", from: "blocked", to: "active", session: "abc12345", live: false });
  assert.equal(drawn, "active");
  assert.deepEqual(started, []);
  assert.equal(patched.length, 1);
});
