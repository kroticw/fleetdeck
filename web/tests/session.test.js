// The session panel, driven through the same handlers a person's clicks reach.
//
// This is the part of the interface that writes into a live Claude Code session,
// and what is worth pinning about it is wiring rather than any pure function:
// that polling never multiplies, that a failure is retried instead of freezing a
// tab, that a key button sends bytes rather than the word printed on it, and
// that text which could not be sent is still in the box.
//
// What none of it shows is that any of this looks right. No browser runs here.

import { test, beforeEach, afterEach } from "node:test";
import assert from "node:assert/strict";

import { installDOM, fireEvent, settle } from "./fake-dom.js";
import { KEYS, createPoller, renderSession } from "../js/session.js";
// The panel's own dictionary, not a copy of its strings: what is pinned below is
// that the label the operator reads comes from a key that exists, in whichever
// language this machine runs in.
import { t } from "../js/i18n.js";

const SHORT = "sess-1";
const FULL = "sess-1-4f2c-11ee-9d3a-0242ac120002";

let dom;
let calls;
let realFetch;
let realTerminal;

// A clock the test advances by hand.
//
// It models setInterval as well as setTimeout, which is not decoration: the
// defect this file exists to pin is a re-armed interval, and a clock that could
// only express timeouts would make that defect unrepresentable and the test that
// "catches" it meaningless.
function fakeTimers() {
  let nextId = 1;
  const pending = new Map();
  const cancel = (id) => pending.delete(id);
  return {
    setTimeout(fn) {
      const id = nextId++;
      pending.set(id, { fn, repeating: false });
      return id;
    },
    setInterval(fn) {
      const id = nextId++;
      pending.set(id, { fn, repeating: true });
      return id;
    },
    clearTimeout: cancel,
    clearInterval: cancel,
    count() {
      return pending.size;
    },
    // Fire every armed timer once. A timeout is removed before it runs; an
    // interval stays, exactly as a real clock behaves.
    async tick() {
      for (const [id, timer] of [...pending.entries()]) {
        if (!timer.repeating) pending.delete(id);
        timer.fn();
      }
      await settle();
    },
  };
}

function answer({ status = 200, body, statusText = "" } = {}) {
  return {
    status,
    ok: status >= 200 && status < 300,
    statusText,
    async json() {
      if (body === undefined) throw new SyntaxError("Unexpected end of JSON input");
      return body;
    },
  };
}

function stubFetch(respond) {
  globalThis.fetch = async (url, init) => {
    calls.push({ url, init });
    return typeof respond === "function" ? respond(url, init) : respond;
  };
}

// The terminal the panel finds on the global object, which is where
// web/vendor/xterm.js puts the real one.
function installTerminal() {
  const made = [];
  globalThis.Terminal = class {
    constructor(options) {
      this.options = options;
      this.writes = [];
      this.resets = 0;
      this.disposed = 0;
      this.host = null;
      made.push(this);
    }
    open(host) {
      this.host = host;
    }
    reset() {
      this.resets += 1;
    }
    write(data) {
      this.writes.push(data);
    }
    dispose() {
      this.disposed += 1;
    }
  };
  return made;
}

beforeEach(() => {
  dom = installDOM();
  calls = [];
  realFetch = globalThis.fetch;
  realTerminal = Object.hasOwn(globalThis, "Terminal") ? globalThis.Terminal : undefined;
});

afterEach(() => {
  dom.restore();
  globalThis.fetch = realFetch;
  if (realTerminal === undefined) delete globalThis.Terminal;
  else globalThis.Terminal = realTerminal;
});

async function mount({ lookup = () => ({ short: SHORT, sessionId: FULL }) } = {}) {
  const root = dom.element("div");
  const timers = fakeTimers();
  let closed = 0;
  const stop = renderSession(root, SHORT, () => {
    closed += 1;
  }, { timers, lookup });
  await settle(); // let the first poll land

  const errorText = () => {
    const line = root.querySelector(".s-error");
    return line.hidden ? "" : line.textContent;
  };
  return {
    root,
    timers,
    stop,
    errorText,
    closes: () => closed,
    input: () => root.querySelector(".s-input"),
    async click(selector) {
      fireEvent(root.querySelector(selector), "click");
      await settle();
    },
    async pressEnter(shiftKey = false) {
      fireEvent(root.querySelector(".s-input"), "keydown", { key: "Enter", shiftKey });
      await settle();
    },
    async openScreenTab() {
      fireEvent(root.querySelector('[data-tab="screen"]'), "click");
      await settle();
    },
  };
}

