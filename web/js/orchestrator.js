import { subscribe, get } from "./store.js";
import { sendText, setOrchestratorSession } from "./api.js";
import { t } from "./i18n.js";
import { renderMarkdown } from "./markdown.js";

// The orchestrator is not one session among many: it is the standing place of
// conversation, so it keeps its own column and its own input.
//
// It reads two different session identifiers, and mixing them up is the one
// mistake that would silently break this module: `short` is the daemon's
// short id — what `orchestrator.session` in configuration holds, what
// POST /api/sessions/{short}/text takes — while `sessionId` is the
// transcript UUID that GET /api/sessions/{sessionId}/digest matches on via
// transcript.Locate. A SessionView carries both; it has no `.id` field.
//
// The conversation is UPDATED, never rebuilt. It used to be assembled with one
// innerHTML write per draw, and draw ran on every snapshot the daemon pushed
// (two seconds apart by default) as well as on the three-second digest poll —
// so the whole column, thread and input included, was thrown away and recreated
// more than once a second. Three things that a person needs and a rebuild
// destroys: the scroll position, the selection they made with the mouse, and
// the caret in the textarea. None of them survives a node being replaced, and
// no amount of restoring them afterwards is the same thing, because the restore
// only knows what the code thought to save.
//
// So: the frame is built once per mode, the thread's steps are diffed against
// what is on screen, and a step that has not changed is not touched at all.

// resolveOrchestrator is the pure core of "which session, if any, does the
// pin point at right now" — kept free of get()/DOM so it can be tested
// directly against fixture snapshots instead of only through a full render.
export function resolveOrchestrator(snap) {
  const sessions = snap?.sessions ?? [];
  const short = snap?.orchestratorSession ?? "";
  const session = short ? sessions.find((s) => s.short === short) : undefined;
  return { sessions, short, session };
}

// contextPercent turns a transcript.Usage-shaped object into a rounded
// percentage, or null when there is nothing to show. transcript.Usage's JSON
// fields are exactly `tokens`/`window`/`estimated` — there is no `percent`
// field to read instead.
export function contextPercent(ctx) {
  return ctx?.window ? Math.round((ctx.tokens / ctx.window) * 100) : null;
}

// pickableSessions is the list the picker offers: every session that has a
// short id, because the short id is what a pin points at — a session without
// one cannot be pinned to and so cannot be offered.
export function pickableSessions(sessions) {
  return (sessions ?? []).filter((s) => s.short);
}

// pickerLabel is what a session's button reads. Session data comes from the
// daemon, not from this codebase (spec 3.1: the fleet is open), so it is
// untrusted — but it reaches the DOM through textContent and dataset now, not
// through markup, so it cannot be markup no matter what it contains.
export function pickerLabel(session) {
  return session.name || session.short;
}

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
const AGENT_MESSAGE = /^<agent-message\s+([^>]*)>([\s\S]*?)(?:<\/agent-message>)?$/;
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
  return { from: attributes.from, at: attributes.at ?? "", id: attributes.id ?? "", body: match[2].trim() };
}

// A background task's notification arrives as eight nested tags, of which two
// say what happened — the status and the summary — and the rest are identifiers.
// Raw, it is a screenful of machinery around one sentence.
//
// The identifiers are not dropped: they are useless to read and they are the
// only way to chase a lead afterwards, so they move out of the reading line
// into detail, which the panel hangs on the row as a tooltip.
const TASK_NOTIFICATION = /^<task-notification>([\s\S]*?)(?:<\/task-notification>)?$/;

function nested(text, name) {
  const match = new RegExp(`<${name}>([\\s\\S]*?)</${name}>`).exec(text);
  return match ? match[1].trim() : "";
}

