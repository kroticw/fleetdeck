// The panel, driven from the golden snapshot and a stubbed fetch.
//
// Everything here is about one of three things the panel is easy to get wrong:
// showing a value the card file does not hold, wiping its own notice on the next
// snapshot, and rebuilding itself once a second under an open control.

import { test, beforeEach, afterEach } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

import { installDOM, fireEvent, fireDocumentEvent, settle } from "./fake-dom.js";
import { t } from "../js/i18n.js";

const FIXTURE = readFileSync(new URL("./testdata/snapshot.json", import.meta.url), "utf8");
const FLEET_UI = "/board/fleet-ui.md";
const CARD_KEEPING = "/board/card-keeping.md";
const BROKEN = "/board/broken.md";

function snapshot() {
  // Parsed afresh for each test so a test that edits a card cannot reach the
  // next one.
  return JSON.parse(FIXTURE);
}

function fakeStore(initial) {
  let listener = null;
  return {
    subscribe(fn) {
      listener = fn;
      fn(initial, true);
      return () => {
        listener = null;
      };
    },
    push(snap) {
      listener?.(snap, true);
    },
    get live() {
      return listener !== null;
    },
  };
}

let dom;
let realFetch;
let renderCard;

beforeEach(async () => {
  dom = installDOM();
  realFetch = globalThis.fetch;
  // Imported after the document exists. The module reads it only when called,
  // but importing here keeps that independent of module caching order.
  ({ renderCard } = await import("../js/card.js"));
});

afterEach(() => {
  globalThis.fetch = realFetch;
  dom.restore();
});

function open(snap, path = FLEET_UI, options = {}) {
  const root = dom.element("div");
  const store = fakeStore(snap);
  const closed = [];
  const dispose = renderCard(root, path, () => closed.push(true), {
    subscribe: store.subscribe,
    ...options,
  });
  return { root, store, closed, dispose };
}

function stubFetch(response) {
  const calls = [];
  globalThis.fetch = async (url, init) => {
    calls.push({ url, init, body: JSON.parse(init.body) });
    return response;
  };
  return calls;
}

function answer(status, body) {
  return {
    status,
    ok: status >= 200 && status < 300,
    statusText: "",
    async json() {
      if (body === undefined) throw new SyntaxError("no body");
      return body;
    },
  };
}

test("a null snapshot is a state of its own, not a crash", () => {
  const { root } = open(null);
  assert.equal(root.querySelector(".card-empty").textContent, t("card_waiting"));
  assert.equal(root.querySelectorAll("select").length, 0);
  // Still closable while there is nothing to show.
  assert.ok(root.querySelector(".card-close"));
});

test("a card that is no longer on the board says so", () => {
  const { root } = open(snapshot(), "/board/deleted.md");
  assert.equal(root.querySelector(".card-empty").textContent, t("card_gone"));
});

test("the title is shown as text, never as markup", () => {
  const { root } = open(snapshot());
  assert.equal(root.querySelector("h3").textContent, 'Fleet UI <panel> "v2"');
});

test("the body is rendered escaped", () => {
  const { root } = open(snapshot());
  const html = root.querySelector(".card-body").innerHTML;
  assert.ok(html.includes("Depends on"), html);
  assert.ok(!html.includes("<img"), html);
  assert.ok(!html.includes("<script"), html);
});

test("both fields show what the card holds", () => {
  const { root } = open(snapshot());
  assert.equal(root.querySelector("select[data-field=stage]").value, "active");
  assert.equal(root.querySelector("select[data-field=progress]").value, "40");
});

test("a card that does not parse offers no controls", () => {
  const { root } = open(snapshot(), BROKEN);
  assert.equal(root.querySelectorAll("select").length, 0);
  assert.ok(root.querySelector(".card-parse-error").textContent.includes("no frontmatter block"));
});

test("a card pointing at a dead session says so and offers nothing to click", () => {
  const { root } = open(snapshot(), CARD_KEEPING);
  const dead = root.querySelector(".card-session-dead");
  assert.ok(dead);
  assert.equal(dead.textContent, t("card_session_dead"));
  // Writing `session` is not something this panel does, so there is no control
  // here to offer it with.
  assert.equal(root.querySelector(".card-meta").querySelectorAll("button").length, 0);
});

test("a live session is shown without a dead marker", () => {
  const { root } = open(snapshot());
  assert.equal(root.querySelector(".card-session").textContent, "a1b2c3");
  assert.equal(root.querySelector(".card-session-dead"), null);
});

test("a backlink is listed and moves the panel to that card", () => {
  const { root } = open(snapshot());
  const backlink = root.querySelector(".card-backlink");
  assert.ok(backlink, "the card linking here was not listed");
  assert.equal(backlink.dataset.link, "card-keeping");

  fireEvent(backlink, "click");

  assert.equal(root.querySelector("h3").textContent, "Card keeping");
  assert.equal(root.querySelector("select[data-field=stage]").value, "review");
});

test("204 is a silent success and the control keeps the new value", async () => {
  const { root } = open(snapshot());
  const calls = stubFetch(answer(204));

  const stage = root.querySelector("select[data-field=stage]");
  stage.value = "review";
  fireEvent(stage, "change");
  await settle();

  assert.deepEqual(calls[0].body, { path: FLEET_UI, field: "stage", value: "review" });
  assert.equal(root.querySelector(".card-error"), null);
  assert.equal(root.querySelector(".card-notice"), null);
  assert.equal(root.querySelector("select[data-field=stage]").value, "review");
});

