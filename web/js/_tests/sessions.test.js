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
import { createStalledTracker, BLOCKED_SETTLE_MS } from "../header.js";

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
  const html = rowHtml({ short: "bb22", name: "n", needs: "", state: "blocked", detail }, true);
  const title = html.match(/class="sreason" title="([^"]*)"/)?.[1];
  assert.equal(title, detail);
});

test("a quote or a tag in the reason cannot break out of the title attribute", () => {
  const nasty = 'he said "go" <img src=x onerror=alert(1)>';
  const html = rowHtml({ short: "cc33", name: "n", needs: "", state: "blocked", detail: nasty }, true);
  assert.ok(!html.includes("<img"), "the reason must never reach the DOM as markup");
  assert.ok(!html.includes('="go"'), "an unescaped quote would end the attribute early");
  assert.ok(html.includes("&quot;go&quot;"));
});

test("a session that is neither waiting nor stalled has no reason row at all", () => {
  const html = rowHtml({ short: "dd44", name: "n", state: "working", detail: "some detail" });
  assert.equal(html.includes("sreason"), false, "an absent reason is absent, not an empty box");
});

// --- the operator's own name for a session ---

test("the row prefers the operator's own label, then the name, then the short id", () => {
  assert.match(rowHtml({ short: "ee55", label: "my name for it", name: "n" }), /class="sname">my name for it</);
  assert.match(rowHtml({ short: "ee55", label: "", name: "n" }), /class="sname">n</);
  assert.match(rowHtml({ short: "ee55", name: "" }), /class="sname">ee55</);
});

test("a hostile label reaches the row only as escaped text, never as markup", () => {
  const nasty = 'he said "go" <img src=x onerror=alert(1)>';
  const html = rowHtml({ short: "ff66", label: nasty, name: "n" });
  assert.ok(!html.includes("<img"), "the label must never reach the DOM as markup");
  assert.ok(html.includes("&quot;go&quot;"), "the quote is escaped, not left to break an attribute");
  assert.ok(html.includes("&lt;img"), "the tag itself is escaped, not stripped or interpreted");
});

test("the edit button carries the session's transcript UUID, not its short id", () => {
  const html = rowHtml({ short: "gg77", name: "n", sessionId: "11111111-1111-1111-1111-111111111111" });
  assert.match(html, /class="label-edit-btn" data-session-id="11111111-1111-1111-1111-111111111111"/);
});

test("a session with no transcript UUID gets no edit button — there is nothing to write a label against", () => {
  const html = rowHtml({ short: "hh88", name: "n" });
  assert.equal(html.includes("label-edit-btn"), false);
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

async function list(snapshot, opts) {
  const dom = installDOM();
  const store = await import("../store.js");
  store.connect();
  // A real parent, not a detached node: the resize grip is created as a
  // sibling of root inside root.parentElement (web/js/columnresize.js),
  // the same as the orchestrator column's own, and mounts nothing at all
  // without one.
  const main = dom.element("main");
  const root = dom.element("aside");
  main.appendChild(root);
  const resize = renderSessions(root, () => {}, undefined, opts);
  listSocket.push(snapshot);
  await settle();
  return { root, dom, resize };
}

const FLEET = {
  orchestratorSession: "orch",
  sessions: [
    { short: "orch", name: "the orchestrator", sessionId: "u-0" },
    { short: "aa11", name: "a task", sessionId: "u-1" },
    { short: "bb22", name: "another task", sessionId: "u-2" },
  ],
};

// Array.prototype.map passes (element, index, array) to its callback.
// ordered.map(rowHtml) would hand rowHtml's own second parameter the row's
// numeric index -- 0 for the first row (falsy, harmless by accident) but
// truthy for every row after it, badging almost the whole list as Stalled
// regardless of stalledNow. Two fresh flag-only blocked sessions at
// positions 1 and 2 catch exactly that, through the real renderSessions
// pipeline rather than a direct rowHtml call.
test("a fresh flag-only blocked session is not badged Stalled wherever it sits in the list", async () => {
  const fleet = {
    sessions: [
      { short: "aa11", name: "first", state: "working" },
      { short: "bb22", name: "second", needs: "", state: "blocked" },
      { short: "cc33", name: "third", needs: "", state: "blocked" },
    ],
  };
  const { root, dom } = await list(fleet);
  const html = root.innerHTML;
  assert.equal(
    (html.match(/sbadge-stalled/g) ?? []).length,
    0,
    "no fresh flag-only stall is badged, wherever it sits in the list",
  );
  dom.restore();
});

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
  const html = rowHtml({ short: "aa11", name: "n", needs: "", state: "blocked", detail: wrapped }, true);

  // The visible text only: the title keeps the envelope on purpose.
  const shown = html.match(/class="sreason" title="[^"]*">([^<]*)</)?.[1] ?? "";
  assert.ok(!shown.includes("&lt;agent-message"), "the tag was never the reason");
  assert.ok(shown.includes("06a1f607"), "who asked is part of the reason");
  assert.ok(shown.includes("approve the **three** MRs"), "and the words are carried exactly");
  assert.ok(!shown.includes("<strong>"), "a reason is not typeset: spec 3.1 wants detail verbatim");
});

