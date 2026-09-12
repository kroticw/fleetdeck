// web/js/header.js
import { subscribe } from "./store.js";
import { t } from "./i18n.js";
import { envelopeText } from "./envelope.js";
import { initTheme, cycleTheme, currentTheme } from "./theme.js";
import { brandHTML, hasUnsentText, pageStorage } from "./buildcheck.js";
import { headerSessions, fleetEntries, switchFleet } from "./fleet.js";
import { fleetIconHTML } from "./icon.js";
import { UPDATE_BINDING, WAY_BINDING, PROGRESS_FUNCTION, UPDATE_REPAINT_MS, initialState, onPress, onProgress, updateHTML } from "./update.js";

// The icon beside the fleet's name in the header: small enough to sit in a
// row of controls, large enough to be the application's mark rather than a
// dot. The start page shows the same drawing much larger.
const FLEET_ICON_SIZE = 20;

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

// How long a flag-only stall must go without a sign of life before the
// counter shows it.
//
// A bare blocked flag (state or tempo, needs empty) is genuinely ambiguous,
// not just briefly noisy: it covers both a message still mid-delivery
// (transient, clears on its own) and a session truly parked waiting on a
// person (real, does not clear). Dropping either signal to "fix" the
// transient case would silently reintroduce the other: this project already
// caught and fixed the under-reporting side once -- see
// internal/daemon/client_test.go's "e4fa5037" fixture record, an hour-long
// real stall with this exact flag shape (tempo=active, state=blocked) -- and
// under-reporting is the worse of the two failures (it hides someone
// genuinely waiting), so the threshold has to sit clearly above the
// transient case without crowding the real one.
//
// What this threshold measures changed once, and the number did not. It used
// to be the age of the flag itself, counted from the tracker's first sight of
// it; state, however, sticks. A session observed live on 2026-09-12 (fleet
// session 512ed1ad) held state=blocked through 55% of a sampling window while
// writing to its transcript every few seconds, and the badge stayed lit
// through all of it, because the clock had run out during an earlier, real
// stall and the sticky flag never let it restart. So the threshold is now
// measured against silentFor -- the age of the last write to the session's
// transcript, the one value in the snapshot that is a measurement rather than
// a flag. "Has been silent continuously for T" is simply silentFor >= T, with
// no clock of our own to accumulate and nothing to reset: a single write to
// the transcript is the reset.
//
// The number, 10 minutes, is measured rather than reasoned. Every transcript
// on this machine over the 30 days to 2026-09-12 (103 files, 98 665 gaps
// between consecutive writes while the session was working rather than
// waiting on a person) puts a live session's silence at p99 = 55s,
// p99.9 = 5.5m, p99.99 = 17.8m, longest 34.9m. Ten minutes therefore sits at
// roughly twice the p99.9 knee: 42 of those 98 665 gaps reach it, about 1.4 a
// day across the whole fleet, and each still needs a stuck blocked flag
// alongside it to light anything. The step up to 15 minutes was measured too
// and rejected: it removes about one false badge a day and delays every one
// of the ~10 real stalls a day by a further five minutes. The attested real
// case (e4fa5037, roughly an hour) clears any of these by a wide margin.
export const BLOCKED_SETTLE_MS = 10 * 60 * 1000;

// silentFor is a Go time.Duration crossing the wire (internal/state's
// SessionView), so it arrives in nanoseconds.
const NS_PER_MS = 1e6;

// How long this session has been silent, in milliseconds, or 0 for "not
// measured". Zero on the wire means there was no transcript to stat, never
// "silent for zero time" -- cmd/fleetdeck/collect.go's transcriptState says
// so, and sessions.js's silentLabel renders it as unknown for the same
// reason. Absent and zero are the same answer here, and both mean the caller
// must fall back to something else rather than read a session as freshly
// alive.
function silentForMs(s) {
  const ns = s.silentFor;
  if (!ns) return 0;
  return ns / NS_PER_MS;
}

// Session identity for tracking how long a flag-only stall has held.
// Mirrors the field sessions.js keys its own DOM rows on (data-short).
function sessionKey(s) {
  return s.short ?? s.sessionId ?? "";
}

