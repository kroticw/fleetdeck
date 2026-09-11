// web/js/update.js
//
// The update button. It exists only in the fleetdeck window: cmd/fleetdeck-
// window binds window.fleetdeckUpdate, and a browser tab has nothing to run
// an update with. The window does the work -- brings the source tree forward,
// builds the new app beside the installed one, starts it, and lets the new
// window put itself in place -- and reports each step back through
// window.fleetdeckUpdateProgress. This file is what the person sees of it.
//
// The operator's rules for the button, each pinned in web/tests/update.test.js:
// it changes on the first press; a second update does not start while one
// runs (the window also holds a lock, for a second window or a terminal);
// any wait longer than two seconds shows how long it has been; and text typed
// and not sent is not lost without a word -- an update ends with the page
// reloading into the new version.

import { t } from "./i18n.js";

export const UPDATE_BINDING = "fleetdeckUpdate";
export const PROGRESS_FUNCTION = "fleetdeckUpdateProgress";
export const WAIT_SHOWN_AFTER_MS = 2000;
// How often a running update is repainted: the time of a wait appears no
// later than this after it passes WAIT_SHOWN_AFTER_MS.
export const UPDATE_REPAINT_MS = 250;

// phase: idle | confirm | running | done | current | busy | failed
export function initialState() {
  return { phase: "idle" };
}

// onPress returns the state after a press and whether the update is to start.
export function onPress(state, { unsent, now }) {
  switch (state.phase) {
    case "running":
    case "done":
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
export function onProgress(state, { step, detail = "" }, now) {
  switch (step) {
    case "done":
      return { phase: "done", detail };
    case "current":
      return { phase: "current", detail };
    case "busy":
      return { phase: "busy" };
    case "failed":
      return { phase: "failed", detail };
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
  handover: "update_step_handover",
  "handover:alive": "update_step_alive",
  "handover:panel": "update_step_panel",
  "handover:swapped": "update_step_swapped",
  "handover:done": "update_step_swapped",
};

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
      let text = fill(STEP_KEYS[state.step] ?? "update_step_press", { rev: shortRev(state.detail) });
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
    case "failed":
      inner = button(t("update_button"), false) + status(fill("update_failed", { detail: state.detail }), "update-problem");
      break;
    default:
      inner = button(t("update_button"), false);
  }
  return `<span class="update-control">${inner}</span>`;
}