test("the title still holds the reason exactly as the daemon wrote it", () => {
  const wrapped = '<agent-message id="m-9" from="06a1f607" at="t">body</agent-message>';
  const html = rowHtml({ short: "aa11", name: "n", needs: "", state: "blocked", detail: wrapped }, true);
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

// Editing a task's own name in place (startEditing, inside renderSessions)
// is NOT covered here on purpose. This column rebuilds with a single
// root.innerHTML = ... write (see renderSessions), and fake-dom.js's own
// header says why that is invisible to it: "innerHTML is stored and never
// parsed" — so none of .srow, .sname or .label-edit-btn ever become real,
// queryable nodes in this harness, the same reason renderSessions' own
// pre-existing click-to-select was never exercised here either. rowHtml's
// output — the label priority, the escaping, the edit button's presence and
// its data-session-id — is fully covered above because it is a pure string
// function. The interactive behavior (Enter/Esc/blur, the freeze-while-
// editing guard, the visible failure message) was verified by hand against
// a real running panel in a real browser instead.

// --- the row's Stalled badge and the header's counter agree ---------------
//
// This is the fix itself: rowHtml's own badge decision now comes from
// createStalledTracker, the exact function header.js's counter uses,
// instead of a fresh per-row isStalled() check. The three cases below are
// the acceptance the orchestrator required by name -- a fresh flag-only
// block and a settled one must read the same in both places, and a
// needs-based stall must fire immediately in both, with a mutation proof
// that "immediately" is actually being tested and not merely asserted.

function isBadgedStalled(html) {
  return html.includes("srow-stalled") && html.includes("sbadge-stalled");
}

// Two independent tracker instances, not one shared between the two
// checks: this mirrors production exactly (header.js and sessions.js each
// own their own instance -- see sessions.js's own comment for why) and
// proves the agreement holds without smuggling a shared closure into the
// test that neither file actually has at runtime.
test("control case: a fresh flag-only blocked session reads the same in the header counter and the row", () => {
  const headerTracker = createStalledTracker();
  const rowTracker = createStalledTracker();
  const s = { short: "aa11", name: "n", needs: "", state: "blocked" };
  const nowMs = 0;

  const headerCountsIt = headerTracker.update([s], nowMs).length > 0;
  const stalledNow = new Set(rowTracker.update([s], nowMs).map((x) => x.short));
  const html = rowHtml(s, stalledNow.has(s.short));

  assert.equal(headerCountsIt, false, "a fresh flag-only stall must not be counted by the header yet");
  assert.equal(isBadgedStalled(html), false, "the row must agree with the header: not yet Stalled");
});

test("control case: a flag-only blocked session held past the threshold reads the same in both places", () => {
  const headerTracker = createStalledTracker();
  const rowTracker = createStalledTracker();
  const s = { short: "aa11", name: "n", needs: "", state: "blocked" };

  headerTracker.update([s], 0);
  rowTracker.update([s], 0);
  const nowMs = BLOCKED_SETTLE_MS + 1;

  const headerCountsIt = headerTracker.update([s], nowMs).length > 0;
  const stalledNow = new Set(rowTracker.update([s], nowMs).map((x) => x.short));
  const html = rowHtml(s, stalledNow.has(s.short));

  assert.equal(headerCountsIt, true, "a flag-only stall held past BLOCKED_SETTLE_MS must be counted");
  assert.equal(isBadgedStalled(html), true, "the row must agree with the header: Stalled");
});

// The case the orchestrator named explicitly: a needs-based stall is the
// daemon naming a real reason, and must read Stalled at age zero, in both
// places, with no threshold at all -- unlike the flag-only case above.
test("a needs-based stall fires immediately, at age zero, in both the header and the row", () => {
  const headerTracker = createStalledTracker();
  const rowTracker = createStalledTracker();
  const s = { short: "aa11", name: "n", needs: "login required: run `claude login`" };
  const nowMs = 0; // this session was seen for the very first time, right now

  const headerCountsIt = headerTracker.update([s], nowMs).length > 0;
  const stalledNow = new Set(rowTracker.update([s], nowMs).map((x) => x.short));
  const html = rowHtml(s, stalledNow.has(s.short));

  assert.equal(headerCountsIt, true, "a needs-based stall must be counted immediately, no threshold");
  assert.equal(isBadgedStalled(html), true, "the row must agree: Stalled immediately, no threshold");
});

// --- collapse/resize: the same mechanism as the orchestrator column's own
// (web/js/columnresize.js) — the operator's own instruction was to reuse it
// whole, not build a second, similar one. What is pinned here is the wiring:
// that the same controls exist, that they read and write this column's own
// storage entries (SESSIONS_KEYS, never the orchestrator's), and above all
// that the stalled tracker keeps counting while the column is folded — see
// the last test in this section.

// This column sits at the window's RIGHT edge, so its resize edge has to be
// on its own left — the side facing the centre — which in the DOM means
// immediately BEFORE root, not after it. Placed after (the orchestrator
// column's own side, and the defect the operator actually reported: "no
// resize handle found") it ends up pinned against the window's outer edge,
// past the last column, with nothing beyond it to drag against.
test("the session list has a resize grip on its own left, toward the centre — not pinned against the window's edge", async () => {
  const { root } = await list({ sessions: [] });
  const main = root.parentElement;
  const grip = main?.children.find((n) => String(n.className).includes("col-grip"));
  assert.ok(grip, "no resize handle was created at all");
  assert.equal(main.children.indexOf(grip), main.children.indexOf(root) - 1, "the grip is not immediately before the column, toward the centre");
  assert.notEqual(main.children.indexOf(grip), main.children.length - 1, "the grip ended up pinned against the window's outer edge");
});

// The mirror of the orchestrator column's own equivalent test: this column
// folds away to the RIGHT (toward its own edge), so its fold arrow points
// right, and unfolding brings it back left, toward the centre — the
// opposite of the orchestrator's, and a bare existence check cannot tell
// the two apart.
test("the session list's controls sit on its right-hand side, with arrows pointing the right column's own way", async () => {
  const { root } = await list({ sessions: [] });
  assert.match(root.innerHTML, /class="col-size col-size-right"/, "the strip is not marked as a right column's");
  assert.match(root.innerHTML, /class="col-size-btn col-size-fold"[^>]*>»</, "folding away must point toward this column's own edge, not the centre");
  assert.match(root.innerHTML, /class="col-size-btn col-size-unfold"[^>]*>«</, "coming back must point toward the centre");
});

test("the session list carries its own remembered width from the first paint", async () => {
  const { root } = await list({ sessions: [] });
  // DEFAULT_PERCENT, same value the orchestrator's own default is, but
  // written under SESSIONS_KEYS -- see the next test for the part that
  // actually distinguishes the two.
  assert.equal(root.style.getPropertyValue("--col-width"), "25%");
});

// The fold/unfold buttons inside root are markup, not addressable nodes in
// web/tests/fake-dom.js (its innerHTML is stored, never parsed), so this
// drives the fold/unfold itself through resize.width — the same call a
// click makes in a real browser (see sessions.js's own note on
// renderSessions' return value) — and checks the markup those buttons would
// be by matching the raw HTML string, the same way existing tests here check
// for a badge.
test("folding the session list hides the rows but keeps the way back, exactly like the orchestrator column's", async () => {
  const { root, resize } = await list({ sessions: [{ short: "aa11", name: "a task", state: "working" }] });

  assert.match(root.innerHTML, /class="col-size-btn col-size-unfold"[^>]*aria-label="[^"]/, "the way back has no label");

  resize.width.fold();
  await settle();

  assert.equal(root.dataset.folded, "1", "the column was not marked folded");

  const grip = root.parentElement?.children.find((n) => String(n.className).includes("col-grip"));
  assert.equal(grip.hidden, true, "a folded column kept an edge that resizes nothing");

  resize.width.unfold();
  await settle();
  assert.equal(root.dataset.folded, undefined, "the column stayed folded");
  assert.equal(grip.hidden, false, "the edge did not come back with the column");
});

test("resizing and folding the session list never touches the orchestrator column's own remembered state", async () => {
  const previous = Object.hasOwn(globalThis, "localStorage") ? globalThis.localStorage : undefined;
  const map = new Map();
  globalThis.localStorage = {
    getItem: (k) => (map.has(k) ? map.get(k) : null),
    setItem: (k, v) => map.set(k, String(v)),
    removeItem: (k) => map.delete(k),
  };

  try {
    const { resize } = await list({ sessions: [] });
    resize.width.fold();

    assert.equal(map.get("fleetdeck-sessions-folded"), "1", "the session list's own fold was not remembered");
    assert.equal(
      map.has("fleetdeck-orchestrator-folded"),
      false,
      "folding the session list wrote to the orchestrator column's own storage entry",
    );
  } finally {
    if (previous === undefined) delete globalThis.localStorage;
    else globalThis.localStorage = previous;
  }
});

// The one the orchestrator named as the risk this whole task carries: two
// independent trackers (this column's own, the header's) settle on the same
// answer only for as long as both keep receiving every snapshot. Folding
// this column must not be the thing that stops it receiving them — render()
// runs unconditionally regardless of the [data-folded] attribute it itself
// sets, so a stall that started before the fold keeps aging while the rows
// are hidden, and is already correctly badged, at the true age, the instant
// the column reopens.
//
// Real elapsed time cannot stand in for BLOCKED_SETTLE_MS (10 minutes) in a
// unit test, so this drives renderSessions' own injectable clock rather than
// waiting on the wall clock -- see renderSessions' own `now` parameter.
test("a stall held past the threshold while the session list is folded is badged the instant it reopens, not from a clock that restarted", async () => {
  const dom = installDOM();
  const store = await import("../store.js");
  store.connect();
  const main = dom.element("main");
  const root = dom.element("aside");
  main.appendChild(root);

  let clock = 0;
  const resize = renderSessions(root, () => {}, undefined, { now: () => clock });

  const fleet = { sessions: [{ short: "aa11", name: "first", needs: "", state: "blocked" }] };

  // Seen for the first time, fresh: neither place counts it yet.
  listSocket.push(fleet);
  await settle();
  assert.equal(
    (root.innerHTML.match(/sbadge-stalled/g) ?? []).length,
    0,
    "precondition: a fresh flag-only stall is not yet badged",
  );

  resize.width.fold();
  assert.equal(root.dataset.folded, "1", "precondition: the column folded");

  // The clock crosses BLOCKED_SETTLE_MS while the column is still folded,
  // and the same still-blocked session arrives again -- render() must run
  // anyway, feeding the one tracker instance that has been counting this
  // session continuously since clock 0.
  clock = BLOCKED_SETTLE_MS + 1;
  listSocket.push(fleet);
  await settle();

  assert.equal(root.dataset.folded, "1", "precondition: still folded when the threshold was crossed");
  assert.equal(
    (root.innerHTML.match(/sbadge-stalled/g) ?? []).length,
    1,
    "a stall that crossed the threshold while folded must already be badged, even before the column reopens",
  );

  resize.width.unfold();
  assert.equal(root.dataset.folded, undefined, "precondition: the column reopened");
  assert.equal(
    (root.innerHTML.match(/sbadge-stalled/g) ?? []).length,
    1,
    "the badge must still be there on reopening, not reset by having been hidden",
  );

  dom.restore();
});

// --- several fleets: this fleet's sessions, the unclaimed, and the others ---
//
// The server sends every session with the fleets claiming it; the column shows
// this fleet's tasks as rows, the sessions no fleet claims under their own
// heading (they belong to every fleet until a card claims them, so hiding them
// would leave a waiting question where no tab looks), and each other fleet as
// one line with its count, never as its sessions' rows.

const TWO_FLEETS = {
  fleet: "B",
  fleets: ["A", "B"],
  orchestratorSession: "borch",
  sessions: [
    { short: "borch", name: "B's orchestrator", sessionId: "u-0", fleets: ["B"] },
    { short: "bb22", name: "a task of B", sessionId: "u-1", fleets: ["B"] },
    { short: "nn33", name: "nobody's session", sessionId: "u-2" },
    { short: "aa11", name: "a task of A", sessionId: "u-3", fleets: ["A"], needs: "answer: which one?" },
    { short: "aa12", name: "another task of A", sessionId: "u-4", fleets: ["A"] },
  ],
};

test("several fleets: this fleet's tasks are rows, the other fleet is one line", async () => {
  const { root, dom } = await list(structuredClone(TWO_FLEETS));
  const html = root.innerHTML;
  assert.ok(html.includes('data-short="bb22"'), "this fleet's task is a row");
  assert.ok(!html.includes('data-short="borch"'), "its orchestrator still has a column of its own");
  assert.ok(!html.includes('data-short="aa11"') && !html.includes('data-short="aa12"'), "the other fleet's sessions are not rows here");
  const other = html.match(/<button type="button" class="fleet-other" data-fleet="A">(.*?)<\/button>/s)?.[1];
  assert.ok(other, "the other fleet is one line that switches to it");
  assert.match(other, /fleet-other-count">[^<]*: 2</, "with how many sessions it has");
  assert.match(other, /fleet-other-waiting">[^<]*: 1</, "and how many of them wait for an answer");
  dom.restore();
});

test("several fleets: an unclaimed session is listed under its own heading", async () => {
  const { root, dom } = await list(structuredClone(TWO_FLEETS));
  const html = root.innerHTML;
  const head = html.indexOf('class="fleet-group-head"');
  assert.ok(head > 0, "the unclaimed sessions have a heading");
  assert.ok(html.indexOf('data-short="bb22"') < head, "this fleet's tasks come first");
  assert.ok(html.indexOf('data-short="nn33"') > head, "the unclaimed session is under the heading");
  dom.restore();
});

test("several fleets: a fleet that is only its orchestrator still shows the rest", async () => {
  const snap = structuredClone(TWO_FLEETS);
  snap.sessions = snap.sessions.filter((s) => s.short !== "bb22");
  const { root, dom } = await list(snap);
  const html = root.innerHTML;
  assert.ok(html.includes("besides the orchestrator") || html.includes("Кроме оркестратора"));
  assert.ok(html.includes('data-short="nn33"'), "the unclaimed session is still there");
  assert.ok(html.includes('data-fleet="A"'), "and so is the other fleet");
  dom.restore();
});

test("one fleet: the column looks exactly as it did before there were fleets", async () => {
  const snap = {
    fleet: "A",
    fleets: ["A"],
    orchestratorSession: "",
    sessions: [
      { short: "aa11", name: "a task", sessionId: "u-1", fleets: ["A"] },
      { short: "nn33", name: "nobody's session", sessionId: "u-2" },
    ],
  };
  const { root, dom } = await list(snap);
  const html = root.innerHTML;
  assert.ok(!html.includes("fleet-group-head") && !html.includes("fleet-other"), "nothing is grouped");
  assert.ok(html.includes('data-short="aa11"') && html.includes('data-short="nn33"'), "every session is a row");
  dom.restore();
});
