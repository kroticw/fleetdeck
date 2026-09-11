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

// A Set, like the real store's, not a single slot: a store that could only ever
// hold one listener would quietly absorb a panel that was never disposed, which
// is one of the two leaks these tests exist to see.
function fakeStore(initial) {
  const listeners = new Set();
  return {
    subscribe(fn) {
      listeners.add(fn);
      fn(initial, true);
      return () => listeners.delete(fn);
    },
    push(snap) {
      for (const fn of [...listeners]) fn(snap, true);
    },
    get live() {
      return listeners.size > 0;
    },
    get count() {
      return listeners.size;
    },
  };
}

let dom;
let realFetch;
let renderCard;
let createCardPanel;

beforeEach(async () => {
  dom = installDOM();
  realFetch = globalThis.fetch;
  // Imported after the document exists. The module reads it only when called,
  // but importing here keeps that independent of module caching order.
  ({ renderCard, createCardPanel } = await import("../js/card.js"));
});

afterEach(() => {
  globalThis.fetch = realFetch;
  dom.restore();
});

function open(snap, path = FLEET_UI, options = {}) {
  const root = dom.element("div");
  // In the page before anything is drawn into it, as the panel's root is in a
  // browser. It matters for more than realism: a node outside the document has
  // no layout, so anything the panel measures while building is measured
  // against zero.
  dom.document.body.appendChild(root);
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
    return typeof response === "function" ? response(JSON.parse(init.body)) : response;
  };
  return calls;
}