// --- the two identifiers ---------------------------------------------------

test("the digest goes out under the full session id and everything else under the short one", async () => {
  // Found by opening the panel, not by reading the routes: the daemon knows a
  // session by its short id and answers EUNKNOWN to the full one, while the
  // transcript is a file named after the full id and the digest route resolves
  // nothing shorter. The panel showed "transcript not found: sess-1" on every
  // session until the two were told apart, and both routes are spelled
  // /api/sessions/{id}/… so nothing but a running panel would have said which
  // id each wanted.
  installTerminal();
  stubFetch((url) => (url.includes("/screen") ? answer({ body: { screen: "x" } }) : answer({ body: [] })));
  const panel = await mount();

  assert.equal(calls[0].url, `/api/sessions/${FULL}/digest?limit=30`);

  await panel.openScreenTab();
  assert.equal(calls[calls.length - 1].url, `/api/sessions/${SHORT}/screen`);

  await panel.click("[data-key=enter]");
  assert.equal(calls[calls.length - 1].url, `/api/sessions/${SHORT}/keys`);

  panel.input().value = "hello";
  await panel.pressEnter();
  assert.equal(calls[calls.length - 1].url, `/api/sessions/${SHORT}/text`);
});

test("a session the snapshot does not hold says so instead of asking for an empty id", async () => {
  stubFetch(answer({ body: [] }));
  const panel = await mount({ lookup: () => undefined });

  assert.equal(calls.length, 0, "no request may go out with an empty id in the path");
  assert.notEqual(panel.errorText(), "");
});

test("a panel opened before the first snapshot starts working when the session appears", async () => {
  // The failure this pins was a picture, not a test: opened before the socket's
  // first frame, the panel captured an empty full id and went on saying the
  // session was not listed while the session sat in the list beside it. The id
  // is looked up on every pass now, so the tab recovers on its own.
  let known;
  stubFetch(answer({ body: [{ role: "user", text: "there it is" }] }));
  const panel = await mount({ lookup: () => known });

  assert.notEqual(panel.errorText(), "", "before the snapshot: says so");
  assert.equal(calls.length, 0);

  known = { short: SHORT, sessionId: FULL };
  await panel.timers.tick();

  assert.equal(panel.errorText(), "", "after the snapshot: recovered without being reopened");
  assert.equal(calls[calls.length - 1].url, `/api/sessions/${FULL}/digest?limit=30`);
});

// --- polling ---------------------------------------------------------------

test("the digest tab keeps exactly one timer armed, however long it runs", async () => {
  stubFetch(answer({ body: [{ role: "user", text: "hi" }] }));
  const panel = await mount();

  assert.equal(panel.timers.count(), 1, "one timer after the first pass");

  for (let tick = 1; tick <= 12; tick += 1) {
    await panel.timers.tick();
    assert.equal(panel.timers.count(), 1, `still one timer after ${tick} ticks`);
  }

  // One request per pass, and no more. Arming a new timer without cancelling the
  // one that fired doubles this every tick: 12 ticks would be 4096 requests
  // rather than 13, which is invisible until the machine is on fire.
  assert.equal(calls.length, 13);
});

test("the screen tab keeps exactly one timer armed too", async () => {
  installTerminal();
  stubFetch((url) => (url.includes("/screen") ? answer({ body: { screen: "ready" } }) : answer({ body: [] })));
  const panel = await mount();
  await panel.openScreenTab();

  for (let tick = 1; tick <= 8; tick += 1) {
    await panel.timers.tick();
    assert.equal(panel.timers.count(), 1, `still one timer after ${tick} ticks`);
  }
});

test("stopping the panel leaves nothing running", async () => {
  stubFetch(answer({ body: [] }));
  const panel = await mount();

  const before = calls.length;
  panel.stop();

  assert.equal(panel.timers.count(), 0, "stop must leave no timer armed");
  await panel.timers.tick();
  await settle();
  assert.equal(calls.length, before, "no request after stop");
});

