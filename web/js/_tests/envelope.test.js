// web/js/_tests/envelope.test.js
//
// The envelope is the one thing all four panes share, and the reason it is its
// own module: they must all strip it, and only two of them may render what is
// inside as markdown.
//
// Run with: node --test web/js/_tests/envelope.test.js

import test from "node:test";
import assert from "node:assert/strict";
import { unwrapEnvelope, envelopeText } from "../envelope.js";

globalThis.navigator ??= { language: "en" };

const AGENT = '<agent-message id="m-1" from="06a1f607" at="2026-09-10T15:00:00+05:00">**bold** body</agent-message>';

test("envelopeText drops the tag and keeps every word that was inside it", () => {
  const text = envelopeText(AGENT);
  assert.ok(!text.includes("<agent-message"), "the tag is what a person did not need");
  assert.ok(text.includes("06a1f607"), "who wrote it is not lost");
  assert.ok(text.includes("**bold** body"), "and the message is carried exactly, markdown source and all");
});

test("envelopeText renders nothing — a reason is carried verbatim, not typeset", () => {
  // spec 3.1: a person decides from a stalled session's exact words whether
  // they are being called, so the panes that show `detail` must not typeset it.
  const text = envelopeText(AGENT);
  assert.ok(!text.includes("<strong>"), "no markup is produced here");
  assert.ok(text.includes("**"), "the asterisks a session wrote are the session's own words");
});

test("envelopeText leaves text that is not an envelope exactly as it was", () => {
  assert.equal(envelopeText("usage limit reached"), "usage limit reached");
  assert.equal(envelopeText("a sentence naming <agent-message> in passing"), "a sentence naming <agent-message> in passing");
  assert.equal(envelopeText(""), "");
  assert.equal(envelopeText(undefined), "");
});

test("envelopeText unwraps a task notification into its outcome and result", () => {
  const notification = [
    "<task-notification>",
    "<task-id>abc123</task-id>",
    "<status>completed</status>",
    "<summary>Agent finished</summary>",
    "<result>all green</result>",
    "</task-notification>",
  ].join("\n");
  const text = envelopeText(notification);
  assert.ok(text.includes("completed"));
  assert.ok(text.includes("Agent finished"));
  assert.ok(text.includes("all green"));
  assert.ok(!text.includes("<task-id>"), "the identifiers are not part of the reading line");
});

test("unwrapEnvelope and envelopeText agree about what is an envelope", () => {
  // One recognition rule, not two: a pane that strips and a pane that renders
  // must never disagree about whether a step was wrapped.
  for (const sample of [AGENT, "plain text", "prefix <agent-message from=\"x\">y</agent-message>", ""]) {
    const wrapped = unwrapEnvelope(sample) !== null;
    const changed = envelopeText(sample) !== String(sample ?? "");
    assert.equal(wrapped, changed, `disagreement on: ${sample.slice(0, 40)}`);
  }
});

// --- what follows the closing tag ---
//
// Found by looking, not by counting: a step routinely carries a system note
// after the envelope's closing tag, and matching to the end of the string left
// a stray `</agent-message>` sitting in the middle of the message on screen.

test("a note after the closing tag is kept, and the tag itself is not", () => {
  const step = '<agent-message id="m-1" from="a" at="t">the message</agent-message>\n\n[m-1] A note the system appended.';
  const wrapper = unwrapEnvelope(step);
  assert.ok(!wrapper.body.includes("</agent-message>"), "the tag was the only thing to remove");
  assert.ok(wrapper.body.includes("the message"), "what was inside stays");
  assert.ok(wrapper.body.includes("A note the system appended."), "and what came after stays too");
});

test("a message with no note after it is unchanged by that rule", () => {
  const wrapper = unwrapEnvelope('<agent-message from="a" at="t">just the message</agent-message>');
  assert.equal(wrapper.body, "just the message");
});

test("an envelope cut off by the digest's tail still yields its body", () => {
  const wrapper = unwrapEnvelope('<agent-message from="a">cut off mid-sen');
  assert.equal(wrapper.body, "cut off mid-sen");
});

test("a notification with a note after its closing tag keeps both", () => {
  const step = "<task-notification>\n<status>completed</status>\n<summary>done</summary>\n<result>green</result>\n</task-notification>\n\ntrailing note";
  const wrapper = unwrapEnvelope(step);
  assert.ok(!wrapper.body.includes("</task-notification>"));
  assert.ok(wrapper.body.includes("green"));
  assert.ok(wrapper.body.includes("trailing note"));
});

