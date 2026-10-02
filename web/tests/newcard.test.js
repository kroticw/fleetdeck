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
let card;

async function fakeCreate(title, zone, repo, description, attachments = []) {
  sent.push({ title, zone, repo, description, ...(attachments.length ? { attachments } : {}) });
  if (answer instanceof Error) throw answer;
  return answer;
}

function open() {
  card.open();
  return host.querySelector("div.newcard");
}

beforeEach(async () => {
  dom = installDOM();
  host = dom.element("nav");
  dom.document.body.appendChild(host);
  sent = [];
  answer = { path: "/b/cards/x.md", committed: true, reason: "" };
  picked = [];
  pickAnswer = "src/picked";
  boardCards = [];
  ({ createNewCard } = await import("../js/newcard.js"));
  card = createNewCard(host, { create: fakeCreate, pick: fakePick, cards: () => boardCards });
});

let picked;
let pickAnswer;
let boardCards;

async function fakePick(prompt) {
  picked.push(prompt);
  if (pickAnswer instanceof Error) throw pickAnswer;
  return pickAnswer;
}

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

// The zone is the card's urgency, not its stage, and the two fields looked the
// same on screen: an unlabelled list of schema identifiers, read as columns
// the operator could not find. The list carries a visible label that says
// urgency, and every option reads as a word from the dictionary. What the
// option sends is still the schema's identifier: a translated string in the
// card's zone field is a card the validator rejects.
test("new card: the zone list is labelled as urgency, and its options read as words, not identifiers", async () => {
  const { t } = await import("../js/i18n.js");
  const form = open();
  const label = form.querySelector("label.newcard-zone-label");
  assert.ok(label, "the zone list has a visible label");
  assert.equal(label.querySelector("span.newcard-zone-caption").textContent, t("new_card_zone"));
  const select = label.querySelector("select.newcard-zone");
  assert.ok(select, "the label wraps the list, so it names it without an id");
  for (const option of select.children) {
    assert.equal(option.textContent, t(`zone_${option.value}`), `option ${option.value} reads from the dictionary`);
    assert.notEqual(option.textContent, option.value, `option ${option.value} does not show its identifier`);
  }
});

test("new card: choosing an option by its label sends the zone's identifier", async () => {
  const { t } = await import("../js/i18n.js");
  const form = open();
  const select = form.querySelector("select.newcard-zone");
  const chosen = select.children.find((o) => o.textContent === t("zone_niceToHave"));
  assert.ok(chosen, "the nice-to-have zone is offered under its label");
  select.value = chosen.value;
  form.querySelector("input.newcard-title").value = "Later";
  fireEvent(form.querySelector("button.newcard-create"), "click");
  await settle();
  assert.deepEqual(sent, [{ title: "Later", zone: "niceToHave", repo: "", description: "" }]);
});

test("new card: create sends the trimmed title and the chosen zone, then closes", async () => {
  const form = open();
  form.querySelector("input.newcard-title").value = "  Fix the header  ";
  form.querySelector("select.newcard-zone").value = "urgent";
  fireEvent(form.querySelector("button.newcard-create"), "click");
  await settle();

  assert.deepEqual(sent, [{ title: "Fix the header", zone: "urgent", repo: "", description: "" }]);
  assert.equal(form.hidden, true);
  assert.equal(form.querySelector("input.newcard-title").value, "", "the next card starts from an empty title");
  assert.equal(host.querySelector("span.newcard-note").hidden, true, "a committed card has nothing missing to report");
});

// A worker starts in the checkout its card names, so the form asks for it.
// It stays filled after a card is made: the next card is usually for the same
// repository.
test("new card: the repo is sent trimmed and kept for the next card", async () => {
  const form = open();
  const repo = form.querySelector("input.newcard-repo");
  assert.ok(repo, "the form has a repo field");
  form.querySelector("input.newcard-title").value = "With a repo";
  repo.value = "  src/fleetdeck ";
  fireEvent(form.querySelector("button.newcard-create"), "click");
  await settle();
  assert.deepEqual(sent, [{ title: "With a repo", zone: "unplanned", repo: "src/fleetdeck", description: "" }]);
  assert.equal(open().querySelector("input.newcard-repo").value, "src/fleetdeck");
});

