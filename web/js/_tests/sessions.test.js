// web/js/_tests/sessions.test.js
//
// Lives under _tests/ for the same reason header.test.js does: web/embed.go's
// plain (non "all:") directory pattern already excludes any directory whose
// name starts with "_", so this subtree never reaches the binary or the HTTP
// surface.
//
// Run with: node --test web/js/_tests/sessions.test.js

import test from "node:test";
import assert from "node:assert/strict";
import { rowHtml, renderSessions } from "../sessions.js";

// sessions.js reaches for navigator.language at import time through i18n.js.
globalThis.navigator ??= { language: "en" };

test("a waiting session's reason keeps its full text in the title", () => {
  const long = "answer: which branch should this go to? " + "y".repeat(500);
  const html = rowHtml({ short: "aa11", name: "n", needs: long });
  const title = html.match(/class="sreason" title="([^"]*)"/)?.[1];
  assert.equal(title, long, "the clipped row must still carry the whole reason");
});

test("a stalled session with no needs carries detail, in full, in the title", () => {
  const detail = "waiting on my own subagents\nsecond line\nthird line";
  const html = rowHtml({ short: "bb22", name: "n", needs: "", state: "blocked", detail });
  const title = html.match(/class="sreason" title="([^"]*)"/)?.[1];
  assert.equal(title, detail);
});

test("a quote or a tag in the reason cannot break out of the title attribute", () => {
  const nasty = 'he said "go" <img src=x onerror=alert(1)>';
  const html = rowHtml({ short: "cc33", name: "n", needs: "", state: "blocked", detail: nasty });
  assert.ok(!html.includes("<img"), "the reason must never reach the DOM as markup");
  assert.ok(!html.includes('="go"'), "an unescaped quote would end the attribute early");
  assert.ok(html.includes("&quot;go&quot;"));
});

test("a session that is neither waiting nor stalled has no reason row at all", () => {
  const html = rowHtml({ short: "dd44", name: "n", state: "working", detail: "some detail" });
  assert.equal(html.includes("sreason"), false, "an absent reason is absent, not an empty box");
});

// --- the orchestrator belongs to the other column ---
//
// The operator asked for the two columns to stop showing the same thing: the
// orchestrator on the left with its conversation, the tasks on the right. These
// drive renderSessions against the fake DOM and the real store, the same way
// the orchestrator column's own tests do.

import { installDOM, settle } from "../../tests/fake-dom.js";

class ListSocket {
  constructor() {
    this.onmessage = null;
    this.onclose = null;
    this.onerror = null;
    listSocket = this;
  }
  close() {}
  push(snapshot) {
    this.onmessage?.({ data: JSON.stringify(snapshot) });
  }
}

let listSocket = null;
globalThis.WebSocket = ListSocket;
globalThis.location = { protocol: "http:", host: "127.0.0.1:7777" };

async function list(snapshot) {
  const dom = installDOM();
  const store = await import("../store.js");
  store.connect();
  const root = dom.element("aside");
  renderSessions(root, () => {});
  listSocket.push(snapshot);
  await settle();
  return { root, dom };
}

const FLEET = {
  orchestratorSession: "orch",
  sessions: [
    { short: "orch", name: "the orchestrator", sessionId: "u-0" },
    { short: "aa11", name: "a task", sessionId: "u-1" },
    { short: "bb22", name: "another task", sessionId: "u-2" },
  ],
};

test("the pinned orchestrator is not listed among the tasks", async () => {
  const { root, dom } = await list(structuredClone(FLEET));
  const html = root.innerHTML;
  assert.ok(html.includes("a task") && html.includes("another task"), "the tasks are listed");
  assert.ok(!html.includes("the orchestrator"), "and the orchestrator is not, it has a column of its own");
  assert.ok(!html.includes('data-short="orch"'), "not under its short id either");
  dom.restore();
});

test("with nothing pinned every session is a task, including one with no short id", async () => {
  // The decoy matters: an empty pin is "nothing is pinned", and a filter that
  // ran anyway would compare against "" and drop exactly this session.
  const nothingPinned = { ...structuredClone(FLEET), orchestratorSession: "" };
  nothingPinned.sessions.push({ short: "", name: "a session with no short id", sessionId: "u-3" });
  const { root, dom } = await list(nothingPinned);
  assert.ok(root.innerHTML.includes("the orchestrator"), "there is no orchestrator to leave out yet");
  assert.ok(root.innerHTML.includes("a session with no short id"), "and nothing else is dropped either");
  dom.restore();
});

test("a fleet that is only the orchestrator says so, and does not read as an empty fleet", async () => {
  const alone = { orchestratorSession: "orch", sessions: [{ short: "orch", name: "the orchestrator", sessionId: "u-0" }] };
  const { root, dom } = await list(alone);
  assert.ok(root.innerHTML.includes("besides the orchestrator") || root.innerHTML.includes("Кроме оркестратора"),
    "an empty task list with an orchestrator is a different fact from an empty fleet");
  dom.restore();
});

test("a genuinely empty fleet still says there are no sessions", async () => {
  const { root, dom } = await list({ orchestratorSession: "", sessions: [] });
  assert.ok(root.innerHTML.includes("No sessions") || root.innerHTML.includes("Нет сессий"));
  assert.ok(!root.innerHTML.includes("besides the orchestrator"), "nothing was filtered out here");
  dom.restore();
});
