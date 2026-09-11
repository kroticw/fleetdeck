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

// Written down before reloading, not after: after is a different page.
export function reloadNow(storage, own, reload) {
  rememberReloadFrom(storage, own);
  reload();
}

export function bannerHTML(state) {
  if (state === "stale") {
    return `<span class="build-banner-text">${escape(t("build_stale"))}</span>
      <button type="button" class="build-reload">${escape(t("build_reload"))}</button>`;
  }
  if (state === "reloadFailed") {
    // No button here on purpose. The action that just failed is not offered
    // again; the message names the step that does work -- quitting the
    // window takes its web view, cache and all, with it.
    return `<span class="build-banner-text">${escape(t("build_reload_failed"))}</span>`;
  }
  return "";
}

// The brand in the header, with the panel's build beside it. The short commit
// is on screen; the rest -- the full commit, when it was made, when this
// binary was built and where the binary is -- sits in the title, for the
// moment someone asks which of several installs is answering on this port.
export function brandHTML(build) {
  const revision = build?.revision ?? "";
  const short = revision.slice(0, 7);
  const mark = build?.modified ? "*" : "";

  const lines = [];
  if (revision) lines.push(`${t("build_commit")}: ${revision}${build.modified ? ` (${t("build_modified")})` : ""}`);
  if (build?.commitTime) lines.push(`${t("build_commit_time")}: ${build.commitTime}`);
  if (build?.builtAt) lines.push(`${t("build_built_at")}: ${build.builtAt}`);
  if (build?.executable) lines.push(`${t("build_executable")}: ${build.executable}`);

  const title = lines.length ? ` title="${escape(lines.join("\n"))}"` : "";
  const rev = short ? ` <span class="build-rev">${escape(short + mark)}</span>` : "";
  return `<div class="brand"${title}>fleetdeck${rev}</div>`;
}

// Wires the banner to a root element and the store. Kept apart from the
// functions above so those can be tested without a page.
export function renderBuildBanner(root, subscribe, { doc = document, storage = sessionStorage, reload = () => location.reload() } = {}) {
  const own = readOwnBuild(doc);
  const reloadFrom = takeReloadFrom(storage);
  let shown = null;

  root.addEventListener("click", (event) => {
    if (!event.target.closest(".build-reload")) return;
    reloadNow(storage, own, reload);
  });

  subscribe((snap) => {
    const state = buildState(own, snap?.build?.web, reloadFrom);
    // Redrawn only when the state changes: snapshots arrive about once a
    // second, and redrawing every time would take focus off the button.
    if (state === shown) return;
    shown = state;
    const html = bannerHTML(state);
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
