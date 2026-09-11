// The live terminal on its own (web/js/liveterminal.js), for what only the
// orchestrator column asks of it: coming back by itself.
//
// The session panel's screen tab never reconnects on its own — coming back to
// the tab is its reconnect (web/tests/session.test.js pins that). The column has
// no tab to come back to: it is on screen all the time, and without this it
// would sit on "connection lost" after every restart of the panel under it.
// What is pinned here is when it tries again, how long it waits, what it does
// not try again after, and that trying again leaves nothing doubled behind.

import { test, beforeEach, afterEach } from "node:test";
import assert from "node:assert/strict";

import { installDOM, settle } from "./fake-dom.js";
import { fakeTimers, answer, installTerminal, installFit, installSocket, installWheelEvent, ready, frame, asText } from "./terminal-fakes.js";
import { createLiveTerminal, wheelLines } from "../js/liveterminal.js";
import { t } from "../js/i18n.js";

const TOKEN = "token-for-this-test";

let dom;
let sockets;
let tokenCalls;
let tokenAnswer;
const saved = {};

beforeEach(() => {
  for (const name of ["fetch", "WebSocket", "Terminal", "FitAddon", "ResizeObserver", "WheelEvent"]) {
    saved[name] = Object.hasOwn(globalThis, name) ? globalThis[name] : undefined;
  }
  delete globalThis.ResizeObserver;
  dom = installDOM();
  sockets = installSocket();
  installFit();
  tokenCalls = 0;
  tokenAnswer = () => answer({ body: { token: TOKEN } });
  globalThis.fetch = async (url) => {
    if (!String(url).startsWith("/api/terminal-token")) throw new Error(`unexpected request ${url}`);
    tokenCalls += 1;
    return tokenAnswer();
  };
});

afterEach(() => {
  dom.restore();
  for (const [name, value] of Object.entries(saved)) {
    if (value === undefined) delete globalThis[name];
    else globalThis[name] = value;
  }
});

// A live terminal wired the way a caller wires it, with the clock recording the
// delay of every timer armed, and everything it reports kept in order.
async function start({ reconnect = true } = {}) {
  const terminals = installTerminal();
  const timers = fakeTimers();
  const delays = [];
  const setTimeout = timers.setTimeout;
  timers.setTimeout = (fn, ms) => {
    delays.push(ms);
    return setTimeout(fn, ms);
  };
  const said = { stream: [], action: [], ready: 0 };
  const host = dom.element("div");
  dom.document.body.appendChild(host);
  const live = createLiveTerminal(host, "sess-1", {
    timers,
    reconnect,
    report: {
      streamError: (m) => said.stream.push(m),
      actionError: (m) => said.action.push(m),
      ready: () => (said.ready += 1),
    },
  });
  live.open();
  await settle();
  return { live, timers, delays, said, terminals, lastStream: () => said.stream[said.stream.length - 1] ?? "" };
}

test("a terminal that reconnects comes back after its connection is lost, with the screen it had", async () => {
  const run = await start();
  ready(sockets[0]);
  sockets[0].serverSend(frame("what the session said before"));

  sockets[0].serverClose(1006);
  assert.ok(run.lastStream().includes(t("terminal_link_lost")), "it says what happened");
  assert.equal(run.lastStream().includes(t("terminal_connection_lost")), false, "without telling anyone to reopen a tab it does not have");
  assert.ok(run.lastStream().includes(t("terminal_reconnecting")), "and that it is trying again");
  assert.equal(run.timers.count(), 1, "one attempt armed");

  await run.timers.tick();
  assert.equal(sockets.length, 2, "a new socket");
  assert.equal(tokenCalls, 2, "with a token read afresh, as every socket does");
  assert.equal(run.terminals.length, 1, "into the same terminal");
  assert.equal(run.terminals[0].disposed, 0, "which kept what it had drawn");

  ready(sockets[1]);
  assert.equal(run.lastStream(), "", "and once it is back, nothing is said about the loss any more");
});

