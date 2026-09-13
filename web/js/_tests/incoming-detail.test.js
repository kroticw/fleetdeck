// web/js/_tests/incoming-detail.test.js
//
// The daemon writes the text of the last message sent to a session into that
// session's `detail`, envelope and all, until the session says something of its
// own. The panel used to show `detail` as the reason a session stopped, with the
// envelope stripped and the words kept, so a person read the words of whoever
// wrote to a session as that session's explanation of itself — an orchestrator
// writing to a frozen session saw its own letter on screen as the reason the
// session froze.
//
// These pin the property, not one sample: whatever kind of envelope arrives, on
// whatever row would have shown `detail` as the reason, its text is never the
// reason — not in the row, not in the header's list — and when it is shown it is
// signed as an incoming message. A session's own words in `detail` stay the
// reason, as before.
//
// Lives under _tests/ for the same reason header.test.js does: web/embed.go's
// plain directory pattern excludes it from the binary.
//
// Run with: node --test web/js/_tests/incoming-detail.test.js

import test from "node:test";
import assert from "node:assert/strict";

globalThis.navigator ??= { language: "en" };

const { rowHtml } = await import("../sessions.js");
const { stallReason, stalledList } = await import("../header.js");
const envelope = await import("../envelope.js");
const { t } = await import("../i18n.js");

const NS_PER_MS = 1e6;
const minutes = (m) => m * 60 * 1000 * NS_PER_MS;

// Three kinds of incoming text, in the shapes the daemon was measured writing.
// The agent message is cut off with an ellipsis and has no closing tag: the
// daemon truncates `detail`, and that is how it looked on df840f0b and d16fd6bc.
// The queued command is what lands when a session is written to mid-turn and
// more than one thing queues up: the operator's typed text and an agent's
// envelope run together into one string, the tag in the middle of it.
const KINDS = [
  {
    kind: "agent-message",
    sender: "06a1f607",
    detail: (body) =>
      `<agent-message id="m-2d92b3" from="06a1f607" to="worker [df840f0b]" at="2026-09-12T23:50:40+05:00"> ${body}…`,
  },
  {
    kind: "cross-session-message",
    sender: "orchestrator",
    detail: (body) =>
      `<cross-session-message from="uds:/tmp/claude-501/orchestrator.sock" from-name="orchestrator">${body}</cross-session-message>`,
  },
  {
    kind: "queued command",
    sender: "52d3591d",
    detail: (body) =>
      `take a look at the board<agent-message id="m-ff496c" from="52d3591d" to="worker [df840f0b]" at="2026-09-13T10:00:00+05:00">${body}</agent-message>`,
  },
];

const BODIES = [
  "The review is accepted, the decisions are mine",
  "approve the **three** MRs",
  "first line\nsecond line",
  'quote " and <b>markup</b> & an ampersand',
];

// Every row that falls back to `detail` for its reason: a bare flag stall, the
// same stall said by tempo, a source that never reports `needs`, and a session
// left unanswered past the limit. The second argument is what the stall tracker
// would have answered for the row.
const SHAPES = [
  { shape: "flag-only stall", session: { needs: "", state: "blocked", tempo: "active" }, stalled: true },
  { shape: "tempo stall", session: { needs: "", state: "working", tempo: "blocked" }, stalled: true },
  { shape: "needs not reported", session: { state: "working", tempo: "active" }, stalled: false },
  {
    shape: "left unanswered",
    session: { needs: "", state: "working", tempo: "active", silentFor: minutes(20), unansweredFor: minutes(20) },
    stalled: false,
  },
];

function cases() {
  const out = [];
  for (const k of KINDS) {
    for (const body of BODIES) {
      for (const sh of SHAPES) {
        out.push({ ...k, ...sh, body, session: { short: "df840f0b", name: "n", ...sh.session, detail: k.detail(body) } });
      }
    }
  }
  return out;
}

const escaped = (text) =>
  String(text)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#39;");

// The words that must not surface as a reason, in the form they would take in the
// markup. The first line is enough: a line clamp or a join can only shorten it.
const needle = (body) => escaped(body.split("\n")[0]);

function element(html, className) {
  const match = new RegExp(`<div class="${className}"([^>]*)>([^<]*)</div>`).exec(html);
  return match ? { attributes: match[1], text: match[2] } : null;
}

// --- the property -------------------------------------------------------------

test("an incoming message in detail is never a session row's reason, for any kind of envelope", () => {
  for (const c of cases()) {
    const html = rowHtml(c.session, c.stalled);
    const reason = element(html, "sreason");
    const where = `${c.kind} on a ${c.shape} row`;
    if (reason) {
      assert.ok(!reason.text.includes(needle(c.body)), `${where}: the message is shown as the reason`);
      assert.ok(!reason.attributes.includes(needle(c.body)), `${where}: the message is the reason's title`);
    }
  }
});

test("an incoming message in detail is never a stalled reason in the header, for any kind of envelope", () => {
  for (const c of cases()) {
    const where = `${c.kind} on a ${c.shape} row`;
    assert.equal(stallReason(c.session), "", `${where}: stallReason offers the message as a reason`);
    assert.ok(!stalledList([c.session]).includes(needle(c.body)), `${where}: the header lists the message as a reason`);
  }
});

