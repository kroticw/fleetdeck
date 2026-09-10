import { subscribe, get } from "./store.js";
import { sendText, setOrchestratorSession, setSessionLabel } from "./api.js";
import { t } from "./i18n.js";
// Re-exported rather than moved out of sight: these were this module's public
// surface before the shared one existed, and the tests that pin their behaviour
// are the same tests. Where a step is drawn now lives in steps.js.
export { parseAgentMessage, parseTaskNotification, unwrapEnvelope } from "./envelope.js";
export { atBottom, stepKey, STICK_THRESHOLD_PX } from "./steps.js";
import { syncSteps as syncStepRows } from "./steps.js";

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

// pickerLabel is what a session's button reads. label is the operator's own
// name for the session (internal/config.SessionLabels, written through
// PATCH /api/sessions/{id}/label) and wins when set; name is whatever the
// daemon itself reports; short is what is left when neither exists — never
// blank, never invented. Session data comes from the daemon, not from this
// codebase (spec 3.1: the fleet is open), so it is untrusted — but it
// reaches the DOM through textContent and dataset now, not through markup,
// so it cannot be markup no matter what it contains.
export function pickerLabel(session) {
  return session.label || session.name || session.short;
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
    sessions: pinned ? null : pickableSessions(sessions).map((s) => [s.short, s.name ?? "", s.label ?? ""]),
    session: session
      ? [session.short, session.name ?? "", session.label ?? "", session.sessionId, contextPercent(session.context)]
      : null,
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

  // Set while the pinned session's own name is being edited in place. draw()
  // must not touch .o-name while this is true — the same "do not disturb
  // what is being typed into" rule the textarea already gets for free by
  // never being replaced, applied here to a node that IS replaced (by the
  // input) for the duration of the edit.
  let editingLabel = false;

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
    // Always visible, never only on hover: a control that only shows itself
    // to a pointer already hovering it does not exist for a person who has
    // not found it yet — the exact way the theme button's own plain text
    // once went unnoticed.
    const editBtn = el("button", "o-name-edit", "✎");
    editBtn.setAttribute("type", "button");
    editBtn.setAttribute("aria-label", t("edit_label"));
    editBtn.setAttribute("title", t("edit_label"));
    editBtn.addEventListener("click", startEditingLabel);
    head.append(editBtn);
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

  // startEditingLabel swaps .o-name for a text input, pre-filled with the
  // label alone — never with the name/short fallback text that is merely
  // displayed when no label is set. Pre-filling with the fallback would mean
  // pressing Enter without changing anything sets a label identical to what
  // was already shown, which is not "no change", it is a new label that
  // happens to read the same. The fallback is shown as the input's
  // placeholder instead, so the operator still sees what is currently
  // displayed while they decide what to type.
  const startEditingLabel = () => {
    const { session } = resolve();
    if (!session) return; // nothing to label when the daemon does not list it
    editingLabel = true;
    draw();
    const head = root.querySelector(".o-head");
    const nameSpan = head.querySelector(".o-name");
    const input = document.createElement("input");
    input.type = "text";
    input.className = "o-name-input";
    input.value = session.label ?? "";
    input.placeholder = session.name || session.short;
    head.insertBefore(input, nameSpan);
    nameSpan.remove();
    input.focus();

    // Enter and losing focus both save; Esc alone discards. Three outcomes,
    // never a fourth: a person who clicks away after typing loses nothing
    // silently invisible, which is worse than a save they can see and undo
    // by editing again, and a third, different behaviour on top of these two
    // would be one more thing to remember for no benefit.
    let settled = false;
    const finish = async (save) => {
      if (settled) return; // Enter's own save must not also run as the blur it causes
      settled = true;
      editingLabel = false;
      if (save) {
        try {
          const next = input.value.trim();
          await setSessionLabel(session.sessionId, next);
          // Reflected on the resolved session object itself, not only sent —
          // the next real snapshot fully replaces this object graph anyway
          // (store.js parses each push fresh), so this is a display-only
          // nudge that self-corrects the moment a server snapshot disagrees,
          // never a value this module invents and keeps believing.
          session.label = next;
          sendError = "";
        } catch (err) {
          sendError = `${t("label_save_failed")}: ${err.message}`;
        }
      }
      head.insertBefore(el("span", "o-name", ""), input);
      input.remove();
      draw();
    };

    input.addEventListener("keydown", (e) => {
      if (e.key === "Enter") {
        e.preventDefault();
        finish(true);
      } else if (e.key === "Escape") {
        e.preventDefault();
        finish(false);
      }
    });
    input.addEventListener("blur", () => finish(true));
  };

  // How a step's row is classed here. The shared renderer owns everything
  // inside a step; a pane owns what its rows are called.
  const stepClass = (role) => `o-msg o-${role}`;

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

  const drawPicker = (sessions, currentShort) => {
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
      // The picker is reached only after goBack has already cleared the pin
      // (or nothing was ever pinned), so currentShort is ordinarily empty —
      // except when that clearing PATCH itself failed, and the screen still
      // shows the list while the old pin is, in fact, still the real one. A
      // person reading the list in that moment must see which item that is,
      // not mistake a browser focus ring on the first button for a mark that
      // was never drawn — the exact confusion a naive read of this screen
      // produced once already.
      const current = currentShort !== "" && session.short === currentShort;
      const item = el("button", current ? "o-pick-item o-pick-item-current" : "o-pick-item", pickerLabel(session));
      item.setAttribute("type", "button");
      item.dataset.short = session.short;
      if (current) {
        item.setAttribute("aria-current", "true");
        item.setAttribute("title", t("current_orchestrator"));
      }
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
      drawPicker(sessions, short);
      return;
    }

    if (mode !== "conversation") {
      mode = "conversation";
      buildConversationFrame(goBack);
    }

    const head = root.querySelector(".o-head");
    const thread = root.querySelector(".o-thread");

    showRow(root, "o-stale", isConnected ? "" : t("offline"), head);
    // Skipped while the name is being edited: .o-name has been replaced by
    // an <input> for the duration, and querying for a span that is not
    // there right now would be a silent no-op anyway — this says why.
    if (!editingLabel) {
      setText(head.querySelector(".o-name"), session ? session.label || session.name || session.short : short);
    }

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

    // A session the daemon no longer lists has no sessionId to write a label
    // against — disabled rather than hidden, so the control's place on
    // screen stays stable and its state (not just its presence) says why a
    // click would do nothing.
    const editBtn = head.querySelector(".o-name-edit");
    if (editBtn) editBtn.disabled = !session;

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

    syncStepRows(thread, steps, stepClass);
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