// A panel restarting is down for a moment; a panel stopped for good is down
// for as long as it takes someone to start it. The first try comes quickly,
// the rest back off, and one that got through starts the count over.
test("each failed attempt waits longer, up to a ceiling, and a success starts over", async () => {
  const run = await start();
  for (let i = 0; i < 5; i++) {
    sockets[sockets.length - 1].serverClose(1006);
    await run.timers.tick();
  }
  assert.deepEqual(run.delays, [1000, 2000, 5000, 10000, 10000]);

  ready(sockets[sockets.length - 1]);
  sockets[sockets.length - 1].serverClose(1006);
  assert.equal(run.delays[run.delays.length - 1], 1000, "a connection that came back starts the count again");
});

// Each of these is a verdict, not a hiccup: the session is gone or has ended,
// or another window took the terminal over — on Windows that is the operator's
// own terminal, and taking it back would evict them over and over. Trying
// again changes none of it.
test("an ending that trying again cannot fix is not tried again", async () => {
  for (const code of [4000, 4001, 4404]) {
    sockets.length = 0;
    const run = await start();
    ready(sockets[0]);
    sockets[0].serverClose(code, "");
    assert.equal(run.timers.count(), 0, `close ${code} armed a retry`);
    assert.equal(run.lastStream().includes(t("terminal_reconnecting")), false, `close ${code} promised a retry`);
    run.live.stop();
  }
});

test("a daemon that is away is named while the terminal waits for it", async () => {
  const run = await start();
  ready(sockets[0]);
  sockets[0].serverClose(4503, "daemon unavailable");
  assert.ok(run.lastStream().startsWith(t("terminal_daemon_unavailable")), run.lastStream());
  assert.ok(run.lastStream().includes(t("terminal_reconnecting")));
});

// The token lives as long as the panel's process. A panel that restarts
// between the page reading the token and the socket presenting it refuses the
// old one — and the next attempt reads the new process's.
test("a refused token is tried again, with a token read afresh", async () => {
  const run = await start();
  ready(sockets[0]);
  sockets[0].serverClose(4403, "");
  assert.ok(run.lastStream().startsWith(t("terminal_token_stale")), run.lastStream());
  assert.ok(run.lastStream().includes(t("terminal_reconnecting")), "a refused token was taken as final");
  assert.equal(run.lastStream().includes(t("terminal_token_refused")), false, "and told to reopen a tab it does not have");

  await run.timers.tick();
  assert.equal(sockets.length, 2);
  assert.equal(tokenCalls, 2, "the retry presented the token that was just refused");
});

test("a token that cannot be read while the panel restarts is tried again", async () => {
  tokenAnswer = () => answer({ status: 502, body: { error: "the panel is restarting" } });
  const run = await start();
  assert.equal(sockets.length, 0);
  assert.ok(run.lastStream().includes(t("terminal_token_unavailable")));
  assert.ok(run.lastStream().includes(t("terminal_reconnecting")));

  tokenAnswer = () => answer({ body: { token: TOKEN } });
  await run.timers.tick();
  assert.equal(sockets.length, 1, "the panel came back and so did the terminal");
});

test("stopping leaves no attempt armed", async () => {
  const run = await start();
  ready(sockets[0]);
  sockets[0].serverClose(1006);
  run.live.stop();
  assert.equal(run.timers.count(), 0);
  await run.timers.tick();
  assert.equal(sockets.length, 1, "a stopped terminal opened a socket");
});

test("a terminal that does not reconnect leaves a lost connection lost", async () => {
  const run = await start({ reconnect: false });
  ready(sockets[0]);
  sockets[0].serverClose(1006);
  assert.equal(run.timers.count(), 0);
  assert.equal(run.lastStream(), t("terminal_connection_lost"));
});

