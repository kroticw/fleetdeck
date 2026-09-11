// web/js/_tests/store.test.js
//
// store.js is the module every other module reads state from, and until now it
// was the only one in web/js/ with no tests at all.
//
// It keeps its state at module scope — the snapshot, the connected flag, the
// listener set, whether connect() has already run — which is right for a module
// there is exactly one of in a page, and awkward for a test file where each case
// needs a store that has not seen the previous case. So each case imports the
// module under its own URL query: node caches an ES module by resolved URL, so
// "../store.js?case=parse" and "../store.js?case=drop" are two separate module
// instances with two separate sets of state. Nothing in store.js is arranged for
// the tests; the tests are arranged around it.
//
// Lives under _tests/ so web/embed.go's plain (non "all:") directory pattern
// excludes it: a leading underscore keeps this subtree out of the binary and off
// the HTTP surface.
//
// Run with: node --test web/js/_tests/store.test.js

import test from "node:test";
import assert from "node:assert/strict";

// The sockets a case has opened, oldest first, so a test can drive the one it
// means — including an old one, to prove it has been detached.
let sockets = [];
// Every setTimeout the module asked for: [delayMs, callback]. Reconnects are
// scheduled, never immediate, so a test has to be able to see the delay and
// decide when (or whether) to let it run.
let timers = [];

class FakeSocket {
  constructor(url) {
    this.url = url;
    this.closed = false;
    this.onmessage = null;
    this.onclose = null;
    this.onerror = null;
    sockets.push(this);
  }

  close() {
    this.closed = true;
    // A real socket's close() leads to onclose; the module relies on that to
    // schedule its reconnect, so the fake must do it too.
    this.onclose?.();
  }

  // --- test-side drivers, named for what the browser would be doing ---
  deliver(data) {
    this.onmessage?.({ data });
  }

  drop() {
    this.onclose?.();
  }

  fail() {
    this.onerror?.();
  }
}

// A fresh store, with the browser globals it reaches for. Returns the module and
// leaves `sockets`/`timers` empty, so a case never sees the previous one's.
async function freshStore(caseName, { protocol = "http:", host = "127.0.0.1:7777", search = "" } = {}) {
  sockets = [];
  timers = [];
  globalThis.WebSocket = FakeSocket;
  globalThis.location = { protocol, host, search };
  globalThis.setTimeout = (fn, ms) => {
    timers.push([ms, fn]);
    return timers.length;
  };
  return import(`../store.js?case=${caseName}`);
}

// Run the reconnect the module scheduled, the way the browser's timer would.
function runScheduledReconnect() {
  const timer = timers.shift();
  assert.ok(timer, "the store must schedule a reconnect, not give up on the socket");
  timer[1]();
  return timer[0];
}

const SNAPSHOT = { sessions: [{ short: "aa11" }], cards: [], at: "2026-09-10T00:00:00Z" };

// --- what a subscriber is promised ---

test("a subscriber is called immediately, with null, before any frame has arrived", async () => {
  const store = await freshStore("immediate");
  const seen = [];
  store.subscribe((snap, connected) => seen.push([snap, connected]));
  assert.deepEqual(seen, [[null, false]], "a module must be told there is nothing yet, not left waiting");
  assert.equal(store.get(), null);
  assert.equal(store.isConnected(), false);
});

test("a good frame reaches get(), isConnected() and every subscriber", async () => {
  const store = await freshStore("good-frame");
  const seen = [];
  store.subscribe((snap, connected) => seen.push([snap, connected]));
  store.connect();
  sockets[0].deliver(JSON.stringify(SNAPSHOT));

  assert.deepEqual(store.get(), SNAPSHOT);
  assert.equal(store.isConnected(), true);
  assert.deepEqual(seen.at(-1), [SNAPSHOT, true]);
});

