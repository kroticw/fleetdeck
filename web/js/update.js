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
// One more rule, from 2026-09-13: the button is on screen only while there is
// something to update to, and its appearing is the notice that a newer version
// is out. From 2026-09-12 it stood there always, meaning "press and I will go
// and look": it was pressed for nothing, and on the day a release came out
// nothing on screen changed. The window now looks by itself while it runs and
// reports what it finds ("available", and "none" when it takes one back);
// window.fleetdeckUpdateKnown is what the page asks, as it loads, for what
// the window already knows.

import { t } from "./i18n.js";

export const UPDATE_BINDING = "fleetdeckUpdate";
export const KNOWN_BINDING = "fleetdeckUpdateKnown";
export const PROGRESS_FUNCTION = "fleetdeckUpdateProgress";
export const WAIT_SHOWN_AFTER_MS = 2000;
// How often a running update is repainted: the time of a wait appears no
// later than this after it passes WAIT_SHOWN_AFTER_MS.
export const UPDATE_REPAINT_MS = 250;
// How long the answer to Check for Updates… stays when there is nothing to
// install -- "nothing newer", or why there was no answer (decided by the
// operator, 2026-10-02). A version found stays, with its button.
export const CHECK_SHOWN_MS = 10_000;

// phase: idle | available | cannot | confirm | running | done | current | busy | failed
//        | checking | latest | checkFailed
//
// The last three are Check for Updates… in the app menu, which asks the
// releases page now. They carry offer: the version on offer before the check,
// so that a check with no answer does not take it away -- the window keeps it
// too (cmd/fleetdeck-window/watch.go).
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
    // What the window found by looking on its own: a newer version, or no
    // longer one. Nobody pressed anything, so a version has to show itself.
    // It never interrupts a press -- an update running, the question about
    // unsent text, or an update just done and about to reload the page.
    case "available":
    case "none":
      if (busyWithAPress(state)) return state;
      return step === "available" ? { phase: "available", detail } : initialState();
    // What Check for Updates… in the app menu reports. A person asked, so
    // every outcome is said; none of it interrupts a press either.
    case "checking":
      if (busyWithAPress(state)) return state;
      return { phase: "checking", since: now, offer: offerOf(state) };
    case "latest":
      if (busyWithAPress(state)) return state;
      return { phase: "latest", detail, since: now };
    case "check-failed":
      if (busyWithAPress(state)) return state;
      return { phase: "checkFailed", reason, detail, since: now, offer: offerOf(state) };
    default:
      // A new step starts its own clock: the time shown is how long this step
      // has taken, which is the wait the person is in now.
      if (state.phase === "running" && state.step === step) return state;
      return running(step, detail, now);
  }
}

// STAND_UPDATE is the report a stand holds the update control in, by what it
// opened (FLEETDECK_STAND_OPEN check-*, web/js/host.js). The particulars are
// what scripts/standcheck looks for in the frame's words (updatecontrol.go).
export const STAND_UPDATE = {
  "check-checking": { step: "checking" },
  "check-latest": { step: "latest", detail: "v1.0.0" },
  "check-failed": {
    step: "check-failed",
    reason: "offline",
    detail: 'Head "https://github.com/kroticw/fleetdeck/releases/latest": dial tcp: lookup github.com: no such host',
  },
  "check-available": { step: "available", detail: "v1.1.0" },
};

// updateControlReport is what a stand's orchestrator surface says of its
// update control in the window's log: its words, whether the Update button is
// there, and whether the words are marked as a problem.
export function updateControlReport(control) {
  const status = control?.querySelector(".update-status");
  return {
    report: "update",
    text: status?.textContent ?? "",
    button: !!control?.querySelector(".update-button"),
    problem: !!control?.querySelector(".update-problem"),
  };
}

// busyWithAPress: an update running, the question about unsent text, or an
// update just done and about to reload the page. Nothing the window finds or
// is asked to check knocks those out of the way.
function busyWithAPress(state) {
  return state.phase === "running" || state.phase === "confirm" || state.phase === "done";
}

// offerOf is the version on offer in state, or "".
function offerOf(state) {
  if (state.phase === "available") return state.detail;
  return state.offer ?? "";
}

// settle takes away the answer to a check once it has been on screen for
// CHECK_SHOWN_MS, back to the version on offer before it, if any.
export function settle(state, now) {
  if (state.phase !== "latest" && state.phase !== "checkFailed") return state;
  if (now - state.since < CHECK_SHOWN_MS) return state;
  return state.offer ? { phase: "available", detail: state.offer } : initialState();
}

// needsRepaint says whether the header has to repaint state as time passes:
// a wait whose time is shown, or an answer that is to go away.
export function needsRepaint(state) {
  return ["running", "checking", "latest", "checkFailed"].includes(state.phase);
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
    `<button type="button" class="btn btn-sm update-button"${disabled ? " disabled" : ""}>${escapeHTML(label)}</button>`;
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
    // A press found nothing newer after all. There is nothing to update to, so
    // there is no button -- only the answer.
    case "current":
      inner = status(fill("update_current", { rev: shortRev(state.detail) }));
      break;
    // Found by the window, with nobody asking. This is the notice: the button
    // beside it is the one that installs it.
    case "available":
      inner = button(t("update_button"), false) + status(fill("update_available", { version: state.detail }), "update-available");
      break;
    case "busy":
      inner = button(t("update_button"), false) + status(t("update_busy"), "update-problem");
      break;
    // Check for Updates…: the releases page is being asked. No button: what
    // there is to install is not known until the answer.
    case "checking": {
      let text = t("update_checking");
      const waited = now - state.since;
      if (waited >= WAIT_SHOWN_AFTER_MS) {
        text += " " + fill("update_elapsed", { n: Math.floor(waited / 1000) });
      }
      inner = status(text);
      break;
    }
    case "latest":
      inner = status(fill("update_latest", { version: state.detail }));
      break;
    // A check with no answer. It says why in the reader's language, with the
    // particulars beside it, and keeps the button for a version already found.
    case "checkFailed": {
      const why = reasonText("update_check_reason", state.reason);
      const text = why
        ? fill("update_check_failed_because", { why, detail: state.detail })
        : fill("update_check_failed", { detail: state.detail });
      inner = (state.offer ? button(t("update_button"), false) : "") + status(text, "update-problem");
      break;
    }
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
    // This build cannot update itself at all. It never finds anything to
    // update to, so this arrives only if the update binding is called anyway;
    // the answer is said in words, with no button for an update it cannot do.
    case "cannot":
      inner = status(reasonText("update_cannot", state.reason) || t("update_cannot_other"), "update-problem");
      break;
    // Nothing to update to: no button at all, not even a disabled one. The
    // control stays in the page, empty, as the place a found version is
    // painted into.
    default:
      inner = "";
  }
  return `<span class="update-control">${inner}</span>`;
}
