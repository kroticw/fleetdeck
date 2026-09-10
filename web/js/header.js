// web/js/header.js
import { subscribe } from "./store.js";
import { t } from "./i18n.js";
import { envelopeText } from "./envelope.js";
import { initTheme, cycleTheme, currentTheme } from "./theme.js";

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
// consulted when needs is empty (spec 3.1). This mirrors
// daemon.Session.Stalled() exactly -- timeless, per-snapshot -- and stays
// that way for the sake of that mirror; the counter below does not use it
// directly, see isFlagOnlyStalled and createStalledTracker.
export function isStalled(s) {
  if (s.dying) return false;
  if (s.needs) return isStalledNeeds(s.needs);
  return s.state === "blocked" || s.tempo === "blocked";
}

// isStalled's two branches, split apart so the counter can treat them
// differently: a needs-based stall is a word from the daemon and is real the
// instant it appears; a flag-only stall (state/tempo === "blocked" with no
// needs text) is exactly what a session looks like for the length of one
// message delivery too, and has been observed to read as stalled twice in
// one hour on live sessions that were not actually stalled at all --
// including the orchestrator's own. See createStalledTracker.
function isNeedsStalled(s) {
  if (s.dying) return false;
  if (!s.needs) return false;
  return isStalledNeeds(s.needs);
}

function isFlagOnlyStalled(s) {
  if (s.dying) return false;
  if (s.needs) return false;
  return s.state === "blocked" || s.tempo === "blocked";
}

// How long a flag-only stall must hold before the counter shows it. No live
// daemon was available to stopwatch the actual delivery-to-settle window for
// this fix, so this is an architecture-derived estimate, not a measured
// one: daemon.poll_interval defaults to 2s (internal/config.go), so the
// registry itself can lag a message's arrival by up to one poll cycle
// before state/needs catch up, and the panel's own snapshot push adds up to
// ~1s more (see the comment on subscribe(), in store.js). 4 poll cycles is
// a deliberately generous multiple of that floor, not a number read off a
// stopwatch -- replace it with a real one if a live daemon ever supplies it.
export const BLOCKED_SETTLE_MS = 8000;

// Session identity for tracking how long a flag-only stall has held.
// Mirrors the field sessions.js keys its own DOM rows on (data-short).
function sessionKey(s) {
  return s.short ?? s.sessionId ?? "";
}

// createStalledTracker holds, per session, the moment a flag-only stall was
// first observed. update(sessions, nowMs) is called once per snapshot and
// returns the sessions the counter should show as stalled right now: every
// needs-based stall immediately, plus every flag-only stall that has held
// continuously for at least BLOCKED_SETTLE_MS. nowMs is always supplied by
// the caller rather than read from Date.now() in here, so a test can drive
// the threshold without waiting on a real clock.
export function createStalledTracker() {
  const since = new Map();
  return {
    update(sessions, nowMs) {
      const seen = new Set();
      const result = [];
      for (const s of sessions) {
        if (isNeedsStalled(s)) {
          result.push(s);
          continue;
        }
        if (!isFlagOnlyStalled(s)) continue;
        const key = sessionKey(s);
        seen.add(key);
        let startedAt = since.get(key);
        if (startedAt === undefined) {
          startedAt = nowMs;
          since.set(key, startedAt);
        }
        if (nowMs - startedAt >= BLOCKED_SETTLE_MS) result.push(s);
      }
      // Forget sessions no longer flag-only stalled -- resolved, gone, or now
      // carrying needs text -- so a later re-entry starts a fresh clock
      // instead of reusing a stale timestamp from an unrelated stall.
      for (const key of since.keys()) {
        if (!seen.has(key)) since.delete(key);
      }
      return result;
    },
  };
}

// How long usageError must hold before it stops reading as a quiet,
// self-resolving blip and becomes a worded notice. Measured: a live
// usage_down was observed to clear on its own within about 3 minutes on
// master (this card's own log, 2026-09-10). Chosen: 15 minutes -- well past
// that single sample, not tuned to it, so an ordinary network hiccup never
// crosses it while a genuinely expired token (which never self-resolves)
// does not sit muted for long.
export const USAGE_ERROR_STALE_MS = 15 * 60 * 1000;

