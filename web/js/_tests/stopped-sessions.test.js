// web/js/_tests/stopped-sessions.test.js
//
// A session someone stops with `claude stop` used to vanish from the panel
// entirely, and the only trace left of it was its board card, marked
// orphaned — the panel saying "this card lost its session" about a session
// that was sitting in Claude Code's job store with its whole history, one
// resume away. These are about it staying in the list, staying there as what
// it is, and about what the panel must not start doing because it is there.
//
// Under _tests/ for the same reason the neighbouring files are: web/embed.go's
// plain (non "all:") directory pattern excludes any directory whose name
// starts with "_", so this subtree never reaches the binary.
//
// Run with: node --test web/js/_tests/stopped-sessions.test.js

import test from "node:test";
import assert from "node:assert/strict";
import { goneRowHtml, renderSessions } from "../sessions.js";
import { columnHTML } from "../board.js";
import { headerSessions } from "../fleet.js";
import { isLive, isResumable } from "../lifecycle.js";
import { installDOM, settle } from "../../tests/fake-dom.js";

globalThis.navigator ??= { language: "en" };

// --- the vocabulary itself ---

test("an absent lifecycle reads as live, so a snapshot from before this existed still lists its sessions", () => {
  assert.equal(isLive({ short: "aa11" }), true);
  assert.equal(isLive({ short: "aa11", lifecycle: "live" }), true);
  assert.equal(isLive({ short: "aa11", lifecycle: "stopped" }), false);
  assert.equal(isLive({ short: "aa11", lifecycle: "dead" }), false);
});

test("only a stopped session is resumable — a live one has nothing to resume, a dead one cannot", () => {
  assert.equal(isResumable({ lifecycle: "stopped" }), true);
  assert.equal(isResumable({ lifecycle: "live" }), false);
  assert.equal(isResumable({ lifecycle: "dead" }), false);
  assert.equal(isResumable({}), false);
});

// --- the row ---

test("a stopped row says it is stopped and carries the button that brings it back", () => {
  const html = goneRowHtml({ short: "aa11", name: "a paused task", lifecycle: "stopped" });
  assert.match(html, /sbadge-stopped/, "it carries the stopped badge");
  assert.match(html, /class="sname">a paused task</);
  assert.match(html, /class="sresume-btn" data-short="aa11"/, "the row carries the button that resumes it");
  assert.ok(!html.includes("claude resume aa11"),
    "the command to type in a terminal is gone: the button does it here");
  assert.ok(!html.includes("sbadge-stalled"), "a stopped session is not a stalled one");
  assert.ok(!html.includes("sbadge-waiting"), "and it is not waiting for anyone");
});

// A resume takes as long as replaying the session's history takes, which on a
// long one is most of a minute. For all that time the only thing on screen
// saying anything is happening is this button, so it has to say it — and it
// has to stop taking presses, because a second dispatch for one session is how
// one resume becomes two workers.
//
// Asserted as "the label changed" rather than against the English words: this
// process picks its dictionary from the machine's own locale (see i18n.js),
// so a test naming either language's wording would pass or fail depending on
// whose machine ran it. What it must do is change, and stop taking presses.
const buttonLabel = (html) => html.match(/<button[^>]*class="sresume-btn"[^>]*>([^<]*)</)?.[1] ?? "";

test("while a resume is in flight the button says so and takes no more presses", () => {
  const session = { short: "aa11", name: "a paused task", lifecycle: "stopped" };
  const idle = goneRowHtml(session);
  const busy = goneRowHtml(session, { busy: true });

  assert.match(busy, /class="sresume-btn"[^>]*disabled/, "the button refuses a second press");
  assert.ok(!/class="sresume-btn"[^>]*disabled/.test(idle), "and takes one when nothing is in flight");
  assert.notEqual(buttonLabel(busy), "", "a button with no words on it says nothing is happening");
  assert.notEqual(buttonLabel(busy), buttonLabel(idle),
    "it says what it is doing rather than looking untouched for the minute this takes");
});

// The failure is the whole reason the button is better than the hint it
// replaced: a resume that quietly does nothing is worse than no button at all.
// The words are the daemon's own — "exit 1" is the difference between knowing
// the worker died at startup and guessing.
test("a failed resume stays on its row, in the words it failed in", () => {
  const html = goneRowHtml(
    { short: "aa11", name: "a paused task", lifecycle: "stopped" },
    { error: "resumed worker crashed during startup: exit 1" },
  );
  assert.match(html, /class="sresume-error"/);
  assert.ok(html.includes("crashed during startup: exit 1"), "the daemon's own words survive to the screen");
  assert.match(html, /class="sresume-btn"/, "and the button is still there to try again with");
});