test("unsubscribing stops the calls, and does not disturb the other subscribers", async () => {
  const store = await freshStore("unsubscribe");
  const kept = [];
  const dropped = [];
  const unsubscribe = store.subscribe((snap) => dropped.push(snap));
  store.subscribe((snap) => kept.push(snap));
  store.connect();

  unsubscribe();
  sockets[0].deliver(JSON.stringify(SNAPSHOT));

  assert.equal(dropped.length, 1, "only the immediate call, nothing after unsubscribing");
  assert.equal(kept.length, 2, "the remaining subscriber still gets the frame");
});

// --- a dropped socket must be visible, and must not erase the board ---

test("a dropped socket turns connected off and says so, keeping the last snapshot", async () => {
  const store = await freshStore("drop");
  const seen = [];
  store.connect();
  sockets[0].deliver(JSON.stringify(SNAPSHOT));
  store.subscribe((snap, connected) => seen.push([snap, connected]));

  sockets[0].drop();

  assert.equal(store.isConnected(), false, "a stale board that looks live is the failure this prevents");
  assert.deepEqual(store.get(), SNAPSHOT, "the last known state stays; it is stale, not gone");
  assert.deepEqual(seen.at(-1), [SNAPSHOT, false], "subscribers are told, not left to notice");
});

test("an error on the socket closes it, which is what starts the reconnect", async () => {
  const store = await freshStore("error");
  store.connect();
  sockets[0].fail();
  assert.equal(sockets[0].closed, true);
  assert.equal(store.isConnected(), false);
});

// --- a frame that does not parse ---

test("a frame that is not JSON drops connected rather than being ignored", async () => {
  const store = await freshStore("garbage");
  const errors = [];
  const realError = console.error;
  console.error = (...args) => errors.push(args);
  try {
    store.connect();
    sockets[0].deliver(JSON.stringify(SNAPSHOT));
    sockets[0].deliver("{not json at all");

    assert.equal(store.isConnected(), false, "the page must not claim to be connected on a frame it could not read");
    assert.deepEqual(store.get(), SNAPSHOT, "the unreadable frame replaces nothing");
    assert.equal(errors.length, 1, "an unreadable frame is worth a console line");
  } finally {
    console.error = realError;
  }
});

// --- reconnecting ---

test("a dropped socket schedules a reconnect and the reconnect opens a new socket", async () => {
  const store = await freshStore("reconnect");
  store.connect();
  assert.equal(sockets.length, 1);

  sockets[0].drop();
  const delay = runScheduledReconnect();

  assert.equal(delay, 1000, "the first retry waits the initial backoff");
  assert.equal(sockets.length, 2, "a reconnect means a new socket, not a revived one");
  sockets[1].deliver(JSON.stringify(SNAPSHOT));
  assert.equal(store.isConnected(), true);
});

test("the backoff doubles while retries keep failing, and stops at its ceiling", async () => {
  const store = await freshStore("backoff");
  store.connect();

  const delays = [];
  for (let i = 0; i < 5; i += 1) {
    sockets.at(-1).drop();
    delays.push(runScheduledReconnect());
  }

  assert.deepEqual(delays, [1000, 2000, 4000, 5000, 5000], "doubling, capped — not unbounded, not fixed");
});

test("one good frame resets the backoff, so a later drop retries quickly again", async () => {
  const store = await freshStore("backoff-reset");
  store.connect();

  sockets.at(-1).drop();
  runScheduledReconnect();
  sockets.at(-1).drop();
  assert.equal(runScheduledReconnect(), 2000, "two failures in a row have grown the wait");

  sockets.at(-1).deliver(JSON.stringify(SNAPSHOT));
  sockets.at(-1).drop();

  assert.equal(runScheduledReconnect(), 1000, "a working connection means the backoff has done its job");
});

// --- a frame from a socket that has already been replaced ---