// A response that does not settle until the test says so. Every ordering test
// below is about what happens while a request is still in flight.
function deferred() {
  let release;
  const promise = new Promise((resolve) => {
    release = resolve;
  });
  return { promise, release: (value) => release(value) };
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
  assert.equal(dead.textContent, t("session_dead"));
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

// Escape inside a live terminal belongs to the session — in a Claude Code
// session it interrupts the turn — except while a card is open. Then it closes
// the card and nothing else.
//
// Measured before this was written, by pressing: a click into a terminal
// already closes an open card (the click-outside rule), so the one way to have
// a card open and the focus in a terminal is a [[link]] clicked in the terminal
// itself, which opens the card and leaves the focus where it was. The next
// Escape — the natural way back from a link just followed — interrupted the
// session's turn and left the card open: one keystroke, the wrong one of two
// things, and the card saying nothing had happened.
//
// So while a card is open, Escape closes it and is taken before the terminal
// ever sees it. The session can still be interrupted from the keyboard: with
// the card closed, the next Escape reaches it.
function terminalTarget() {
  const host = dom.element("div");
  host.dataset.terminal = "";
  const typedInto = dom.element("textarea");
  host.appendChild(typedInto);
  dom.document.body.appendChild(host);
  return typedInto;
}

test("while a card is open, Escape pressed inside a live terminal closes the card and never reaches the terminal", () => {
  const { closed } = open(snapshot());

  const event = fireDocumentEvent(dom.document, "keydown", { key: "Escape", target: terminalTarget() });

  assert.equal(closed.length, 1, "the card stayed open under an Escape meant to close it");
  assert.equal(event.propagationStopped, true, "the terminal still receives the Escape and sends it into the session");
  assert.equal(event.defaultPrevented, true);
});

test("an Escape from anywhere else closes the card and is left to its own target too", () => {
  const { closed } = open(snapshot());

  const event = fireDocumentEvent(dom.document, "keydown", { key: "Escape", target: dom.element("input") });

  assert.equal(closed.length, 1);
  assert.notEqual(event.propagationStopped, true, "an Escape an input handles itself was taken from it");
});

// The listener has to run before the terminal's own: xterm turns a keydown on
// its textarea into bytes for the session in that element's handler, so a
// listener on the document's way back up would close the card after the Escape
// had already gone into the session. Capture is what puts it first — and the
// same flag has to be given to take it off again, or a closed card keeps
// listening.
test("the panel hears Escape before anything in the page does, and stops hearing it when it goes", () => {
  const calls = [];
  const add = dom.document.addEventListener.bind(dom.document);
  const remove = dom.document.removeEventListener.bind(dom.document);
  dom.document.addEventListener = (type, fn, options) => {
    calls.push(["add", type, options]);
    add(type, fn, options);
  };
  dom.document.removeEventListener = (type, fn, options) => {
    calls.push(["remove", type, options]);
    remove(type, fn, options);
  };
  const { dispose } = open(snapshot());
  dispose();

  const capture = (o) => o === true || o?.capture === true;
  const keydown = calls.filter(([, type]) => type === "keydown");
  assert.deepEqual(keydown.map(([op, , o]) => [op, capture(o)]), [["add", true], ["remove", true]]);
});

test("with no card open, an Escape in a live terminal is the terminal's", () => {
  const { dispose } = open(snapshot());
  dispose();

  const event = fireDocumentEvent(dom.document, "keydown", { key: "Escape", target: terminalTarget() });

  assert.notEqual(event.propagationStopped, true, "a closed card still took the Escape from the terminal");
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
  // A button, not an href-less <a>: the latter is not focusable and not in the
  // tab order, so it would work only for a mouse.
  assert.equal(session.tagName, "BUTTON");
  fireEvent(session, "click");
  assert.deepEqual(opened, ["a1b2c3"]);

  const plain = open(snapshot());
  assert.equal(plain.root.querySelector(".card-session").tagName, "SPAN");
});

test("a backlink is a control the keyboard can reach", () => {
  const { root } = open(snapshot());
  assert.equal(root.querySelector(".card-backlink").tagName, "BUTTON");
});

test("an answer about one card never lands on another", async () => {
  const { root } = open(snapshot());
  const slow = deferred();
  globalThis.fetch = async () => slow.promise;

  const stage = root.querySelector("select[data-field=stage]");
  stage.value = "review";
  fireEvent(stage, "change");

  // The operator follows a backlink while the write is still in flight.
  fireEvent(root.querySelector(".card-backlink"), "click");
  assert.equal(root.querySelector("h3").textContent, "Card keeping");

  slow.release(answer(200, { written: true, committed: false, reason: "gpg-agent asked for a PIN" }));
  await settle();

  // A message about a write to fleet-ui drawn over card-keeping would be a
  // message about one file shown on another.
  assert.equal(
    root.querySelector(".card-notice"),
    null,
    `${root.querySelector("h3").textContent} is showing a notice about a write to another card`,
  );
  assert.equal(root.querySelector(".card-error"), null);
});

test("a refusal about one card never reverts another card's control", async () => {
  const { root } = open(snapshot());
  const slow = deferred();
  globalThis.fetch = async () => slow.promise;

  const stage = root.querySelector("select[data-field=stage]");
  stage.value = "review";
  fireEvent(stage, "change");
  fireEvent(root.querySelector(".card-backlink"), "click");

  slow.release(answer(422, { error: "card has no stage field" }));
  await settle();

  assert.equal(root.querySelector(".card-error"), null);
  // card-keeping's own stage, untouched by the answer to fleet-ui's write.
  assert.equal(root.querySelector("select[data-field=stage]").value, "review");
});

test("two edits in a row keep their own answers and their own controls", async () => {
  const { root } = open(snapshot());
  const slowStage = deferred();
  globalThis.fetch = async (_url, init) =>
    JSON.parse(init.body).field === "stage" ? slowStage.promise : answer(204);

  const stage = root.querySelector("select[data-field=stage]");
  stage.value = "review";
  fireEvent(stage, "change");

  // A second edit, of the other field, answered before the first.
  const progress = root.querySelector("select[data-field=progress]");
  progress.value = "60";
  fireEvent(progress, "change");
  await settle();

  slowStage.release(answer(200, { written: true, committed: false, reason: "gpg-agent asked for a PIN" }));
  await settle();

  // The slow answer must not have reverted the fast edit's control...
  assert.equal(root.querySelector("select[data-field=progress]").value, "60");
  // ...and must have arrived as its own message, naming its own field.
  const notice = root.querySelector(".card-notice");
  assert.ok(notice, "the slower write's answer was swallowed");
  assert.ok(notice.textContent.startsWith("stage:"), notice.textContent);
  assert.equal(root.querySelector("select[data-field=stage]").value, "review");
});

test("both fields can carry an answer at once, each naming itself", async () => {
  const { root } = open(snapshot());
  globalThis.fetch = async (_url, init) =>
    JSON.parse(init.body).field === "stage"
      ? answer(422, { error: "card has no stage field" })
      : answer(200, { written: true, committed: false, reason: "gpg-agent asked for a PIN" });

  const stage = root.querySelector("select[data-field=stage]");
  stage.value = "review";
  fireEvent(stage, "change");
  await settle();

  const progress = root.querySelector("select[data-field=progress]");
  progress.value = "60";
  fireEvent(progress, "change");
  await settle();

  const error = root.querySelector(".card-error");
  const notice = root.querySelector(".card-notice");
  assert.ok(error && error.textContent.startsWith("stage:"), error?.textContent);
  assert.ok(notice && notice.textContent.startsWith("progress:"), notice?.textContent);
  // The refused one reverted, the written one kept its value.
  assert.equal(root.querySelector("select[data-field=stage]").value, "active");
  assert.equal(root.querySelector("select[data-field=progress]").value, "60");
});

test("a superseded edit of the same field does not speak for the newer one", async () => {
  const { root } = open(snapshot());
  const first = deferred();
  let call = 0;
  globalThis.fetch = async () => {
    call += 1;
    return call === 1 ? first.promise : answer(204);
  };

  let stage = root.querySelector("select[data-field=stage]");
  stage.value = "review";
  fireEvent(stage, "change");

  stage = root.querySelector("select[data-field=stage]");
  stage.value = "blocked";
  fireEvent(stage, "change");
  await settle();

  first.release(answer(422, { error: "card has no stage field" }));
  await settle();

  assert.equal(root.querySelector(".card-error"), null, "a replaced edit reported its own failure");
  assert.equal(root.querySelector("select[data-field=stage]").value, "blocked");
});

test("a value the board does not allow is shown as it is, not replaced", () => {
  const next = snapshot();
  next.cards[0].stage = "whatever-the-agent-wrote";

  const { root } = open(next);

  const stage = root.querySelector("select[data-field=stage]");
  assert.equal(stage.value, "whatever-the-agent-wrote");
  assert.equal(stage.children[0].disabled, true);
});

// createCardPanel — what makes the panel a panel, and what main.js is reduced to.
//
// The click delegation is deliberately not tested here: board.js owns it and
// calls this module's `open`, which is the whole contract between the two.

test("opening draws the card and unhides the panel", () => {
  const panel = dom.element("div");
  panel.hidden = true;
  const store = fakeStore(snapshot());

  createCardPanel(panel, { subscribe: store.subscribe }).open(FLEET_UI);

  assert.equal(panel.hidden, false);
  assert.equal(panel.querySelector("h3").textContent, 'Fleet UI <panel> "v2"');
});

test("opening a second card disposes the first", () => {
  const panel = dom.element("div");
  const store = fakeStore(snapshot());
  const cardPanel = createCardPanel(panel, { subscribe: store.subscribe });

  cardPanel.open(FLEET_UI);
  cardPanel.open(CARD_KEEPING);

  assert.equal(panel.querySelector("h3").textContent, "Card keeping");

  // Exactly one panel is alive. An undisposed first panel leaves its own
  // subscription and its own Escape and click-outside handlers behind, and every
  // card the operator opens adds another set — invisible on screen, because the
  // second panel draws over the first, and unbounded.
  assert.equal(store.count, 1, "a panel was left subscribed to the store");
  assert.equal(
    dom.document.listeners.get("keydown").size,
    1,
    "a panel left its Escape handler on the document",
  );
  assert.equal(dom.document.listeners.get("mousedown").size, 1);

  fireDocumentEvent(dom.document, "keydown", { key: "Escape" });
  assert.equal(panel.hidden, true);
  assert.equal(store.live, false);
  assert.equal(dom.document.listeners.get("keydown").size, 0);
});

test("closing empties the panel and hides it again", () => {
  const panel = dom.element("div");
  const store = fakeStore(snapshot());
  const cardPanel = createCardPanel(panel, { subscribe: store.subscribe });

  cardPanel.open(FLEET_UI);
  fireEvent(panel.querySelector(".card-close"), "click");

  assert.equal(panel.hidden, true);
  assert.equal(panel.children.length, 0);
  assert.equal(store.live, false);
});

test("closing a panel that is not open is not an error", () => {
  const panel = dom.element("div");
  createCardPanel(panel).close();
  assert.equal(panel.hidden, true);
});

test("a page without the panel element says so instead of doing nothing quietly", () => {
  const errors = [];
  const realError = console.error;
  console.error = (...args) => errors.push(args.join(" "));
  try {
    const cardPanel = createCardPanel(null);
    cardPanel.open(FLEET_UI);
    cardPanel.close();
  } finally {
    console.error = realError;
  }
  assert.equal(errors.length, 1);
  assert.ok(errors[0].includes("#card-panel"), errors[0]);
});

test("the scroll indicator measures the body after it is in the page", () => {
  // A live browser found this and no unit test could: the mark was taken while
  // the body was still being assembled, so every table and code block in a card
  // "fitted" and none was ever marked. The fade turned up only after a resize —
  // after the reader had already found the scrolling by hand.
  //
  // The tree cannot show it, because the tree is identical either way. What
  // separates the two is when the measurement happened, so that is what is
  // asserted: no search for the scrolling boxes may run against a detached node.
  const { root } = open(snapshot());
  const early = dom.document.searches.filter((s) => s.selector.includes("md-table") && !s.connected);
  assert.deepEqual(early, [], "measured before the body was in the page");
  assert.ok(
    dom.document.searches.some((s) => s.selector.includes("md-table") && s.connected),
    "and it is measured at all",
  );
  assert.ok(root);
});

