// The envelopes the fleet wraps its messages in, and how to get a message back
// out of one.
//
// This is not about markdown and does not live in markdown.js. That module is
// about a format the world defines; this one is about the shape of our own
// fleet's traffic — which tags wrap a message, which of their fields a person
// needs. A month from now nobody would be able to say which half a change
// belongs to if the two were one file.
//
// It is separate from steps.js for a sharper reason. Four places show text that
// can arrive wrapped: the orchestrator column, the session panel's digest, a
// session's reason in the right-hand column, and the stalled counter in the
// header. All four must strip the envelope. Only the first two may render what
// is inside as markdown — a session's `detail` is carried verbatim on purpose
// (spec 3.1), because a person decides from its exact words whether they are
// being called. Folding "unwrap" and "render" into one function would make
// those two panes render markdown they must not.

import { t } from "./i18n.js";

// Fleet messages arrive in a transcript wrapped in an <agent-message> tag that
// carries who wrote it and when. Shown raw it is half a line of attributes
// before every message and a closing tag after it — the operator reads more
// wrapper than message.
//
// The match is deliberately strict and the failure is deliberately soft: the
// tag must open the step and its attributes must include a sender, or the step
// is left exactly as it arrived. Something that merely looks like a tag is
// text, and swallowing text is worse than showing a tag — a digest is read to
// find out what happened, and what it does not show did not happen as far as
// the reader can tell.
//
// A missing closing tag is accepted because a digest is a tail of a transcript
// and can cut anywhere, including mid-message.
// The closing tag, when there is one, ends the message — and whatever follows
// it is text of its own, not part of the message. A wrapped step routinely
// carries a system note after the closing tag, and matching to the end of the
// string left that note inside the body with a stray `</agent-message>` in
// front of it, visible on screen.
//
// Both parts are kept. Only the tag itself goes.
const AGENT_MESSAGE = /^<agent-message\s+([^>]*)>([\s\S]*?)(?:<\/agent-message>([\s\S]*))?$/;
const ATTRIBUTE = /([a-z-]+)="([^"]*)"/g;

export function parseAgentMessage(text) {
  const match = AGENT_MESSAGE.exec(String(text ?? "").trim());
  if (!match) return null;
  const attributes = {};
  for (const [, name, value] of match[1].matchAll(ATTRIBUTE)) attributes[name] = value;
  // Without a sender the wrapper says nothing the body does not, so there is
  // nothing to gain by removing it and a line of text to lose by getting it
  // wrong.
  if (!attributes.from) return null;
  return { from: attributes.from, at: attributes.at ?? "", id: attributes.id ?? "", body: joinParts(match[2], match[3]) };
}

// A background task's notification arrives as eight nested tags, of which two
// say what happened — the status and the summary — and the rest are identifiers.
// Raw, it is a screenful of machinery around one sentence.
//
// The identifiers are not dropped: they are useless to read and they are the
// only way to chase a lead afterwards, so they move out of the reading line
// into detail, which the panel hangs on the row as a tooltip.
const TASK_NOTIFICATION = /^<task-notification>([\s\S]*?)(?:<\/task-notification>([\s\S]*))?$/;

// joinParts puts a message back together from what was inside the envelope and
// whatever followed it, keeping both and losing only the tag between them.
function joinParts(inside, after) {
  return [String(inside ?? "").trim(), String(after ?? "").trim()].filter(Boolean).join("\n\n");
}

function nested(text, name) {
  const match = new RegExp(`<${name}>([\\s\\S]*?)</${name}>`).exec(text);
  return match ? match[1].trim() : "";
}

export function parseTaskNotification(text) {
  const match = TASK_NOTIFICATION.exec(String(text ?? "").trim());
  if (!match) return null;
  const inner = match[1];
  const trailing = (match[2] ?? "").trim();
  const status = nested(inner, "status");
  const summary = nested(inner, "summary");
  // Without either of these there is nothing a person could read in place of
  // the tags, and replacing text with less text is not an improvement.
  if (!status && !summary) return null;
  const label = [t("background_task"), status, summary].filter(Boolean).join(" · ");
  const detail = [nested(inner, "task-id"), nested(inner, "tool-use-id"), nested(inner, "output-file")]
    .filter(Boolean)
    .join("\n");
  return { label, detail, body: joinParts(nested(inner, "result") || nested(inner, "note"), trailing) };
}

// unwrapEnvelope is the one question every pane asks: is this text an envelope,
// and if so, what should a person see instead of it?
//
// Every wrapper here is recognised the same strict way and fails the same soft
// way: the tag must open the step and must carry something worth showing, or the
// step is left exactly as it arrived. The fleet talks about these tags, so a
// step that merely names one has to survive — and swallowing text is worse than
// showing a tag, since a digest is read to find out what happened, and what it
// does not show did not happen as far as the reader can tell.
export function unwrapEnvelope(text) {
  const agent = parseAgentMessage(text);
  if (agent) {
    return {
      label: agent.at ? `${agent.from} · ${agent.at}` : agent.from,
      detail: agent.id ?? "",
      body: agent.body,
    };
  }
  return parseTaskNotification(text);
}

// The runtime bolts a paragraph of instructions onto a message from another
// agent: how to reply, which MCP tool to call, what to do without it. It is
// addressed to the agent, not to the person reading the panel, and on screen it
// took a third of the card and stood above the message it was attached to.
//
// Each form is matched by BOTH of its ends, and neither end alone is enough.
// That is not caution for its own sake: the fleet discusses these tools by name
// in ordinary messages — "call send_message, that is what I did" is content —
// and a rule keyed on a phrase would eat it. A note whose closing phrase is
// missing is something else that merely begins the same way, and is left alone.
//
// Only the note is removed. Text before it and text after it both stay: a real
// transcript has a link written after one, and cutting to the end of the string
// would have taken it.
const TOOL_NOTES = [
  {
    start: /\[m-[0-9a-z]{6}\] This is a message from another agent\b/,
    end: "answering into the void.",
  },
  {
    start: /This came from another Claude session\b/,
    end: "permission laundering.",
  },
];

export function stripToolNote(text) {
  let out = String(text ?? "");
  for (const note of TOOL_NOTES) {
    const from = note.start.exec(out);
    if (!from) continue;
    const closes = out.indexOf(note.end, from.index);
    // No closing phrase: not this note, whatever it looks like.
    if (closes < 0) continue;
    out = out.slice(0, from.index) + out.slice(closes + note.end.length);
  }
  return out.replace(/\n{3,}/g, "\n\n").trim();
}

// envelopeText is unwrapEnvelope for a pane that shows plain text: the label
// and the body as one line, with nothing rendered and nothing dropped.
//
// It exists so that a pane bound to show text verbatim can still lose the tag
// around it. Removing the envelope is not a paraphrase — every word that was
// inside is still here, in order — which is what keeps this compatible with
// carrying `detail` verbatim. A step that is not an envelope comes back
// unchanged.
export function envelopeText(text) {
  const wrapper = unwrapEnvelope(text);
  if (!wrapper) return String(text ?? "");
  return [wrapper.label, wrapper.body].filter(Boolean).join(" · ");
}