test("the close button stops the panel and calls back exactly once", async () => {
  stubFetch(answer({ body: [] }));
  const panel = await mount();

  await panel.click(".s-close");

  assert.equal(panel.closes(), 1);
  assert.equal(panel.timers.count(), 0);
});

test("switching tabs stops the tab being left behind, and disposes its terminal", async () => {
  const terminals = installTerminal();
  stubFetch((url) => (url.includes("/screen") ? answer({ body: { screen: "x" } }) : answer({ body: [] })));
  const panel = await mount();
  await panel.openScreenTab();

  assert.equal(panel.timers.count(), 1, "one timer, not one per tab visited");
  assert.equal(terminals.length, 1);

  fireEvent(panel.root.querySelector('[data-tab="digest"]'), "click");
  await settle();
  assert.equal(panel.timers.count(), 1);
  // xterm holds a renderer, listeners and an observer; dropping the reference
  // without disposing leaks all three for the life of the page.
  assert.equal(terminals[0].disposed, 1);

  await panel.timers.tick();
  assert.match(calls[calls.length - 1].url, /\/digest\?/);
});

// Pins the fix for a real defect: xterm's own default theme is a fixed
// light-grey-on-black regardless of the page's own theme, which read fine
// for as long as the whole app was dark-only and became a solid black
// rectangle in an otherwise light panel once a light theme actually shipped.
// This cannot check the *colours* — fake-dom.js has no CSS engine, and
// terminalTheme() itself degrades to undefined without one — but it does
// pin that a terminal is never constructed with the option silently
// dropped, which is the shape a future refactor could plausibly break.
test("the terminal is always constructed with a theme option, even if undefined", async () => {
  const terminals = installTerminal();
  stubFetch((url) => (url.includes("/screen") ? answer({ body: { screen: "x" } }) : answer({ body: [] })));
  const panel = await mount();
  await panel.openScreenTab();

  assert.equal(terminals.length, 1);
  assert.ok("theme" in terminals[0].options, "Terminal was constructed with no theme option at all");
});

test("a poller started twice still arms only one timer", async () => {
  const timers = fakeTimers();
  const poller = createPoller(async () => {}, 1000, { timers });

  poller.start();
  poller.start();
  await settle();

  assert.equal(timers.count(), 1);
  poller.stop();
  assert.equal(timers.count(), 0);
});

// --- failure is retried, not fatal -----------------------------------------

test("a failed digest poll shows the failure and is tried again", async () => {
  stubFetch(answer({ status: 404, statusText: "Not Found", body: { error: "transcript not found: sess-1" } }));
  const panel = await mount();

  // A session that has left no transcript is a fact the operator must see: an
  // empty pane is indistinguishable from a session that is simply quiet.
  assert.equal(panel.errorText(), "transcript not found: sess-1");
  assert.equal(panel.timers.count(), 1, "a failed pass must still arm the next one");

  await panel.timers.tick();
  assert.equal(calls.length, 2, "the failed tab keeps polling");
});

test("a route that answers with something that is not JSON still produces words", async () => {
  // What an unregistered route answers: the request falls through to the static
  // file server, whose 404 body is plain text and parses as nothing.
  stubFetch(answer({ status: 404, statusText: "Not Found" }));
  const panel = await mount();

  assert.equal(panel.errorText(), "Not Found");
});

test("a failed screen poll shows the failure and is tried again", async () => {
  // The defect this pins: the plan's screen branch returned from its error path
  // before arming the next timer, so one dropped request — a daemon restart, a
  // lost attach — froze the tab until somebody closed and reopened the panel.
  // Its digest branch did not have the bug, which is how it survived review: the
  // two branches disagreed.
  installTerminal();
  let attempt = 0;
  stubFetch((url) => {
    if (!url.includes("/screen")) return answer({ body: [] });
    attempt += 1;
    // A request that never completes: the failure a browser reports by
    // rejecting, not by answering.
    if (attempt === 1) throw new TypeError("daemon is not running");
    return answer({ body: { screen: "back again" } });
  });
  const panel = await mount();
  await panel.openScreenTab();

  assert.equal(panel.errorText(), "daemon is not running");
  assert.equal(panel.timers.count(), 1, "a transient screen failure must not freeze the tab");

  await panel.timers.tick();
  assert.equal(panel.errorText(), "", "the recovered poll clears the error");
});

