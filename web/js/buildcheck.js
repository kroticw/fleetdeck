// Whether the page this window is showing is the page the panel serves now.
//
// The panel is rebuilt and replaced under a window that stays open for days.
// store.js reconnects the socket to the new panel on its own, and until this
// module nothing told the page that the code it was running was no longer the
// code being served -- the new panel's features simply did not appear, and a
// changed server under old code lost features without any visible cause.
//
// Who creates what, and who reads it:
//
//   - internal/server writes the fingerprint of the build that served this
//     document into a <meta name="fleetdeck-build"> tag, in the same response.
//     That is the page's own build. It is read once, at load, and never
//     updated: the question is what this page IS, not what the panel is now.
//   - every snapshot carries snapshot.build, the panel's build as of now.
//   - the two web hashes are compared. Only the web hash: a commit changes
//     without the page changing (a Go-only change) and stays the same while
//     it does (two builds of one commit with different uncommitted edits).
//
// A reload can fail to deliver a new page -- a document served from cache
// brings the old fingerprint straight back. So before reloading, the page
// writes down which build it is leaving; the next load reads that back once,
// and if it is still that build, says the reload did not help instead of
// offering the same button again.

import { t } from "./i18n.js";

// sessionStorage, not localStorage: the note is about one reload of this one
// window, and must not outlive it into a different window opened tomorrow.
export const RELOAD_FROM_KEY = "fleetdeck-reload-from";

const META_SELECTOR = 'meta[name="fleetdeck-build"]';

// The session storage of this page, or undefined. Reading the sessionStorage
// property itself throws where site data is blocked -- not only calls on it --
// and a default parameter that throws takes the whole module's wiring down
// with it, not just the memory of a reload.
export function pageStorage() {
  try {
    return globalThis.sessionStorage;
  } catch {
    return undefined;
  }
}

export function readOwnBuild(doc) {
  return doc?.querySelector(META_SELECTOR)?.content ?? "";
}

// "unknown"      nothing to compare: no banner, as before this module existed
// "current"      the page is what the panel serves
// "stale"        the panel serves a different interface than this page runs
// "reloadFailed" a reload was asked for and brought back the same document
export function buildState(own, served, reloadFrom) {
  if (!own || !served) return "unknown";
  if (own === served) return "current";
  if (reloadFrom && reloadFrom === own) return "reloadFailed";
  return "stale";
}

// Storage can be absent, or present and throwing on every call. Either way
// the answer is "" -- the page then cannot recognise a failed reload, and
// behaves exactly as it would without this: the banner still works.
export function takeReloadFrom(storage) {
  try {
    const value = storage?.getItem(RELOAD_FROM_KEY) ?? "";
    storage?.removeItem(RELOAD_FROM_KEY);
    return value;
  } catch {
    return "";
  }
}

export function rememberReloadFrom(storage, own) {
  try {
    storage?.setItem(RELOAD_FROM_KEY, own);
  } catch {
    // Nothing to do: see takeReloadFrom.
  }
}

// Which fleet the page was showing when it reloaded itself.
//
// The window reloads by navigating to a fixed address (cmd/fleetdeck-window
// binds fleetdeckReload against its -url flag), and that address is the start
// page now. Without this the page would come back on a list of fleets: the
// fleet the operator was in is lost, and with it the open session, since the
// start page never runs takeOpenSession and the first fleet it is sent to
// clears the key. So the fleet is written down here, beside the build, and the
// start page carries the page back — see web/js/start.js.
//
// Session storage, like everything else here: it belongs to this tab and to
// this reload, and it is taken once.
const RELOAD_FLEET_KEY = "fleetdeck-reload-fleet";

export function takeReloadFleet(storage) {
  try {
    const value = storage?.getItem(RELOAD_FLEET_KEY) ?? "";
    storage?.removeItem(RELOAD_FLEET_KEY);
    return value;
  } catch {
    return "";
  }
}

