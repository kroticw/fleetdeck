// web/js/_tests/folded-rail-signals.test.js
//
// Folded in the fleetdeck window, the sessions panel is a rail with no head:
// the counters "waiting for you" and "stalled" are not on it (web/app.css). So
// the rail itself has to carry what a person must not miss while it is folded,
// through the column's real rendering: a session waiting for them is a red
// mark, and a panel that has lost the daemon or its own connection shows an em
// dash where the count would be, never a quiet zero.
//
// Run with: node --test web/js/_tests/folded-rail-signals.test.js

import test from "node:test";
import assert from "node:assert/strict";
import { installDOM, settle } from "../../tests/fake-dom.js";
import { renderSessions } from "../sessions.js";

globalThis.navigator ??= { language: "en" };

class RailSocket {
  constructor() {
    this.onmessage = null;
    this.onclose = null;
    this.onerror = null;
    railSocket = this;
  }
  close() {}
  push(snapshot) {
    this.onmessage?.({ data: JSON.stringify(snapshot) });
  }
}

let railSocket = null;
globalThis.WebSocket = RailSocket;
globalThis.location = { protocol: "http:", host: "127.0.0.1:7777" };

async function rail(snapshot) {
  const dom = installDOM();
  const store = await import("../store.js");
  store.connect();
  const main = dom.element("main");
  const root = dom.element("aside");
  main.appendChild(root);
  renderSessions(root, () => {});
  railSocket.push(snapshot);
  await settle();
  return { root, dom };
}

const strip = (root) => /<div class="sfold">[\s\S]*?<\/div>(?=<)/.exec(root.innerHTML)?.[0] ?? root.innerHTML;

test("a session waiting for a person is a red mark on the folded rail", async () => {
  const { root, dom } = await rail({
    sessions: [
      { short: "aa11", name: "a task", state: "working" },
      { short: "bb22", name: "another task", state: "blocked", needs: "answer: first or after?" },
    ],
  });
  assert.match(root.innerHTML, /class="sfold-mark sfold-waiting"[^>]*data-session="bb22"/);
  dom.restore();
});

test("a panel whose daemon does not answer shows a dash on the folded rail, not a count", async () => {
  const { root, dom } = await rail({ daemonError: "daemon not answering", sessions: [] });
  assert.match(root.innerHTML, /class="sfold-count sfold-unknown"[^>]*>—</);
  assert.doesNotMatch(strip(root), /class="sfold-count"[^>]*>0</);
  dom.restore();
});

test("a panel that lost its connection shows a dash on the folded rail, not the last count", async () => {
  const { root, dom } = await rail({ sessions: [{ short: "aa11", name: "a task", state: "working" }] });
  assert.match(root.innerHTML, /class="sfold-count"[^>]*>1</);
  railSocket.onclose?.();
  await settle();
  assert.match(root.innerHTML, /class="sfold-count sfold-unknown"[^>]*>—</);
  dom.restore();
});