export function parseTaskNotification(text) {
  const match = TASK_NOTIFICATION.exec(String(text ?? "").trim());
  if (!match) return null;
  const inner = match[1];
  const status = nested(inner, "status");
  const summary = nested(inner, "summary");
  // Without either of these there is nothing a person could read in place of
  // the tags, and replacing text with less text is not an improvement.
  if (!status && !summary) return null;
  const label = [t("background_task"), status, summary].filter(Boolean).join(" · ");
  const detail = [nested(inner, "task-id"), nested(inner, "tool-use-id"), nested(inner, "output-file")]
    .filter(Boolean)
    .join("\n");
  return { label, detail, body: nested(inner, "result") || nested(inner, "note") };
}

// unwrapStep is the one question buildStep asks: is this step an envelope, and
// if so, what should a person see instead of it?
//
// Every wrapper here is recognised the same strict way and fails the same soft
// way: the tag must open the step and must carry something worth showing, or the
// step is left exactly as it arrived. The fleet talks about these tags, so a
// step that merely names one has to survive — and swallowing text is worse than
// showing a tag, since a digest is read to find out what happened, and what it
// does not show did not happen as far as the reader can tell.
export function unwrapStep(text) {
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

// stepKey is what tells an unchanged step from a changed one. It is the step's
// own role and text, not the markup they render into: the rendered form is
// derived, and comparing derived output would make the diff depend on the
// renderer as well as on the data.
export function stepKey(step) {
  return `${step.role}\u0000${step.text}`;
}

// How close to the end counts as "reading the newest message". A person who
// has scrolled up even slightly is reading, and their position is theirs; a
// person sitting at the bottom is following along and wants to keep following.
// A few dozen pixels of slack absorbs a part-line offset and a sub-pixel
// rounding difference without turning either into a decision.
export const STICK_THRESHOLD_PX = 48;

// atBottom must be asked BEFORE the DOM changes, because appending to a thread
// changes scrollHeight and so changes the answer. An element that does not
// scroll at all (nothing in it yet, or shorter than its box) is at the bottom
// by definition, which is what puts a freshly opened conversation at its newest
// message.
export function atBottom(el, threshold = STICK_THRESHOLD_PX) {
  if (!el) return true;
  return el.scrollHeight - el.scrollTop - el.clientHeight <= threshold;
}

// viewSignature is everything this column actually shows, and nothing else.
// The daemon pushes a snapshot every couple of seconds and almost none of them
// change anything here — another session's progress moved, a context percentage
// somewhere else ticked. Redrawing on those is what made the column feel slow;
// worse, a redraw is what loses a selection, so an unrelated session's progress
// bar could wipe the text a person was in the middle of copying.
export function viewSignature(snap, connected, pickerRequested) {
  const { sessions, short, session } = resolveOrchestrator(snap);
  const pinned = short && !pickerRequested;
  return JSON.stringify({
    connected,
    pickerRequested,
    short,
    hasSnapshot: snap != null,
    // The picker lists sessions, so it depends on the list; the conversation
    // does not, and must not redraw when the list changes under it.
    sessions: pinned ? null : pickableSessions(sessions).map((s) => [s.short, s.name ?? ""]),
    session: session ? [session.short, session.name ?? "", session.sessionId, contextPercent(session.context)] : null,
  });
}

export function renderOrchestrator(root) {
  let steps = [];

  // Set by the store subscription on every push (including the initial
  // synchronous one) and read by draw() whenever it runs — including the
  // redraws triggered from inside this module itself (a send, a pick), which
  // happen between socket pushes and must still reflect the last known
  // connection state rather than assuming "connected" by default.
  let isConnected = false;

  // Two independent error slots, deliberately not one shared `error`. A
  // single variable let a successful background digest poll silently erase
  // the message from a failed send: sendText fails, refreshDigest is kicked
  // off right after it regardless, and if the transcript itself is still
  // readable that poll succeeds and blanks the very error the operator needed
  // to see. sendError covers sendText, the picker's setOrchestratorSession and
  // the back button's unpin — anything the operator directly triggered — and is
  // cleared only by the next such attempt. digestError covers the periodic
  // digest poll alone and is cleared only by that poll succeeding. Neither may
  // clear the other.
  let sendError = "";
  let digestError = "";

  // The transcript UUID the digest was last fetched for. A change of pin, or
  // the pinned session reappearing after being absent, is detected by
  // comparing against this and triggers an immediate re-fetch instead of
  // waiting out the 3-second interval.
  let fetchedFor = null;

  // The operator asked to go back to the session list. It is held here, not
  // derived from configuration, because the configuration write that clears the
  // pin can fail — and when it does, the screen must still do what was asked
  // while saying that a reload will undo it. Without this the next snapshot,
  // still carrying the old pin, would drag the column straight back into the
  // conversation the operator just left.
  let pickerRequested = false;

  // The mode currently built into the DOM, so the frame is rebuilt only when
  // the mode itself changes. null means nothing has been built yet.
  let mode = null;
  let painted = null;

  const resolve = () => {
    const snap = get();
    return { snap, ...resolveOrchestrator(snap) };
  };

  const el = (tag, className, text) => {
    const node = document.createElement(tag);
    if (className) node.className = className;
    if (text !== undefined) node.textContent = text;
    return node;
  };

  // setText writes only when the text differs. Assigning the same string still
  // replaces the text node, which drops a selection inside it — the reason this
  // module diffs at all.
  const setText = (node, text) => {
    if (node && node.textContent !== text) node.textContent = text;
  };

  // showRow keeps an optional single-line row (an error, a stale banner) in
  // sync without rebuilding its neighbours: created when first needed, updated
  // in place while it stays needed, removed when it is not.
  const showRow = (parent, className, text, before) => {
    const existing = parent.querySelector(`.${className.split(" ")[0]}`);
    if (!text) {
      if (existing) existing.remove();
      return;
    }
    if (existing) {
      setText(existing, text);
      return;
    }
    const row = el("div", className, text);
    if (before) parent.insertBefore(row, before);
    else parent.appendChild(row);
  };

  const buildConversationFrame = (onBack) => {
    root.replaceChildren();

    const head = el("div", "o-head");
    const back = el("button", "o-back", t("back_to_sessions"));
    back.setAttribute("type", "button");
    back.addEventListener("click", onBack);
    head.append(back);
    head.append(el("span", "o-name", ""));
    root.appendChild(head);

    root.appendChild(el("div", "o-thread"));

    const form = el("form", "o-form");
    const area = document.createElement("textarea");
    area.setAttribute("rows", "3");
    area.setAttribute("placeholder", t("write_to_orchestrator"));
    form.appendChild(area);
    root.appendChild(form);

    // The textarea is wired once and never replaced, so the draft, the caret
    // and the focus are simply never lost — there is nothing to restore because
    // nothing is destroyed. `sendTo` is read at send time rather than captured,
    // so the same node keeps working when the pin moves to another session.
    area.addEventListener("keydown", async (e) => {
      if (e.key !== "Enter" || e.shiftKey) return;
      e.preventDefault();
      const text = area.value.trim();
      if (!text) return;
      const target = sendTo();
      if (!target) return;
      area.value = "";
      try {
        await sendText(target, text);
        sendError = "";
      } catch (err) {
        sendError = err.message;
        area.value = text;
      }
      await refreshDigest();
    });

    return { head, thread: root.querySelector(".o-thread"), form, area };
  };

  // Which session a typed message goes to, resolved at the moment of sending.
  const sendTo = () => {
    const { short, session } = resolve();
    return session ? session.short : short;
  };

  // buildStep renders one step of the conversation.
  //
  // The body goes through markdown.js — the same renderer the cards and the
  // documentation use, rather than a second one — so ** reads as bold and a
  // fenced block reads as code. That renderer escapes its whole input before it
  // assembles anything, which is what makes it safe to hand its output to
  // innerHTML: a step's text comes from the daemon, and the fleet is open (spec
  // 3.1), so it is untrusted like a card body is untrusted. Nothing else here
  // touches innerHTML; the sender and the time are set as text.
  //
  // An empty Set of known cards, deliberately: a conversation has no card names
  // to resolve links against, so a [[link]] renders as a link that does not
  // work rather than one that goes somewhere wrong.
  const NO_CARDS = new Set();

  const buildStep = (step) => fillStep(el("div", ""), step);

  // fillStep writes one step into a row, whether the row is new or is being
  // updated. A row that is being updated keeps its node: only a step whose own
  // data changed gets here at all, and reusing the node keeps the thread's
  // children stable for everything around it.
  const fillStep = (row, step) => {
    row.className = `o-msg o-${step.role}`;
    row.dataset.stepKey = stepKey(step);
    row.replaceChildren();

    const wrapper = unwrapStep(step.text);
    if (wrapper) {
      const from = el("div", "o-msg-from", wrapper.label);
      // The identifiers hang here rather than in the reading line: out of the
      // way, and one hover from being read when someone needs to chase a lead.
      if (wrapper.detail) from.setAttribute("title", wrapper.detail);
      row.appendChild(from);
    }
    const body = el("div", "o-msg-body");
    const text = wrapper ? wrapper.body : step.text;
    // A wrapper whose whole content was the envelope leaves nothing to render;
    // the attribution line is then the entire step, which is honest — that is
    // all the notification actually said.
    if (text) body.innerHTML = renderMarkdown(text, NO_CARDS);
    row.appendChild(body);
    return row;
  };

  // syncSteps updates the thread in place: a step whose role and text are
  // unchanged is left exactly as it is, node and all. That is what lets a
  // selection survive a poll that returned the same twenty steps it returned
  // three seconds ago.
  const syncSteps = (thread) => {
    const stick = atBottom(thread);
    const rows = thread.children;

    for (let i = 0; i < steps.length; i += 1) {
      const step = steps[i];
      const existing = rows[i];
      if (!existing) {
        thread.appendChild(buildStep(step));
        continue;
      }
      // The comparison is on the step's own data, kept on the node, not on the
      // markup it produced: a rendered form is derived, and diffing derived
      // output would make an unchanged step depend on the renderer holding
      // still as well as on the data.
      if (existing.dataset.stepKey === stepKey(step)) continue;
      fillStep(existing, step);
    }
    while (thread.children.length > steps.length) {
      thread.children[thread.children.length - 1].remove();
    }

    // Only now, and only if they were already following the newest message. A
    // person who scrolled up is reading, and dragging them back down makes a
    // conversation longer than one screen impossible to read at all.
    if (stick) thread.scrollTop = thread.scrollHeight;
  };

  const goBack = async () => {
    pickerRequested = true;
    try {
      // Clearing the pin is what makes the screen and the configuration agree.
      // Leaving it set would mean a reload silently puts the operator back in
      // the conversation they just chose to leave, which is the same class of
      // lie as a control showing a value the file does not hold.
      await setOrchestratorSession("");
      sendError = "";
    } catch (err) {
      // The navigation still happens — it is local, and it is what was asked.
      // What failed is only the saving of it, and that is worth saying plainly
      // rather than hiding behind a screen that looks like it worked.
      sendError = `${t("unpin_failed")}: ${err.message}`;
    }
    draw();
  };

  const drawPicker = (sessions) => {
    // The picker holds no scroll position, no caret and no selection, so it is
    // rebuilt whole — there is nothing here for a rebuild to destroy. It is
    // built from nodes rather than markup all the same: session names and short
    // ids come from the daemon, and text put in with textContent cannot be
    // markup however it is spelled.
    const pick = el("div", "o-pick");
    if (!isConnected) pick.appendChild(el("div", "o-stale", t("offline")));
    pick.appendChild(el("p", "o-pick-empty", t("pick_orchestrator")));

    const list = el("div", "o-pick-list");
    for (const session of pickableSessions(sessions)) {
      const item = el("button", "o-pick-item", pickerLabel(session));
      item.setAttribute("type", "button");
      item.dataset.short = session.short;
      item.addEventListener("click", async () => {
        try {
          await setOrchestratorSession(session.short);
          sendError = "";
          // The operator has chosen; the request to see the list is spent, and
          // leaving it set would keep them staring at the list they just used.
          pickerRequested = false;
        } catch (err) {
          sendError = err.message;
        }
        draw();
      });
      list.appendChild(item);
    }
    pick.appendChild(list);

    if (sendError) pick.appendChild(el("div", "o-error o-error-send", sendError));
    root.replaceChildren(pick);
  };

  const draw = () => {
    const { snap, sessions, short, session } = resolve();
    const pinned = short !== "" && !pickerRequested;

    // A pin change, or the pinned session showing up after being absent,
    // means the digest on screen belongs to a different session (or none)
    // and must be refetched rather than left showing someone else's steps.
    if (pinned && session && session.sessionId !== fetchedFor) {
      fetchedFor = session.sessionId;
      refreshDigest();
    } else if ((!pinned || !session) && fetchedFor !== null) {
      fetchedFor = null;
      steps = [];
      // Nothing is being polled for any more, so a stale poll failure from
      // the session that just disappeared has nothing left to describe.
      digestError = "";
    }

    // Nothing pinned, and nothing to pick from either: the socket may simply
    // not be connected yet, or the daemon has no sessions. Say so plainly —
    // this is not a failure.
    if (!pinned && (!snap || sessions.length === 0)) {
      mode = "empty";
      const pick = el("div", "o-pick");
      if (!isConnected) pick.appendChild(el("div", "o-stale", t("offline")));
      pick.appendChild(el("p", "o-pick-empty", t("pick_orchestrator")));
      root.replaceChildren(pick);
      return;
    }

    if (!pinned) {
      mode = "picker";
      drawPicker(sessions);
      return;
    }

    if (mode !== "conversation") {
      mode = "conversation";
      buildConversationFrame(goBack);
    }

    const head = root.querySelector(".o-head");
    const thread = root.querySelector(".o-thread");

    showRow(root, "o-stale", isConnected ? "" : t("offline"), head);
    setText(head.querySelector(".o-name"), session ? session.name || session.short : short);

    const pct = session ? contextPercent(session.context) : null;
    const ctx = head.querySelector(".o-ctx");
    if (pct === null) {
      if (ctx) ctx.remove();
    } else if (ctx) {
      setText(ctx, `${pct}%`);
    } else {
      head.appendChild(el("span", "o-ctx", `${pct}%`));
    }

    showRow(root, "o-error o-error-digest", digestError, thread);
    showRow(root, "o-error o-error-send", sendError, root.querySelector(".o-form"));

    if (!session) {
      // Pinned, but the daemon does not currently list that session: there is
      // nothing to resolve a digest against, so none is requested. The one row
      // saying so is the thread's whole content.
      const only = thread.children[0];
      if (!only || only.className !== "o-msg o-dead") {
        thread.replaceChildren(el("div", "o-msg o-dead", t("session_not_listed")));
      }
      return;
    }

    syncSteps(thread);
  };

  const refreshDigest = async () => {
    const { short, session } = resolve();
    if (!session || (short !== "" && pickerRequested)) return;
    try {
      const res = await fetch(`/api/sessions/${encodeURIComponent(session.sessionId)}/digest?limit=20`);
      if (!res.ok) throw new Error((await res.json().catch(() => ({}))).error ?? res.statusText);
      steps = await res.json();
      digestError = "";
    } catch (err) {
      steps = [];
      digestError = err.message;
    }
    draw();
  };

  subscribe((snap, connected) => {
    // The gate: a snapshot that changes nothing this column shows changes
    // nothing on screen either. Without it every push from the daemon reached
    // draw(), and every draw could disturb the thread.
    const signature = viewSignature(snap, connected, pickerRequested);
    if (signature === painted) return;
    painted = signature;
    isConnected = connected;
    draw();
  });
  setInterval(refreshDigest, 3000);
}