// createUsageErrorTracker holds the moment usageError last turned true.
// update(active, nowMs) is called once per snapshot and returns "none" (not
// failing), "fresh" (failing, under the threshold -- read calmly, the
// gauges already show dashes instead of a number) or "stale" (failing past
// the threshold -- the quiet reading would now be a quiet lie, so this
// becomes a visible, worded notice instead). nowMs is always supplied by the
// caller, never read from Date.now() in here, for the same reason as
// createStalledTracker.
export function createUsageErrorTracker() {
  let since = null;
  return {
    update(active, nowMs) {
      if (!active) {
        since = null;
        return "none";
      }
      if (since === null) since = nowMs;
      return nowMs - since >= USAGE_ERROR_STALE_MS ? "stale" : "fresh";
    },
  };
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
  // The envelope comes off here too — the counter showed a whole
  // `<agent-message id=… from=… to=… at=…>` tag where it meant to show a
  // reason — but the words inside are still carried verbatim, unrendered, for
  // the reason spec 3.1 gives: a person decides from them whether they are
  // being called.
  const reasons = stalledSessions
    .map((s) => ({ raw: stallReason(s), shown: envelopeText(stallReason(s)) }))
    .filter((reason) => reason.shown !== "");
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
      // The title carries the text exactly as the daemon wrote it, envelope and
      // all, so stripping the tag never puts anything out of reach.
      return `<span class="stall-reason" title="${escapeHTML(reason.raw)}">${escapeHTML(reason.shown)}</span>`;
    });
  const rest = reasons.length - shown.length;
  const tail = rest > 0 ? `<span class="stall-more">+${rest} more</span>` : "";
  return `<span class="stall-reasons">${shown.join("")}${tail}</span>`;
}

// currentTheme()/cycleTheme() return null for "no override" — not a missing
// case here, the auto state genuinely has its own label and button state.
function themeLabelKey(theme) {
  return theme === "light" ? "theme_light" : theme === "dark" ? "theme_dark" : "theme_auto";
}

function themeButtonHTML() {
  return `<button type="button" class="theme-toggle">${t(themeLabelKey(currentTheme()))}</button>`;
}

// alarmHTML renders the two problems that genuinely mean "you see no live
// data and something may need fixing" — always red, as before.
export function alarmHTML(connected, snap) {
  const alarms = [];
  if (!connected) alarms.push(t("offline"));
  if (snap.daemonError) alarms.push(t("daemon_down"));
  return alarms.length ? `<span class="problem">${alarms.join(" · ")}</span>` : "";
}

// usageProblemHTML renders usage_down at the severity createUsageErrorTracker
// decided: nothing while the endpoint answers, a calm muted line while it has
// only just started failing, or a visible worded notice once it has failed
// long enough that "wait, it will come back" would be a lie.
export function usageProblemHTML(severity) {
  if (severity === "fresh") return `<span class="problem-quiet">${t("usage_down")}</span>`;
  if (severity === "stale") return `<span class="problem-notice">${t("usage_down_stale")}</span>`;
  return "";
}

export function renderHeader(root) {
  initTheme();

  // One tracker per renderHeader() call, outside subscribe: both hold state
  // across snapshots (since-timestamps keyed by session, or a single
  // since-timestamp for usageError) that must survive from one snapshot to
  // the next, not be rebuilt on every render.
  const stalledTracker = createStalledTracker();
  const usageTracker = createUsageErrorTracker();

  // Delegated and attached once, outside the render below: root.innerHTML is
  // replaced whole on every snapshot (subscribe below fires roughly once a
  // second), so a listener on the button itself would need re-attaching on
  // every one of those — the same reasoning board.js's own delegated click
  // handler documents.
  root.addEventListener("click", (event) => {
    const button = event.target.closest(".theme-toggle");
    if (!button) return;
    button.textContent = t(themeLabelKey(cycleTheme()));
  });

  subscribe((rawSnap, connected) => {
    const snap = rawSnap ?? {};
    const sessions = snap.sessions ?? [];
    const nowMs = Date.now();
    const waitingCount = sessions.filter(isWaiting).length;
    const stalledSessions = stalledTracker.update(sessions, nowMs);
    const stalledCount = stalledSessions.length;
    const usageSeverity = usageTracker.update(!!snap.usageError, nowMs);

    root.innerHTML = `
      <div class="brand">fleetdeck</div>
      ${themeButtonHTML()}
      <div class="limits">
        ${snap.limits ? gauge(t("limit_5h"), snap.limits.fiveHour) : gauge(t("limit_5h"), null)}
        ${snap.limits ? gauge(t("limit_7d"), snap.limits.sevenDay) : gauge(t("limit_7d"), null)}
      </div>
      <div class="counters">
        ${alarmHTML(connected, snap)}
        ${usageProblemHTML(usageSeverity)}
        <span class="counter counter-waiting ${waitingCount > 0 ? "counter-on" : ""}">${waitingCount} ${t("waiting_count")}</span>
        <span class="counter counter-stalled ${stalledCount > 0 ? "counter-on" : ""}">${stalledCount} ${t("stalled_count")} ${stalledList(stalledSessions)}</span>
      </div>`;
  });
}

export default renderHeader;