test("written but not committed keeps the value, names the reason, offers no retry", async () => {
  const { root } = open(snapshot());
  stubFetch(answer(200, { written: true, committed: false, reason: "gpg-agent asked for a PIN" }));

  const stage = root.querySelector("select[data-field=stage]");
  stage.value = "review";
  fireEvent(stage, "change");
  await settle();

  const notice = root.querySelector(".card-notice");
  assert.ok(notice, "nothing told the operator the commit did not happen");
  assert.ok(notice.textContent.includes(t("card_not_committed")), notice.textContent);
  assert.ok(notice.textContent.includes("gpg-agent asked for a PIN"), notice.textContent);
  // The field is in the file. Repeating the edit would apply it twice, which on
  // progress means two steps, so there must be nothing here inviting a retry.
  assert.equal(notice.querySelectorAll("button").length, 0);
  assert.equal(root.querySelector(".card-error"), null);
  assert.equal(root.querySelector("select[data-field=stage]").value, "review");
});

test("the not-committed notice survives the next snapshot", async () => {
  const { root, store } = open(snapshot());
  stubFetch(answer(200, { written: true, committed: false, reason: "gpg-agent asked for a PIN" }));

  const stage = root.querySelector("select[data-field=stage]");
  stage.value = "review";
  fireEvent(stage, "change");
  await settle();
  assert.ok(root.querySelector(".card-notice"));

  // A second later the panel is redrawn from a snapshot that now carries the
  // written value and some unrelated change. A notice written straight into the
  // DOM would be gone by now.
  const next = snapshot();
  next.cards[0].stage = "review";
  next.cards[0].body += "\n- the agent appended a line\n";
  store.push(next);

  const notice = root.querySelector(".card-notice");
  assert.ok(notice, "the notice was wiped by a redraw");
  assert.ok(notice.textContent.includes("gpg-agent asked for a PIN"), notice.textContent);
  assert.ok(root.querySelector(".card-body").innerHTML.includes("appended a line"));
});

test("a refusal is shown and the control goes back to what the file holds", async () => {
  const { root } = open(snapshot());
  stubFetch(answer(422, { error: "card has no stage field" }));

  const stage = root.querySelector("select[data-field=stage]");
  stage.value = "done";
  fireEvent(stage, "change");
  await settle();

  const error = root.querySelector(".card-error");
  assert.ok(error, "a refused write was silent");
  assert.ok(error.textContent.includes("card has no stage field"), error.textContent);
  assert.equal(root.querySelector(".card-notice"), null);
  // Nothing was written, so a control still showing "done" would be a lie about
  // the card file.
  assert.equal(root.querySelector("select[data-field=stage]").value, "active");
});

test("a repeated edit clears the previous notice", async () => {
  const { root } = open(snapshot());
  stubFetch(answer(200, { written: true, committed: false, reason: "gpg-agent asked for a PIN" }));

  let stage = root.querySelector("select[data-field=stage]");
  stage.value = "review";
  fireEvent(stage, "change");
  await settle();
  assert.ok(root.querySelector(".card-notice"));

  stubFetch(answer(204));
  stage = root.querySelector("select[data-field=stage]");
  stage.value = "blocked";
  fireEvent(stage, "change");
  await settle();

  assert.equal(root.querySelector(".card-notice"), null);
});

test("an unchanged snapshot does not rebuild the panel", () => {
  const { root, store } = open(snapshot());
  const before = root.querySelector("select[data-field=stage]");

  store.push(snapshot());

  // Same node, not an equal one: a rebuilt <select> is a <select> that collapsed
  // under the operator's cursor, once a second, forever.
  assert.equal(root.querySelector("select[data-field=stage]"), before);
});

test("a changed card does rebuild the panel", () => {
  const { root, store } = open(snapshot());
  const next = snapshot();
  next.cards[0].progress = 60;

  store.push(next);

  assert.equal(root.querySelector("select[data-field=progress]").value, "60");
});

test("Escape, the close button and a click outside all close the panel", () => {
  const first = open(snapshot());
  fireDocumentEvent(dom.document, "keydown", { key: "Escape" });
  assert.equal(first.closed.length, 1);

  const second = open(snapshot());
  fireEvent(second.root.querySelector(".card-close"), "click");
  assert.equal(second.closed.length, 1);

  const third = open(snapshot());
  fireDocumentEvent(dom.document, "mousedown", { target: dom.element("div") });
  assert.equal(third.closed.length, 1);
});

test("a click inside the panel does not close it", () => {
  const { root, closed } = open(snapshot());
  fireDocumentEvent(dom.document, "mousedown", { target: root.querySelector("h3") });
  assert.equal(closed.length, 0);
});

test("disposing stops the panel listening to anything", () => {
  const { store, closed, dispose } = open(snapshot());
  dispose();

  fireDocumentEvent(dom.document, "keydown", { key: "Escape" });
  fireDocumentEvent(dom.document, "mousedown", { target: dom.element("div") });

  assert.equal(closed.length, 0);
  assert.equal(store.live, false);
});

test("a session id becomes a control only when someone can act on it", () => {
  const opened = [];
  const { root } = open(snapshot(), FLEET_UI, { onOpenSession: (id) => opened.push(id) });
  const session = root.querySelector(".card-session");
  assert.equal(session.tagName, "A");
  fireEvent(session, "click");
  assert.deepEqual(opened, ["a1b2c3"]);

  const plain = open(snapshot());
  assert.equal(plain.root.querySelector(".card-session").tagName, "SPAN");
});

test("a value the board does not allow is shown as it is, not replaced", () => {
  const next = snapshot();
  next.cards[0].stage = "whatever-the-agent-wrote";

  const { root } = open(next);

  const stage = root.querySelector("select[data-field=stage]");
  assert.equal(stage.value, "whatever-the-agent-wrote");
  assert.equal(stage.children[0].disabled, true);
});
