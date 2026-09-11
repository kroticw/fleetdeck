import { subscribe, get } from "./store.js";
import { setOrchestratorSession, setSessionLabel } from "./api.js";
import { t } from "./i18n.js";
import { ORCHESTRATOR_KEYS } from "./columnwidth.js";
import { mountColumnResize } from "./columnresize.js";
import { createLiveTerminal } from "./liveterminal.js";
import { FONT_KEYS } from "./terminalfont.js";

// The orchestrator is not one session among many: it is the standing place of
// conversation, so it keeps its own column.
//
// The column is the orchestrator session's own terminal — the same live
// terminal the session panel's screen tab draws (web/js/liveterminal.js), held
// open for as long as the column shows that session. It used to be a feed of
// the transcript's last steps with a box to type into, polled every three
// seconds; a person talking to the orchestrator through it saw a digest of the
// conversation rather than the conversation, and every question the session
// asked on its own screen — a permission, a choice — was not in the feed at
// all. The terminal is the session itself: what it shows, it shows as it
// happens, and what is typed into it reaches the session byte by byte.
//
// Three things follow from that and are decided here:
//   • the column's width is the session's width. The terminal follows its pane
//     and tells the session every size it settles on, so dragging the column's
//     edge reshapes the orchestrator session for everyone watching it — the
//     operator's own terminal included. That is the operator's choice: the
//     column is where the orchestrator is read;
//   • the terminal comes back by itself. The column has no tab to reopen, so a
//     lost connection, a restarting panel or a daemon briefly away is tried
//     again rather than left on "connection lost";
//   • a folded column holds no terminal. A column nobody can see is not where
//     the session is read, and what it attached with would keep the session at
//     a size nobody is looking at; folding lets it go, unfolding attaches anew.
//
// It reads two different session identifiers, and mixing them up is the one
// mistake that would silently break this module: `short` is the daemon's
// short id — what `orchestrator.session` in configuration holds and what the
// terminal attaches to — while `sessionId` is the transcript UUID a label is
// written against (PATCH /api/sessions/{sessionId}/label). A SessionView
// carries both; it has no `.id` field.

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
// somewhere else ticked. A redraw on those is work for nothing, and the head is
// where a person may be in the middle of editing a name.
//
// The pickable session list is tracked unconditionally, pinned or not: the
// dropdown that assigns the orchestrator lives in this column's own head at all
// times, so a session joining or leaving the fleet has to reach it. None of it
// can disturb the terminal: the terminal is reopened only when the session it
// draws changes, never by a redraw.
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