// createStalledTracker decides which sessions the counter and the row badge
// should call stalled right now. update(sessions, nowMs) is called once per
// snapshot and returns every needs-based stall immediately, plus every
// flag-only stall that has also been silent for at least BLOCKED_SETTLE_MS.
//
// Two ways of establishing that silence, because one of them is not always
// available:
//
//   - silentFor measured: it is the answer outright. It is an age, not an
//     accumulator, so it needs no state here and cannot survive the session
//     coming back to life -- which is exactly the failure this replaced.
//   - silentFor unmeasured (no transcript to stat yet): fall back to the age
//     of the flag itself, held per session in `since`, as this tracker did
//     for every session before silentFor was consulted. A session that
//     stalls before writing anything must still be counted, and there is
//     nothing else left to measure it by.
//
// nowMs is always supplied by the caller rather than read from Date.now() in
// here, so a test can drive the fallback without waiting on a real clock.
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
        const silentMs = silentForMs(s);
        if (silentMs > 0) {
          if (silentMs >= BLOCKED_SETTLE_MS) result.push(s);
          continue;
        }
        const key = sessionKey(s);
        seen.add(key);
        let startedAt = since.get(key);
        if (startedAt === undefined) {
          startedAt = nowMs;
          since.set(key, startedAt);
        }
        if (nowMs - startedAt >= BLOCKED_SETTLE_MS) result.push(s);
      }
      // Forget sessions no longer being timed by the fallback -- resolved,
      // gone, now carrying needs text, or now measurable through silentFor --
      // so a later re-entry starts a fresh clock instead of reusing a stale
      // timestamp from an unrelated stall.
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

// How old snap.limits.fetchedAt must be before the panel shows its age
// instead of drawing it as live. This is not about an error at all: the
// local rate-limits file (cmd/fleetdeck-status) is rewritten every time any
// session's statusline ticks, often several times a minute while a session
// is actively working, so a value this old means no session has ticked in
// a while -- a fact the operator should see, not one the colorful gauge
// should paper over just because nothing is currently failing. Chosen well
// past a single statusline cycle so an ordinary few-seconds-old value never
// crosses it.
export const RATE_LIMITS_AGE_WORTH_SHOWING_MS = 2 * 60 * 1000;

// isUsageStale decides the calm-vs-severity read for the limits gauges.
// Stale for two different reasons, both meaning "this number is not this
// instant's": a value present alongside an error is usage.Fetcher's own
// cache fallback (cmd/fleetdeck/collect.go) -- real numbers, just not from
// this cycle's fetch; a value old enough on its own (RATE_LIMITS_AGE_WORTH
// _SHOWING_MS) is the local-file source (cmd/fleetdeck-status) not having
// been rewritten in a while, with no error at all -- nothing failed, a
// session just has not ticked its statusline recently. gauge() reads calm
// rather than hot/warm/cool for either case -- see its own comment. nowMs
// is always supplied by the caller, never read from Date.now() in here, for
// the same reason as createStalledTracker.
export function isUsageStale(snap, nowMs) {
  if (!snap.limits) return false;
  if (snap.usageError) return true;
  const ageMs = nowMs - new Date(snap.limits.fetchedAt).getTime();
  return ageMs > RATE_LIMITS_AGE_WORTH_SHOWING_MS;
}

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

