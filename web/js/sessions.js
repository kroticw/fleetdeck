// Session list column.
//
// Renders every session the daemon knows about, split into three states that
// must never be conflated (internal/daemon/types.go, Session.Waiting /
// Session.Stalled):
//
//   - Waiting:  a person must answer before the session can move. Sorted to
//     the top and given a strong highlight — the point of the panel is that
//     these cannot be missed.
//   - Stalled:  stopped for a reason no answer fixes (usage limit, login,
//     API error, rate limit), or stopped with only a bare flag and no words.
//     Gets its own quiet marker. Never sorted to the top with Waiting, never
//     styled the same as Waiting — the whole point of the split is that
//     "needs a person right now" and "stopped on its own" are different
//     situations.
//   - Neither: running normally.
//
// There is no `id`/`status`/`title` field on a session (see snapshot.go /
// daemon/types.go) — `short` is the identity used for onSelect, `needs` /
// `state` / `tempo` / `dying` decide Waiting/Stalled, and `detail` is the
// only field that still explains a flags-only Stalled session.
import { subscribe } from "./store.js";
import { t } from "./i18n.js";
import { envelopeText } from "./envelope.js";
import { setSessionLabel } from "./api.js";
import { createStalledTracker } from "./header.js";
import { SESSIONS_KEYS } from "./columnwidth.js";
import { mountColumnResize } from "./columnresize.js";

// Closed vocabulary of "no person needed" needs strings, copied verbatim
// (case-sensitive prefix match, exact order) from daemon.stalledNeedsPrefixes
// in internal/daemon/types.go. Deliberately narrow: an unrecognized prefix
// must fall through to Waiting, never Stalled.
const STALLED_NEEDS_PREFIXES = [
  "usage limit reached",
  "login required",
  "API error",
  "API overloaded",
  "API unavailable",
  "invalid API request",
  "rate limited",
];

function isStalledNeeds(needs) {
  return STALLED_NEEDS_PREFIXES.some((p) => needs.startsWith(p));
}

// Waiting: needs decides alone. Never look at state/tempo here — those are
// mechanism flags a session does not control and cannot tell "waiting on a
// person" apart from "waiting on its own subagents".
function isWaiting(s) {
  if (s.dying) return false;
  if (!s.needs) return false;
  return !isStalledNeeds(s.needs);
}

// Stalled: mutually exclusive with isWaiting by construction. Kept exactly
// as daemon.Session.Stalled() mirrors it in header.js's own copy -- this is
// the per-instant rule, not the row's own display decision (see rowHtml's
// own comment for why the two are no longer the same question).
function isStalled(s) {
  if (s.dying) return false;
  if (s.needs) return isStalledNeeds(s.needs);
  return s.state === "blocked" || s.tempo === "blocked";
}

// The text explaining *why* a Waiting/Stalled session isn't moving. For a
// flags-only Stalled session (needs empty) detail is the only field that
// still says anything; everywhere else needs is the words that matter.
// stalled is the row's own tracker-backed decision (rowHtml's stalledNow),
// not a fresh isStalled(s) call: a flag-only stall not yet counted by the
// tracker must not show detail either, or the row would carry a "why it
// stopped" reason for a stop the badge itself does not yet claim happened.
function reasonText(s, stalled) {
  if (stalled && !s.needs) return s.detail || "";
  return s.needs || "";
}

function escapeHtml(value) {
  return String(value ?? "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#39;");
}

// silentFor is nanoseconds since the session's transcript was last written.
// Zero is not "silent for zero time" — it means no transcript exists yet, so
// nothing has been measured. That must render as unknown, never as "0m" or
// as if the session were freshly active.
function silentLabel(silentForNs) {
  if (!silentForNs) return t("silent_unmeasured");
  const seconds = silentForNs / 1e9;
  if (seconds < 60) return `${Math.max(1, Math.round(seconds))}s`;
  const minutes = seconds / 60;
  if (minutes < 60) return `${Math.floor(minutes)}m`;
  const hours = minutes / 60;
  if (hours < 24) return `${Math.floor(hours)}h`;
  const days = hours / 24;
  return `${Math.floor(days)}d`;
}