test("a resume failure reaches the row as escaped text, never as markup", () => {
  const html = goneRowHtml(
    { short: "aa11", lifecycle: "stopped" },
    { error: `<img src=x onerror="alert(1)">` },
  );
  assert.ok(!html.includes("<img"), "an error is text, whoever wrote it");
  assert.ok(html.includes("&lt;img"));
});

// A dead session has nothing to press. The button appearing on it would be
// the panel offering an action it has already said is impossible, two lines
// above, on the same row.
test("a session that cannot come back has no resume button", () => {
  const html = goneRowHtml({ short: "bb22", name: "a lost task", lifecycle: "dead", cwd: "/gone" });
  assert.ok(!html.includes("sresume-btn"), "nothing to press on a session that cannot be resumed");
});

test("a dead row says it cannot be resumed, and names the directory that is gone", () => {
  const html = goneRowHtml({
    short: "bb22",
    name: "a lost task",
    lifecycle: "dead",
    cwd: "/Users/x/project/.claude/worktrees/deleted",
  });
  assert.match(html, /sbadge-gone/);
  assert.ok(html.includes("/Users/x/project/.claude/worktrees/deleted"),
    "naming the directory is the difference between knowing and guessing");
  assert.ok(!html.includes("claude resume"), "it must not offer an action that cannot work");
});

// The class is what keeps these rows out of the click handler that opens a
// session's live terminal. There is no terminal to attach to, and a row that
// opens one would show an error instead of an explanation.
test("a row for a session that is not running does not carry the class that opens a terminal", () => {
  for (const lifecycle of ["stopped", "dead"]) {
    const html = goneRowHtml({ short: "aa11", name: "n", lifecycle });
    // The row element's own classes, not any inner one's: .srow-head is a
    // layout class the two kinds of row share on purpose, and it is not what
    // the click handler selects on.
    const classes = html.match(/<article class="([^"]*)"/)[1].split(/\s+/);
    assert.ok(!classes.includes("srow"), `${lifecycle}: must not be an .srow`);
    assert.ok(classes.includes("sgone"), `${lifecycle}: must be an .sgone`);
  }
});

test("the last state is shown as the last state, never as a reading of now", () => {
  const html = goneRowHtml({ short: "aa11", name: "n", lifecycle: "stopped", lastState: "blocked" });
  assert.ok(html.includes("blocked"), "the value the session recorded is still shown");
  assert.match(html, /last state|последнее состояние/, "and it is labelled as the last one");
});

test("a hostile name or path reaches a stopped row only as escaped text", () => {
  const nasty = 'he said "go" <img src=x onerror=alert(1)>';
  const html = goneRowHtml({ short: "aa11", name: nasty, lifecycle: "dead", cwd: nasty });
  assert.ok(!html.includes("<img"), "never as markup");
  assert.ok(html.includes("&lt;img"), "escaped, not stripped");
  assert.ok(html.includes("&quot;go&quot;"), "an unescaped quote would end an attribute early");
});

test("a stopped session with a card keeps the button that opens it", () => {
  const html = goneRowHtml({ short: "aa11", name: "n", lifecycle: "stopped", cardPath: "/b/c.md" });
  assert.match(html, /class="scard" data-card="\/b\/c\.md"/,
    "the card is a file, not a session: opening it still works");
});

test("a stopped session's card button names the card's number too", () => {
  const html = goneRowHtml({ short: "aa11", name: "n", lifecycle: "stopped", cardPath: "/b/c.md", cardId: "T-018" });
  assert.match(html, /<button[^>]*class="scard"[^>]*>[\s\S]*class="knum"[^>]*>T-018<[\s\S]*<\/button>/,
    "the question 'which card was this' outlives the session");
});

// --- the list ---

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

async function list(snapshot, onSelect = () => {}) {
  const dom = installDOM();
  const store = await import("../store.js");
  store.connect();
  const main = dom.element("main");
  const root = dom.element("aside");
  main.appendChild(root);
  renderSessions(root, onSelect, () => {});
  listSocket.push(snapshot);
  await settle();
  return { root, dom };
}

test("a stopped session is in the list, below the running ones", async () => {
  const { root, dom } = await list({
    sessions: [
      { short: "aa11", name: "a running task", lifecycle: "live", state: "working" },
      { short: "bb22", name: "a stopped task", lifecycle: "stopped" },
    ],
  });
  const html = root.innerHTML;
  assert.ok(html.includes("a stopped task"), "the stopped session is listed at all — the whole point");
  assert.ok(html.indexOf("a running task") < html.indexOf("a stopped task"),
    "and it is below what is still running");
  dom.restore();
});

