// web/js/update.js
//
// The update button. It exists only in the fleetdeck window: cmd/fleetdeck-
// window binds window.fleetdeckUpdate, and a browser tab has nothing to run
// an update with. The window does the work -- gets the new app, either by
// building the source tree beside it or by downloading a release and checking
// who signed it, then starts it and lets the new window put itself in place --
// and reports each step back through window.fleetdeckUpdateProgress. This file
// is what the person sees of it.
//
// The operator's rules for the button, each pinned in web/tests/update.test.js:
// it changes on the first press; a second update does not start while one
// runs (the window also holds a lock, for a second window or a terminal);
// any wait longer than two seconds shows how long it has been; and text typed
// and not sent is not lost without a word -- an update ends with the page
// reloading into the new version.
//
// One rule was added on 2026-09-12, and it is why this file changed: a build
// that cannot update itself says so. It used to say nothing, because the page
// drew a button only where the window had bound one -- so an app installed
// from a release had no button, and nobody who had one could find out that
// updating existed at all. The button is always drawn now; window.
// fleetdeckUpdateWay is what the page asks, as it loads, about whether this
// build can use it, and a build that cannot answers with the reason.

import { t } from "./i18n.js";

export const UPDATE_BINDING = "fleetdeckUpdate";
export const WAY_BINDING = "fleetdeckUpdateWay";
export const PROGRESS_FUNCTION = "fleetdeckUpdateProgress";
export const WAIT_SHOWN_AFTER_MS = 2000;
// How often a running update is repainted: the time of a wait appears no
// later than this after it passes WAIT_SHOWN_AFTER_MS.
export const UPDATE_REPAINT_MS = 250;

// phase: idle | cannot | confirm | running | done | current | busy | failed
export function initialState() {
  return { phase: "idle" };
}

// onPress returns the state after a press and whether the update is to start.
export function onPress(state, { unsent, now }) {
  switch (state.phase) {
    case "running":
    case "done":
    // A build that cannot update starts nothing, whatever reaches the button:
    // a keyboard, or a click the browser delivered before the window answered.
    case "cannot":
      return { state, start: false };
    case "confirm":
      return { state: running("press", "", now), start: true };
    default:
      if (unsent) return { state: { phase: "confirm" }, start: false };
      return { state: running("press", "", now), start: true };
  }
}

function running(step, detail, now) {
  return { phase: "running", step, detail, since: now };
}

// onProgress folds one report from the window into the state.
export function onProgress(state, { step, detail = "", reason = "" }, now) {
  switch (step) {
    case "done":
      return { phase: "done", detail };
    case "current":
      return { phase: "current", detail };
    case "busy":
      return { phase: "busy" };
    case "failed":
      return { phase: "failed", detail, reason };
    // What the window answers when this build cannot update itself at all.
    case "cannot":
      return { phase: "cannot", reason };
    // The window's answer when it can. Nothing to show, and it must not be
    // taken for a step of an update that is not running.
    case "can":
      return state;
    default:
      // A new step starts its own clock: the time shown is how long this step
      // has taken, which is the wait the person is in now.
      if (state.phase === "running" && state.step === step) return state;
      return running(step, detail, now);
  }
}

const STEP_KEYS = {
  press: "update_step_press",
  check: "update_step_check",
  build: "update_step_build",
  download: "update_step_download",
  verify: "update_step_verify",
  handover: "update_step_handover",
  "handover:alive": "update_step_alive",
  "handover:panel": "update_step_panel",
  "handover:swapped": "update_step_swapped",
  "handover:done": "update_step_swapped",
};

// reasonKey is the dictionary key for one of the window's reason codes. The
// codes are written the way the Go side spells them -- "seal:wrong-team" --
// and the dictionary keys the way every other key in it is written, so the
// two are held together here rather than in a table that can be added to on
// one side only.
export function reasonKey(prefix, reason) {
  return `${prefix}_${String(reason).replaceAll(/[:-]/g, "_")}`;
}

// reasonText is the sentence for a reason code, or "" when there is none to
// say -- an older window, or a code nobody foresaw. The caller then shows the
// particulars on their own rather than an empty line.
function reasonText(prefix, reason) {
  if (!reason) return "";
  const key = reasonKey(prefix, reason);
  const text = t(key);
  // i18n.js answers a missing key with the key itself, which on screen would
  // read as a piece of the program rather than a sentence.
  return text === key ? "" : text;
}

function shortRev(rev) {
  return String(rev ?? "").slice(0, 7);
}

function escapeHTML(s) {
  return String(s ?? "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;");
}

function fill(key, values) {
  return t(key).replace(/\{(\w+)\}/g, (_, name) => values[name] ?? "");
}

export function updateHTML(state, now) {
  const button = (label, disabled) =>
    `<button type="button" class="update-button"${disabled ? " disabled" : ""}>${escapeHTML(label)}</button>`;
  const status = (text, cls = "") => `<span class="update-status ${cls}">${escapeHTML(text)}</span>`;
  let inner;
  switch (state.phase) {
    case "confirm":
      inner = button(t("update_confirm_unsent"), false);
      break;
    case "running": {
      // Two names for the same detail, because the two sources say different
      // things with it: a tree names a commit, which is shortened to be
      // readable, and a release names a version tag, which is already short
      // and must not be cut.
      let text = fill(STEP_KEYS[state.step] ?? "update_step_press", {
        rev: shortRev(state.detail),
        version: state.detail,
      });
      const waited = now - state.since;
      if (waited >= WAIT_SHOWN_AFTER_MS) {
        text += " " + fill("update_elapsed", { n: Math.floor(waited / 1000) });
      }
      inner = button(t("update_button"), true) + status(text);
      break;
    }
    case "done":
      inner = status(fill("update_done", { rev: shortRev(state.detail) }));
      break;
    case "current":
      inner = button(t("update_button"), false) + status(fill("update_current", { rev: shortRev(state.detail) }));
      break;
    case "busy":
      inner = button(t("update_button"), false) + status(t("update_busy"), "update-problem");
      break;
    case "failed": {
      // The sentence comes from the reason code, so it is in the reader's
      // language; the detail is what the system said, in whatever language it
      // says things, and it is shown because it carries the particulars a
      // person can act on -- which team signed the app, how much room is
      // missing, what could not be reached.
      const why = reasonText("update_reason", state.reason);
      const text = why
        ? fill("update_failed_because", { why, detail: state.detail })
        : fill("update_failed", { detail: state.detail });
      inner = button(t("update_button"), false) + status(text, "update-problem");
      break;
    }
    // This build cannot update itself at all. The button stays on screen and
    // stays unpressable: its being there is how a person learns that updating
    // exists, and the sentence beside it is how they learn why not here.
    case "cannot":
      inner =
        button(t("update_button"), true) +
        status(reasonText("update_cannot", state.reason) || t("update_cannot_other"), "update-problem");
      break;
    default:
      inner = button(t("update_button"), false);
  }
  return `<span class="update-control">${inner}</span>`;
}
