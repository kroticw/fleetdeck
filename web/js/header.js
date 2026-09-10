// web/js/header.js
import { subscribe } from "./store.js";
import { t } from "./i18n.js";

// Mirrors daemon.Session.Waiting()/.Stalled() in internal/daemon/types.go.
// Keep both lists and both functions in sync with that file if it ever
// changes -- it is the source of truth, this is a JS restatement of it.
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
  return STALLED_NEEDS_PREFIXES.some((prefix) => needs.startsWith(prefix));
}

// waiting: a person must answer before this session can move. needs is the
// only signal -- never state/tempo, which are set by a mechanism the session
// does not control (spec 3.1).
export function isWaiting(s) {
  if (s.dying) return false;
  if (!s.needs) return false;
  return !isStalledNeeds(s.needs);
}

// stalled: stopped for a reason no answer fixes, or stopped with no words at
// all. Order matters: needs decides first; the state/tempo flags are only
// consulted when needs is empty (spec 3.1).
export function isStalled(s) {
  if (s.dying) return false;
  if (s.needs) return isStalledNeeds(s.needs);
  return s.state === "blocked" || s.tempo === "blocked";
}

// The row text a person reads for a stalled session. needs wins when present
// (it already matched a stalled prefix); otherwise detail must be shown
// verbatim (spec 3.1: the row MUST carry detail's text verbatim).
export function stallReason(s) {
  return s.needs || s.detail || "";
}

const MAX_STALL_REASONS = 3;

// escapeHTML neutralizes the five characters that matter when text is
// interpolated into an HTML template literal. Sessions are not
// developer-controlled text (spec 3.1: the fleet is open, sessions we did
// not write can join it) -- needs/detail must be treated as untrusted
// content wherever they reach innerHTML, even though CSP's script-src
// 'self' already blocks the classic <script>-execution form of the attack.
// Dictionary strings from t() are our own literals and are never passed
// through this function.
export function escapeHTML(text) {
  return text
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;");
}

// humanDuration turns a future timestamp into a short "3h 20m" string.
function humanDuration(iso) {
  const ms = new Date(iso).getTime() - Date.now();
  if (!isFinite(ms) || ms <= 0) return "0m";
  const minutes = Math.floor(ms / 60000);
  const days = Math.floor(minutes / 1440);
  const hours = Math.floor((minutes % 1440) / 60);
  if (days > 0) return `${days}d ${hours}h`;
  if (hours > 0) return `${hours}h ${minutes % 60}m`;
  return `${minutes}m`;
}

// gauge renders one usage window as a <meter>-based bar. No inline style is
// used anywhere here: the page's CSP ships style-src 'self' with no
// 'unsafe-inline', so a style="..." attribute or an el.style.* write would
// silently fail to paint (no console error) rather than throw. The <meter>
// element draws its fill natively from min/max/value attributes instead.
function gauge(label, window_) {
  if (!window_) {
    return `<span class="gauge gauge-off">${label} <meter min="0" max="100" value="0" class="gauge-track" disabled></meter> —</span>`;
  }
  const pct = Math.max(0, Math.min(100, Math.round(window_.utilization)));
  const level = pct >= 90 ? "hot" : pct >= 60 ? "warm" : "cool";
  return `
    <span class="gauge gauge-${level}" title="${t("resets_in")} ${humanDuration(window_.resetsAt)}">
      ${label}
      <meter class="gauge-track" min="0" max="100" value="${pct}"></meter>
      ${pct}%
    </span>`;
}

// stalledList renders the "M stalled" counter's reason rows: up to
// MAX_STALL_REASONS reasons shown verbatim (escaped -- see escapeHTML),
// then a "+N more" tail. The stalled *count* is never truncated -- only
// this reason list is, and only the reason list's own truncation is what
// the tail counts.
//
// Invariant: filter empty reasons out FIRST, across every stalled session,
// then slice the resulting reason list for display. The tail is exactly
// (total non-empty reasons) - (reasons shown). A session with no reason
// text (the flag-only Stalled branch with an empty detail) never had a
// reason to display in the first place, so it must never inflate the
// "+N more" count -- it is absence, not truncation. Slicing session
// objects first and filtering empties second would undercount real,
// visible reasons that exist past the cap whenever an empty-reason session
// happens to occupy one of the first MAX_STALL_REASONS slots; do not
// reorder these two steps.
export function stalledList(stalledSessions) {
  if (stalledSessions.length === 0) return "";
  const reasons = stalledSessions.map((s) => stallReason(s)).filter((reason) => reason !== "");
  // Each reason is its own box, and each box is clipped to one line by the
  // stylesheet. The daemon writes the text of an incoming message into detail
  // verbatim, so a reason is routinely a paragraph rather than a phrase, and
  // three of those joined into one run of text turn this strip into a wall
  // that pushes the whole panel down.
  //
  // The clipping is CSS, not a substring: the point of cutting is that the
  // header stays one line wide, which is a question about the width of the
  // window and the width of the glyphs, and neither is known here. A JS cut at
  // N characters is either too early on a wide window or too late on a narrow
  // one, and it also throws the rest away.
  //
  // Which is the other half: the full reason goes in title. Spec 3.1 requires
  // the row to carry detail's text verbatim, and the reason that rule exists
  // is that a person has to be able to read it -- so the text has to stay
  // reachable without leaving the panel, not merely be present in a variable.
  const shown = reasons
    .slice(0, MAX_STALL_REASONS)
    .map((reason) => {
      const escaped = escapeHTML(reason);
      return `<span class="stall-reason" title="${escaped}">${escaped}</span>`;
    });
  const rest = reasons.length - shown.length;
  const tail = rest > 0 ? `<span class="stall-more">+${rest} more</span>` : "";
  return `<span class="stall-reasons">${shown.join("")}${tail}</span>`;
}

export function renderHeader(root) {
  subscribe((rawSnap, connected) => {
    const snap = rawSnap ?? {};
    const sessions = snap.sessions ?? [];
    const waitingCount = sessions.filter(isWaiting).length;
    const stalledSessions = sessions.filter(isStalled);
    const stalledCount = stalledSessions.length;

    const problems = [];
    if (!connected) problems.push(t("offline"));
    if (snap.daemonError) problems.push(t("daemon_down"));
    if (snap.usageError) problems.push(t("usage_down"));

    root.innerHTML = `
      <div class="brand">fleetdeck</div>
      <div class="limits">
        ${snap.limits ? gauge(t("limit_5h"), snap.limits.fiveHour) : gauge(t("limit_5h"), null)}
        ${snap.limits ? gauge(t("limit_7d"), snap.limits.sevenDay) : gauge(t("limit_7d"), null)}
      </div>
      <div class="counters">
        ${problems.length ? `<span class="problem">${problems.join(" · ")}</span>` : ""}
        <span class="counter counter-waiting ${waitingCount > 0 ? "counter-on" : ""}">${waitingCount} ${t("waiting_count")}</span>
        <span class="counter counter-stalled ${stalledCount > 0 ? "counter-on" : ""}">${stalledCount} ${t("stalled_count")} ${stalledList(stalledSessions)}</span>
      </div>`;
  });
}

export default renderHeader;