test("a fleet whose every session is stopped does not read as an empty fleet", async () => {
  const { root, dom } = await list({
    sessions: [{ short: "bb22", name: "a stopped task", lifecycle: "stopped" }],
  });
  const html = root.innerHTML;
  assert.ok(html.includes("a stopped task"));
  assert.ok(!html.includes("No sessions") && !html.includes("Нет сессий"),
    "there is a session; it is stopped, which is not the same as there being none");
  dom.restore();
});

// Asserted on the markup rather than by dispatching a click: web/tests/
// fake-dom.js stores innerHTML without parsing it (see its own header), so
// the rows this column builds are never nodes there and neither the handler
// nor a click on it exists to observe. What the handler selects on does
// exist, and it is the whole of the mechanism — renderSessions binds the
// open-a-session click to `.srow` and to nothing else.
test("in a full render, only the running rows carry the class the click handler binds to", async () => {
  const { root, dom } = await list({
    sessions: [
      { short: "aa11", name: "a running task", lifecycle: "live" },
      { short: "bb22", name: "a stopped task", lifecycle: "stopped" },
      { short: "cc33", name: "a lost task", lifecycle: "dead" },
    ],
  });

  const rows = [...root.innerHTML.matchAll(/<article class="([^"]*)" data-short="([^"]*)"/g)]
    .map(([, classes, short]) => ({ short, classes: classes.split(/\s+/) }));
  assert.equal(rows.length, 3, "every session is a row");

  const clickable = rows.filter((r) => r.classes.includes("srow")).map((r) => r.short);
  assert.deepEqual(clickable, ["aa11"],
    "only the running session's row can be opened; there is no terminal behind the other two");
  dom.restore();
});

// The badge and the counter used to be able to disagree; the tracker is what
// made them agree. A session that is not running must not reach it at all —
// it has already stopped, for good, and no one can unstick it.
test("a session that stopped while blocked is not badged as stalled", async () => {
  const { root, dom } = await list({
    sessions: [
      { short: "bb22", name: "stopped while blocked", lifecycle: "stopped", lastState: "blocked" },
    ],
  });
  assert.ok(!root.innerHTML.includes("sbadge-stalled"),
    "a frozen 'blocked' is not a stall: nobody can answer it and nothing will clear it");
  dom.restore();
});

test("an unreadable job store is said out loud, not shown as a fleet with nothing stopped", async () => {
  const { root, dom } = await list({
    sessions: [{ short: "aa11", name: "a running task", lifecycle: "live" }],
    jobsError: "read job store /x: permission denied",
  });
  const html = root.innerHTML;
  assert.match(html, /sgone-error/, "the failure has a place on screen");
  assert.ok(html.includes("permission denied"), "with the reason reachable");
  dom.restore();
});

// --- the counters ---

test("the header counts running sessions only", () => {
  const snap = {
    sessions: [
      { short: "aa11", lifecycle: "live" },
      { short: "bb22", lifecycle: "stopped" },
      { short: "cc33", lifecycle: "dead" },
    ],
  };
  assert.deepEqual(headerSessions(snap).map((s) => s.short), ["aa11"],
    "a counter that can never come down is a counter nobody reads");
});

// --- the board ---

test("a card whose session is stopped is marked stopped, not orphaned", () => {
  const cards = [{ path: "/b/c.md", title: "paused work", session: "bb22", zone: "planned", progress: 40 }];
  const html = columnHTML("active", "active", cards, new Set(), new Set(["/b/c.md"]));
  assert.match(html, /kcard-stopped/);
  assert.ok(!html.includes("kcard-orphan"), "the card did not lose its session; the session is paused");
  assert.match(html, /ksession-stopped/);
});

test("a card whose session cannot come back is still an orphan", () => {
  const cards = [{ path: "/b/c.md", title: "lost work", session: "bb22", zone: "planned", progress: 40 }];
  const html = columnHTML("active", "active", cards, new Set(["/b/c.md"]), new Set());
  assert.match(html, /kcard-orphan/);
  assert.match(html, /ksession-dead/);
  assert.ok(!html.includes("kcard-stopped"));
});

test("a card with a live session is marked neither way", () => {
  const cards = [{ path: "/b/c.md", title: "work", session: "bb22", zone: "planned", progress: 40 }];
  const html = columnHTML("active", "active", cards, new Set(), new Set());
  assert.ok(!html.includes("kcard-orphan") && !html.includes("kcard-stopped"));
  assert.match(html, /class="ksession">bb22</);
});