// Context occupancy bar. ctx may be entirely absent (Go omitempty) — that
// renders as unknown, never as a 0% bar, which would falsely claim the
// session's context is empty. No `percent` field exists on the wire; it is
// computed here from tokens/window.
//
// No inline style="..." anywhere (CSP is style-src 'self', which silently
// drops inline styles with no visible error). The percentage travels as a
// data-pct attribute; callers must apply it via .style.width after the
// markup lands in the DOM (see applyContextWidths below).
function contextBarHtml(ctx) {
  if (!ctx || !ctx.window || ctx.window <= 0) {
    return `<div class="ctx ctx-unknown">${escapeHtml(t("context"))}: ${escapeHtml(t("context_unknown"))}</div>`;
  }
  const pct = (ctx.tokens / ctx.window) * 100;
  const rounded = Math.round(pct);
  const clamped = Math.max(0, Math.min(rounded, 100));
  const level = rounded >= 80 ? "hot" : rounded >= 50 ? "warm" : "cool";
  // <abbr> rather than <span>: the tilde IS an abbreviation, and marking it as
  // one is what makes its expansion available to a screen reader instead of
  // only to a mouse. The tilde stays — it is the honest difference between a
  // measured value and one estimated from the transcript, and dropping it would
  // pass an estimate off as a reading.
  const mark = ctx.estimated
    ? ` <abbr class="est" title="${escapeHtml(t("estimated"))}">~</abbr>`
    : "";
  // The label is text in the row, not a tooltip. A bare "56%" beside a session
  // is honest and unreadable: nothing on screen said what was measured, and a
  // number whose unit is only reachable by hovering is not reachable for
  // someone who does not hover. It is the same word the unknown state already
  // used — which meant the label appeared exactly where there was nothing to
  // label, and vanished where there was.
  return `
    <div class="ctx ctx-${level}">
      <span class="ctx-label">${escapeHtml(t("context"))}</span>
      <span class="ctx-track"><i class="ctx-fill" data-pct="${clamped}"></i></span>
      <span class="ctx-pct">${rounded}%${mark}</span>
    </div>`;
}

// CSP-safe width application: direct CSSOM property assignment is not
// affected by style-src, unlike a style="" attribute or .style.cssText.
function applyContextWidths(root) {
  for (const fill of root.querySelectorAll(".ctx-fill[data-pct]")) {
    fill.style.width = `${fill.dataset.pct}%`;
  }
}

// stalledNow is the row's own Stalled verdict, already decided by the same
// createStalledTracker the header's counter shares -- see the header.js
// import above and renderSessions' own use of it below. Never recomputed
// here from isStalled(s) directly: that per-instant check is what put a
// "Stalled" badge on a session's very first blocked tick, several times an
// hour, for as long as its conversation with the orchestrator ran -- the
// header's own counter, watching the same session through the same
// tracker, said "0 stalled" in the same screenshot. Omitting stalledNow
// (every existing caller that is not testing the badge itself) reads as
// false, matching a session that has not been through the tracker at all.
export function rowHtml(s, stalledNow) {
  const waiting = isWaiting(s);
  const stalled = Boolean(stalledNow);
  const classes = ["srow"];
  if (waiting) classes.push("srow-waiting");
  if (stalled) classes.push("srow-stalled");

  const badge = waiting
    ? `<span class="sbadge sbadge-waiting">${escapeHtml(t("waiting"))}</span>`
    : stalled
      ? `<span class="sbadge sbadge-stalled">${escapeHtml(t("stalled"))}</span>`
      : "";

  // The reason is clipped to a few lines by the stylesheet, with the whole of
  // it in title. The daemon writes an incoming message's text into detail
  // verbatim, so this is a paragraph as often as it is a phrase, and an
  // unclipped one pushes every session below it off the screen. Clipping in
  // CSS rather than by substring keeps the cut at the column's real width and
  // keeps the text itself intact -- spec 3.1 wants detail carried verbatim
  // because a person has to read it, so it has to stay reachable here rather
  // than only in the daemon.
  const raw = waiting || stalled ? reasonText(s, stalled) : "";
  // The envelope comes off, and nothing else does. What a session says about
  // why it stopped is carried verbatim (spec 3.1) because a person decides from
  // its exact words whether they are being called — so this is NOT rendered as
  // markdown, unlike a conversation step. Stripping the tag is not a paraphrase:
  // every word that was inside it is still here, in order.
  //
  // The title keeps the text exactly as the daemon wrote it, envelope included,
  // so nothing is out of reach.
  const reason = envelopeText(raw);
  const reasonHtml = reason
    ? `<div class="sreason" title="${escapeHtml(raw)}">${escapeHtml(reason)}</div>`
    : "";

  // label is the operator's own name for the session (internal/config's
  // session_labels, written through PATCH /api/sessions/{id}/label) and
  // wins when set; name is whatever the daemon itself reports; short is
  // what is left when neither exists — never blank, never invented.
  const name = s.label || s.name || s.short || "";
  // A button, because it does something. It used to be a <div> with no handler:
  // a click on it bubbled to the row and opened the SESSION, while its tooltip
  // showed the path to a CARD. That is worse than unreachable — it promised one
  // action and performed another, and only because the absence of code was
  // masked by the parent's behaviour. Nothing in the file said so; only
  // pressing it did.
  //
  // Labelled rather than left as a bare arrow: an arrow says "elsewhere", not
  // "to this session's card". The full path stays in the title for whoever
  // needs it; the label is what the rest of us read.
  const cardHtml = s.cardPath
    ? `<button type="button" class="scard" data-card="${escapeHtml(s.cardPath)}" title="${escapeHtml(t("open_card_hint"))}: ${escapeHtml(s.cardPath)}">${escapeHtml(t("open_card"))} &#8599;</button>`
    : "";

  const costHtml =
    s.costUSD !== undefined && s.costUSD !== null
      ? `<span class="scost">$${s.costUSD.toFixed(2)}</span>`
      : "";

  // The pencil is a real, always-visible button — not a hover-only
  // affordance and not the name text itself, so clicking a session's name to
  // edit it can never be confused with clicking the row to open it. It
  // carries the session's own transcript UUID: the write route is keyed on
  // that, never on the short id short is (see api.js's setSessionLabel).
  const editBtn = s.sessionId
    ? `<button type="button" class="label-edit-btn" data-session-id="${escapeHtml(s.sessionId)}" aria-label="${escapeHtml(t("edit_label"))}" title="${escapeHtml(t("edit_label"))}">✎</button>`
    : "";

  return `
    <article class="${classes.join(" ")}" data-short="${escapeHtml(s.short)}">
      <div class="srow-head">
        <span class="sname">${escapeHtml(name)}</span>
        ${editBtn}
        ${badge}
      </div>
      <div class="smeta">
        <span class="sstate">${escapeHtml(s.state || "")}</span>
        <span class="ssilent">${escapeHtml(silentLabel(s.silentFor))}</span>
        ${costHtml}
      </div>
      ${reasonHtml}
      ${contextBarHtml(s.context)}
      ${cardHtml}
    </article>`;
}

