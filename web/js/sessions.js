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

// Stalled: mutually exclusive with isWaiting by construction.
function isStalled(s) {
  if (s.dying) return false;
  if (s.needs) return isStalledNeeds(s.needs);
  return s.state === "blocked" || s.tempo === "blocked";
}

// The text explaining *why* a Waiting/Stalled session isn't moving. For a
// flags-only Stalled session (needs empty) detail is the only field that
// still says anything; everywhere else needs is the words that matter.
function reasonText(s) {
  if (isStalled(s) && !s.needs) return s.detail || "";
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
  const mark = ctx.estimated
    ? ` <span class="est" title="${escapeHtml(t("estimated"))}">~</span>`
    : "";
  return `
    <div class="ctx ctx-${level}">
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

export function rowHtml(s) {
  const waiting = isWaiting(s);
  const stalled = isStalled(s);
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
  const reason = waiting || stalled ? reasonText(s) : "";
  const reasonHtml = reason
    ? `<div class="sreason" title="${escapeHtml(reason)}">${escapeHtml(reason)}</div>`
    : "";

  const name = s.name || s.short || "";
  const cardHtml = s.cardPath
    ? `<div class="scard" title="${escapeHtml(s.cardPath)}">&#8599;</div>`
    : "";

  const costHtml =
    s.costUSD !== undefined && s.costUSD !== null
      ? `<span class="scost">$${s.costUSD.toFixed(2)}</span>`
      : "";

  return `
    <article class="${classes.join(" ")}" data-short="${escapeHtml(s.short)}">
      <div class="srow-head">
        <span class="sname">${escapeHtml(name)}</span>
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

export function renderSessions(root, onSelect) {
  subscribe((snap, connected) => {
    // Before the first successful connection, or after a dropped/unparseable
    // frame, snapshot is null and connected is false — render a neutral
    // connecting state rather than dereferencing a snapshot that isn't there.
    if (!snap || !connected) {
      root.innerHTML = `<div class="sempty">${escapeHtml(t("connecting"))}</div>`;
      return;
    }

    if (snap.daemonError) {
      root.innerHTML = `<div class="sempty sempty-error">${escapeHtml(t("daemon_down"))}</div>`;
      return;
    }

    // Go's zero slice serializes as JSON null, so sessions may be absent
    // even on a real, connected snapshot.
    const sessions = snap.sessions ?? [];

    if (sessions.length === 0) {
      root.innerHTML = `<div class="sempty">${escapeHtml(t("no_sessions"))}</div>`;
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

    root.innerHTML = [...waitingRows, ...otherRows].map(rowHtml).join("");
    applyContextWidths(root);

    for (const el of root.querySelectorAll(".srow")) {
      el.addEventListener("click", () => onSelect(el.dataset.short));
    }
  });
}