// Every opening wires what is typed to the socket it opens. Wired again on
// top of the old wiring, one keystroke would go into the session twice.
test("what is typed after a reconnect goes into the session once", async () => {
  const run = await start();
  ready(sockets[0]);
  sockets[0].serverClose(1006);
  await run.timers.tick();
  ready(sockets[1]);

  run.terminals[0].type("x");
  const typed = sockets[1].sent.filter((d) => typeof d !== "string");
  assert.equal(typed.length, 1);
  assert.equal(asText(typed[0]), "x");
  assert.equal(sockets[0].sent.filter((d) => typeof d !== "string").length, 0);
});

// --- the wheel ---------------------------------------------------------------
//
// A Claude Code session turns mouse tracking on, so the wheel over its terminal
// does not scroll the terminal: xterm turns it into wheel reports the session
// answers by moving its own view, one line per report (measured on a live
// session). xterm sends at most one report per wheel event, whatever distance
// the event asks for, and damps pixel steps under 50 px to a third — so a wheel
// notch moved the view one line where a page moves seven, and a trackpad moved
// it a third of the way. The rule pinned here: the view moves as far as the
// browser would scroll any page for the same event.

const CELL = 14; // px, the terminal's measured cell height at font size 12

// The terminal's element with its .xterm-screen, measured at CELL per row,
// and a record of every wheel event that reaches the element.
function giveScreen(terminal) {
  const element = dom.element("div");
  const screen = dom.element("div");
  screen.className = "xterm-screen";
  screen.getBoundingClientRect = () => ({ x: 0, y: 0, width: 400, height: terminal.rows * CELL });
  const row = dom.element("span");
  screen.appendChild(row);
  element.appendChild(screen);
  terminal.element = element;
  const replayed = [];
  element.addEventListener("wheel", (event) => replayed.push(event));
  return { row, replayed };
}

// A wheel event over the terminal, as xterm hands it to the custom handler.
function wheel(terminal, row, init) {
  const event = new WheelEvent("wheel", { clientX: 120, clientY: 80, bubbles: true, cancelable: true, ...init });
  event.target = row;
  return terminal.wheelHandler(event);
}

async function tracking() {
  installWheelEvent();
  const run = await start();
  const terminal = run.terminals[0];
  terminal.modes.mouseTrackingMode = "any";
  return { terminal, ...giveScreen(terminal) };
}

const lines = (replayed) => replayed.reduce((sum, e) => sum + e.deltaY, 0);

test("how far a wheel event asks to move, in lines, for each delta mode", () => {
  assert.equal(wheelLines({ deltaMode: 0, deltaY: 70 }, CELL, 24), 5, "pixels are divided by the cell height");
  assert.equal(wheelLines({ deltaMode: 0, deltaY: -7 }, CELL, 24), -0.5, "a pixel step smaller than a line is a fraction of one");
  assert.equal(wheelLines({ deltaMode: 1, deltaY: 3 }, CELL, 24), 3, "lines are lines");
  assert.equal(wheelLines({ deltaMode: 2, deltaY: -1 }, CELL, 24), -24, "a page is the terminal's rows");
});

test("a wheel event moves the session as far as a page would scroll, one report per line", async () => {
  const { terminal, row, replayed } = await tracking();

  const handled = wheel(terminal, row, { deltaY: -100 });

  assert.equal(handled, false, "xterm still sent its own single report");
  assert.equal(replayed.length, 7, "100 px over 14 px cells is 7 lines, and 7 reports");
  for (const event of replayed) {
    assert.equal(event.deltaMode, 1, "each report is asked for as one line");
    assert.equal(event.deltaY, -1, "upwards, as the wheel went");
  }
});

test("each replayed line is a report xterm makes itself, at the pointer, with the keys held", async () => {
  const { terminal, row, replayed } = await tracking();

  wheel(terminal, row, { deltaY: 28, altKey: true });

  assert.equal(replayed.length, 2);
  assert.equal(replayed[0].clientX, 120, "the report is not at the pointer");
  assert.equal(replayed[0].clientY, 80);
  assert.equal(replayed[0].altKey, true, "a modifier held on the wheel was dropped");
  assert.equal(terminal.wheelHandler(replayed[0]), true, "a replayed line was not let through to xterm");
});