// The column's own name, shown in every state including the empty and error
// ones: the other two columns name themselves through what they show (the
// board's tab, the orchestrator's open session), and this one otherwise
// named nothing at all — the specific gap a live run's screenshot found.
const HEAD = `<div class="slist-head">${escapeHtml(t("sessions_title"))}</div>`;

// The same fold/unfold strip as the orchestrator column's own — same
// classes (web/app.css's .col-size rules act on them for whichever column
// carries them), same i18n keys, same mechanism — mirrored, not copied: this
// column sits at the window's RIGHT edge, not the left, so the strip carries
// col-size-right instead of col-size-left (puts the controls on this
// column's own left, toward the centre, not pinned against the window's
// outer edge) and the glyphs are swapped from the orchestrator's own —
// folding this column sends it right, toward its own edge, so that arrow
// points right; unfolding brings it back left, toward the centre. Getting
// either backwards is exactly the defect the operator found when this
// column first reused the orchestrator's controls unmirrored: an arrow
// pointing the wrong way, and a strip pinned against the window's edge
// instead of reachable from the centre.
//
// Built as markup rather than as DOM nodes the way orchestrator.js builds
// its own: this column's entire content is markup, replaced wholesale on
// every snapshot (see setBody in renderSessions), and a node parked here
// would be destroyed the moment the next snapshot arrived, folded or not —
// innerHTML replaces every child, not only the ones a caller put there. The
// drag grip that resizes this column is real DOM regardless, and is the
// literal same code the orchestrator's own grip runs, given side: "right":
// see web/js/columnresize.js's own note.
function sizeControlsHtml() {
  const unfoldLabel = escapeHtml(t("column_unfold"));
  const foldLabel = escapeHtml(t("column_fold"));
  return `<div class="col-size col-size-right">
    <button type="button" class="col-size-btn col-size-unfold" aria-label="${unfoldLabel}" title="${unfoldLabel}">«</button>
    <button type="button" class="col-size-btn col-size-fold" aria-label="${foldLabel}" title="${foldLabel}">»</button>
  </div>`;
}

