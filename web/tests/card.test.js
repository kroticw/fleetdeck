// The panel, driven from the golden snapshot and a stubbed fetch.
//
// Everything here is about one of three things the panel is easy to get wrong:
// showing a value the card file does not hold, wiping its own notice on the next
// snapshot, and rebuilding itself once a second under an open control.

import { test, beforeEach, afterEach } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

import { installDOM, fireEvent, fireDocumentEvent, settle } from "./fake-dom.js";
import { t, langCode } from "../js/i18n.js";

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
    // No documentation unless a test brings its own: the panel lists a card's
    // documents from the server, and a test about a field write has no server.
    listDocs: async () => [],
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

// A card started with a screenshot shows it: the panel serves the board's
// attachments, and the card links them relative to itself.
test("a card's attached picture is shown in its body", () => {
  const snap = snapshot();
  snap.cards[0].body += "\n## Вложения\n\n- ![shot.png](../attachments/T-001/shot.png)\n";
  const { root } = open(snap);
  const html = root.querySelector(".card-body").innerHTML;
  assert.ok(html.includes('<img class="md-img" src="/api/attachments?path=T-001%2Fshot.png"'), html);
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

// The repo is where the card's worker is started (T-061), so it is shown and
// can be changed from the card, as stage and progress are.
test("the repo is shown and a change is written into the card", async () => {
  const { root } = open(snapshot());
  const calls = stubFetch(answer(204));
  const repo = root.querySelector("input[data-field=repo]");
  assert.ok(repo, "the card has a repo field");
  assert.equal(repo.value, "fleetdeck");

  repo.value = " src/fleetdeck ";
  fireEvent(repo, "change");
  await settle();

  assert.deepEqual(calls[0].body, { path: FLEET_UI, field: "repo", value: "src/fleetdeck", lang: langCode });
  assert.equal(root.querySelector("input[data-field=repo]").value, "src/fleetdeck");
});

test("an unchanged repo writes nothing", async () => {
  const { root } = open(snapshot());
  const calls = stubFetch(answer(204));
  const repo = root.querySelector("input[data-field=repo]");
  repo.value = " fleetdeck ";
  fireEvent(repo, "change");
  await settle();
  assert.equal(calls.length, 0);
  assert.equal(root.querySelector("input[data-field=repo]").value, "fleetdeck");
});

// The operator's rule (T-134): a card with no repo is worked in the home
// directory, and "~" is that same card. Written as ~ the field read back as
// YAML's null, and the operator saw what they had just typed disappear. The
// home directory is written as no repo, and an empty field says it is home.
test("~ and an emptied repo are written as the home directory, and the field says so", async () => {
  for (const typed of ["~", "~/", "$HOME", "  "]) {
    const snap = snapshot();
    const { root, store, dispose } = open(snap);
    const calls = stubFetch(answer(204));
    const repo = root.querySelector("input[data-field=repo]");
    repo.value = typed;
    fireEvent(repo, "change");
    await settle();
    assert.equal(calls.length, 1, `${JSON.stringify(typed)} wrote nothing`);
    assert.equal(calls[0].body.value, "", `${JSON.stringify(typed)} was not written as the home directory`);

    // The card reads back with no repo, as the board holds it.
    snap.cards.find((c) => c.path === FLEET_UI).repo = "";
    store.push(snap);
    const shown = root.querySelector("input[data-field=repo]");
    assert.equal(shown.value, "");
    assert.equal(shown.placeholder, t("card_repo_home"));
    assert.equal(root.querySelector(".card-error"), null);
    dispose();
  }
});

test("a repo from ~ is written as a path from home", async () => {
  const { root } = open(snapshot());
  const calls = stubFetch(answer(204));
  const repo = root.querySelector("input[data-field=repo]");
  repo.value = "~/src/fleetdeck/";
  fireEvent(repo, "change");
  await settle();
  assert.equal(calls[0].body.value, "src/fleetdeck");
});

// The refusal used to be drawn for stage and progress only, so a repo the
// board refused went back to the old value without a word.
test("a refused repo is said in words, by its code", async () => {
  const { root } = open(snapshot());
  stubFetch(answer(400, { error: "repo /Users/x/src/gone: stat: no such file or directory", code: "repo_not_a_directory" }));
  const repo = root.querySelector("input[data-field=repo]");
  repo.value = "src/gone";
  fireEvent(repo, "change");
  await settle();
  const error = root.querySelector(".card-error");
  assert.ok(error, "a refused repo was silent");
  assert.equal(error.textContent, `repo: ${t("card_write_refused")}: ${t("card_refused_repo_not_a_directory")}`);
  assert.equal(root.querySelector("input[data-field=repo]").value, "fleetdeck");
});

// A card's agent writes its log while the operator types: the snapshot that
// carries the log draws the card again, and the field being typed in was
// replaced by a fresh one holding the card's value.
test("what is being typed into the repo survives a snapshot that draws the card again", async () => {
  const snap = snapshot();
  const { root, store } = open(snap);
  const calls = stubFetch(answer(204));
  const repo = root.querySelector("input[data-field=repo]");
  repo.focus();
  repo.value = "src/fleet";
  fireEvent(repo, "input");
  snap.cards.find((c) => c.path === FLEET_UI).body += "\n- a line from the agent\n";
  store.push(snap);

  const shown = root.querySelector("input[data-field=repo]");
  assert.equal(shown.value, "src/fleet", "the typed value was lost");
  assert.equal(dom.document.activeElement, shown, "the field lost the focus");
  shown.value = "src/fleetdeck";
  fireEvent(shown, "change");
  await settle();
  assert.equal(calls[0].body.value, "src/fleetdeck");
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

// --- the card's number ---
//
// The open card is where an operator settles on which card they are talking
// about, so it is where the number has to be readable — beside stage and
// progress, and never mistakable for the session's short id sitting next to it.

test("the open card shows its own number", () => {
  const { root } = open(snapshot());
  assert.equal(root.querySelector(".card-num").textContent, "T-004");
});

test("the number is not drawn as the session's short id", () => {
  const { root } = open(snapshot());
  // Two identifiers, one panel: the card's is permanent and gets said out
  // loud, the session's is per-run. Told apart by class, so app.css can give
  // them different shapes rather than two identical lines of small text.
  assert.equal(root.querySelector(".card-session").textContent, "a1b2c3");
  assert.ok(!root.querySelector(".card-session").classList.contains("card-num"));
  assert.ok(!root.querySelector(".card-num").classList.contains("card-session"));
  assert.ok(root.querySelector(".card-num").getAttribute("title"), "the number must say which identifier it is");
});

test("a card with no number says so instead of leaving a gap", () => {
  const snap = snapshot();
  delete snap.cards[0].id;
  const { root } = open(snap);
  const missing = root.querySelector(".card-num-none");
  assert.ok(missing, "a card without a number must say that it has none");
  assert.equal(missing.textContent, t("card_no_number"));
  assert.ok(!/T-/.test(missing.textContent), "a missing number must never be invented");
});

test("an unreadable card claims nothing about its number", () => {
  const { root } = open(snapshot(), BROKEN);
  // Its frontmatter did not parse: whether it has a number is unknown, and
  // "no number" would be a second invented fact on top of the real failure.
  assert.equal(root.querySelector(".card-num"), null);
  assert.equal(root.querySelector(".card-num-none"), null);
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

  assert.deepEqual(calls[0].body, { path: FLEET_UI, field: "stage", value: "review", lang: langCode });
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

// A stage set here does more than write the field: the agent keeping the card
// is told, and an accepted card has its session tidied away. A step that failed
// is the only place the operator can learn that the session is still running.
test("a step that failed around the write is shown beside the field it belongs to", async () => {
  const { root } = open(snapshot());
  stubFetch(
    answer(200, {
      written: true,
      committed: true,
      steps: [
        { name: "words", note: "read 40 characters of what abc12345 said" },
        { name: "session", error: "abc12345 is still running: claude is not installed" },
      ],
    }),
  );

  const stage = root.querySelector("select[data-field=stage]");
  stage.value = "done";
  fireEvent(stage, "change");
  await settle();

  const notices = [...root.querySelectorAll(".card-notice")].map((n) => n.textContent).join("\n");
  assert.ok(notices.includes("claude is not installed"), notices);
  assert.ok(notices.includes("stage"), "an unlabelled line does not say which edit it is about: " + notices);
  assert.ok(!notices.includes("read 40 characters"), "a step that worked is not news: " + notices);
  // The field is written; the control must not go back, and nothing here may
  // read as a refusal.
  assert.equal(root.querySelector(".card-error"), null);
  assert.equal(root.querySelector("select[data-field=stage]").value, "done");
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

// A refusal by one of the board's rules is said in the page's language, by
// its code: the words the server sends are the board's own, in English, and
// they name the rule without the way out. One code covers every started
// stage, so the sentence is the same whichever of them was asked for.
test("a rule refusal is said by its code, in the page's language", async () => {
  for (const stage of ["review", "done", "blocked"]) {
    const { root, dispose } = open(snapshot());
    stubFetch(
      answer(400, {
        error: `cannot set stage to ${stage} while session is empty: the board requires a session at stage ${stage}`,
        code: "session_required",
      }),
    );

    const select = root.querySelector("select[data-field=stage]");
    select.value = stage;
    fireEvent(select, "change");
    await settle();

    const error = root.querySelector(".card-error");
    assert.ok(error, `a refused ${stage} was silent`);
    assert.equal(error.textContent, `stage: ${t("card_write_refused")}: ${t("card_refused_session_required")}`);
    assert.doesNotMatch(error.textContent, /session is empty/, "the server's English came through");
    assert.equal(root.querySelector("select[data-field=stage]").value, "active");
    dispose();
  }
});

// A code this build has no sentence for — a newer panel, or a rule nobody
// foresaw — still shows the server's words rather than nothing.
test("a rule refusal with an unknown code falls back to the server's words", async () => {
  const { root } = open(snapshot());
  stubFetch(answer(400, { error: "the board refused this for a reason of its own", code: "nobody_knows" }));

  const select = root.querySelector("select[data-field=stage]");
  select.value = "review";
  fireEvent(select, "change");
  await settle();

  const error = root.querySelector(".card-error");
  assert.ok(error, "a refused write was silent");
  assert.equal(error.textContent, `stage: ${t("card_write_refused")}: the board refused this for a reason of its own`);
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

// T-091: the card's own docked terminal is where the operator answers the
// document's author -- Escape there is for the session (AskUserQuestion's
// "Esc to cancel", or interrupting a turn), and leaves the card open.
test("Escape in the card's own docked terminal goes to the session and leaves the card open", () => {
  const { root, closed } = open(snapshot());
  const term = root.querySelector(".card-dock-term");
  const typedInto = dom.element("textarea");
  term.appendChild(typedInto);

  const event = fireDocumentEvent(dom.document, "keydown", { key: "Escape", target: typedInto });

  assert.equal(closed.length, 0, "the card closed under an Escape meant for its own session");
  assert.notEqual(event.propagationStopped, true, "the docked terminal never saw the Escape");
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

// The review overlay's button, offered beside a session's own jump. Unlike the
// jump, it is offered for a session that is gone too: a review reads the
// working tree, not a terminal.
test("a card someone keeps offers its review", () => {
  const opened = [];
  const { root } = open(snapshot(), FLEET_UI, { onOpenReview: (p, how) => opened.push([p, how]) });
  fireEvent(root.querySelector(".card-review-link"), "click");
  assert.deepEqual(opened, [[FLEET_UI, {}]]);
});

// A button of the sheet's head, sized with its close: it stays on screen on a
// document's tab, and the way back from the review lands on that tab. An icon
// with its word as the label, not beside it: a word there narrowed the title,
// and on a narrow sheet the taller head left the docked session too little room.
test("the review is a head button, and the tab it was opened from goes with it", async () => {
  const opened = [];
  const { root } = open(withDocuments(snapshot()), FLEET_UI, {
    listDocs: async () => DOCS,
    onOpenReview: (p, how) => opened.push([p, how]),
  });
  await settle();
  const button = root.querySelector(".card-head .card-review-link");
  assert.ok(button, "the review sits in the card's head");
  assert.deepEqual(String(button.className).split(" ").slice(0, 3), ["btn", "btn-icon", "btn-md"]);
  assert.ok(button.innerHTML.includes("<svg"), "with its icon");
  assert.equal(button.getAttribute("aria-label"), t("review_open"), "its word is its label");
  assert.equal(button.getAttribute("title"), t("review_open"), "and its tooltip");
  assert.equal(button.textContent.trim(), "", "and takes no width from the title");

  fireEvent(root.querySelectorAll(".card-tab")[1], "click");
  await settle();
  fireEvent(root.querySelector(".card-head .card-review-link"), "click");
  assert.deepEqual(opened, [[FLEET_UI, { doc: DOCS[0].path }]]);
});

// --- the jump from a card to its session ---
//
// Offered only where there is somewhere to go: a card whose work is under way
// and whose session is live. Everywhere else a control would be a dead button,
// which is worse than none.

function withStage(snap, path, stage) {
  snap.cards.find((c) => c.path === path).stage = stage;
  return snap;
}

for (const stage of ["active", "review", "blocked"]) {
  test(`a ${stage} card with a live session jumps to it in one click`, () => {
    const opened = [];
    const { root } = open(withStage(snapshot(), FLEET_UI, stage), FLEET_UI, {
      onOpenSession: (id) => opened.push(id),
    });
    const jump = root.querySelector(".card-meta").querySelector("button");
    assert.ok(jump, "no control to jump with");
    fireEvent(jump, "click");
    assert.deepEqual(opened, ["a1b2c3"]);
  });
}

for (const stage of ["new", "done"]) {
  test(`a ${stage} card offers no jump, not even a dead one`, () => {
    const opened = [];
    const { root } = open(withStage(snapshot(), FLEET_UI, stage), FLEET_UI, {
      onOpenSession: (id) => opened.push(id),
    });
    assert.equal(root.querySelector(".card-meta").querySelectorAll("button").length, 0);
    assert.equal(root.querySelector(".card-session").tagName, "SPAN");
  });
}

test("a card whose session is dead says so and offers no jump", () => {
  const { root } = open(snapshot(), CARD_KEEPING, { onOpenSession: () => {} });
  assert.equal(root.querySelector(".card-session-dead").textContent, t("session_dead"));
  assert.equal(root.querySelector(".card-meta").querySelectorAll("button").length, 0);
});

// A stopped session is not dead — the board says so apart — but there is no
// terminal to open either, so the card says which it is instead of offering one.
test("a card whose session is stopped says so and offers no jump", () => {
  const snap = snapshot();
  snap.stoppedCards = [FLEET_UI];
  const { root } = open(snap, FLEET_UI, { onOpenSession: () => {} });
  assert.equal(root.querySelector(".card-session-stopped")?.textContent, t("session_stopped"));
  assert.equal(root.querySelector(".card-session-dead"), null);
  assert.equal(root.querySelector(".card-meta").querySelectorAll("button").length, 0);
});

test("a session that stops while the card is open takes the jump away", () => {
  const snap = snapshot();
  const { root, store } = open(snap, FLEET_UI, { onOpenSession: () => {} });
  assert.equal(root.querySelector(".card-meta").querySelectorAll("button").length, 1);
  const stopped = snapshot();
  stopped.stoppedCards = [FLEET_UI];
  store.push(stopped);
  assert.equal(root.querySelector(".card-meta").querySelectorAll("button").length, 0);
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


// The card's documents: links from its body to documents under the configured
// documentation roots (web/js/docnames.js), listed under its number.

const DOCS = [
  { path: "/board/docs/reports/2026-09-12-report.md", title: "reports/2026-09-12-report.md", root: "/board/docs" },
  { path: "/board/docs/reports/2026-09-13-design.md", title: "reports/2026-09-13-design.md", root: "/board/docs" },
];

function withDocuments(snap) {
  const card = snap.cards.find((c) => c.path === FLEET_UI);
  card.links = [...card.links, "2026-09-12-report", "reports/2026-09-13-design"];
  card.body += "\nReport: [[2026-09-12-report]], design: [[reports/2026-09-13-design]].\n";
  return snap;
}

// The card's own documents open in the card, on their tab (T-091); only a
// document the card does not link goes to the reader over the board.
function activeTab(root) {
  return [...root.querySelectorAll(".card-tab")].find((b) => b.getAttribute("aria-selected") === "true")?.dataset.key;
}

test("a card's documents are listed and each opens on its tab with one click", async () => {
  const opened = [];
  const fetched = [];
  const { root } = open(withDocuments(snapshot()), FLEET_UI, {
    listDocs: async () => DOCS,
    fetchDoc: async (path) => {
      fetched.push(path);
      return "# Design\n";
    },
    onOpenDoc: (path) => opened.push(path),
  });
  await settle();

  const entries = [...root.querySelectorAll(".card-doc")];
  assert.deepEqual(
    entries.map((entry) => entry.textContent),
    ["reports/2026-09-12-report", "reports/2026-09-13-design"],
  );
  fireEvent(entries[1], "click");
  await settle();
  assert.deepEqual(opened, [], "the card's own document is not handed to the reader");
  assert.equal(activeTab(root), "/board/docs/reports/2026-09-13-design.md");
  assert.deepEqual(fetched, ["/board/docs/reports/2026-09-13-design.md"]);
});

test("a card with no documents draws no documents block, not an empty one", async () => {
  const { root } = open(snapshot(), CARD_KEEPING, { listDocs: async () => DOCS });
  await settle();

  assert.equal(root.querySelector(".card-docs"), null);
  assert.ok(!root.textContent.includes(t("card_docs")), "a heading over nothing reads as a panel that lost something");
});

test("a link that opens nothing is listed as one, with the reason, and opens nothing", async () => {
  const opened = [];
  // The fixture card links "nowhere": neither a card nor a document.
  const { root } = open(withDocuments(snapshot()), FLEET_UI, {
    listDocs: async () => DOCS,
    onOpenDoc: (path) => opened.push(path),
  });
  await settle();

  assert.equal(root.querySelectorAll(".card-doc").length, 2, "a broken link is not counted as a document");
  const broken = root.querySelectorAll(".card-doc-missing");
  assert.equal(broken.length, 1);
  assert.ok(broken[0].textContent.includes("nowhere"), broken[0].textContent);
  assert.ok(broken[0].textContent.includes(t("card_doc_missing")), broken[0].textContent);
  fireEvent(broken[0], "click");
  assert.deepEqual(opened, []);
});

test("a card whose only document link is broken still shows the block, to say so", async () => {
  const { root } = open(snapshot(), FLEET_UI, { listDocs: async () => DOCS });
  await settle();

  assert.ok(root.querySelector(".card-docs"), "a broken link hidden is a broken link nobody fixes");
  assert.equal(root.querySelectorAll(".card-doc").length, 0);
  assert.equal(root.querySelectorAll(".card-doc-missing").length, 1);
});

test("a broken link in the body carries the reason it opens nothing", async () => {
  const { root } = open(snapshot(), FLEET_UI, { listDocs: async () => DOCS });
  await settle();

  assert.ok(root.querySelector(".card-body").innerHTML.includes(`title="${t("card_doc_missing")}"`));
});

test("the documents appear when the list arrives, without waiting for a snapshot", async () => {
  let release;
  const gate = new Promise((resolve) => {
    release = resolve;
  });
  const { root } = open(withDocuments(snapshot()), FLEET_UI, { listDocs: () => gate });
  assert.equal(root.querySelector(".card-docs"), null);

  release(DOCS);
  await settle();

  assert.equal(root.querySelectorAll(".card-doc").length, 2);
});

test("documentation that cannot be listed leaves the card whole", async () => {
  const { root } = open(withDocuments(snapshot()), FLEET_UI, {
    listDocs: async () => {
      throw new Error("no documentation roots are configured");
    },
  });
  await settle();

  assert.equal(root.querySelector(".card-docs"), null);
  assert.ok(root.querySelector("select[data-field=stage]"), "the card itself must still be drawn");
});

test("a document link in the body is a control naming the document", async () => {
  const { root } = open(withDocuments(snapshot()), FLEET_UI, { listDocs: async () => DOCS });
  await settle();

  assert.match(root.querySelector(".card-body").innerHTML, /data-doc="2026-09-12-report"/);
});

test("a document link clicked in the body opens that document on its tab", async () => {
  const opened = [];
  const { root } = open(withDocuments(snapshot()), FLEET_UI, {
    listDocs: async () => DOCS,
    fetchDoc: async () => "# Report\n",
    onOpenDoc: (path) => opened.push(path),
  });
  await settle();

  // Appended by hand: the fake DOM does not parse the body's innerHTML.
  const link = dom.element("button");
  link.dataset.doc = "2026-09-12-report";
  root.querySelector(".card-body").appendChild(link);
  fireEvent(link, "click");
  await settle();

  assert.deepEqual(opened, []);
  assert.equal(activeTab(root), "/board/docs/reports/2026-09-12-report.md");
});

test("a link to a document the card does not link goes to the reader", async () => {
  const opened = [];
  const other = { path: "/board/docs/elsewhere.md", title: "elsewhere.md", root: "/board/docs" };
  const { root } = open(withDocuments(snapshot()), FLEET_UI, {
    listDocs: async () => [...DOCS, other],
    onOpenDoc: (path) => opened.push(path),
  });
  await settle();

  const link = dom.element("button");
  link.dataset.doc = "elsewhere";
  root.querySelector(".card-body").appendChild(link);
  fireEvent(link, "click");

  assert.deepEqual(opened, ["/board/docs/elsewhere.md"]);
  assert.equal(activeTab(root), "card");
});

test("the tabs are the card and its documents, in the order its body links them", async () => {
  const { root } = open(withDocuments(snapshot()), FLEET_UI, { listDocs: async () => DOCS });
  await settle();
  assert.deepEqual(
    [...root.querySelectorAll(".card-tab")].map((b) => b.dataset.key),
    ["card", "/board/docs/reports/2026-09-12-report.md", "/board/docs/reports/2026-09-13-design.md"],
  );
  assert.equal(activeTab(root), "card");
});

test("a document's tab shows the document, the cards linking it and who wrote it", async () => {
  const signed = DOCS.map((d, i) => (i === 0 ? { ...d, session: "a41c09d2" } : d));
  const { root } = open(withDocuments(snapshot()), FLEET_UI, {
    listDocs: async () => signed,
    fetchDoc: async () => "# Report\n\nThe questions.\n",
  });
  await settle();
  fireEvent(root.querySelectorAll(".card-tab")[1], "click");
  await settle();
  const pane = root.querySelector(".card-pane");
  assert.equal(pane.querySelector(".card-body"), null, "the card's body is not under the document");
  assert.match(pane.querySelector(".card-doc-body").innerHTML, /The questions/);
  assert.ok(pane.querySelector(".doc-cards"), "the cards linking the document are named");
  const author = pane.querySelector(".card-doc-author");
  assert.ok(author.textContent.includes("a41c09d2"));
  assert.ok(author.textContent.includes(t("card_doc_author_from_document")));

  fireEvent(root.querySelectorAll(".card-tab")[2], "click");
  await settle();
  const fallback = root.querySelector(".card-pane").querySelector(".card-doc-author");
  const card = snapshot().cards.find((c) => c.path === FLEET_UI);
  if (card.session) {
    assert.ok(fallback.textContent.includes(card.session));
    assert.ok(fallback.textContent.includes(t("card_doc_author_from_card")));
  } else {
    assert.equal(fallback, null, "no session named anywhere, no author line");
  }
});

// The frontmatter names who wrote the document and is read for that; on the tab
// it is not text (T-091: the stand's frame showed "---", "session: …", "---"
// above the title).
test("a document's tab draws its body without its frontmatter, and a plain document whole", async () => {
  const bodies = {
    "/board/docs/reports/2026-09-12-report.md": "---\nsession: a41c09d2\n---\n\n# Report\n\nThe questions.\n",
    "/board/docs/reports/2026-09-13-design.md": "First words.\n\n# Design\n",
  };
  const { root } = open(withDocuments(snapshot()), FLEET_UI, { listDocs: async () => DOCS, fetchDoc: async (path) => bodies[path] });
  await settle();
  fireEvent(root.querySelectorAll(".card-tab")[1], "click");
  await settle();
  const signed = root.querySelector(".card-pane").querySelector(".card-doc-body").innerHTML;
  assert.doesNotMatch(signed, /session:/);
  assert.doesNotMatch(signed, /^\s*<hr/);
  assert.match(signed, /<h2>Report<\/h2>/);

  fireEvent(root.querySelectorAll(".card-tab")[2], "click");
  await settle();
  assert.match(root.querySelector(".card-pane").querySelector(".card-doc-body").innerHTML, /First words\./);
});

test("a document's body is fetched once, not again on its tab or on every snapshot", async () => {
  const fetched = [];
  const { root, store } = open(withDocuments(snapshot()), FLEET_UI, {
    listDocs: async () => DOCS,
    fetchDoc: async (path) => {
      fetched.push(path);
      return "# Report\n";
    },
  });
  await settle();
  const tabs = () => [...root.querySelectorAll(".card-tab")];
  fireEvent(tabs()[1], "click");
  await settle();
  store.push(withDocuments(snapshot()));
  fireEvent(tabs()[0], "click");
  fireEvent(tabs()[1], "click");
  await settle();
  assert.deepEqual(fetched, ["/board/docs/reports/2026-09-12-report.md"]);
});

test("a document that cannot be fetched says why on its tab, and the tabs still work", async () => {
  const { root } = open(withDocuments(snapshot()), FLEET_UI, {
    listDocs: async () => DOCS,
    fetchDoc: async () => {
      throw new Error("document not found");
    },
  });
  await settle();
  fireEvent(root.querySelectorAll(".card-tab")[1], "click");
  await settle();
  const pane = root.querySelector(".card-pane");
  assert.ok(pane.textContent.includes(t("card_doc_failed")));
  assert.ok(pane.textContent.includes("document not found"));
  fireEvent(root.querySelectorAll(".card-tab")[0], "click");
  assert.ok(root.querySelector(".card-pane").querySelector(".card-body"));
});

test("a document that could not be fetched is asked for again when its tab is picked again", async () => {
  let calls = 0;
  const { root } = open(withDocuments(snapshot()), FLEET_UI, {
    listDocs: async () => DOCS,
    fetchDoc: async () => {
      calls += 1;
      if (calls === 1) throw new Error("the panel was restarting");
      return "# Report\n\nBack.\n";
    },
  });
  await settle();
  const tabs = () => [...root.querySelectorAll(".card-tab")];
  fireEvent(tabs()[1], "click");
  await settle();
  fireEvent(tabs()[0], "click");
  fireEvent(tabs()[1], "click");
  await settle();
  assert.equal(calls, 2);
  assert.match(root.querySelector(".card-pane").querySelector(".card-doc-body").innerHTML, /Back/);
});

test("a card can be opened straight on one of its documents", async () => {
  const { root } = open(withDocuments(snapshot()), FLEET_UI, {
    listDocs: async () => DOCS,
    fetchDoc: async () => "# Design\n",
    doc: "reports/2026-09-13-design",
  });
  await settle();
  assert.equal(activeTab(root), "/board/docs/reports/2026-09-13-design.md");
});

test("following a link to another card goes back to that card's own tab", async () => {
  const { root } = open(withDocuments(snapshot()), FLEET_UI, {
    listDocs: async () => DOCS,
    fetchDoc: async () => "# Report\n",
  });
  await settle();
  fireEvent(root.querySelectorAll(".card-tab")[1], "click");
  await settle();
  const link = dom.element("button");
  link.dataset.link = baseNameOf(CARD_KEEPING);
  root.querySelector(".card-pane").appendChild(link);
  fireEvent(link, "click");
  assert.equal(activeTab(root), "card");
});

test("the one panel can open a card straight on one of its documents", async () => {
  const panel = dom.element("div");
  dom.document.body.appendChild(panel);
  const store = fakeStore(withDocuments(snapshot()));
  const cardPanel = createCardPanel(panel, { subscribe: store.subscribe, listDocs: async () => DOCS, fetchDoc: async () => "# D\n" });
  cardPanel.open(FLEET_UI, { doc: "2026-09-12-report" });
  await settle();
  assert.equal(activeTab(panel), "/board/docs/reports/2026-09-12-report.md");
  cardPanel.open(FLEET_UI);
  await settle();
  assert.equal(activeTab(panel), "card", "what one opening asked for is not carried into the next");
  cardPanel.close();
});

// --- the author's session next to the open tab (T-091) --------------------------

function fakeTerminals() {
  const made = [];
  const factory = (host, short) => {
    const term = { short, opened: 0, stopped: 0, open() { this.opened += 1; }, stop() { this.stopped += 1; }, type() {} };
    made.push(term);
    return term;
  };
  return { made, factory };
}

function withAuthors(snap, { cardSession = "a41c09d2", orchestrator = "" } = {}) {
  const s = withDocuments(snap);
  s.cards.find((c) => c.path === FLEET_UI).session = cardSession;
  s.orchestratorSession = orchestrator;
  s.sessions = [
    ...(s.sessions ?? []).filter((x) => !["a41c09d2", "909bf9b2", "0c7e1a2b"].includes(x.short)),
    { short: "a41c09d2", needs: "answer: which way?", lifecycle: "live" },
    { short: "909bf9b2", needs: "", lifecycle: "live" },
    { short: "0c7e1a2b", needs: "", lifecycle: "live" },
  ];
  return s;
}

const SIGNED = DOCS.map((d, i) => (i === 1 ? { ...d, session: "909bf9b2" } : d));

test("a card with no session and a document that names none leaves no place for a session", async () => {
  const s = withDocuments(snapshot());
  s.cards.find((c) => c.path === FLEET_UI).session = "";
  const { root } = open(s, FLEET_UI, { listDocs: async () => DOCS, fetchDoc: async () => "# R\n" });
  await settle();
  assert.equal(root.querySelector(".card-dock").hidden, true);
  fireEvent(root.querySelectorAll(".card-tab")[1], "click");
  await settle();
  assert.equal(root.querySelector(".card-dock").hidden, true);
  assert.ok(root.querySelector(".card-doc-body"), "the tabs work without anyone to answer");
});

test("a tab whose document another session wrote moves the open terminal to that session", async () => {
  const terms = fakeTerminals();
  const { root } = open(withAuthors(snapshot()), FLEET_UI, {
    listDocs: async () => SIGNED,
    fetchDoc: async () => "# R\n",
    terminal: terms.factory,
    expand: true,
  });
  await settle();
  assert.deepEqual(terms.made.map((x) => x.short), ["a41c09d2"], "the card's tab: the card's session");
  fireEvent(root.querySelectorAll(".card-tab")[1], "click");
  await settle();
  assert.deepEqual(terms.made.map((x) => x.short), ["a41c09d2"], "a document naming no one: still the card's session");
  fireEvent(root.querySelectorAll(".card-tab")[2], "click");
  await settle();
  assert.deepEqual(terms.made.map((x) => x.short), ["a41c09d2", "909bf9b2"]);
  assert.equal(terms.made[0].stopped, 1);
});

test("snapshots under an open terminal keep the one terminal", async () => {
  const terms = fakeTerminals();
  const { store } = open(withAuthors(snapshot()), FLEET_UI, {
    listDocs: async () => SIGNED,
    terminal: terms.factory,
    expand: true,
  });
  await settle();
  for (let i = 0; i < 5; i += 1) {
    const next = withAuthors(snapshot());
    next.cards.find((c) => c.path === FLEET_UI).progress = 10 * i;
    store.push(next);
  }
  assert.equal(terms.made.length, 1);
  assert.equal(terms.made[0].opened, 1);
  assert.equal(terms.made[0].stopped, 0);
});

test("a document the orchestrator wrote sends to the orchestrator the way the panel was told to", async () => {
  const terms = fakeTerminals();
  let sent = 0;
  const { root } = open(withAuthors(snapshot(), { cardSession: "0c7e1a2b", orchestrator: "0c7e1a2b" }), FLEET_UI, {
    listDocs: async () => DOCS,
    terminal: terms.factory,
    expand: true,
    toOrchestrator: () => {
      sent += 1;
    },
  });
  await settle();
  assert.equal(terms.made.length, 0, "the orchestrator's terminal is not opened a second time");
  fireEvent(root.querySelector(".card-dock-orchestrator"), "click");
  assert.equal(sent, 1);
});

test("closing the sheet lets its terminal go", async () => {
  const terms = fakeTerminals();
  const { dispose } = open(withAuthors(snapshot()), FLEET_UI, { listDocs: async () => DOCS, terminal: terms.factory, expand: true });
  await settle();
  dispose();
  assert.equal(terms.made[0].stopped, 1);
});

// T-017: the page in a plain browser tab, with no fleetdeck window around it,
// still opens a card's tabs, its documents and the author's session.
test("with no window around the page the card's tabs, documents and session all work", async () => {
  assert.equal(globalThis.window?.fleetdeckHost, undefined, "no window host in this test");
  const terms = fakeTerminals();
  const { root } = open(withAuthors(snapshot()), FLEET_UI, {
    listDocs: async () => SIGNED,
    fetchDoc: async () => "# Design\n",
    terminal: terms.factory,
  });
  await settle();
  fireEvent(root.querySelectorAll(".card-tab")[2], "click");
  await settle();
  assert.ok(root.querySelector(".card-doc-body"));
  fireEvent(root.querySelector(".card-dock-open"), "click");
  assert.deepEqual(terms.made.map((x) => x.short), ["909bf9b2"]);
});

test("another card that links the same document still opens on its own tab", async () => {
  const s = withDocuments(snapshot());
  const other = s.cards.find((c) => c.path === CARD_KEEPING);
  other.links = [...(other.links ?? []), "2026-09-12-report"];
  const { root } = open(s, FLEET_UI, { listDocs: async () => DOCS, fetchDoc: async () => "# Report\n" });
  await settle();
  fireEvent(root.querySelectorAll(".card-tab")[1], "click");
  await settle();
  const link = dom.element("button");
  link.dataset.link = baseNameOf(CARD_KEEPING);
  root.querySelector(".card-pane").appendChild(link);
  fireEvent(link, "click");
  assert.ok([...root.querySelectorAll(".card-tab")].some((b) => b.dataset.key === "/board/docs/reports/2026-09-12-report.md"), "the other card has that tab too");
  assert.equal(activeTab(root), "card");
});

// What the author is doing moves every turn; the document being read must not
// be drawn again for it -- a selection, an open <details>, would go with it.
test("the author's state moves the dot on its tab and leaves the open document as it is", async () => {
  const { root, store } = open(withAuthors(snapshot()), FLEET_UI, { listDocs: async () => SIGNED, fetchDoc: async () => "# Notes\n" });
  await settle();
  fireEvent(root.querySelectorAll(".card-tab")[2], "click");
  await settle();
  const body = root.querySelector(".card-pane").querySelector(".card-doc-body");
  const dot = () => root.querySelectorAll(".card-tab")[2].querySelector(".card-tab-dot").dataset.state;
  assert.equal(dot(), "working");

  const next = withAuthors(snapshot());
  next.sessions.find((s) => s.short === "909bf9b2").needs = "answer: which way?";
  store.push(next);

  assert.equal(dot(), "waiting");
  assert.equal(root.querySelector(".card-pane").querySelector(".card-doc-body"), body, "the document was drawn again");
});

function baseNameOf(path) {
  return path.split("/").pop().replace(/\.md$/, "");
}

test("the documents are listed once per opened card, not once per snapshot", async () => {
  let calls = 0;
  const { store } = open(withDocuments(snapshot()), FLEET_UI, {
    listDocs: async () => {
      calls += 1;
      return DOCS;
    },
  });
  await settle();
  store.push(withDocuments(snapshot()));
  store.push(withDocuments(snapshot()));
  await settle();

  assert.equal(calls, 1);
});

// E layout, K1: the close control is a round button in the sheet, and a bare
// glyph sits off-centre in a round button. The cross is drawn instead.
test("the close control is a round button with a drawn cross, not a bare glyph", () => {
  const { root } = open(JSON.parse(FIXTURE));
  const close = root.querySelector(".card-close");
  assert.equal(close.getAttribute("aria-label"), t("card_close"));
  assert.match(close.innerHTML, /^<svg[^>]*aria-hidden="true"/, "the cross is an svg so it sits centred in the round button");
  assert.equal(close.textContent, "", "the old glyph is still in the button beside the drawing");
});

// T-091: the sheet is a frame built once -- a head, a row of tabs, and a stage
// holding the open tab's pane and the place the author's session lives in.
// Snapshots redraw the head and the pane; the session's place outlives them, or
// a live terminal in it would be torn down once a second.
test("a snapshot redraws the card without replacing where its session lives", () => {
  const { root, store } = open(snapshot());
  const dock = root.querySelector(".card-dock");
  const stage = root.querySelector(".card-stage");
  assert.ok(dock, "the sheet has a place for the session");
  assert.ok(stage && stage.contains(dock));
  const next = snapshot();
  next.cards[0].progress = 60;
  store.push(next);
  assert.equal(root.querySelector(".card-dock"), dock);
  assert.equal(root.querySelector(".card-stage"), stage);
  assert.equal(root.querySelector(".card-pane").querySelector("select[data-field=progress]").value, "60");
});

test("the card's fields, meta, body and backlinks sit in the pane, its head above the tabs", () => {
  const { root } = open(snapshot());
  const pane = root.querySelector(".card-pane");
  assert.ok(pane, "the sheet has a pane");
  for (const sel of [".card-fields", ".card-meta", ".card-body"]) assert.ok(pane.querySelector(sel), sel);
  assert.ok(root.querySelector(".card-head"));
  assert.equal(pane.querySelector(".card-head"), null, "the head stays above the tabs");
  assert.ok(root.querySelector(".card-tabs"), "the sheet has a row of tabs");
});

// T-138: the documentation list is fetched when the sheet opens, and an agent
// writes its report afterwards -- the document and the link to it both arrive
// while the card is open. The snapshot carries the documentation roots'
// revision; a new one asks for the list again, so the tab appears without the
// card being opened anew.
test("a document written while the card is open gets its tab when the docs revision moves", async () => {
  let listed = [];
  let asked = 0;
  const first = snapshot();
  first.docsRevision = "r1";
  const { root, store } = open(first, FLEET_UI, {
    listDocs: async () => {
      asked += 1;
      return listed;
    },
  });
  await settle();
  assert.deepEqual([...root.querySelectorAll(".card-tab")].map((b) => b.dataset.key), ["card"]);

  listed = DOCS;
  const next = withDocuments(snapshot());
  next.docsRevision = "r2";
  store.push(next);
  await settle();

  assert.deepEqual(
    [...root.querySelectorAll(".card-tab")].map((b) => b.dataset.key),
    ["card", "/board/docs/reports/2026-09-12-report.md", "/board/docs/reports/2026-09-13-design.md"],
  );
  assert.equal(asked, 2);

  store.push(next);
  await settle();
  assert.equal(asked, 2, "an unchanged revision asks for nothing");
});

test("a document rewritten while its tab is open is read again when the docs revision moves", async () => {
  let text = "# Report\n\nFirst draft.\n";
  const first = withDocuments(snapshot());
  first.docsRevision = "r1";
  const { root, store } = open(first, FLEET_UI, {
    listDocs: async () => DOCS,
    fetchDoc: async () => text,
  });
  await settle();
  fireEvent(root.querySelectorAll(".card-tab")[1], "click");
  await settle();
  assert.match(root.querySelector(".card-doc-body").innerHTML, /First draft/);

  text = "# Report\n\nFinal.\n";
  const next = withDocuments(snapshot());
  next.docsRevision = "r2";
  store.push(next);
  await settle();

  assert.match(root.querySelector(".card-doc-body").innerHTML, /Final/);
});