// Written down before reloading, not after: after is a different page.
export function reloadNow(storage, own, reload, fleet = "") {
  rememberReloadFrom(storage, own);
  try {
    if (fleet) storage?.setItem(RELOAD_FLEET_KEY, fleet);
    else storage?.removeItem(RELOAD_FLEET_KEY);
  } catch {
    // The page comes back on the start page, one click from where it was.
  }
  reload();
}

// --- inside the fleetdeck window ---------------------------------------------
//
// cmd/fleetdeck-window binds window.fleetdeckReload: a function that navigates
// the page afresh from Go. Its presence is how the page knows it is in the
// window and not in a browser, and it is what lets the page be reloaded with
// no person involved. In a browser nothing binds it, and the page behaves
// exactly as before: it shows the banner and waits for a click.
//
// A fresh navigation rather than location.reload(): a reload may take the
// document from cache, a navigation asks the server.

// One extra attempt after a reload that brought the same page back, then the
// page stops and says so. Anything that retries on its own needs a ceiling,
// or a window that is handed a stale page keeps reloading forever.
export const MAX_WINDOW_ATTEMPTS = 1;
export const ATTEMPTS_KEY = "fleetdeck-reload-attempts";

// "none"   nothing to do
// "banner" show the banner for the current state and wait
// "reload" reload now, without asking
export function nextAction({ state, host, unsent, attempts }) {
  if (state !== "stale" && state !== "reloadFailed") return "none";
  if (!host) return "banner";
  // Text typed and not sent is lost by any reload. The page waits for the
  // field to empty -- the operator sending the message is what empties it.
  if (unsent) return "banner";
  if (state === "reloadFailed" && attempts >= MAX_WINDOW_ATTEMPTS) return "banner";
  return "reload";
}

// Whether any field on the page holds text that has not been sent.
//
// xterm.js keeps a hidden textarea of its own for keyboard input; whatever is
// in it has already reached the session byte by byte, and the session's own
// input line survives a reload because it lives in the session, not the page.
// So a terminal never holds a reload back.
export function hasUnsentText(doc) {
  const fields = doc?.querySelectorAll?.("textarea, input[type=text], input:not([type])") ?? [];
  return Array.from(fields).some((el) => !el.closest?.(".xterm") && String(el.value ?? "").trim() !== "");
}

export function takeAttempts(storage) {
  try {
    const value = Number(storage?.getItem(ATTEMPTS_KEY) ?? 0);
    storage?.removeItem(ATTEMPTS_KEY);
    return Number.isInteger(value) && value > 0 ? value : 0;
  } catch {
    return 0;
  }
}

export function rememberAttempts(storage, attempts) {
  try {
    storage?.setItem(ATTEMPTS_KEY, String(attempts));
  } catch {
    // Without storage the retry count resets on every load, and the ceiling
    // becomes per-load rather than per-episode. Still a ceiling.
  }
}

// --- the session that was open -------------------------------------------------
//
// A reload closes whatever session panel was open. A person pressing reload
// expects that; a window reloading by itself would close the panel under them
// for no reason they can see. main.js writes the open session down here and
// opens it again after the load. Which tab inside the session panel was open
// is session.js's to keep, not this module's.
const OPEN_SESSION_KEY = "fleetdeck-open-session";

export function rememberOpenSession(storage, short) {
  try {
    if (short) storage?.setItem(OPEN_SESSION_KEY, short);
    else storage?.removeItem(OPEN_SESSION_KEY);
  } catch {
    // The panel simply does not come back after a reload.
  }
}

export function takeOpenSession(storage) {
  try {
    const value = storage?.getItem(OPEN_SESSION_KEY) ?? "";
    storage?.removeItem(OPEN_SESSION_KEY);
    return value;
  } catch {
    return "";
  }
}

// waiting: the page is in the window, which would reload it by itself, and is
// holding back only because a field has text in it. The banner then says the
// reload will come on its own, and still offers it now, at the person's choice.
export function bannerHTML(state, { waiting = false } = {}) {
  if (state === "stale") {
    const text = waiting ? t("build_stale_waiting") : t("build_stale");
    const button = waiting ? t("build_reload_now") : t("build_reload");
    return `<span class="build-banner-text">${escape(text)}</span>
      <button type="button" class="build-reload">${escape(button)}</button>`;
  }
  if (state === "reloadFailed") {
    // No button here on purpose. The action that just failed is not offered
    // again; the message names the step that does work -- quitting the
    // window takes its web view, cache and all, with it.
    return `<span class="build-banner-text">${escape(t("build_reload_failed"))}</span>`;
  }
  return "";
}