// onOpenCard is optional: without it the card control is not offered at all,
// because a control that cannot do what it says is the defect this replaced.
// `now` defaults to the real clock; a test overrides it to prove the tracker
// keeps counting BLOCKED_SETTLE_MS while this column is folded, which real
// elapsed time cannot practically stand in for.
//
// Returns the resize handle (see web/js/columnresize.js) — main.js has no
// use for it, but a test does: this column's whole content is a markup
// string, and the fold/unfold buttons inside it are unaddressable to
// web/tests/fake-dom.js (its innerHTML is stored, never parsed — see its
// own header comment), so a test drives the fold/unfold the buttons would
// otherwise trigger by calling resize.width.fold()/.unfold() directly, the
// same call a click makes in a real browser.
export function renderSessions(root, onSelect, onOpenCard, { now = Date.now } = {}) {
  // Set while one row's name is being edited in place. This column, unlike
  // the orchestrator's, rebuilds its whole innerHTML on every snapshot — so
  // the only way an <input> mid-edit survives a push arriving under the
  // operator's fingers is to skip the rebuild entirely for as long as the
  // edit lasts. The two most recent arguments are kept so the skipped
  // render can be run once editing ends, instead of waiting out however
  // long is left on the next poll.
  let editingShort = null;
  let lastSnap = null;
  let lastConnected = false;

  // One tracker per renderSessions() call, outside render: it holds
  // since-timestamps across snapshots (the same shape header.js's own
  // instance does), not something a single render may rebuild. Two
  // independent instances -- this one and the header's -- fed the same
  // sessions on every snapshot (both push through the same store.js
  // broadcast) settle on identical per-session answers; a single shared
  // instance would need this column and the header wired together across
  // a third file, which the two-instance shape avoids.
  const stalledTracker = createStalledTracker();

  // The same resize/fold mechanism as the orchestrator column's own
  // (web/js/columnresize.js), reading and writing its own storage entries
  // (SESSIONS_KEYS, not ORCHESTRATOR_KEYS) so folding or resizing this
  // column never touches the orchestrator's remembered state, or the other
  // way round. One instance for the column's lifetime, like stalledTracker
  // above: width and fold are state that persists across snapshots, not
  // something a single render may rebuild.
  //
  // render() is unconditional below — every snapshot reaches it regardless
  // of whether this column is currently folded, the same as it always was.
  // Folding is purely what web/app.css does with a [data-folded] attribute
  // on `root` once painted here; it never gates render() itself, so
  // stalledTracker.update() keeps running, and settling, while the column is
  // folded. A version that skipped rendering — and so skipped feeding the
  // tracker — while folded would silently stop the clock on a flag-only
  // stall the moment the column was put aside, and restart it from zero on
  // unfold: the exact header-says-X/row-says-Y mismatch this file's own
  // stalledNow was built to end, reappearing between two points in time
  // instead of between two places on screen. See sessions.test.js for the
  // mutation that proves it.
  const resize = mountColumnResize(root, SESSIONS_KEYS, { side: "right" });
  // Painted once, now: root has no content yet, but width/folded are root's
  // own style and dataset, untouched by every render() below rewriting its
  // children — the same reason the orchestrator column paints once at its
  // own first build rather than on every draw().
  resize.paint();

  // Shown once, on the next render after a save fails — a silent
  // console.error would never reach the operator, who does not have
  // devtools open, and this column has no other error slot a per-row write
  // failure could route through. Read and cleared by render() itself, so a
  // later, successful edit does not leave a stale failure on screen.
  let labelError = "";

  // setBody is the one place root.innerHTML is written. The fold/unfold
  // strip goes first in every state (matching the orchestrator's own: it is
  // the one control that must stay reachable however the rest of the column
  // reads) and its buttons are rewired every time, the same as every other
  // interactive element in this column — see the querySelectorAll loops
  // below, which do the same for rows, the card link and the edit pencil.
  const setBody = (bodyHtml) => {
    root.innerHTML = sizeControlsHtml() + bodyHtml;
    root.querySelector(".col-size-unfold")?.addEventListener("click", () => resize.width.unfold());
    root.querySelector(".col-size-fold")?.addEventListener("click", () => resize.width.fold());
  };

  const render = (snap, connected) => {
    // Before the first successful connection, or after a dropped/unparseable
    // frame, snapshot is null and connected is false — render a neutral
    // connecting state rather than dereferencing a snapshot that isn't there.
    if (!snap || !connected) {
      setBody(`${HEAD}<div class="sempty">${escapeHtml(t("connecting"))}</div>`);
      return;
    }

    if (snap.daemonError) {
      setBody(`${HEAD}<div class="sempty sempty-error">${escapeHtml(t("daemon_down"))}</div>`);
      return;
    }

    // Go's zero slice serializes as JSON null, so sessions may be absent
    // even on a real, connected snapshot.
    const all = snap.sessions ?? [];

    // This column is the fleet's task list, and the orchestrator is not one of
    // the tasks: it has a column of its own, where its conversation lives. So
    // it is left out here rather than shown twice — the same session in two
    // columns was the operator's own complaint. While nothing is pinned there
    // is no orchestrator to leave out, and this is simply every session.
    const pinned = snap.orchestratorSession ?? "";
    const sessions = pinned ? all.filter((s) => s.short !== pinned) : all;

    if (sessions.length === 0) {
      // "Every session there is, is the orchestrator" and "there are no
      // sessions" are different facts, and a person reading an empty column
      // needs to know which one they are looking at.
      const message = all.length === 0 ? t("no_sessions") : t("only_orchestrator");
      setBody(`${HEAD}<div class="sempty">${escapeHtml(message)}</div>`);
      return;
    }

    // Waiting sessions float to the top; everything else (stalled and
    // running) keeps its relative order. Stalled must NOT be promoted here —
    // conflating it with Waiting throws away the distinction the whole task
    // exists to draw.
    const waitingRows = [];
    const otherRows = [];
    for (const s of sessions) {
      (isWaiting(s) ? waitingRows : otherRows).push(s);
    }
    const ordered = [...waitingRows, ...otherRows];

    // Fed `all`, not the pinned-out `sessions`: the tracker's own per-session
    // answer must match what header.js's identically-fed instance would say
    // for the same session, and excluding the pinned orchestrator session
    // here (it is never rendered as a row in this column) would only cost
    // that one session's own tracked state for no benefit.
    const stalledNow = new Set(stalledTracker.update(all, now()).map((s) => s.short));

    const errorHtml = labelError ? `<div class="sname-edit-error">${escapeHtml(labelError)}</div>` : "";
    labelError = ""; // shown once; a later render must not keep repeating it
    // Never `ordered.map(rowHtml)`: Array.prototype.map passes (element,
    // index, array) to its callback, and rowHtml's own second parameter
    // would then receive the row's numeric index -- truthy for every row
    // but the first, badging almost the whole list as Stalled regardless of
    // stalledNow. The arrow below is required, not stylistic.
    setBody(HEAD + errorHtml + ordered.map((s) => rowHtml(s, stalledNow.has(s.short))).join(""));
    applyContextWidths(root);

    for (const el of root.querySelectorAll(".srow")) {
      el.addEventListener("click", () => onSelect(el.dataset.short));
    }
    for (const el of root.querySelectorAll(".scard")) {
      el.addEventListener("click", (event) => {
        // Stopped here, or the row's own handler runs next and opens the
        // session — which is precisely what this control used to do by
        // accident.
        event.stopPropagation();
        onOpenCard?.(el.dataset.card);
      });
    }

    for (const btn of root.querySelectorAll(".label-edit-btn")) {
      btn.addEventListener("click", (event) => {
        // Editing a name must never also select the row it lives in — the
        // two controls sit on top of each other and must not fire together.
        event.stopPropagation();
        const row = btn.closest(".srow");
        const session = ordered.find((s) => s.short === row.dataset.short);
        if (session) startEditing(row, session);
      });
    }
  };

  // startEditingLabel's own three rules, restated for a row built from a
  // markup string rather than DOM nodes: the field is pre-filled with the
  // label alone (never the name/short fallback that is merely displayed),
  // the fallback becomes the placeholder, Enter and losing focus both save,
  // Esc alone discards, and an empty save is the reset to name-then-short —
  // it needs no special case here because the fallback chain in rowHtml
  // already reads an absent label as absence, not as blank text.
  const startEditing = (row, session) => {
    editingShort = session.short;
    const nameSpan = row.querySelector(".sname");
    const input = document.createElement("input");
    input.type = "text";
    input.className = "sname-input";
    input.value = session.label ?? "";
    input.placeholder = session.name || session.short;
    nameSpan.parentNode.insertBefore(input, nameSpan);
    nameSpan.remove();
    input.focus();

    let settled = false;
    const finish = async (save) => {
      if (settled) return; // Enter's own save must not also run as the blur it causes
      settled = true;
      editingShort = null;
      if (save) {
        try {
          const next = input.value.trim();
          await setSessionLabel(session.sessionId, next);
          // Reflected on the resolved session object itself, not only sent:
          // the render this triggers reads from lastSnap, whose session
          // objects are these same ones, so the row shows the new name at
          // once rather than waiting out a poll — and the very next real
          // snapshot replaces this object graph wholesale regardless (see
          // store.js), so nothing here is a value this module goes on
          // believing past that.
          session.label = next;
          labelError = "";
        } catch (err) {
          labelError = `${t("label_save_failed")}: ${err.message}`;
        }
      }
      render(lastSnap, lastConnected);
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

  subscribe((snap, connected) => {
    lastSnap = snap;
    lastConnected = connected;
    if (editingShort !== null) return;
    render(snap, connected);
  });

  return resize;
}
