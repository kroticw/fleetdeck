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
import { fakeTimers, answer, installTerminal, installFit, installSocket, ready, frame, asText } from "./terminal-fakes.js";
import { createLiveTerminal } from "../js/liveterminal.js";
import { t } from "../js/i18n.js";

const TOKEN = "token-for-this-test";

let dom;
let sockets;
let tokenCalls;
let tokenAnswer;
const saved = {};

beforeEach(() => {
  for (const name of ["fetch", "WebSocket", "Terminal", "FitAddon", "ResizeObserver"]) {
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
// another window took the terminal over — on Windows that is the operator's
// own terminal, and taking it back would evict them over and over — or the
// panel refused the token. Trying again changes none of it.
test("an ending that trying again cannot fix is not tried again", async () => {
  for (const code of [4000, 4001, 4403, 4404]) {
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
