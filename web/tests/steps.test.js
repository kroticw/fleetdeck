// Who said it: the three kinds of thing a thread holds, told apart.
//
// A thread mixes what the operator typed, what the session answered, and what
// another agent sent. The first and the third had the same role, the same class
// and the same shape, so a person's own words read as a message from an agent
// whose signature had gone missing — and the only thing separating them was a
// tint of background and an alignment that does nothing to any line long enough
// to wrap.
//
// These are the tests for the distinction itself, and each of them names what
// would have to break for it to fail.

import { test, beforeEach, afterEach } from "node:test";
import assert from "node:assert/strict";

import { installDOM } from "./fake-dom.js";
import { fillStep } from "../js/steps.js";
import { t } from "../js/i18n.js";

const classFor = (role) => `x-msg x-${role}`;

let dom;

beforeEach(() => {
  dom = installDOM();
});

afterEach(() => {
  dom.restore();
});

const draw = (step) => fillStep(dom.element("div"), step, classFor);

test("what the operator typed is signed, in words", async () => {
  const row = draw({ role: "user", text: "перезапусти панель" });

  const from = row.querySelector(".step-typed");
  assert.ok(from, "the operator's own message carries no attribution at all");
  assert.equal(from.textContent, t("typed_here"));
});

// The bar down the side is the other half: a signature finds the message once
// you are looking at it, an edge finds it while scrolling past answers that run
// twenty times its height.
test("and the row itself is marked, so the pane can find it without knowing why", async () => {
  const row = draw({ role: "user", text: "перезапусти панель" });

  assert.equal(row.dataset.typed, "1");
});

test("an answer is signed by nothing and marked by nothing", async () => {
  const row = draw({ role: "assistant", text: "перезапустил" });

  assert.equal(row.querySelector(".step-from"), null, "an answer got an attribution line");
  assert.equal(row.dataset.typed, undefined, "an answer got the typed mark");
});

// A message from another agent arrives with the same role as a typed one. It
// already carries a sender, and signing it "operator" as well would put another
// session's words in a person's mouth.
test("a message from another agent keeps its own sender and is not signed as the operator", async () => {
  const row = draw({
    role: "user",
    text: '<agent-message id="m-1" from="fleetdeck канбан [befac93e]" at="2026-09-10T17:00:00+05:00">PR готов</agent-message>',
  });

  assert.equal(row.querySelector(".step-typed"), null, "an agent's message was signed as the operator's");
  assert.equal(row.querySelector(".step-from").textContent, "fleetdeck канбан [befac93e] · 2026-09-10T17:00:00+05:00");
  assert.equal(row.dataset.typed, undefined);
});

// The same, for the envelope a session on the other end of a socket uses. It
// was not recognised before, which mattered little while every user step looked
// alike; it matters now, because an unrecognised envelope is signed as a person.
test("a message from a session reached over a socket is signed by that session", async () => {
  const row = draw({
    role: "user",
    text:
      '<cross-session-message from="uds:/tmp/cc-socks/85959.sock" from-name="fleetdeck лимиты аккаунта" from-mode="prompting">\n' +
      "CI на master красный\n</cross-session-message>",
  });

  assert.equal(row.querySelector(".step-typed"), null, "another session's words were signed as the operator's");
  assert.equal(row.querySelector(".step-from").textContent, "fleetdeck лимиты аккаунта");
  assert.ok(row.querySelector(".step-body").innerHTML.includes("CI на master красный"), "and the message survives");
});

// Prose that merely names the tag is text. The fleet talks about these
// envelopes by name, and swallowing a sentence is worse than showing a tag.
test("a sentence that names the tag is still the operator speaking", async () => {
  const row = draw({ role: "user", text: "почему в ленте видно <cross-session-message> целиком?" });

  assert.equal(row.querySelector(".step-typed")?.textContent, t("typed_here"));
  assert.ok(row.querySelector(".step-body").innerHTML.includes("&lt;cross-session-message&gt;"));
});

// A row is refilled in place when the step at that position changes — that is
// what keeps a selection alive across a poll. A mark left behind on a reused
// row would attribute an answer to the operator.
test("a row reused for an answer loses the mark and the signature", async () => {
  const row = draw({ role: "user", text: "перезапусти панель" });
  assert.equal(row.dataset.typed, "1", "precondition: the row was marked");

  fillStep(row, { role: "assistant", text: "перезапустил" }, classFor);

  assert.equal(row.dataset.typed, undefined, "the mark survived into an answer");
  assert.equal(row.querySelector(".step-typed"), null, "so did the signature");
});
