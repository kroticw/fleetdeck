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

// --- the envelope comes off here too, but nothing is typeset ---

test("a reason wrapped in an envelope loses the tag and keeps its words", () => {
  const wrapped = '<agent-message id="m-9" from="06a1f607" at="2026-09-10T15:00:00+05:00">approve the **three** MRs</agent-message>';
  const html = rowHtml({ short: "aa11", name: "n", needs: "", state: "blocked", detail: wrapped });

  // The visible text only: the title keeps the envelope on purpose.
  const shown = html.match(/class="sreason" title="[^"]*">([^<]*)</)?.[1] ?? "";
  assert.ok(!shown.includes("&lt;agent-message"), "the tag was never the reason");
  assert.ok(shown.includes("06a1f607"), "who asked is part of the reason");
  assert.ok(shown.includes("approve the **three** MRs"), "and the words are carried exactly");
  assert.ok(!shown.includes("<strong>"), "a reason is not typeset: spec 3.1 wants detail verbatim");
});

test("the title still holds the reason exactly as the daemon wrote it", () => {
  const wrapped = '<agent-message id="m-9" from="06a1f607" at="t">body</agent-message>';
  const html = rowHtml({ short: "aa11", name: "n", needs: "", state: "blocked", detail: wrapped });
  const title = html.match(/class="sreason" title="([^"]*)"/)?.[1];
  assert.ok(title.includes("&lt;agent-message"), "stripping the tag must not put it out of reach");
});

// --- a number with no unit says nothing ---
//
// The right-hand column showed "56%" beside every session with nothing to say
// what was measured. Checked at the source rather than guessed:
// internal/transcript/context.go computes Tokens/Window, where Tokens is the
// last response's cache_read + cache_creation + input tokens and Window is the
// model's context window. It is context occupancy, and the word for it is
// already in the dictionary — it was only being shown when there was no value.

test("a context percentage is labelled, so a number is not left to speak for itself", () => {
  const html = rowHtml({ short: "aa11", name: "n", context: { tokens: 50, window: 100 } });
  assert.ok(html.includes("50%"), "the value stays");
  assert.ok(/контекст|context/i.test(html), "and now says what it measures");
});

test("the label is there whether the value is known or not", () => {
  // It used to be the other way round: the word appeared only in the state
  // where there was nothing to label.
  const known = rowHtml({ short: "aa11", name: "n", context: { tokens: 50, window: 100 } });
  const unknown = rowHtml({ short: "bb22", name: "n" });
  assert.ok(/контекст|context/i.test(known));
  assert.ok(/контекст|context/i.test(unknown));
});

test("an estimated value keeps its mark and the mark keeps its explanation", () => {
  const html = rowHtml({ short: "aa11", name: "n", context: { tokens: 50, window: 100, estimated: true } });
  assert.ok(html.includes("~"), "the tilde says the number was estimated");
  assert.ok(/title="[^"]*(Оценено|Estimated)/.test(html), "and hovering it says what that means");
});

test("the percentage is readable without a pointer", () => {
  // A value reachable only by hovering is not reachable for someone who does
  // not hover — the number and its label are both plain text in the row.
  const html = rowHtml({ short: "aa11", name: "n", context: { tokens: 50, window: 100 } });
  const visible = html.replace(/<[^>]*>/g, " ");
  assert.ok(/50%/.test(visible));
  assert.ok(/контекст|context/i.test(visible), "the label is text, not only a tooltip");
});

// --- the card link promised one thing and did another ---
//
// The ↗ was a <div> with no click handler at all: a click on it bubbled to the
// row and opened the SESSION, while its tooltip showed the path to a CARD. Not
// merely unreachable from a keyboard — it did something nobody had assigned it,
// because the absence of code was masked by the parent's behaviour.

test("a session with a card offers a control that says what it opens", () => {
  const html = rowHtml({ short: "aa11", name: "n", cardPath: "/board/cards/2026-09-10-thing.md" });
  assert.ok(/<button[^>]*class="[^"]*scard/.test(html), "a control a keyboard can reach");
  assert.ok(/type="button"/.test(html), "and that does not submit anything");
  assert.ok(/карточка|card/i.test(html.replace(/<[^>]*>/g, " ")), "labelled, not a bare arrow");
});

test("the control carries the card it opens, not just a tooltip", () => {
  const html = rowHtml({ short: "aa11", name: "n", cardPath: "/board/cards/2026-09-10-thing.md" });
  assert.ok(html.includes('data-card="/board/cards/2026-09-10-thing.md"'), "the path is data the handler can use");
});

test("a session with no card offers no control", () => {
  const html = rowHtml({ short: "aa11", name: "n" });
  assert.ok(!html.includes("scard"), "nothing to open, so nothing to press");
});

test("a card path cannot break out of the attribute it lands in", () => {
  const html = rowHtml({ short: "aa11", name: "n", cardPath: '/board/x" onclick="alert(1)' });
  assert.ok(!html.includes('onclick="alert(1)"'), "a path is data, not markup");
  assert.ok(html.includes("&quot;"), "the quote is escaped");
});

// --- pressing the card control: why it is not tested here ---
//
// This column builds its rows with one innerHTML write, and the fake DOM stores
// innerHTML without parsing it (see web/tests/fake-dom.js), so no node inside a
// row is addressable and no click on one can be fired. What the markup contains
// is asserted above; that pressing it opens the CARD and not the session is
// checked against a running panel in a real browser, and the PR records the
// numbers. Rewriting this column onto nodes would make it testable here, but
// that is a change to a file two other sessions are working in, not a change
// this task asked for.