// renderOrchestrator draws the column into `root` and keeps it current. It
// returns a function that puts it away — the subscription and the terminal
// with its socket — which the page never needs, since the column lives as
// long as the page; a test mounting one column after another does.
//
// `links` is handed to the column's terminal as it is (see createLiveTerminal).
export function renderOrchestrator(root, { timers = globalThis, links = null } = {}) {
  // How wide this column is, whether it is folded away, and the edge a
  // person drags to change either — the same mechanism the session list
  // shares (web/js/columnresize.js), not a second copy of it. onChange runs
  // after the DOM already reflects the new width and fold state, which is
  // the order syncTerminal needs: a terminal opened on unfolding measures
  // the pane it is opened into, and a pane still marked folded has no size
  // to measure.
  const resize = mountColumnResize(root, ORCHESTRATOR_KEYS, { side: "left", onChange: () => syncTerminal() });
  const width = resize.width;

  // Set by the store subscription on every push (including the initial
  // synchronous one) and read by draw() whenever it runs — including the
  // redraws triggered from inside this module itself (a pick, a label), which
  // happen between socket pushes and must still reflect the last known
  // connection state rather than assuming "connected" by default.
  let isConnected = false;

  // Two error slots, deliberately not one shared `error`, because they are
  // cleared by different things and neither may erase the other.
  //
  // actionError is what the operator's own last action came to: the dropdown's
  // setOrchestratorSession, the label edit's setSessionLabel, a key the
  // terminal could not send. Cleared only by the next such action succeeding.
  //
  // streamError is the terminal's connection: ended and why, or being tried
  // again. Cleared by the terminal itself once it is back, and when the column
  // lets the terminal go — a sentence about a connection that is no longer
  // held describes nothing.
  let actionError = "";
  let streamError = "";

  // The terminal while the column holds one, and the short id of the session
  // it draws. The terminal is reopened when, and only when, that session
  // changes — a pin moved, the session left the fleet or came back, the column
  // was folded or unfolded — because reopening is a fresh attach on the daemon
  // and a repaint of the whole screen.
  let live = null;
  let terminalFor = null;

  // Set while the pinned session's own name is being edited in place. draw()
  // must not touch .o-name while this is true: it has been replaced by an
  // input for the duration, and a person is typing into it.
  let editingLabel = false;

  // Whether the frame has been built into root yet. There is only ever one
  // shape — the operator's complaint was that a second one existed (a
  // full-screen session picker duplicating the task list on the right) — so
  // this is a plain guard against rebuilding it, not a mode to switch between.
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
  // replaces the text node, which drops a selection inside it.
  const setText = (node, text) => {
    if (node && node.textContent !== text) node.textContent = text;
  };

  // showRow keeps an optional single-line row (an error, a stale banner) in
  // sync without rebuilding its neighbours: created when first needed, updated
  // in place while it stays needed, removed when it is not. A row is found by
  // its LAST class, which is the one that names it: both error rows share
  // .o-error for their look, and finding a row by that would hand one error's
  // row to the other.
  const showRow = (parent, className, text, before) => {
    const existing = parent.querySelector(`.${className.split(" ").pop()}`);
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

  // What the terminal says about itself for as long as the column holds one:
  // that it cannot type, and that it is not the size of its pane. Standing
  // facts, not failures, so they go in the quiet row rather than the red one.
  const standingNotice = () =>
    [live?.readOnly ? t("terminal_read_only") : "", live?.unfitted ? t("terminal_not_fitted") : ""]
      .filter(Boolean)
      .join("; ");

  // paintRows writes the error and notice rows, and nothing else. The terminal
  // reports on every key it sends, so this runs far more often than draw() and
  // must not touch the head or the terminal.
  const paintRows = () => {
    if (!built) return;
    const screen = root.querySelector(".o-screen");
    showRow(root, "o-error o-error-stream", streamError, screen);
    showRow(root, "o-error o-error-action", actionError, screen);
    showRow(root, "o-notice", standingNotice(), screen);
  };

  // The controls that size the column, and the one that brings it back.
  //
  // They live in a strip of their own above the head rather than among the
  // session's name, the edit pencil and the picker: those are about which
  // session is pinned, these are about the column itself, and at the narrow end
  // of the ladder a head holding six controls has room for none of them.
  //
  // The unfold button is built here too, and is the reason the strip is the
  // first child: when the column is folded everything below it is hidden, and
  // what remains has to be the way back. A control a person cannot see is a
  // control they do not have — the same rule the edit pencil was fixed for.
  const buildWidthControls = () => {
    const strip = el("div", "col-size col-size-left");

    const button = (className, glyph, label, onClick) => {
      const b = el("button", className, glyph);
      b.setAttribute("type", "button");
      b.setAttribute("aria-label", label);
      b.setAttribute("title", label);
      b.addEventListener("click", onClick);
      return b;
    };

    // Two, not four. The operator looked at "wider" and "narrower" as buttons
    // and asked for the edge instead — that is what .col-grip is. Folding stays
    // a button because it is a state rather than a size, and because dragging to
    // nothing is a bad way to reach it: a column dragged to nothing has no edge
    // left to grab.
    strip.append(
      button("col-size-btn col-size-unfold", "»", t("column_unfold"), () => width.unfold()),
      button("col-size-btn col-size-fold", "«", t("column_fold"), () => width.fold()),
    );
    return strip;
  };

  const buildFrame = () => {
    root.replaceChildren();

    root.appendChild(buildWidthControls());

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

    // The one control for saying which session is the orchestrator: a plain
    // <select>, not a screen of its own. Choosing an option changes the pin in
    // place — the previous picker replaced this whole column with a list of
    // every session, which was a second, confusable way to do exactly what
    // clicking a session in the task list on the right already does. Its own
    // options are synced in draw(), never rebuilt here: this frame is only
    // ever built once.
    const pickSelect = document.createElement("select");
    pickSelect.className = "o-pick-select";
    pickSelect.setAttribute("aria-label", t("pick_orchestrator"));
    pickSelect.title = t("pick_orchestrator");
    pickSelect.addEventListener("change", async () => {
      const nextShort = pickSelect.value;
      try {
        await setOrchestratorSession(nextShort);
        actionError = "";
      } catch (err) {
        // The write failed, so the configuration still names whichever
        // session was actually pinned before — the very next draw() reads
        // that back and sets the select's value to it, reverting the
        // choice on screen without any separate "still really pinned"
        // state to track: the selected option already is the truth.
        actionError = `${t("orchestrator_pin_failed")}: ${err.message}`;
      }
      draw();
    });
    head.append(pickSelect);

    root.appendChild(head);

    // Where the terminal goes, or the sentence saying why there is none. The
    // box itself stays for the life of the column; what is inside it changes
    // with the session it shows.
    root.appendChild(el("div", "o-screen"));
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
          actionError = "";
        } catch (err) {
          actionError = `${t("label_save_failed")}: ${err.message}`;
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

  // syncSelectOptions rebuilds the dropdown's <option> children only when the
  // set actually differs from what is on screen, so a redraw cannot disturb an
  // open dropdown a person happens to be looking at for no reason.
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

  const stopTerminal = () => {
    if (live) live.stop();
    live = null;
    terminalFor = null;
    streamError = "";
  };

  const openTerminal = (screen, short) => {
    const host = el("div", "o-term");
    // Marks the element keys typed into a live session come from, so the rest
    // of the page leaves them alone — Escape above all, which interrupts a
    // Claude Code session's turn and must never also close a card (see onKey
    // in web/js/card.js).
    host.dataset.terminal = "";
    screen.replaceChildren(host);
    live = createLiveTerminal(host, short, {
      timers,
      reconnect: true,
      links,
      fontKey: FONT_KEYS.orchestrator,
      report: {
        streamError: (message) => {
          streamError = message;
          paintRows();
        },
        actionError: (message) => {
          actionError = message;
          paintRows();
        },
        standing: paintRows,
      },
    });
    terminalFor = short;
    live.open();
  };

  // syncTerminal holds a terminal for the session the column shows, and none
  // when it shows none: nothing pinned, a pin the daemon does not list, or a
  // column folded away. In the first two cases the box says which, because the
  // two read the same otherwise and are different facts.
  const syncTerminal = () => {
    if (!built) return;
    const { short, session } = resolve();
    const wanted = session && !width.state().folded ? session.short : null;
    const screen = root.querySelector(".o-screen");
    if (wanted !== terminalFor) {
      stopTerminal();
      if (wanted) openTerminal(screen, wanted);
      else screen.replaceChildren();
    }
    if (!session) {
      const message = short ? t("session_not_listed") : t("no_orchestrator_thread");
      const shown = screen.querySelector(".o-screen-empty");
      if (shown) setText(shown, message);
      else screen.replaceChildren(el("div", "o-screen-empty", message));
    }
    paintRows();
  };

  const draw = () => {
    const { sessions, short, session } = resolve();

    if (!built) {
      built = true;
      buildFrame();
      // The grip already exists — mountColumnResize created it above, before
      // buildFrame even ran. What happens here is the first paint: the
      // remembered width has to be on screen from it, not from the first
      // click — a reload that showed the default for a moment and then
      // jumped would be its own small defect.
      resize.paint();
    }

    const head = root.querySelector(".o-head");

    showRow(root, "o-stale", isConnected ? "" : t("offline"), head);
    // Skipped while the name is being edited: .o-name has been replaced by
    // an <input> for the duration, and querying for a span that is not
    // there right now would be a silent no-op anyway — this says why.
    if (!editingLabel) {
      setText(head.querySelector(".o-name"), session ? pickerLabel(session) : t("not_pinned"));
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
    // choice. A failed write (see buildFrame's change handler) leaves that
    // truth unchanged, so setting the value from `short` here — after the
    // failure, same as before it — is what reverts the control to what is
    // actually pinned, with nothing extra to track.
    const select = head.querySelector(".o-pick-select");
    syncSelectOptions(select, [["", t("not_pinned")], ...pickableSessions(sessions).map((s) => [s.short, pickerLabel(s)])]);
    if (select.value !== short) select.value = short;

    // A pin with no session behind it — nothing pinned, or the daemon no
    // longer lists the pinned one — has nothing to write a label against.
    // Disabled rather than hidden, so the control's place on screen stays
    // stable and its state (not just its presence) says why a click would do
    // nothing.
    const editBtn = head.querySelector(".o-name-edit");
    if (editBtn) editBtn.disabled = !session;

    syncTerminal();
  };

  const unsubscribe = subscribe((snap, connected) => {
    // The gate: a snapshot that changes nothing this column shows changes
    // nothing on screen either.
    const signature = viewSignature(snap, connected);
    if (signature === painted) return;
    painted = signature;
    isConnected = connected;
    draw();
  });

  return () => {
    unsubscribe();
    stopTerminal();
  };
}