test("a screen read that failed part way still draws what it did read", async () => {
  const terminals = installTerminal();
  stubFetch((url) =>
    url.includes("/screen")
      ? answer({
          status: 502,
          statusText: "Bad Gateway",
          body: { error: "attach evicted", screen: "half a line" },
        })
      : answer({ body: [] }),
  );
  const panel = await mount();
  await panel.openScreenTab();

  assert.deepEqual(terminals[0].writes, ["half a line"]);
  assert.equal(panel.errorText(), "attach evicted");
});

test("a missing terminal library is a visible error, not a blank tab", async () => {
  // A <script> tag that 404s reports nothing to anyone. The panel has to.
  delete globalThis.Terminal;
  stubFetch(answer({ body: { screen: "ready" } }));
  const panel = await mount();
  await panel.openScreenTab();

  assert.notEqual(panel.errorText(), "");
  assert.equal(panel.timers.count(), 1);
});

// --- writing into the session ----------------------------------------------

test("every key button sends its escape sequence, never the word on its face", async () => {
  installTerminal();
  stubFetch((url) => (url.includes("/screen") ? answer({ body: { screen: "" } }) : answer({ status: 204 })));
  const panel = await mount();
  await panel.openScreenTab();

  const expected = {
    escape: "\u001b",
    up: "\u001b[A",
    down: "\u001b[B",
    enter: "\r",
  };

  for (const key of KEYS) {
    await panel.click(`[data-key=${key.id}]`);

    const { url, init } = calls[calls.length - 1];
    assert.equal(url, `/api/sessions/${SHORT}/keys`);
    assert.equal(init.headers["Content-Type"], "application/json");
    const sent = JSON.parse(init.body).keys;
    assert.equal(sent, expected[key.id], `${key.id} must send bytes`);
    // internal/server hands this field to the daemon, which writes it into the
    // session's terminal byte for byte. Sending the identifier would type the
    // word into whatever the session is doing.
    assert.notEqual(sent, key.id);
    assert.notEqual(sent, key.label);
  }
});

test("a key that could not be sent is reported", async () => {
  installTerminal();
  stubFetch((url) => {
    if (url.includes("/keys")) {
      return answer({ status: 502, statusText: "Bad Gateway", body: { error: "session is gone" } });
    }
    return url.includes("/screen") ? answer({ body: { screen: "" } }) : answer({ body: [] });
  });
  const panel = await mount();
  await panel.openScreenTab();

  await panel.click("[data-key=enter]");

  assert.equal(panel.errorText(), "session is gone");
});

test("Enter sends what was typed and clears the box", async () => {
  stubFetch((url) => (url.includes("/text") ? answer({ status: 204 }) : answer({ body: [] })));
  const panel = await mount();

  panel.input().value = "run the tests";
  await panel.pressEnter();

  assert.equal(JSON.parse(calls[calls.length - 1].init.body).text, "run the tests");
  assert.equal(panel.input().value, "", "a sent message leaves the box empty");
  assert.equal(panel.errorText(), "");
});

test("text that failed to send stays in the box, exactly as it was typed", async () => {
  // The one failure this panel must not have. Somebody who typed a paragraph
  // into a session that had just died must still have the paragraph.
  stubFetch((url) =>
    url.includes("/text")
      ? answer({ status: 502, statusText: "Bad Gateway", body: { error: "daemon is not running" } })
      : answer({ body: [] }),
  );
  const panel = await mount();

  const typed = "  a long answer\n  with two lines  ";
  panel.input().value = typed;
  await panel.pressEnter();

  assert.equal(panel.input().value, typed, "the text must come back verbatim, untrimmed");
  assert.equal(panel.errorText(), "daemon is not running");
});

test("Shift+Enter is a newline, not a send", async () => {
  stubFetch(answer({ body: [] }));
  const panel = await mount();

  const before = calls.length;
  panel.input().value = "first line";
  await panel.pressEnter(true);

  assert.equal(calls.length, before, "no request on Shift+Enter");
  assert.equal(panel.input().value, "first line");
});

test("an empty box sends nothing", async () => {
  stubFetch(answer({ body: [] }));
  const panel = await mount();

  const before = calls.length;
  panel.input().value = "   \n  ";
  await panel.pressEnter();

  assert.equal(calls.length, before);
});

// --- what the digest tab draws ---------------------------------------------