test("new card: Enter in the repo creates the card too", async () => {
  const form = open();
  form.querySelector("input.newcard-title").value = "By keyboard";
  const repo = form.querySelector("input.newcard-repo");
  repo.value = "src/x";
  fireEvent(repo, "keydown", { key: "Enter" });
  await settle();
  assert.equal(sent.length, 1);
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

test("new card: Escape and the close button close the form without sending", () => {
  let form = open();
  const escape = fireEvent(form.querySelector("input.newcard-title"), "keydown", { key: "Escape" });
  assert.equal(form.hidden, true);
  // Kept to the form: the page's own Escape closes an open card, and a
  // terminal's interrupts a session.
  assert.equal(escape.propagationStopped, true);
  form = open();
  fireEvent(form.querySelector("button.newcard-close"), "click");
  assert.equal(form.hidden, true);
  assert.equal(sent.length, 0);
});

// The fleetdeck window's new card capsule opens the same form; the board hides
// the button there.
test("new card: the form opens without its button", () => {
  const other = dom.element("nav");
  dom.document.body.appendChild(other);
  const card = createNewCard(other, { create: fakeCreate });
  card.open();
  assert.equal(other.querySelector("div.newcard").hidden, false);
});

// In the window the capsule is the button: a second press closes what the
// first opened. v0.10.0's capsule only opened, and with the button hidden
// the form could not be put away but by Cancel.
test("new card: the capsule's second press closes the form, as the button's does", () => {
  const other = dom.element("nav");
  dom.document.body.appendChild(other);
  const card = createNewCard(other, { create: fakeCreate });
  const form = other.querySelector("div.newcard");
  card.toggle();
  assert.equal(form.hidden, false);
  card.toggle();
  assert.equal(form.hidden, true);
  assert.equal(sent.length, 0);
});

test("new card: Escape closes the form from anywhere in it, and goes no further", () => {
  for (const selector of ["select.newcard-zone", "button.newcard-create", "button.newcard-close"]) {
    const form = open();
    const escape = fireEvent(form.querySelector(selector), "keydown", { key: "Escape" });
    assert.equal(form.hidden, true, `Escape on ${selector} left the form open`);
    assert.equal(escape.propagationStopped, true, `Escape on ${selector} reached the page`);
  }
  assert.equal(sent.length, 0);
});

// What the task is goes in beside the title, and reaches the card's task
// section (internal/board). It is optional, and the next card starts without it.
test("new card: the description is sent and cleared with the title", async () => {
  const form = open();
  const description = form.querySelector("textarea.newcard-desc");
  assert.ok(description, "the form has a description field");
  form.querySelector("input.newcard-title").value = "With a description";
  description.value = "  what to do\nand why  ";
  fireEvent(form.querySelector("button.newcard-create"), "click");
  await settle();
  assert.deepEqual(sent, [{ title: "With a description", zone: "unplanned", repo: "", description: "what to do\nand why" }]);
  assert.equal(open().querySelector("textarea.newcard-desc").value, "");
});

// Enter in the description is a new line; the keyboard's way to create from
// there is Cmd+Enter or Ctrl+Enter.
test("new card: Enter in the description is a new line, Cmd+Enter creates", async () => {
  const form = open();
  form.querySelector("input.newcard-title").value = "By keyboard";
  const description = form.querySelector("textarea.newcard-desc");
  const plain = fireEvent(description, "keydown", { key: "Enter" });
  await settle();
  assert.equal(sent.length, 0);
  assert.notEqual(plain.defaultPrevented, true, "the new line is typed");
  fireEvent(description, "keydown", { key: "Enter", metaKey: true });
  await settle();
  assert.equal(sent.length, 1);
});

// The repositories already on the board are the history: every card started
// with a repo left it there, and the board is the panel's, not one browser's.
// Latest first, each once, read when the form opens.
test("new card: the repo field suggests the board's repositories, latest first", () => {
  boardCards = [
    { repo: "src/old", created: "2026-09-01" },
    { repo: "src/fleetdeck", created: "2026-09-30" },
    { repo: "", created: "2026-09-29" },
    { repo: "src/old", created: "2026-09-20" },
    { repo: "src/mid", created: "2026-09-10" },
  ];
  const form = open();
  const repo = form.querySelector("input.newcard-repo");
  const list = form.querySelector("datalist");
  assert.ok(list, "the suggestions are a datalist");
  assert.equal(repo.getAttribute("list"), list.getAttribute("id"));
  assert.deepEqual(list.children.map((o) => o.value), ["src/fleetdeck", "src/old", "src/mid"]);
  card.toggle();
  boardCards = [{ repo: "src/new", created: "2026-09-30" }];
  assert.deepEqual(open().querySelector("datalist").children.map((o) => o.value), ["src/new"]);
});

test("new card: Choose puts the Finder's folder into the repo field", async () => {
  const { t } = await import("../js/i18n.js");
  const form = open();
  const choose = form.querySelector("button.newcard-pick");
  assert.ok(choose, "the form has a choose button");
  fireEvent(choose, "click");
  assert.equal(choose.disabled, true, "one dialog at a time");
  await settle();
  assert.deepEqual(picked, [t("new_card_pick_prompt")]);
  assert.equal(form.querySelector("input.newcard-repo").value, "src/picked");
  assert.equal(choose.disabled, false);
  assert.equal(form.hidden, false, "choosing a folder does not create the card");
  assert.equal(sent.length, 0);
});

test("new card: a cancelled dialog keeps the repo that was there", async () => {
  pickAnswer = "";
  const form = open();
  form.querySelector("input.newcard-repo").value = "src/kept";
  fireEvent(form.querySelector("button.newcard-pick"), "click");
  await settle();
  assert.equal(form.querySelector("input.newcard-repo").value, "src/kept");
  assert.equal(form.querySelector("div.newcard-error").textContent, "");
});

// A folder the agent has never been started in is not trusted by Claude Code,
// and a worker started there fails. The panel cannot tell, so it says what to
// do once, where the folder was chosen.
test("new card: a chosen folder comes with the hint to start an agent there first", async () => {
  const { t } = await import("../js/i18n.js");
  const form = open();
  const hint = form.querySelector(".newcard-hint");
  assert.equal(hint.hidden, true, "no hint before a folder is chosen");
  fireEvent(form.querySelector("button.newcard-pick"), "click");
  await settle();
  assert.equal(hint.hidden, false);
  assert.equal(hint.textContent, t("new_card_pick_trust"));
  card.toggle();
  assert.equal(open().querySelector(".newcard-hint").hidden, true, "the next opening starts without it");
});

// The window's close is a cross in its corner, not a Cancel beside Create.
test("new card: the form closes by a cross with a name, and has no Cancel", async () => {
  const { t } = await import("../js/i18n.js");
  const form = open();
  const close = form.querySelector("button.newcard-close");
  assert.ok(close);
  assert.equal(close.getAttribute("aria-label"), t("new_card_cancel"));
  assert.equal(form.querySelector("button.newcard-cancel"), null);
  // The panel's own buttons (the .btn block): the cross round, Create the primary action.
  assert.equal(close.className, "newcard-close btn btn-icon");
  assert.equal(form.querySelector("button.newcard-create").className, "newcard-create btn btn-primary");
  for (const name of ["pick", "attach"]) assert.equal(form.querySelector(`button.newcard-${name}`).className, `newcard-${name} btn`);
});

function fakeFile(name, text) {
  return { name, arrayBuffer: async () => new TextEncoder().encode(text).buffer };
}

// Screenshots are pasted, documents are dropped or chosen; each shows as a
// chip until the card is made, and goes with it as base64.
test("new card: a pasted picture is attached and sent with the card", async () => {
  const form = open();
  form.querySelector("input.newcard-title").value = "With a screenshot";
  const paste = fireEvent(form.querySelector("textarea.newcard-desc"), "paste", { clipboardData: { files: [fakeFile("image.png", "png")] } });
  assert.equal(paste.defaultPrevented, true, "the picture is not pasted as text");
  assert.deepEqual([...form.querySelectorAll(".newcard-file-name")].map((n) => n.textContent), ["image.png"]);
  fireEvent(form.querySelector("button.newcard-create"), "click");
  await settle();
  assert.deepEqual(sent[0].attachments, [{ name: "image.png", data: btoa("png") }]);
  assert.equal(open().querySelectorAll(".newcard-file").length, 0, "the next card starts without them");
});

test("new card: pasting text attaches nothing and pastes the text", () => {
  const form = open();
  const paste = fireEvent(form.querySelector("textarea.newcard-desc"), "paste", { clipboardData: { files: [] } });
  assert.notEqual(paste.defaultPrevented, true);
  assert.equal(form.querySelectorAll(".newcard-file").length, 0);
});

test("new card: dropped and chosen files are attached, and a chip's cross takes one off", async () => {
  const form = open();
  const over = fireEvent(form, "dragover", { dataTransfer: { types: ["Files"] } });
  assert.equal(over.defaultPrevented, true, "the form takes the drop");
  fireEvent(form, "drop", { dataTransfer: { files: [fakeFile("spec.pdf", "pdf")] } });
  const input = form.querySelector("input.newcard-files-input");
  input.files = [fakeFile("notes.txt", "txt")];
  fireEvent(input, "change");
  assert.deepEqual([...form.querySelectorAll(".newcard-file-name")].map((n) => n.textContent), ["spec.pdf", "notes.txt"]);
  fireEvent(form.querySelectorAll(".newcard-file-remove")[0], "click");
  form.querySelector("input.newcard-title").value = "With a note";
  fireEvent(form.querySelector("button.newcard-create"), "click");
  await settle();
  assert.deepEqual(sent[0].attachments, [{ name: "notes.txt", data: btoa("txt") }]);
});

// Off macOS, or for a folder outside home, the panel's words are said in the
// form rather than the button failing silently.
test("new card: a refused dialog says why in the form", async () => {
  pickAnswer = new Error("this panel is not wired to a folder dialog: it is macOS only");
  const form = open();
  fireEvent(form.querySelector("button.newcard-pick"), "click");
  await settle();
  assert.match(form.querySelector("div.newcard-error").textContent, /macOS only/);
  assert.equal(form.querySelector("button.newcard-pick").disabled, false);
});