// A trackpad sends a stream of small steps. Each is less than a line; together
// they are the distance the fingers went, and that is what the view follows.
test("small steps add up to the distance they cover, with nothing damped", async () => {
  const { terminal, row, replayed } = await tracking();

  for (let i = 0; i < 20; i++) wheel(terminal, row, { deltaY: -7 });

  assert.equal(lines(replayed), -10, "140 px of small steps is 10 lines");
});

// The inertia after a flick is more steps of the same kind, getting smaller.
// It carries the view exactly as far as it would carry a page, never further.
test("the tail of a flick carries the view as far as it would carry a page", async () => {
  const { terminal, row, replayed } = await tracking();
  let total = 0;

  for (let k = 0; k < 60; k++) {
    const step = 20 * 0.93 ** k;
    total += step;
    wheel(terminal, row, { deltaY: -step });
  }

  assert.equal(lines(replayed), -Math.trunc(total / CELL));
});

test("turning back starts from nothing, not from what was left over the other way", async () => {
  const { terminal, row, replayed } = await tracking();

  wheel(terminal, row, { deltaY: 13 }); // most of a line down: nothing yet
  wheel(terminal, row, { deltaY: -1 }); // the hand turns back
  wheel(terminal, row, { deltaY: -13 });

  assert.equal(lines(replayed), -1, "the line down that never happened ate the line up");
});

test("one event moves the view a screen at most", async () => {
  const { terminal, row, replayed } = await tracking();

  wheel(terminal, row, { deltaMode: 2, deltaY: 5 });

  assert.equal(replayed.length, terminal.rows);
});

// Without mouse tracking the terminal scrolls its own scrollback, and xterm
// already moves that by the distance the event asks for.
test("a terminal the application does not track the mouse in keeps xterm's own scrolling", async () => {
  const { terminal, row, replayed } = await tracking();
  terminal.modes.mouseTrackingMode = "none";

  assert.equal(wheel(terminal, row, { deltaY: -100 }), true);
  assert.equal(replayed.length, 0);
});

test("a wheel event with shift held is left to xterm", async () => {
  const { terminal, row, replayed } = await tracking();

  assert.equal(wheel(terminal, row, { deltaY: -100, shiftKey: true }), true);
  assert.equal(replayed.length, 0);
});

test("a terminal that cannot be measured is left to xterm", async () => {
  const { terminal, row, replayed } = await tracking();
  terminal.element = null;

  assert.equal(wheel(terminal, row, { deltaY: -100 }), true);
  assert.equal(replayed.length, 0);
});

// --- links ---------------------------------------------------------------------

// A terminal made with the given links, opened, and the link provider it
// registered (or none).
async function linked(links) {
  const terminals = installTerminal();
  const host = dom.element("div");
  dom.document.body.appendChild(host);
  const live = createLiveTerminal(host, "sess-1", { timers: fakeTimers(), links });
  live.open();
  await settle();
  return { live, terminal: terminals[0] };
}

test("a terminal given links makes the session's wiki links open what they name", async () => {
  const opened = [];
  const { terminal } = await linked({
    resolve: (name) => (name === "plan-a" ? "/cards/plan-a.md" : null),
    open: (path) => opened.push(path),
  });
  assert.equal(terminal.linkProviders?.length, 1, "no link provider was registered");

  // A row as the terminal's buffer hands it out, with one link on it.
  const text = "see [[plan-a]] here";
  terminal.cols = text.length;
  terminal.buffer = {
    active: {
      getLine: (y) => (y === 0 ? { getCell: (x) => (x < text.length ? { getChars: () => text[x], getWidth: () => 1 } : undefined) } : undefined),
    },
  };
  let links;
  terminal.linkProviders[0].provideLinks(1, (found) => (links = found));
  assert.equal(links?.length, 1);
  links[0].activate();
  assert.deepEqual(opened, ["/cards/plan-a.md"], "the link did not open the card through the links it was given");
});

test("a terminal given no links links nothing", async () => {
  const { terminal } = await linked(null);
  assert.equal(terminal.linkProviders?.length ?? 0, 0);
});