test("digest steps are drawn as text, with the role decided here and not by the transcript", async () => {
  stubFetch(
    answer({
      body: [
        { role: "user", text: "please continue" },
        { role: "assistant", text: "<img src=x onerror=alert(1)>" },
        { role: 'smuggled" onload=x', text: "from a session we did not write" },
      ],
    }),
  );
  const panel = await mount();

  const drawn = panel.root.querySelectorAll(".s-step");
  assert.equal(drawn.length, 3);
  // Assigned as text, so markup in a step is characters and never nodes.
  assert.equal(drawn[1].textContent, "<img src=x onerror=alert(1)>");
  assert.equal(drawn[1].children.length, 0);
  // A role the panel does not know is not carried into a class name.
  assert.equal(drawn[2].className, "s-step s-step-other");
  assert.equal(drawn[0].className, "s-step s-step-user");
});

test("a transcript with no readable steps says so rather than showing nothing", async () => {
  stubFetch(answer({ body: [] }));
  const panel = await mount();

  const empty = panel.root.querySelector(".s-empty");
  assert.notEqual(empty, null);
  assert.notEqual(empty.textContent, "");
});

// --- the two classes of control, and which is which ------------------------
//
// The operator's first look at this panel produced one sentence: "непонятно
// зачем нужны эти кнопки" — it is not clear what these buttons are for, about a
// bare `Esc ↑ ↓ Enter ✕` row in the top-right corner of the header. That corner
// is where every window on his machine puts controls that act on the window,
// and four of those five buttons do not: they press a key inside a Claude Code
// session running somewhere else, which no undo reaches. The three tests below
// are what stops that row from coming back.

test("the keys are drawn on the screen tab and on no other", async () => {
  // A control that does nothing meaningful where it is shown teaches the person
  // that controls in this panel need not be understood — and the digest tab,
  // which opens first, is where the keys were met before they were needed.
  installTerminal();
  stubFetch((url) => (url.includes("/screen") ? answer({ body: { screen: "x" } }) : answer({ body: [] })));
  const panel = await mount();

  assert.equal(panel.root.querySelectorAll("[data-key]").length, 0, "no keys on the digest tab");

  await panel.openScreenTab();
  assert.equal(panel.root.querySelectorAll("[data-key]").length, KEYS.length, "every key on the screen tab");

  fireEvent(panel.root.querySelector('[data-tab="digest"]'), "click");
  await settle();
  assert.equal(panel.root.querySelectorAll("[data-key]").length, 0, "and gone again on the way back");
});

test("the keys carry a label saying where the press lands", async () => {
  // Four bare glyphs say nothing about their destination. The label is the only
  // thing on screen that does, so it is not allowed to be absent, empty, or the
  // untranslated name of its own dictionary key.
  installTerminal();
  stubFetch((url) => (url.includes("/screen") ? answer({ body: { screen: "x" } }) : answer({ body: [] })));
  const panel = await mount();
  await panel.openScreenTab();

  const label = panel.root.querySelector(".s-keys-label");
  assert.notEqual(label, null, "the key row has no label at all");
  assert.equal(label.textContent, t("keys_to_session"), "the label is not the panel's own string");
  assert.notEqual(label.textContent, "keys_to_session", "the label fell through to its own key name");
  assert.notEqual(label.textContent.trim(), "");
});

test("the close button is not one of the keys, and the keys are not in the header", async () => {
  // ✕ closes this panel and nothing leaves the machine; the four keys land in
  // somebody's running work. Drawn as one row of five they read as one set, and
  // the operator read them as exactly that.
  installTerminal();
  stubFetch((url) => (url.includes("/screen") ? answer({ body: { screen: "x" } }) : answer({ body: [] })));
  const panel = await mount();
  await panel.openScreenTab();

  const head = panel.root.querySelector(".s-head");
  const keys = panel.root.querySelector(".s-keys");
  assert.notEqual(head.querySelector(".s-close"), null, "the close button belongs to the header");
  assert.equal(head.querySelectorAll("[data-key]").length, 0, "no session key sits in the header row");
  assert.notEqual(keys, null, "the keys have no group of their own");
  assert.equal(keys.querySelector(".s-close"), null, "the close button is inside the key group");
  assert.equal(head.contains(keys), false, "the key group is still nested in the header");
});
