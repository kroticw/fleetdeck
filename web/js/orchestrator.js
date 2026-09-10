import { subscribe, get } from "./store.js";
import { sendText, setOrchestratorSession } from "./api.js";
import { t } from "./i18n.js";

// The orchestrator is not one session among many: it is the standing place of
// conversation, so it keeps its own column and its own input.
//
// It reads two different session identifiers, and mixing them up is the one
// mistake that would silently break this module: `short` is the daemon's
// short id — what `orchestrator.session` in configuration holds, what
// POST /api/sessions/{short}/text takes — while `sessionId` is the
// transcript UUID that GET /api/sessions/{sessionId}/digest matches on via
// transcript.Locate. A SessionView carries both; it has no `.id` field.
export function renderOrchestrator(root) {
  let steps = [];

  // Two independent error slots, deliberately not one shared `error`. A
  // single variable let a successful background digest poll silently erase
  // the message from a failed send: sendText fails, refreshDigest is kicked
  // off right after it regardless, and if the transcript itself is still
  // readable that poll succeeds and blanks the very error the operator needed
  // to see. sendError covers sendText and the picker's setOrchestratorSession
  // — anything the operator directly triggered — and is cleared only by the
  // next such attempt. digestError covers the periodic digest poll alone and
  // is cleared only by that poll succeeding. Neither may clear the other.
  let sendError = "";
  let digestError = "";

  // The transcript UUID the digest was last fetched for. A change of pin, or
  // the pinned session reappearing after being absent, is detected by
  // comparing against this and triggers an immediate re-fetch instead of
  // waiting out the 3-second interval.
  let fetchedFor = null;

  // Preserved across redraws so the once-a-second snapshot push (see
  // internal/server/ws.go) does not erase what the operator is mid-typing —
  // draw() rebuilds the whole column's markup on every call.
  let draftText = "";

  const resolve = () => {
    const snap = get();
    const sessions = snap?.sessions ?? [];
    const short = snap?.orchestratorSession ?? "";
    const session = short ? sessions.find((s) => s.short === short) : undefined;
    return { snap, sessions, short, session };
  };

  const formHTML = () => `
    <form class="o-form">
      <textarea rows="3" placeholder="${t("write_to_orchestrator")}">${escapeHTML(draftText)}</textarea>
    </form>`;

  const wireForm = (targetShort, hadFocus) => {
    const form = root.querySelector(".o-form");
    if (!form) return;
    const area = form.querySelector("textarea");
    area.addEventListener("input", () => {
      draftText = area.value;
    });
    area.addEventListener("keydown", async (e) => {
      if (e.key !== "Enter" || e.shiftKey) return;
      e.preventDefault();
      const text = area.value.trim();
      if (!text) return;
      area.value = "";
      draftText = "";
      try {
        await sendText(targetShort, text);
        sendError = "";
      } catch (err) {
        sendError = err.message;
        area.value = text;
        draftText = text;
      }
      await refreshDigest();
    });
    if (hadFocus) {
      area.focus();
      const len = area.value.length;
      area.setSelectionRange(len, len);
    }
  };

  const draw = () => {
    const { snap, sessions, short, session } = resolve();

    // A pin change, or the pinned session showing up after being absent,
    // means the digest on screen belongs to a different session (or none)
    // and must be refetched rather than left showing someone else's steps.
    if (session && session.sessionId !== fetchedFor) {
      fetchedFor = session.sessionId;
      refreshDigest();
    } else if (!session && fetchedFor !== null) {
      fetchedFor = null;
      steps = [];
      // Nothing is being polled for any more, so a stale poll failure from
      // the session that just disappeared has nothing left to describe.
      digestError = "";
    }

    // Capture whether the textarea (if one is on screen right now) has focus,
    // so it can be restored after this redraw replaces it with a new node.
    const prevArea = root.querySelector(".o-form textarea");
    const hadFocus = prevArea != null && document.activeElement === prevArea;

    // Nothing pinned, and nothing to pick from either: the socket may simply
    // not be connected yet, or the daemon has no sessions. Say so plainly —
    // this is not a failure.
    if (!short && (!snap || sessions.length === 0)) {
      root.innerHTML = `<div class="o-pick"><p class="o-pick-empty">${escapeHTML(t("pick_orchestrator"))}</p></div>`;
      return;
    }

    // Nothing pinned, but there is something to choose from: offer the picker.
    if (!short) {
      const pickable = sessions.filter((s) => s.short);
      root.innerHTML = `
        <div class="o-pick">
          <p class="o-pick-empty">${escapeHTML(t("pick_orchestrator"))}</p>
          <div class="o-pick-list">
            ${pickable
              .map(
                (s) =>
                  `<button type="button" class="o-pick-item" data-short="${escapeHTML(s.short)}">${escapeHTML(s.name || s.short)}</button>`,
              )
              .join("")}
          </div>
          ${sendError ? `<div class="o-error o-error-send">${escapeHTML(sendError)}</div>` : ""}
        </div>`;
      root.querySelectorAll(".o-pick-item").forEach((btn) => {
        btn.addEventListener("click", async () => {
          try {
            await setOrchestratorSession(btn.dataset.short);
            sendError = "";
          } catch (err) {
            sendError = err.message;
          }
          draw();
        });
      });
      return;
    }

    // Pinned, but the daemon does not currently list that session: there is
    // nothing to resolve a digest against, so none is requested.
    if (!session) {
      root.innerHTML = `
        <div class="o-head"><span class="o-name">${escapeHTML(short)}</span></div>
        ${sendError ? `<div class="o-error o-error-send">${escapeHTML(sendError)}</div>` : ""}
        <div class="o-thread"><div class="o-msg o-dead">session is not currently listed by the daemon</div></div>
        ${formHTML()}`;
      wireForm(short, hadFocus);
      return;
    }

    const ctx = session.context;
    const pct = ctx?.window ? Math.round((ctx.tokens / ctx.window) * 100) : null;

    root.innerHTML = `
      <div class="o-head">
        <span class="o-name">${escapeHTML(session.name || session.short)}</span>
        ${pct === null ? "" : `<span class="o-ctx">${pct}%</span>`}
      </div>
      ${digestError ? `<div class="o-error o-error-digest">${escapeHTML(digestError)}</div>` : ""}
      <div class="o-thread">
        ${steps.map((s) => `<div class="o-msg o-${escapeHTML(s.role)}">${escapeHTML(s.text)}</div>`).join("")}
      </div>
      ${sendError ? `<div class="o-error o-error-send">${escapeHTML(sendError)}</div>` : ""}
      ${formHTML()}`;
    wireForm(session.short, hadFocus);

    const thread = root.querySelector(".o-thread");
    if (thread) thread.scrollTop = thread.scrollHeight;
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

  subscribe(draw);
  setInterval(refreshDigest, 3000);
}

function escapeHTML(s) {
  return String(s).replace(/[&<>]/g, (ch) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;" })[ch]);
}
