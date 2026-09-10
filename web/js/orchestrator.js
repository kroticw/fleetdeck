import { subscribe, get } from "./store.js";
import { sendText, setOrchestratorSession, setSessionLabel } from "./api.js";
import { t } from "./i18n.js";
// Re-exported rather than moved out of sight: these were this module's public
// surface before the shared one existed, and the tests that pin their behaviour
// are the same tests. Where a step is drawn now lives in steps.js.
export { parseAgentMessage, parseTaskNotification, unwrapEnvelope } from "./envelope.js";
export { atBottom, stepKey, STICK_THRESHOLD_PX } from "./steps.js";
import { syncSteps as syncStepRows } from "./steps.js";
import { wireImagePaste } from "./pasteimage.js";

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
// So: the frame is built once, the thread's steps are diffed against what is
// on screen, and a step that has not changed is not touched at all.

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
//
// The pickable session list is tracked unconditionally now, pinned or not:
// the dropdown that assigns the orchestrator lives in this column's own head
// at all times (see renderOrchestrator), not only while nothing is pinned,
// so a session joining or leaving the fleet has to reach it even mid-
// conversation. Losing a selection to that is not the risk it used to be —
// every part draw() touches is diffed in place (setText, syncStepRows, the
// dropdown's own option-list comparison), so a redraw the list forces is not
// a rebuild the way the picker's old full-screen replacement was.
export function viewSignature(snap, connected) {
  const { sessions, short, session } = resolveOrchestrator(snap);
  return JSON.stringify({
    connected,
    short,
    hasSnapshot: snap != null,
    sessions: pickableSessions(sessions).map((s) => [s.short, s.name ?? "", s.label ?? ""]),
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
  // to see. sendError covers sendText, the dropdown's own setOrchestratorSession
  // and the label edit's setSessionLabel — anything the operator directly
  // triggered — and is cleared only by the next such attempt. digestError
  // covers the periodic digest poll alone and is cleared only by that poll
  // succeeding. Neither may clear the other.
  let sendError = "";
  let digestError = "";

  // A pasted image is stored, and the path to it goes into the box — but the
  // session's first read from that directory stops to ask permission, and a
  // session stopping to ask looks exactly like a session that hung. This slot
  // is where it says so. Not an error: nothing went wrong, and putting it in
  // the red row would teach the operator to ignore the red row.
  let pasteNotice = "";

  // The transcript UUID the digest was last fetched for. A change of pin, or
  // the pinned session reappearing after being absent, is detected by
  // comparing against this and triggers an immediate re-fetch instead of
  // waiting out the 3-second interval.
  let fetchedFor = null;

  // Set while the pinned session's own name is being edited in place. draw()
  // must not touch .o-name while this is true — the same "do not disturb
  // what is being typed into" rule the textarea already gets for free by
  // never being replaced, applied here to a node that IS replaced (by the
  // input) for the duration of the edit.
  let editingLabel = false;

  // Whether the conversation frame has been built into root yet. There is
  // only ever one shape now — the operator's complaint was that a second one
  // existed (a full-screen session picker duplicating the task list on the
  // right) — so this is a plain guard against rebuilding it, not a mode to
  // switch between.
  let built = false;
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

  const buildConversationFrame = () => {
    root.replaceChildren();

    const head = el("div", "o-head");
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

    // The one control left for saying which session is the orchestrator: a
    // plain <select>, not a screen of its own. Choosing an option changes the
    // pin in place, without ever leaving the conversation already on screen —
    // the previous picker replaced this whole column with a list of every
    // session, which was a second, confusable way to do exactly what
    // clicking a session in the task list on the right already does. Its
    // own options are synced in draw(), never rebuilt here: this frame is
    // only ever built once.
    const pickSelect = document.createElement("select");
    pickSelect.className = "o-pick-select";
    pickSelect.setAttribute("aria-label", t("pick_orchestrator"));
    pickSelect.title = t("pick_orchestrator");
    pickSelect.addEventListener("change", async () => {
      const nextShort = pickSelect.value;
      try {
        await setOrchestratorSession(nextShort);
        sendError = "";
      } catch (err) {
        // The write failed, so the configuration still names whichever
        // session was actually pinned before — the very next draw() reads
        // that back and sets the select's value to it, reverting the
        // choice on screen without any separate "still really pinned"
        // state to track: the selected option already is the truth.
        sendError = `${t("orchestrator_pin_failed")}: ${err.message}`;
      }
      draw();
    });
    head.append(pickSelect);

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
      // Whatever the last paste had to say, it said about a path that has now
      // left the box. Keeping it would leave a sentence about a permission
      // prompt standing over a conversation it no longer describes.
      pasteNotice = "";
      try {
        await sendText(target, text);
        sendError = "";
      } catch (err) {
        sendError = err.message;
        area.value = text;
      }
      await refreshDigest();
    });

    // Cmd+V puts an image in here too, the same gesture the session panel
    // takes and through the same module — a second copy of it would start
    // diverging the day one of the two was fixed.
    //
    // `sendTo` is passed as the function it already is, so the session is
    // resolved at the moment of the paste. This column is exactly the place
    // that matters: the same textarea is re-pointed at a different session
    // whenever the pin moves, and a session captured when this frame was built
    // would keep sending images to whichever session used to be pinned —
    // quietly, with a path in the box and every appearance of success.
    //
    // Nothing is disposed because nothing is rebuilt: this frame is built once
    // (see `built`), and the textarea outlives every redraw.
    wireImagePaste(area, sendTo, {
      onError: (message) => {
        sendError = message;
        draw();
      },
      onNotice: (message) => {
        pasteNotice = message;
        draw();
      },
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

  // syncSelectOptions rebuilds the dropdown's <option> children only when the
  // set actually differs from what is on screen — the same "diff before you
  // touch it" discipline every other part of draw() follows, so a redraw
  // this column's own gate lets through cannot disturb an open dropdown a
  // person happens to be looking at for no reason.
  const syncSelectOptions = (select, wanted) => {
    const have = [...select.options].map((o) => [o.value, o.textContent]);
    if (JSON.stringify(have) === JSON.stringify(wanted)) return;
    select.replaceChildren(
      ...wanted.map(([value, label]) => {
        const opt = document.createElement("option");
        opt.value = value;
        opt.textContent = label;
        return opt;
      }),
    );
  };

  const draw = () => {
    const { snap, sessions, short, session } = resolve();
    const pinned = short !== "";

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

    if (!built) {
      built = true;
      buildConversationFrame();
    }

    const head = root.querySelector(".o-head");
    const thread = root.querySelector(".o-thread");

    showRow(root, "o-stale", isConnected ? "" : t("offline"), head);
    // Skipped while the name is being edited: .o-name has been replaced by
    // an <input> for the duration, and querying for a span that is not
    // there right now would be a silent no-op anyway — this says why.
    if (!editingLabel) {
      setText(
        head.querySelector(".o-name"),
        session ? session.label || session.name || session.short : t("not_pinned"),
      );
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

    // The dropdown's options mirror the daemon's own pickable session list;
    // its value mirrors the truth in the snapshot, never a locally-held
    // choice. A failed write (see buildConversationFrame's change handler)
    // leaves that truth unchanged, so setting the value from `short` here —
    // after the failure, same as before it — is what reverts the control to
    // what is actually pinned, with nothing extra to track.
    const select = head.querySelector(".o-pick-select");
    syncSelectOptions(select, [["", t("not_pinned")], ...pickableSessions(sessions).map((s) => [s.short, pickerLabel(s)])]);
    if (select.value !== short) select.value = short;

    showRow(root, "o-error o-error-digest", digestError, thread);
    showRow(root, "o-error o-error-send", sendError, root.querySelector(".o-form"));
    // Below the errors and above the box, where the path it is about has just
    // landed. Its own row rather than a third .o-error variant: it reports
    // something working as intended, and the red family is for what is not.
    showRow(root, "o-notice", pasteNotice, root.querySelector(".o-form"));

    // A session with no sessionId — nothing pinned, or the daemon no longer
    // lists the pinned one — has nothing to write a label against, and
    // nothing live to type a message into. Disabled rather than hidden, so
    // each control's place on screen stays stable and its state (not just
    // its presence) says why a click or a keystroke would do nothing.
    const editBtn = head.querySelector(".o-name-edit");
    if (editBtn) editBtn.disabled = !session;
    const area = root.querySelector("textarea");
    if (area) area.disabled = !session;

    if (!session) {
      // Two different facts read the same at this point — nothing pinned at
      // all, or pinned to a session the daemon no longer lists — and the
      // thread's one row must say which, not paper over the difference.
      const message = pinned ? t("session_not_listed") : t("no_orchestrator_thread");
      const cls = pinned ? "o-msg o-dead" : "o-thread-empty";
      const only = thread.children[0];
      if (!only || only.className !== cls) {
        thread.replaceChildren(el("div", cls, message));
      }
      return;
    }

    syncStepRows(thread, steps, stepClass);
  };

  const refreshDigest = async () => {
    const { session } = resolve();
    if (!session) return;
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
    const signature = viewSignature(snap, connected);
    if (signature === painted) return;
    painted = signature;
    isConnected = connected;
    draw();
  });
  setInterval(refreshDigest, 3000);
}
