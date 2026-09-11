// The session panel, driven through the same handlers a person's clicks reach.
//
// This is the part of the interface that writes into a live Claude Code session,
// and what is worth pinning about it is wiring rather than any pure function:
// that the digest's polling never multiplies and the screen holds exactly one
// socket, that every failure reaches the screen as words — a digest poll is
// retried, a terminal stream deliberately is not — that a key button sends bytes
// rather than the word printed on it, and that text which could not be sent is
// still in the box.
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
function installTerminal({ cols = 80, rows = 24 } = {}) {
  const made = [];
  globalThis.Terminal = class {
    constructor(options) {
      this.options = options;
      this.writes = [];
      this.resets = 0;
      this.disposed = 0;
      this.host = null;
      // xterm's own default geometry, which is what the panel's terminal is built
      // with while it has no fit to a container.
      this.cols = cols;
      this.rows = rows;
      this.dataListeners = [];
      made.push(this);
    }
    open(host) {
      this.host = host;
    }
    // xterm hands the terminal to an addon's activate() when it is loaded.
    loadAddon(addon) {
      addon.activate(this);
    }
    resize(cols, rows) {
      this.cols = cols;
      this.rows = rows;
    }
    onData(fn) {
      this.dataListeners.push(fn);
      return { dispose: () => (this.dataListeners = this.dataListeners.filter((f) => f !== fn)) };
    }
    // What xterm does when a person types into it: hands the characters to every
    // onData listener.
    type(data) {
      for (const fn of this.dataListeners) fn(data);
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

// The fit addon the panel finds on the global object, which is where
// web/vendor/addon-fit.js puts the real one. `pane` is the size the real addon
// would measure the terminal's element at: "own" answers with the terminal's
// current size, so every test that is not about fitting sees no change; null is
// a pane with nothing to measure yet, which the real addon answers with nothing.
function installFit(pane = "own") {
  const made = [];
  globalThis.FitAddon = {
    FitAddon: class {
      constructor() {
        this.terminal = null;
        made.push(this);
      }
      activate(terminal) {
        this.terminal = terminal;
      }
      dispose() {}
      proposeDimensions() {
        // Like the real one: a terminal not yet opened into an element has no
        // size to propose.
        if (!this.terminal?.host || pane === null) return undefined;
        return pane === "own" ? { cols: this.terminal.cols, rows: this.terminal.rows } : { ...pane };
      }
      fit() {
        const dims = this.proposeDimensions();
        if (dims) this.terminal.resize(dims.cols, dims.rows);
      }
    },
  };
  return made;
}

// The socket the screen tab opens for its live terminal: a browser's WebSocket
// reduced to what the panel touches, with the server's side driven by hand.
function installSocket() {
  const opened = [];
  globalThis.WebSocket = class {
    static CONNECTING = 0;
    static OPEN = 1;
    static CLOSING = 2;
    static CLOSED = 3;
    constructor(url) {
      this.url = url;
      this.readyState = 0;
      this.binaryType = "blob";
      this.sent = [];
      this.closedWith = null;
      this.onopen = null;
      this.onmessage = null;
      this.onclose = null;
      opened.push(this);
    }
    send(data) {
      this.sent.push(data);
    }
    close(code, reason) {
      if (this.readyState === 3) return;
      this.closedWith = { code, reason };
      this.readyState = 3;
    }
    // The server's side.
    serverOpen() {
      this.readyState = 1;
      this.onopen?.({});
    }
    serverSend(data) {
      this.onmessage?.({ data });
    }
    serverClose(code, reason = "") {
      this.readyState = 3;
      this.onclose?.({ code, reason });
    }
  };
  return opened;
}

// What the bridge says first on a socket it has attached.
function ready(socket, writable = true) {
  socket.serverOpen();
  socket.serverSend(JSON.stringify({ type: "ready", writable }));
}

// A frame the panel sent, as text. Keystrokes go out as bytes.
const asText = (data) => new TextDecoder().decode(data instanceof ArrayBuffer ? new Uint8Array(data) : data);

// Bytes as the server sends them: an ArrayBuffer, because the panel asks for one.
const frame = (s) => new TextEncoder().encode(s).buffer;

let sockets;
let realSocket;
let realFit;

beforeEach(() => {
  dom = installDOM();
  calls = [];
  realFetch = globalThis.fetch;
  realTerminal = Object.hasOwn(globalThis, "Terminal") ? globalThis.Terminal : undefined;
  realSocket = Object.hasOwn(globalThis, "WebSocket") ? globalThis.WebSocket : undefined;
  realFit = Object.hasOwn(globalThis, "FitAddon") ? globalThis.FitAddon : undefined;
  sockets = installSocket();
  installFit();
});

afterEach(() => {
  dom.restore();
  globalThis.fetch = realFetch;
  if (realTerminal === undefined) delete globalThis.Terminal;
  else globalThis.Terminal = realTerminal;
  if (realSocket === undefined) delete globalThis.WebSocket;
  else globalThis.WebSocket = realSocket;
  if (realFit === undefined) delete globalThis.FitAddon;
  else globalThis.FitAddon = realFit;
});

async function mount({ lookup = () => ({ short: SHORT, sessionId: FULL }) } = {}) {
  const root = dom.element("div");
  // In the page before the panel draws into it, as it is in a browser: a node
  // outside the document has no layout, and anything measured against it reads
  // zero.
  dom.document.body.appendChild(root);
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
  const noticeText = () => {
    const line = root.querySelector(".s-notice");
    return line.hidden ? "" : line.textContent;
  };
  return {
    root,
    timers,
    stop,
    errorText,
    noticeText,
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
  // id each wanted. The terminal socket reaches the daemon, so it is the short one.
  installTerminal();
  stubFetch(answer({ body: [] }));
  const panel = await mount();

  assert.equal(calls[0].url, `/api/sessions/${FULL}/digest?limit=30`);

  await panel.openScreenTab();
  assert.equal(new URL(sockets[0].url).pathname, `/api/sessions/${SHORT}/pty`);

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
  stubFetch(answer({ body: [] }));
  const panel = await mount();
  await panel.openScreenTab();

  assert.equal(panel.timers.count(), 0, "the digest's timer went with the digest tab");
  assert.equal(terminals.length, 1);

  fireEvent(panel.root.querySelector('[data-tab="digest"]'), "click");
  await settle();
  assert.equal(panel.timers.count(), 1, "one timer, not one per tab visited");
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




// --- the live terminal -------------------------------------------------------
//
// The screen tab is a held terminal, not a polled picture. What it has to keep
// from the polled one: one connection and no more, however long it stays open;
// a failure the operator can read; whatever was drawn staying drawn. What it
// adds: the session's bytes arrive as they happen, and typing goes straight in.

test("the screen tab opens exactly one socket and arms no timer, however long it runs", async () => {
  installTerminal();
  stubFetch(answer({ body: [] }));
  const panel = await mount();
  await panel.openScreenTab();
  ready(sockets[0]);

  for (let tick = 1; tick <= 8; tick += 1) {
    await panel.timers.tick();
  }
  assert.equal(sockets.length, 1, "one socket, not one per tick");
  assert.equal(panel.timers.count(), 0, "a stream needs no timer");
  assert.equal(calls.filter((c) => c.url.includes("/screen")).length, 0, "and no screen poll behind it");
});

test("the socket goes to this session's terminal route and asks for the terminal's own size", async () => {
  // The size is the terminal's, not a number of the panel's own: attaching at it
  // resizes the session for everyone watching, so it has to be what is drawn here.
  // Not 80x24 on purpose — that is also the fallback the panel would reach for.
  const terminals = installTerminal({ cols: 132, rows: 41 });
  stubFetch(answer({ body: [] }));
  const panel = await mount();
  await panel.openScreenTab();

  const url = new URL(sockets[0].url);
  assert.equal(url.protocol, "ws:");
  assert.equal(url.pathname, `/api/sessions/${SHORT}/pty`);
  assert.equal(url.searchParams.get("cols"), String(terminals[0].cols));
  assert.equal(url.searchParams.get("rows"), String(terminals[0].rows));
  assert.equal(sockets[0].binaryType, "arraybuffer", "terminal bytes are read as bytes, not as a Blob");
});

// Attaching sets the session's size for everyone watching it, so the size the
// socket asks for has to be the pane's, measured before the socket exists —
// resizing after the attach would reshape the session twice.
test("the terminal is fitted to its pane before the socket asks for a size", async () => {
  const terminals = installTerminal();
  const fits = installFit({ cols: 173, rows: 52 });
  stubFetch(answer({ body: [] }));
  const panel = await mount();
  await panel.openScreenTab();

  assert.equal(fits.length, 1, "one fit addon for the one terminal");
  assert.equal(fits[0].terminal, terminals[0], "loaded into the terminal it measures");
  assert.equal(terminals[0].cols, 173);
  assert.equal(terminals[0].rows, 52);
  const url = new URL(sockets[0].url);
  assert.equal(url.searchParams.get("cols"), "173", "the attach asked for the fitted width");
  assert.equal(url.searchParams.get("rows"), "52", "the attach asked for the fitted height");
  assert.equal(panel.noticeText(), "", "a fitted terminal has nothing to say about its size");
});

// The real addon's fit() returns without a word when it has nothing to measure,
// and a missing script tag reports nothing to anybody. Either would leave a
// terminal at its default size in a larger pane, with the session reshaped to
// match, and nothing on screen saying why.
test("a terminal the fit addon cannot size is drawn at its own size, says so, and still connects", async () => {
  for (const [label, setup] of [
    ["addon missing", () => delete globalThis.FitAddon],
    ["nothing to measure", () => installFit(null)],
  ]) {
    sockets.length = 0;
    const terminals = installTerminal({ cols: 80, rows: 24 });
    setup();
    stubFetch(answer({ body: [] }));
    const panel = await mount();
    await panel.openScreenTab();

    assert.equal(sockets.length, 1, `${label}: the terminal still connects`);
    const url = new URL(sockets[0].url);
    assert.equal(url.searchParams.get("cols"), String(terminals[0].cols), `${label}: the attach asked for the size drawn`);
    assert.ok(panel.noticeText().includes(t("terminal_not_fitted")), `${label}: and it says why`);
    panel.stop();
  }
});

// What the terminal says about itself — that it cannot type, that it is not
// the size of its pane — holds for as long as the stream does. A sent message
// clears the notice line (web/js/session.js, submitTyped), and a ready frame
// used to overwrite it; neither may take these two sentences with it. And they
// belong to the screen tab, so they leave with it.
test("what the terminal says about itself outlasts a sent message and leaves with the tab", async () => {
  installTerminal();
  installFit(null);
  stubFetch((url) => (url.includes("/text") ? answer({ status: 204 }) : answer({ body: [] })));
  const panel = await mount();
  await panel.openScreenTab();
  ready(sockets[0], false);

  const standing = () => {
    const text = panel.noticeText();
    return { readOnly: text.includes(t("terminal_read_only")), notFitted: text.includes(t("terminal_not_fitted")) };
  };
  assert.deepEqual(standing(), { readOnly: true, notFitted: true }, "both are said once the stream is ready");

  panel.input().value = "hello";
  await panel.pressEnter();
  assert.equal(calls.filter((c) => c.url.includes("/text")).length, 1, "the message was sent");
  assert.deepEqual(standing(), { readOnly: true, notFitted: true }, "a sent message took them off screen");

  fireEvent(panel.root.querySelector('[data-tab="digest"]'), "click");
  await settle();
  assert.equal(panel.noticeText(), "", "the digest tab has no terminal to talk about");
});

test("a stream that has ended no longer says it cannot type", async () => {
  installTerminal();
  stubFetch(answer({ body: [] }));
  const panel = await mount();
  await panel.openScreenTab();
  ready(sockets[0], false);
  assert.equal(panel.noticeText(), t("terminal_read_only"));

  sockets[0].serverClose(4000);
  assert.equal(panel.noticeText(), "", "there is no stream left to be read-only");
  assert.equal(panel.errorText(), t("terminal_session_ended"), "the ending is what is said instead");
});

test("the session's bytes are written to the terminal as they arrive, never by redrawing it", async () => {
  const terminals = installTerminal();
  stubFetch(answer({ body: [] }));
  const panel = await mount();
  await panel.openScreenTab();
  ready(sockets[0]);

  sockets[0].serverSend(frame("[1mfirst"));
  sockets[0].serverSend(frame(" second"));

  const written = terminals[0].writes.map((w) => asText(w)).join("");
  assert.equal(written, "[1mfirst second");
  assert.equal(terminals[0].resets, 0, "a stream is appended to, not reset per frame");
});

test("what the operator types into the terminal goes into the socket as bytes", async () => {
  const terminals = installTerminal();
  stubFetch(answer({ body: [] }));
  const panel = await mount();
  await panel.openScreenTab();
  ready(sockets[0]);

  terminals[0].type("ls -la\r");

  assert.equal(sockets[0].sent.length, 1);
  assert.equal(typeof sockets[0].sent[0], "object", "a keystroke is a binary frame; text frames are control messages");
  assert.equal(asText(sockets[0].sent[0]), "ls -la\r");
});

test("every key button sends its escape sequence into the socket, and nothing through POST .../keys", async () => {
  // POST .../keys opens an attach of its own on every press, and an attach
  // resizes the session: with a live terminal open that would be a resize per
  // key. The buttons go through the one connection the terminal already holds.
  installTerminal();
  stubFetch(answer({ body: [] }));
  const panel = await mount();
  await panel.openScreenTab();
  ready(sockets[0]);

  const expected = { escape: "", up: "[A", down: "[B", enter: "\r" };
  for (const key of KEYS) {
    await panel.click(`[data-key=${key.id}]`);
    const sent = asText(sockets[0].sent[sockets[0].sent.length - 1]);
    assert.equal(sent, expected[key.id], `${key.id} must send its bytes`);
    assert.notEqual(sent, key.id);
    assert.notEqual(sent, key.label);
  }
  assert.equal(calls.filter((c) => c.url.includes("/keys")).length, 0, "no key went out as a request");
});

test("a terminal that cannot type says so as soon as it opens, and typing sends nothing", async () => {
  const terminals = installTerminal();
  stubFetch(answer({ body: [] }));
  const panel = await mount();
  await panel.openScreenTab();
  ready(sockets[0], false);

  const notice = panel.root.querySelector(".s-notice");
  assert.equal(notice.hidden, false, "read-only is said before anybody types");
  assert.equal(notice.textContent, t("terminal_read_only"));

  terminals[0].type("x");
  await panel.click("[data-key=enter]");
  assert.equal(sockets[0].sent.length, 0, "nothing went into a session through a terminal without a key");
  assert.equal(panel.errorText(), t("terminal_read_only"), "and the refusal is on screen");
});

test("a key pressed before the terminal is connected is refused where it can be seen", async () => {
  installTerminal();
  stubFetch(answer({ body: [] }));
  const panel = await mount();
  await panel.openScreenTab();

  await panel.click("[data-key=enter]");
  assert.equal(sockets[0].sent.length, 0);
  assert.equal(panel.errorText(), t("terminal_not_connected"));
});

test("an error the bridge reports is shown", async () => {
  installTerminal();
  stubFetch(answer({ body: [] }));
  const panel = await mount();
  await panel.openScreenTab();
  ready(sockets[0]);

  sockets[0].serverSend(JSON.stringify({ type: "error", error: "resize failed: session is gone" }));
  assert.equal(panel.errorText(), "resize failed: session is gone");
});

test("a stream that ends says why, for every way it can end", async () => {
  const cases = [
    [4000, "", t("terminal_session_ended")],
    [4001, "kicked: Session opened in another window", `${t("terminal_kicked")}: Session opened in another window`],
    [4404, "no such session", t("terminal_no_session")],
    [4401, "", t("terminal_key_refused")],
    [4503, "", t("terminal_daemon_unavailable")],
    [1006, "", t("terminal_connection_lost")],
    [1011, "attach failed: boom", `${t("terminal_connection_lost")}: attach failed: boom`],
  ];
  for (const [code, reason, want] of cases) {
    sockets.length = 0;
    installTerminal();
    stubFetch(answer({ body: [] }));
    const panel = await mount();
    await panel.openScreenTab();
    ready(sockets[0]);
    sockets[0].serverClose(code, reason);
    assert.equal(panel.errorText(), want, `close ${code}`);
    panel.stop();
  }
});

// The polled screen used to retry by itself. A stream must not: on a daemon that
// evicts the previous attacher (Windows), reconnecting on its own would take the
// operator's terminal back from them, again and again. Coming back to the tab is
// the reconnect.
test("an ended stream is not reopened by itself, and coming back to the tab opens a new one", async () => {
  installTerminal();
  stubFetch(answer({ body: [] }));
  const panel = await mount();
  await panel.openScreenTab();
  ready(sockets[0]);
  sockets[0].serverClose(4001, "kicked: Session opened in another window");

  await panel.timers.tick();
  await settle();
  assert.equal(sockets.length, 1, "no socket was opened behind the operator's back");

  fireEvent(panel.root.querySelector('[data-tab="digest"]'), "click");
  await settle();
  await panel.openScreenTab();
  assert.equal(sockets.length, 2, "coming back to the tab is the reconnect");
});

test("what the stream drew before it ended stays on screen", async () => {
  const terminals = installTerminal();
  stubFetch(answer({ body: [] }));
  const panel = await mount();
  await panel.openScreenTab();
  ready(sockets[0]);
  sockets[0].serverSend(frame("the last thing the session said"));
  sockets[0].serverClose(4000);

  assert.equal(terminals[0].disposed, 0, "the terminal is kept, not thrown away with the stream");
  assert.equal(terminals[0].resets, 0);
  assert.equal(terminals[0].writes.map((w) => asText(w)).join(""), "the last thing the session said");
});

test("leaving the tab, closing the panel or stopping it closes the socket", async () => {
  // A socket left open is an attach left open on the daemon, with this panel's
  // geometry still set on somebody's session.
  installTerminal();
  stubFetch(answer({ body: [] }));

  let panel = await mount();
  await panel.openScreenTab();
  fireEvent(panel.root.querySelector('[data-tab="digest"]'), "click");
  await settle();
  assert.notEqual(sockets[0].closedWith, null, "tab switch");

  panel = await mount();
  await panel.openScreenTab();
  await panel.click(".s-close");
  assert.notEqual(sockets[1].closedWith, null, "close button");

  panel = await mount();
  await panel.openScreenTab();
  panel.stop();
  assert.notEqual(sockets[2].closedWith, null, "stop");
});

test("a panel's own close is not reported as a lost connection", async () => {
  installTerminal();
  stubFetch(answer({ body: [] }));
  const panel = await mount();
  await panel.openScreenTab();
  ready(sockets[0]);
  const socket = sockets[0];
  fireEvent(panel.root.querySelector('[data-tab="digest"]'), "click");
  await settle();
  // A browser delivers onclose for a close the page asked for, too.
  socket.serverClose(1000, "panel closed");
  assert.equal(panel.errorText(), "", "switching away is not a failure");
});

// A socket the panel has let go of can still deliver what was already in
// flight. None of it may reach a terminal: the one it was drawing into has been
// disposed, and writing into a disposed xterm is an error in a real browser.
test("bytes still in flight on a socket the panel has let go of go nowhere", async () => {
  const terminals = installTerminal();
  stubFetch(answer({ body: [] }));
  const panel = await mount();
  await panel.openScreenTab();
  ready(sockets[0]);
  const old = sockets[0];
  fireEvent(panel.root.querySelector('[data-tab="digest"]'), "click");
  await settle();

  old.onmessage?.({ data: frame("late") });
  assert.equal(terminals[0].writes.length, 0, "a late frame was written into a disposed terminal");
});

// The other end of the card panel's Escape rule (web/tests/card.test.js): that
// test builds its own marked element, so it cannot tell whether the terminal
// this panel draws is marked at all. Without the mark, Escape typed into the
// session closes a card on its way to interrupting the session.
test("the terminal is drawn inside an element marked as a terminal", async () => {
  const terminals = installTerminal();
  stubFetch(answer({ body: [] }));
  const panel = await mount();
  await panel.openScreenTab();

  assert.notEqual(terminals[0].host, null, "the terminal was never opened into the page");
  assert.notEqual(terminals[0].host.closest("[data-terminal]"), null, "what the terminal is drawn into carries no data-terminal mark");
});

test("a missing WebSocket or terminal library is a visible error, and opens nothing", async () => {
  delete globalThis.Terminal;
  stubFetch(answer({ body: [] }));
  let panel = await mount();
  await panel.openScreenTab();
  assert.notEqual(panel.errorText(), "");
  assert.equal(sockets.length, 0, "no socket for a terminal that could not be built");
  panel.stop();

  installTerminal();
  delete globalThis.WebSocket;
  panel = await mount();
  await panel.openScreenTab();
  assert.notEqual(panel.errorText(), "");
});

// --- writing into the session ----------------------------------------------



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
  // A step's text is rendered by web/js/steps.js now, the same renderer the
  // orchestrator column uses, so it arrives as markdown rather than as a flat
  // string. The invariant this test was always about is unchanged and is what
  // is asserted here: markup inside a step becomes characters, never nodes.
  const second = drawn[1].querySelector(".step-body");
  assert.ok(second.innerHTML.includes("&lt;img src=x onerror=alert(1)&gt;"), "shown as the text it is");
  assert.ok(!second.innerHTML.includes("<img"), "and never as an element");
  assert.equal(drawn[1].querySelectorAll("img").length, 0);
  // A role the panel does not know is not carried into a class name.
  assert.equal(drawn[2].className, "s-step s-step-other");
  assert.equal(drawn[0].className, "s-step s-step-user");
});

test("the session panel unwraps an envelope and renders markdown, like the other pane", async () => {
  // The defect the operator found: this pane drew steps with its own code and
  // had none of what the orchestrator column had learned. Both draw with the
  // same renderer now, and this is the test that says so from this side.
  stubFetch(
    answer({
      body: [
        { role: "user", text: '<agent-message id="m-1" from="06a1f607" at="2026-09-10T15:00:00+05:00">**bold** here</agent-message>' },
        { role: "assistant", text: "a sentence naming <agent-message> stays whole" },
      ],
    }),
  );
  const panel = await mount();
  const drawn = panel.root.querySelectorAll(".s-step");

  const from = drawn[0].querySelector(".step-from");
  assert.ok(from, "the envelope became an attribution line");
  assert.equal(from.textContent, "06a1f607 · 2026-09-10T15:00:00+05:00");
  assert.ok(drawn[0].querySelector(".step-body").innerHTML.includes("<strong>bold</strong>"), "and the body is markdown");

  assert.equal(drawn[1].querySelector(".step-from"), null, "prose that merely names the tag is not an envelope");
  assert.ok(drawn[1].querySelector(".step-body").innerHTML.includes("&lt;agent-message&gt;"), "and keeps its sentence");
});

test("an unchanged step is not redrawn when the digest polls again", async () => {
  // The other half of what this pane was missing: it rebuilt every step on
  // every poll, which loses a selection and drags the pane to the bottom.
  const steps = [{ role: "user", text: "first" }, { role: "assistant", text: "second" }];
  stubFetch(answer({ body: steps }));
  const panel = await mount();

  const rows = panel.root.querySelectorAll(".s-step");
  const firstRow = rows[0];
  const firstBody = firstRow.querySelector(".step-body");
  const writesBefore = firstBody.htmlWrites;

  await panel.timers.tick();

  const after = panel.root.querySelectorAll(".s-step");
  assert.equal(after[0], firstRow, "the same node, not an identical replacement");
  assert.equal(after[0].querySelector(".step-body"), firstBody, "and the same body inside it");
  assert.equal(firstBody.htmlWrites, writesBefore, "an unchanged step must not be re-rendered");
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

// --- which session this panel is pointing at -------------------------------
//
// The label under the keys says they are pressed in a live session. Which one
// was answered nowhere on screen, and "whose session did I just press ↓ in" is
// the same question the operator asked in the first place, one step further on.

test("the header names the session the panel has open", async () => {
  stubFetch(answer({ body: [] }));
  const panel = await mount({ lookup: () => ({ short: SHORT, sessionId: FULL, name: "fleetdeck server" }) });

  const who = panel.root.querySelector(".s-who");
  assert.notEqual(who, null, "nothing in the header names the session");
  assert.equal(who.textContent, "fleetdeck server");
  // The short id is the identity the operator can match against the session
  // list, and two sessions may carry the same name.
  // Set as a property, the way every other title in this panel is: the fake DOM
  // reflects neither direction, and a browser reflects both.
  assert.equal(who.title, SHORT);

  // Resolved on every pass, but written only when it changed: a header rewritten
  // once a second drops a selection inside it and costs work for nothing.
  const writes = who.textWrites;
  await panel.timers.tick();
  await panel.timers.tick();
  assert.equal(who.textWrites, writes, "the header is rewritten on every poll");
});

test("a session with no name yet is named by its short id, never by nothing", async () => {
  // A blank space says the panel does not know where it points. The short id is
  // what the session list shows when a session has no name of its own.
  stubFetch(answer({ body: [] }));
  const panel = await mount({ lookup: () => ({ short: SHORT, sessionId: FULL }) });

  assert.equal(panel.root.querySelector(".s-who").textContent, SHORT);
});

test("a panel opened before the first snapshot names the session once it appears", async () => {
  // The same defect, and the same fix, as the full session id below it: captured
  // when the panel opens, the name of a session that was not in the snapshot yet
  // stays missing for as long as the panel stays open — and the panel is opened
  // from a list that is itself drawn from that snapshot, so the race is ordinary.
  let known;
  stubFetch(answer({ body: [] }));
  const panel = await mount({ lookup: () => known });

  assert.equal(panel.root.querySelector(".s-who").textContent, SHORT, "before the snapshot: the short id");

  known = { short: SHORT, sessionId: FULL, name: "fleetdeck server" };
  await panel.timers.tick();

  assert.equal(
    panel.root.querySelector(".s-who").textContent,
    "fleetdeck server",
    "after the snapshot: named, without being reopened",
  );
});

// --- what the person typed -------------------------------------------------

test("text typed and not sent survives a tab switch, in both directions", async () => {
  // This panel already holds the rule that losing somebody's words is the one
  // failure it must not have — text that fails to send comes back into the box.
  // A tab switch is not even a failure, and switching to the screen to see what
  // you are about to answer is exactly when a half-written answer exists.
  installTerminal();
  stubFetch((url) => (url.includes("/screen") ? answer({ body: { screen: "x" } }) : answer({ body: [] })));
  const panel = await mount();

  const typed = "  half an answer\n  with two lines  ";
  panel.input().value = typed;

  await panel.openScreenTab();
  assert.equal(panel.input().value, typed, "gone on the way to the screen tab");

  fireEvent(panel.root.querySelector('[data-tab="digest"]'), "click");
  await settle();
  assert.equal(panel.input().value, typed, "gone on the way back");
});

// --- a sent message on screen at once ---------------------------------------
//
// Measured before any of this was written: from the keystroke to the text
// appearing, the request itself takes about six milliseconds and the rest is
// waiting for the message to come back out of the transcript through a poll —
// a second in the common case, ten in the worst run seen. Meanwhile the box
// emptied and the thread did not change, which reads as "it did not send".

// A step's text is markdown assigned as innerHTML, and the stand-in DOM's
// textContent does not see through that — a probe reading textContent finds
// nothing for ANY step, including the ones the server sent, so it reports the
// same "not there" whether the feature works or not. Read the bodies.
const bodies = (panel) => [...panel.root.querySelectorAll(".step-body")].map((n) => n.innerHTML).join("\n");

test("a message is in the thread before any poll brings it back", async () => {
  stubFetch(answer({ body: [{ role: "assistant", text: "готово" }] }));
  const panel = await mount();
  assert.match(bodies(panel), /готово/, "control: the probe can see a step that is definitely drawn");

  panel.input().value = "перезапусти панель";
  await panel.pressEnter();

  // No tick: this is the state of the thread between polls, which is where the
  // whole wait used to live. Break it by drawing only what the server sent and
  // this test fails with the message nowhere.
  assert.match(bodies(panel), /перезапусти панель/);
});

test("and it is gone again if the send failed", async () => {
  stubFetch((url) => {
    if (url.includes("/text")) return answer({ status: 502, body: { error: "the daemon went away" } });
    return answer({ body: [{ role: "assistant", text: "готово" }] });
  });
  const panel = await mount();

  panel.input().value = "перезапусти панель";
  await panel.pressEnter();

  // Both halves matter: a message that stayed on screen would say it reached
  // the session, and text that vanished from the box would be lost outright.
  assert.doesNotMatch(bodies(panel), /перезапусти панель/, "a message nobody received was left on screen");
  assert.equal(panel.input().value, "перезапусти панель", "and the words were lost with it");
  assert.match(panel.errorText(), /daemon went away/);
});

test("when the poll brings the real one, it is there once", async () => {
  let sent = false;
  stubFetch((url) => {
    if (url.includes("/text")) {
      sent = true;
      return answer({ body: null, status: 204 });
    }
    return answer({ body: sent ? [{ role: "user", text: "перезапусти панель" }] : [] });
  });
  const panel = await mount();

  panel.input().value = "перезапусти панель";
  await panel.pressEnter();
  await panel.timers.tick();

  const rows = [...panel.root.querySelectorAll(".s-step")].filter((r) =>
    (r.querySelector(".step-body")?.innerHTML ?? "").includes("перезапусти панель"),
  );
  assert.equal(rows.length, 1, `the message is on screen ${rows.length} times`);
});

test("the thread's scrolling boxes are measured after they are in the page", async () => {
  stubFetch(answer({ body: [{ role: "assistant", text: "| a | b |\n| --- | --- |\n| 1 | 2 |" }] }));
  await mount();
  // The same defect the card panel had, in the column that shows a session's
  // conversation: fillStep builds a body, renders markdown into it, and appends
  // it to the row afterwards. Measured at build time, a wide table or a long
  // command line in a step never got the fade that says there is more to the
  // right — and nothing on screen showed the mark was missing.
  const searches = dom.document.searches.filter((s) => s.selector.includes("md-table"));
  assert.deepEqual(
    searches.filter((s) => !s.connected),
    [],
    "measured before the step was in the page",
  );
  assert.ok(searches.length > 0, "and measured at all");
});