test("a late frame from a replaced socket cannot reach the store", async () => {
  const store = await freshStore("stale-frame");
  store.connect();
  const old = sockets[0];
  old.deliver(JSON.stringify(SNAPSHOT));

  old.drop();
  runScheduledReconnect();
  const fresh = sockets[1];
  fresh.deliver(JSON.stringify({ ...SNAPSHOT, at: "2026-09-10T00:00:05Z" }));

  // The browser does not strictly rule this out on every platform, and a store
  // that accepted it would show state older than what it already has.
  old.deliver(JSON.stringify({ ...SNAPSHOT, at: "1999-01-01T00:00:00Z" }));

  assert.equal(store.get().at, "2026-09-10T00:00:05Z", "the newer frame stands");
});

test("a late close from a replaced socket cannot schedule a second reconnect", async () => {
  const store = await freshStore("stale-close");
  store.connect();
  const old = sockets[0];

  old.drop();
  runScheduledReconnect();
  assert.equal(timers.length, 0, "the scheduled reconnect has been consumed");

  old.drop();

  assert.equal(timers.length, 0, "a detached socket must not queue another attempt");
  assert.equal(sockets.length, 2, "and must not cause a third socket to be opened");
});

// --- connect() itself ---

test("connect() twice opens one socket, not two pushing into the same store", async () => {
  const store = await freshStore("reentrant");
  store.connect();
  store.connect();
  assert.equal(sockets.length, 1);
});

test("the socket scheme follows the page: ws from http, wss from https", async () => {
  const plain = await freshStore("scheme-plain", { protocol: "http:", host: "127.0.0.1:7777" });
  plain.connect();
  assert.equal(sockets[0].url, "ws://127.0.0.1:7777/ws");

  const secure = await freshStore("scheme-secure", { protocol: "https:", host: "panel.example" });
  secure.connect();
  // A browser refuses a plain ws:// from a secure page as mixed content, and
  // that refusal arrives as onerror with no visible cause.
  assert.equal(sockets[0].url, "wss://panel.example/ws");
});

// A page in the browser's back/forward cache keeps its sockets open (measured
// on a stand, see liveterminal.js). The snapshot socket lets go when the page
// is hidden and does not come back by itself; a page restored from the cache
// reloads, so it never shows a fleet it left from a socket that went quiet.
function pageEvents() {
  const page = new EventTarget();
  globalThis.addEventListener = page.addEventListener.bind(page);
  return page;
}

function pageshow(persisted) {
  const e = new Event("pageshow");
  e.persisted = persisted;
  return e;
}

test("a hidden page lets go of the snapshot socket and does not reconnect", async () => {
  const page = pageEvents();
  const store = await freshStore("pagehide");
  store.connect();
  page.dispatchEvent(new Event("pagehide"));
  assert.equal(sockets[0].closed, true, "the snapshot socket stayed open on a hidden page");
  assert.equal(timers.length, 0, "a hidden page scheduled a reconnect");
  delete globalThis.addEventListener;
});

test("a page restored from the back/forward cache reloads, and only that page", async () => {
  const page = pageEvents();
  let reloads = 0;
  const store = await freshStore("pageshow");
  globalThis.location.reload = () => (reloads += 1);
  store.connect();
  page.dispatchEvent(pageshow(false));
  assert.equal(reloads, 0, "an ordinary first show reloaded the page");
  page.dispatchEvent(pageshow(true));
  assert.equal(reloads, 1);
  delete globalThis.addEventListener;
});

test("the socket asks for the fleet the tab's address names", async () => {
  const store = await freshStore("fleet", { search: "?fleet=%D0%BE%D1%81%D0%BD%D0%BE%D0%B2%D0%BD%D0%BE%D0%B9" });
  store.connect();
  // The server serves whichever fleet a socket names; a socket naming none
  // gets the first, which is what a tab with no fleet in its address shows.
  assert.equal(sockets[0].url, "ws://127.0.0.1:7777/ws?fleet=%D0%BE%D1%81%D0%BD%D0%BE%D0%B2%D0%BD%D0%BE%D0%B9");
});