test("where an incoming message is shown on a row, it is signed as incoming and names its sender", () => {
  for (const c of cases()) {
    const html = rowHtml(c.session, c.stalled);
    const incoming = element(html, "sincoming");
    const where = `${c.kind} on a ${c.shape} row`;
    assert.ok(incoming, `${where}: the row does not say a message came in`);
    assert.ok(incoming.text.includes(escaped(t("incoming_from"))), `${where}: the line is not signed as incoming`);
    assert.ok(incoming.text.includes(escaped(c.sender)), `${where}: the line does not say who wrote it`);
    assert.ok(incoming.text.includes(needle(c.body)), `${where}: the line does not say what was written`);
    assert.ok(!incoming.text.includes("&lt;agent-message") && !incoming.text.includes("&lt;cross-session-message"),
      `${where}: the envelope's tag is on screen`);
    assert.ok(incoming.attributes.includes(escaped(c.detail(c.body)).split("\n")[0]),
      `${where}: the title does not keep the text exactly as the daemon wrote it`);
  }
});

test("a stalled row whose detail is only an incoming message carries no reason at all", () => {
  // The daemon knows nothing about why this session stopped; the panel must not
  // invent it out of somebody else's letter.
  for (const c of cases().filter((x) => x.stalled)) {
    assert.equal(element(rowHtml(c.session, true), "sreason"), null, `${c.kind} on a ${c.shape} row`);
  }
});

test("the header lists the real reasons and does not count an incoming message as one left out", () => {
  const letter = { short: "aa11", needs: "", state: "blocked", detail: KINDS[0].detail("please answer") };
  const html = stalledList([letter, { needs: "usage limit reached" }]);
  assert.ok(html.includes("usage limit reached"));
  assert.ok(!html.includes("please answer"));
  assert.ok(!html.includes("stall-more"), "an incoming message is not a reason past the cap");
});

// --- what the parser says about the text ---------------------------------------

test("the parser reads every kind of incoming text as parts signed by their senders", () => {
  assert.equal(typeof envelope.incomingMessage, "function", "envelope.js has no reader for incoming text");
  for (const k of KINDS) {
    const message = envelope.incomingMessage(k.detail("body text"));
    assert.ok(message, `${k.kind} is not recognised as incoming`);
    const signed = message.parts.filter((part) => part.from === k.sender);
    assert.equal(signed.length, 1, `${k.kind}: the sender is not named`);
    assert.equal(signed[0].body.replace(/…$/, "").trim(), "body text");
  }
});

test("the operator's text queued before an agent's envelope is not signed with the agent's name", () => {
  // Signing a person's words with another session's name is the mistake the
  // cross-session reader was written to avoid; a queued pair must not reintroduce it.
  assert.equal(typeof envelope.incomingMessage, "function", "envelope.js has no reader for incoming text");
  const message = envelope.incomingMessage(KINDS[2].detail("the agent's words"));
  assert.deepEqual(
    message.parts.map((part) => [part.from, part.body]),
    [["", "take a look at the board"], ["52d3591d", "the agent's words"]],
  );
});

test("the reply instructions the runtime bolts onto a message are not shown as its words", () => {
  assert.equal(typeof envelope.incomingMessage, "function", "envelope.js has no reader for incoming text");
  const note =
    "[m-4cdba5] This is a message from another agent, not from your user. To answer, call send_message, " +
    "or say so in your own session rather than answering into the void.";
  const message = envelope.incomingMessage(`<agent-message from="06a1f607">the words\n\n${note}</agent-message>`);
  assert.equal(message.parts[0].body, "the words");
});

// --- no regression: a session's own words are still its reason ------------------

const OWN = [
  "v0.6.0 & v0.9.0 benches ready; awaiting operator + orchestrator",
  "waiting on my own subagents",
  "waiting for an <agent-message> reply from the orchestrator",
  'quoting <agent-message from="x"> in the middle of my own status',
];

test("a session's own summary in detail is still its reason, in the row and in the header", () => {
  for (const own of OWN) {
    for (const sh of SHAPES) {
      const session = { short: "bb22", name: "n", ...sh.session, detail: own };
      const reason = element(rowHtml(session, sh.stalled), "sreason");
      assert.ok(reason, `${sh.shape}: "${own}" lost its reason`);
      assert.equal(reason.text, escaped(own), `${sh.shape}: "${own}" is not shown verbatim`);
      assert.equal(element(rowHtml(session, sh.stalled), "sincoming"), null, `${sh.shape}: "${own}" is called incoming`);
      assert.equal(stallReason(session), own, `${sh.shape}: the header lost "${own}"`);
    }
  }
});

test("a session's own words are not read as incoming by the parser", () => {
  assert.equal(typeof envelope.incomingMessage, "function", "envelope.js has no reader for incoming text");
  for (const own of [...OWN, "", "plain text"]) {
    assert.equal(envelope.incomingMessage(own), null, `"${own}" was read as incoming`);
  }
  assert.equal(envelope.incomingMessage(undefined), null);
});

test("a waiting session's question is still its reason when detail holds a letter", () => {
  const session = { short: "cc33", name: "n", needs: "answer: which branch? (a · b)", detail: KINDS[0].detail("hello") };
  const reason = element(rowHtml(session), "sreason");
  assert.equal(reason.text, escaped("answer: which branch? (a · b)"));
  assert.equal(stallReason(session), "answer: which branch? (a · b)");
});
