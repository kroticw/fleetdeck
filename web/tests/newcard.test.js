// Starting a card from the panel: a title and a zone, nothing else (the
// orchestrator's decision, 2026-09-11). These pin what the form sends, that it
// sends nothing it should not, and that a failure keeps what was typed.

import { test, beforeEach, afterEach } from "node:test";
import assert from "node:assert/strict";

import { installDOM, fireEvent, settle } from "./fake-dom.js";

let dom;
let host;
let createNewCard;
let sent;
let answer;

async function fakeCreate(title, zone) {
  sent.push({ title, zone });
  if (answer instanceof Error) throw answer;
  return answer;
}

function open() {
  fireEvent(host.querySelector("button.newcard-open"), "click");
  return host.querySelector("div.newcard");
}

beforeEach(async () => {
  dom = installDOM();
  host = dom.element("nav");
  dom.document.body.appendChild(host);
  sent = [];
  answer = { path: "/b/cards/x.md", committed: true, reason: "" };
  ({ createNewCard } = await import("../js/newcard.js"));
  createNewCard(host, { create: fakeCreate });
});

afterEach(() => {
  dom.restore();
});

test("new card: the form is closed until the button opens it", () => {
  const form = host.querySelector("div.newcard");
  assert.equal(form.hidden, true);
  open();
  assert.equal(form.hidden, false);
  assert.equal(dom.document.activeElement, form.querySelector("input.newcard-title"));
});

test("new card: the form offers exactly the board's four zones", () => {
  const form = open();
  const zones = form.querySelector("select.newcard-zone").children.map((o) => o.value);
  assert.deepEqual(zones, ["urgent", "unplanned", "planned", "niceToHave"]);
});

test("new card: create sends the trimmed title and the chosen zone, then closes", async () => {
  const form = open();
  form.querySelector("input.newcard-title").value = "  Fix the header  ";
  form.querySelector("select.newcard-zone").value = "urgent";
  fireEvent(form.querySelector("button.newcard-create"), "click");
  await settle();

  assert.deepEqual(sent, [{ title: "Fix the header", zone: "urgent" }]);
  assert.equal(form.hidden, true);
  assert.equal(form.querySelector("input.newcard-title").value, "", "the next card starts from an empty title");
  assert.equal(host.querySelector("span.newcard-note").hidden, true, "a committed card has nothing missing to report");
});

test("new card: Enter in the title creates the card", async () => {
  const form = open();
  const input = form.querySelector("input.newcard-title");
  input.value = "By keyboard";
  fireEvent(input, "keydown", { key: "Enter" });
  await settle();
  assert.equal(sent.length, 1);
  assert.equal(sent[0].title, "By keyboard");
});

test("new card: an empty title sends nothing and says why", async () => {
  const form = open();
  form.querySelector("input.newcard-title").value = "   ";
  fireEvent(form.querySelector("button.newcard-create"), "click");
  await settle();
  assert.equal(sent.length, 0);
  assert.equal(form.hidden, false);
  assert.notEqual(form.querySelector("div.newcard-error").textContent, "");
});

test("new card: a refusal keeps the form open with the title and the server's words", async () => {
  answer = new Error('invalid card: unknown zone "someday"');
  const form = open();
  form.querySelector("input.newcard-title").value = "Kept";
  fireEvent(form.querySelector("button.newcard-create"), "click");
  await settle();
  assert.equal(form.hidden, false);
  assert.equal(form.querySelector("input.newcard-title").value, "Kept");
  assert.match(form.querySelector("div.newcard-error").textContent, /unknown zone/);
  assert.equal(form.querySelector("button.newcard-create").disabled, false, "the operator can try again");
});

// The card exists; creating it again would make a second one. So the form
// closes as on success, and the missing commit is said where it stays visible.
test("new card: a card created without its commit closes the form and says the commit is missing", async () => {
  answer = { path: "/b/cards/x.md", committed: false, reason: "the commit timed out" };
  const form = open();
  form.querySelector("input.newcard-title").value = "Uncommitted";
  fireEvent(form.querySelector("button.newcard-create"), "click");
  await settle();
  assert.equal(form.hidden, true);
  const note = host.querySelector("span.newcard-note");
  assert.match(note.textContent, /the commit timed out/);
  assert.equal(note.hidden, false);
  fireEvent(note, "click");
  assert.equal(note.hidden, true, "the note goes when the operator has read it");
});

test("new card: a second press while the first is on its way sends one card", async () => {
  let release;
  answer = new Promise((resolve) => {
    release = resolve;
  });
  const form = open();
  form.querySelector("input.newcard-title").value = "Once";
  const create = form.querySelector("button.newcard-create");
  fireEvent(create, "click");
  fireEvent(create, "click");
  release({ path: "/b/cards/x.md", committed: true, reason: "" });
  await settle();
  assert.equal(sent.length, 1);
});

test("new card: Escape and Cancel close the form without sending", () => {
  let form = open();
  const escape = fireEvent(form.querySelector("input.newcard-title"), "keydown", { key: "Escape" });
  assert.equal(form.hidden, true);
  // Kept to the form: the page's own Escape closes an open card, and a
  // terminal's interrupts a session.
  assert.equal(escape.propagationStopped, true);
  form = open();
  fireEvent(form.querySelector("button.newcard-cancel"), "click");
  assert.equal(form.hidden, true);
  assert.equal(sent.length, 0);
});