// humanAge is humanDuration's mirror: a past timestamp's "how long ago",
// rather than a future one's "how long until".
function humanAge(iso) {
  const ms = Date.now() - new Date(iso).getTime();
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
//
// stale (with fetchedAt) is the case cmd/fleetdeck/collect.go's own comment
// describes: the day's refresh failed, but usage.Fetcher had a last known
// value cached and fell back to it rather than the panel discarding it --
// snap.limits is real data, just aged, alongside snap.usageError. Coloring
// an old percentage by severity (hot/warm/cool) would assert a freshness it
// does not have, so it reads calm instead, the same voice the muted grey
// already carries for a dead session or a stalled counter elsewhere on this
// page -- this is ordinary network life, not a fresh failure demanding
// attention.
export function gauge(label, window_, stale, fetchedAt) {
  if (!window_) {
    return `<span class="gauge gauge-off">${label} <meter min="0" max="100" value="0" class="gauge-track" disabled></meter> —</span>`;
  }
  const pct = Math.max(0, Math.min(100, Math.round(window_.utilization)));
  if (stale) {
    const age = humanAge(fetchedAt);
    return `
      <span class="gauge gauge-stale" title="${t("last_known")} ${age}">
        ${label}
        <meter class="gauge-track" min="0" max="100" value="${pct}"></meter>
        ${pct}% · ${age}
      </span>`;
  }
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
//
// kind is snap.usageErrorKind (cmd/fleetdeck/collect.go's classifyUsageError)
// and only changes the *worded* notice, not the quiet one: a brand-new
// failure could still be anything, so the fresh line stays generic on
// purpose. "sign-in needed" is shown only for kind "auth" -- the one case
// where it is actually true (usage.ErrNoToken or an HTTP 401). "rate_limit"
// gets its own wording that says what happened and that it should recover
// on its own; anything else ("other", or an older snapshot with no kind at
// all) falls back to the same generic notice the fresh state already uses,
// which promises nothing it cannot back up.
export function usageProblemHTML(severity, kind) {
  if (severity === "fresh") return `<span class="problem-quiet">${t("usage_down")}</span>`;
  if (severity === "stale") {
    if (kind === "auth") return `<span class="problem-notice">${t("usage_down_auth")}</span>`;
    if (kind === "rate_limit") return `<span class="problem-notice">${t("usage_down_rate_limited")}</span>`;
    return `<span class="problem-notice">${t("usage_down")}</span>`;
  }
  return "";
}

// fleetMenuHTML is the fleet control in the header: the icon, the name of the
// fleet this tab shows, and — open — every fleet with the number of its own
// sessions waiting for an answer, the way back to the start page and the way
// to a fleet that does not exist yet.
//
// It replaced a row of buttons that appeared only on a panel with more than
// one fleet, and it is shown with one fleet too. That is deliberate: a control
// that turns up only once a second fleet exists is a second fleet nobody can
// find out about, and making one is now a thing this menu leads to. The name
// being on screen at all times is the other half — which fleet a tab shows was
// readable only by its address before.
//
// Switching is still a navigation to ?fleet=<name>, a full page load, and this
// changed none of that: what changed is how a fleet is chosen, not what
// choosing one does (web/js/fleet.js).
export function fleetMenuHTML(entries, open) {
  const current = entries.find((e) => e.current) ?? entries[0];
  const name = current ? current.name : t("fleet_none");
  const items = entries.map((e) => {
    const cls = e.current ? "fleet-menu-entry fleet-menu-entry-current" : "fleet-menu-entry";
    const marked = e.current ? ' aria-current="page"' : "";
    const waiting = e.waiting ? ` <span class="fleet-menu-waiting">${e.waiting}</span>` : "";
    return `<button type="button" class="${cls}" data-fleet-menu="pick" data-fleet="${escapeHTML(e.name)}"${marked}>${escapeHTML(e.name)}${waiting}</button>`;
  });
  const list = open
    ? `<div class="fleet-menu-list" role="menu">${items.join("")}<div class="fleet-menu-sep"></div>` +
      `<button type="button" class="fleet-menu-other" data-fleet-menu="all">${escapeHTML(t("fleet_menu_all"))}</button>` +
      `<button type="button" class="fleet-menu-other fleet-menu-new" data-fleet-menu="new">${escapeHTML(t("fleet_menu_new"))}</button></div>`
    : "";
  return (
    `<div class="fleet-menu">` +
    `<button type="button" class="fleet-menu-button" data-fleet-menu="toggle" aria-haspopup="true" aria-expanded="${open ? "true" : "false"}" aria-label="${escapeHTML(t("fleet_menu"))}">` +
    `${fleetIconHTML(FLEET_ICON_SIZE)}<span class="fleet-menu-name">${escapeHTML(name)}</span>` +
    `<svg class="fleet-menu-chevron" width="10" height="10" viewBox="0 0 10 10" aria-hidden="true"><path d="M2 4 L5 7 L8 4" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round"/></svg>` +
    `</button>${list}</div>`
  );
}

// nextMenuState is what a click means: whether the menu stays open, and where
// the tab goes. A function of its own because the menu is built as markup, and
// a handler attached to markup is invisible to the unit tests — three mutants
// in exactly such handlers survived every test once, and only a stand killed
// them (docs/engineering/multiple-fleets.md §7).
//
// Choosing the fleet already on screen goes nowhere on purpose: switching is a
// full page load, and reloading the panel a person is looking at would close
// every terminal they have open.
export function nextMenuState(open, action, fleet, current) {
  switch (action) {
    case "toggle":
      return { open: !open, go: null };
    case "pick":
      return { open: false, go: fleet && fleet !== current ? { fleet } : null };
    case "all":
      return { open: false, go: { path: "/" } };
    case "new":
      // The form lives on the start page; the fragment is what opens it there.
      return { open: false, go: { path: "/#new" } };
    default:
      return { open: false, go: null };
  }
}

// headerCounts is what the header's two counters count: this fleet's sessions
// and the sessions no fleet claims. Another fleet's waiting sessions are
// counted on its switcher entry instead, so they are neither lost nor taken
// for this fleet's. stalled is the tracker's answer for every session.
export function headerCounts(snap, stalled) {
  const counted = new Set(headerSessions(snap));
  return {
    waiting: [...counted].filter(isWaiting),
    stalled: stalled.filter((s) => counted.has(s)),
  };
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
  // The update button exists only where the window gave the page something to
  // run an update with. Its state lives here, outside the render below, and
  // it is repainted on its own -- on a press, on each report from the window,
  // and every UPDATE_REPAINT_MS while an update runs -- because the header's
  // render follows snapshots, and during an update the panel sending them is
  // the thing being replaced. A repaint once a second showed a wait's time up
  // to a second after it passed two seconds (found on a live page, not in the
  // unit tests, which take the time as an argument).
  // Whether the fleet menu is open, here rather than in the DOM: the header's
  // markup is replaced whole roughly once a second, and a menu that lived only
  // in that markup would shut itself under the operator's hand on the next
  // snapshot. The last snapshot is kept beside it so the menu can be repainted
  // on a click without waiting for one.
  let menuOpen = false;
  let lastSnapshot = null;
  let lastPaint = () => {};
  const applyMenu = (action, fleet) => {
    const snap = lastSnapshot ?? {};
    const current = snap.fleet ?? "";
    const next = nextMenuState(menuOpen, action, fleet, current);
    menuOpen = next.open;
    lastPaint();
    if (!next.go) return;
    if (next.go.fleet) switchFleet(next.go.fleet, { storage: pageStorage() });
    else globalThis.location.assign(next.go.path);
  };

  // The button belongs to the window, not to a browser tab: only the window
  // binds these. What the window no longer decides is whether there is a
  // button at all -- every window binds one now, and a build that cannot
  // update answers the way binding with the reason (web/js/update.js).
  const hostUpdate = typeof window[UPDATE_BINDING] === "function" ? () => window[UPDATE_BINDING]() : null;
  let update = initialState();
  const paintUpdate = () => {
    const el = root.querySelector(".update-control");
    if (el) el.outerHTML = updateHTML(update, Date.now());
  };
  const tookReport = (report) => {
    update = onProgress(update, report ?? {}, Date.now());
    paintUpdate();
  };
  if (hostUpdate) {
    window[PROGRESS_FUNCTION] = tookReport;
    setInterval(() => {
      if (update.phase === "running") paintUpdate();
    }, UPDATE_REPAINT_MS);
    // Asked once, as the page loads. A window that cannot update answers with
    // the reason, and the person reads it beside the button instead of
    // finding no button and drawing their own conclusion -- which for a whole
    // release nobody did, because there was nothing there to draw one from.
    if (typeof window[WAY_BINDING] === "function") {
      Promise.resolve(window[WAY_BINDING]()).then(tookReport, () => {});
    }
  }

  root.addEventListener("click", (event) => {
    if (hostUpdate && event.target.closest(".update-button")) {
      const pressed = onPress(update, { unsent: hasUnsentText(document), now: Date.now() });
      update = pressed.state;
      paintUpdate();
      if (pressed.start) hostUpdate();
      return;
    }
    const menu = event.target.closest("[data-fleet-menu]");
    if (menu) {
      applyMenu(menu.dataset.fleetMenu, menu.dataset.fleet ?? "");
      return;
    }
    const button = event.target.closest(".theme-toggle");
    if (!button) return;
    const label = t(themeLabelKey(cycleTheme()));
    // The label is written to whatever button is in the page now, not to the
    // node that was clicked. Clicking the theme with the fleet menu open
    // closes the menu first, on the capture phase, and that repaints the
    // header whole — the clicked node is detached by the time this runs, and
    // writing to it would leave the header reading "theme: auto" on a page
    // that had just turned light. The update button beside it has always
    // re-queried for the same reason.
    const live = root.querySelector(".theme-toggle") ?? button;
    live.textContent = label;
  });

  const paint = (rawSnap, connected) => {
    const snap = rawSnap ?? {};
    const sessions = snap.sessions ?? [];
    const nowMs = Date.now();
    // The tracker sees every session, so a session's stall clock does not
    // restart because a fleet claimed or released it; headerCounts then keeps
    // what this fleet's counters count.
    const counts = headerCounts(snap, stalledTracker.update(sessions, nowMs));
    const waitingCount = counts.waiting.length;
    const stalledSessions = counts.stalled;
    const stalledCount = stalledSessions.length;
    const usageSeverity = usageTracker.update(!!snap.usageError, nowMs);
    const usageStale = isUsageStale(snap, nowMs);

    root.innerHTML = `
      ${brandHTML(snap.build)}
      ${fleetMenuHTML(fleetEntries(snap, isWaiting), menuOpen)}
      ${themeButtonHTML()}
      ${hostUpdate ? updateHTML(update, nowMs) : ""}
      <div class="limits">
        ${snap.limits ? gauge(t("limit_5h"), snap.limits.fiveHour, usageStale, snap.limits.fetchedAt) : gauge(t("limit_5h"), null)}
        ${snap.limits ? gauge(t("limit_7d"), snap.limits.sevenDay, usageStale, snap.limits.fetchedAt) : gauge(t("limit_7d"), null)}
      </div>
      <div class="counters">
        ${alarmHTML(connected, snap)}
        ${usageProblemHTML(usageSeverity, snap.usageErrorKind)}
        <span class="counter counter-waiting ${waitingCount > 0 ? "counter-on" : ""}">${waitingCount} ${t("waiting_count")}</span>
        <span class="counter counter-stalled ${stalledCount > 0 ? "counter-on" : ""}">${stalledCount} ${t("stalled_count")} ${stalledList(stalledSessions)}</span>
      </div>`;
  };

  subscribe((rawSnap, connected) => {
    lastSnapshot = rawSnap ?? {};
    lastPaint = () => paint(rawSnap, connected);
    paint(rawSnap, connected);
  });

  // An open menu closes on a click anywhere else and on Escape. Both are on
  // the document, because the point is what happens outside the header: a menu
  // that could only be closed by the control that opened it is a menu covering
  // the panel until someone finds that control again.
  // The click listener is on the capture phase, and that is load-bearing. On
  // the bubble phase the header's own handler has already run and replaced the
  // markup whole, so the clicked node is detached by the time this asks where
  // it was: closest() then answers null for a click inside the menu, and the
  // menu shuts itself in the same gesture that opened it. Seen on a live page,
  // where opening it worked once and then did not — the unit tests cannot look
  // here, because they never run a real event through a real document.
  document.addEventListener(
    "click",
    (event) => {
      if (!menuOpen) return;
      if (event.target.closest?.(".fleet-menu")) return;
      menuOpen = false;
      lastPaint();
    },
    true,
  );
  document.addEventListener("keydown", (event) => {
    if (!menuOpen || event.key !== "Escape") return;
    menuOpen = false;
    lastPaint();
  });
}

export default renderHeader;