// The brand in the header, with the panel's build beside it. On screen: a
// release's version, which is what a person who downloaded the app can read;
// for a build from a checkout, which reports "dev", the word dev and the short
// commit, so it never passes for a release; for a panel from before the version
// was reported, the short commit alone, as it was then. The rest -- the version,
// the full commit, when it was made, when this binary was built and where the
// binary is -- sits in the title, for the moment someone asks which of several
// installs is answering on this port.
export function brandHTML(build) {
  const version = build?.version ?? "";
  const revision = build?.revision ?? "";
  const short = revision.slice(0, 7);
  const mark = build?.modified ? "*" : "";

  let label = short;
  if (version === "dev") label = short ? `dev ${short}` : "dev";
  else if (version) label = version;

  const lines = [];
  if (version) lines.push(`${t("build_version")}: ${version}`);
  if (revision) lines.push(`${t("build_commit")}: ${revision}${build.modified ? ` (${t("build_modified")})` : ""}`);
  if (build?.commitTime) lines.push(`${t("build_commit_time")}: ${build.commitTime}`);
  if (build?.builtAt) lines.push(`${t("build_built_at")}: ${build.builtAt}`);
  if (build?.executable) lines.push(`${t("build_executable")}: ${build.executable}`);

  const title = lines.length ? ` title="${escape(lines.join("\n"))}"` : "";
  const rev = label ? ` <span class="build-rev">${escape(label + mark)}</span>` : "";
  return `<div class="brand"${title}>fleetdeck${rev}</div>`;
}

// Wires the banner to a root element and the store. Kept apart from the
// functions above so those can be tested without a page.
//
// hostReload is window.fleetdeckReload when the page is inside the fleetdeck
// window, and undefined in a browser; see the section above nextAction.
export function renderBuildBanner(
  root,
  subscribe,
  {
    doc = document,
    storage = pageStorage(),
    reload = () => location.reload(),
    hostReload = typeof window.fleetdeckReload === "function" ? () => window.fleetdeckReload() : undefined,
  } = {},
) {
  const own = readOwnBuild(doc);
  const reloadFrom = takeReloadFrom(storage);
  const attempts = takeAttempts(storage);
  const doReload = hostReload ?? reload;
  let shown = null;
  let reloading = false;
  // The fleet on screen, for the reload to carry. Taken from the snapshot and
  // not from the address: an address naming no fleet is served the first one,
  // and only the snapshot says which that is.
  let fleet = "";

  root.addEventListener("click", (event) => {
    if (!event.target.closest(".build-reload")) return;
    reloadNow(storage, own, doReload, fleet);
  });

  subscribe((snap) => {
    fleet = snap?.fleet ?? fleet;
    if (reloading) return;
    const state = buildState(own, snap?.build?.web, reloadFrom);
    const unsent = hasUnsentText(doc);
    const action = nextAction({ state, host: !!hostReload, unsent, attempts });

    if (action === "reload") {
      // Once: snapshots keep arriving until the navigation actually starts,
      // and each would otherwise ask for another one.
      reloading = true;
      if (state === "reloadFailed") rememberAttempts(storage, attempts + 1);
      reloadNow(storage, own, doReload, fleet);
      return;
    }

    const waiting = action === "banner" && state === "stale" && !!hostReload && unsent;
    const key = action === "none" ? "none" : `${state}:${waiting}`;
    // Redrawn only when what is shown changes: snapshots arrive about once a
    // second, and redrawing every time would take focus off the button.
    if (key === shown) return;
    shown = key;
    const html = action === "none" ? "" : bannerHTML(state, { waiting });
    root.innerHTML = html;
    root.hidden = html === "";
    root.classList.toggle("build-banner-failed", state === "reloadFailed");
  });
}

function escape(value) {
  return String(value)
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;");
}
