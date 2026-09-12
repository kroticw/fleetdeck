// The first-run wizard, one page in two steps.
//
// The folder: a panel that has no configuration yet serves this page, the
// person chooses the folder fleetdeck keeps its board and documentation in,
// and the panel makes it. The page says, before the button is pressed, what is
// written outside that folder: Claude Code's settings get the folder as an
// allowed directory and fleetdeck's status line. Every step's outcome is shown
// afterwards, a refused one with its reason — the same lines `fleetdeck init`
// prints.
//
// The orchestrator: once the panel has taken over, the same page asks which
// session the fleet is led from — a new one, or one already running. Both are
// appointed the same way (internal/orchestrator): the working order is written
// into a file and the session is sent one line telling it to read it. An
// existing session has a conversation and a task of its own, and adding to it
// is said in so many words before its button, not discovered after.
//
// A panel that is set up serves this page too, at /setup.html, and there it
// opens at the orchestrator step: that is how the wizard is run again.

import { t, langCode } from "./i18n.js";
import { envelopeText } from "./envelope.js";
import { belongsTo, fleetFromSearch, withFleet } from "./fleet.js";
import { fleetIconHTML } from "./icon.js";

// What the panel names the workspace folder inside a directory the person
// picked with the window's chooser.
const WORKSPACE_NAME = "fleetdeck";

// The icon on the folder step. A person with no configuration never reaches
// the start page — there is no panel yet, only this surface — so this is the
// screen the application opens on for them, and it carries the application's
// mark for the same reason the start page does.
const ICON_SIZE = 48;

// How long the page waits for the panel to take over after setup: tries times
// the pause. The panel swaps itself in right after the setup answers; this is
// the margin, not the expected wait.
const HANDOVER_TRIES = 50;
const HANDOVER_PAUSE_MS = 200;

// How often the orchestrator step asks the panel for the sessions again: what
// a session is busy with is the point of the list, and it changes.
const SESSIONS_EVERY_MS = 2000;

// panelAddress is where the wizard leaves for: the panel, showing the fleet
// the wizard was run for.
//
// The fleet is always named, even when the wizard does not know which one it
// was — the first launch has one fleet and no name for it yet. An address with
// no fleet at all is the start page now (internal/server/static.go), and
// finishing the wizard by landing on a list of one fleet would be a step
// backwards from where the person already is. The empty value is what
// fleet.Select reads as "the first fleet".
export function panelAddress(fleet) {
  return `/?fleet=${encodeURIComponent(fleet)}`;
}

function el(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}

function button(className, text) {
  const node = el("button", className, text);
  node.setAttribute("type", "button");
  return node;
}

// fill puts values into a sentence from the dictionary: {name} and the like.
function fill(sentence, values) {
  return Object.entries(values).reduce((s, [key, value]) => s.replaceAll(`{${key}}`, value), sentence);
}

async function readJSON(response) {
  try {
    return await response.json();
  } catch {
    return null;
  }
}

// showSteps lists what a write did, step by step, a refused step with its
// reason. The folder step and the orchestrator step report the same way.
function showSteps(target, list) {
  target.replaceChildren(
    ...list.map((step) => {
      const item = el("li", step.error ? "setup-step setup-step-skipped" : "setup-step");
      item.textContent = step.error ? `${step.name}: ${t("setup_skipped")}: ${step.error}` : `${step.name}: ${step.note}`;
      if (step.detail) item.append(el("div", "setup-step-detail", step.detail));
      return item;
    }),
  );
}

/**
 * renderSetup fills root with the wizard, at the step the panel is at: the
 * folder while the panel has no configuration (its setup surface answers
 * /api/setup), the orchestrator once it has one (the panel has no such route).
 *
 * Everything that reaches outside the page is passed in, so a test can stand in
 * for it: fetch, reload (open the panel), wait (the pause between asking the
 * panel whether it has taken over), choose — the window's folder chooser, which
 * exists only inside the fleetdeck window — and every, which runs a function
 * on an interval and returns what stops it.
 */
export function renderSetup(root, { fetch: get = globalThis.fetch, reload, wait, choose, every } = {}) {
  const pause = wait ?? (() => new Promise((resolve) => setTimeout(resolve, HANDOVER_PAUSE_MS)));
  // Run again from a fleet's orchestrator column the wizard is that fleet's:
  // /setup.html?fleet=B asks for B's preview and sessions, appoints into B and
  // goes back to B's tab (see fleet.js). The first launch has one fleet and no
  // parameter, and is served as it always was.
  const fleet = fleetFromSearch(globalThis.location?.search ?? "");
  const fleetGet = (url, init) => get(withFleet(url, fleet), init);
  const open = reload ?? (() => globalThis.location.replace(panelAddress(fleet)));
  const repeat =
    every ??
    ((fn, ms) => {
      const id = setInterval(fn, ms);
      return () => clearInterval(id);
    });
  const orchestratorStep = (made = []) => renderOrchestratorStep(root, { get: fleetGet, open, fleet, every: repeat, made });
  const workspaceStep = (proposed) => renderWorkspaceStep(root, { get, pause, choose, proposed, onReady: orchestratorStep });

  get("/api/setup")
    .then(async (response) => {
      if (response.status === 404) {
        orchestratorStep();
        return;
      }
      const body = await readJSON(response);
      workspaceStep(body?.default ? String(body.default) : "");
    })
    .catch(() => workspaceStep(""));
}

// renderWorkspaceStep is the folder step. onReady runs once the panel has
// taken over from the setup surface, with the steps setup reported: the next
// step carries them, or they would be on screen for as long as one poll.
function renderWorkspaceStep(root, { get, pause, choose, proposed, onReady }) {
  const icon = el("div", "setup-icon");
  icon.innerHTML = fleetIconHTML(ICON_SIZE);
  const heading = el("h1", "setup-title", t("setup_title"));
  const head = el("div", "setup-head");
  head.append(icon, heading);
  const intro = el("p", "setup-intro", t("setup_intro"));
  const made = el("ul", "setup-made");
  made.append(el("li", "", t("setup_board")), el("li", "", t("setup_docs")));
  const outside = el("p", "setup-outside", t("setup_outside"));

  const path = el("input", "setup-path");
  path.setAttribute("type", "text");
  path.setAttribute("aria-label", t("setup_path"));
  path.setAttribute("spellcheck", "false");
  path.value = proposed;

  const row = el("div", "setup-row");
  row.append(path);
  if (typeof choose === "function") {
    const chooseButton = button("setup-choose", t("setup_choose"));
    chooseButton.addEventListener("click", async () => {
      // The window has no dictionary of its own, so the chooser's words go
      // with the call.
      const picked = String((await choose(t("setup_choose_message"), t("setup_choose_prompt"))) ?? "").replace(/\/+$/, "");
      if (picked !== "") path.value = `${picked}/${WORKSPACE_NAME}`;
    });
    row.append(chooseButton);
  }
  const createButton = button("setup-create", t("setup_create"));
  row.append(createButton);

  const error = el("div", "setup-error");
  const steps = el("ul", "setup-steps");
  const status = el("p", "setup-status");

  root.replaceChildren(head, intro, made, outside, row, error, steps, status);

  let reported = [];
  const handOver = async () => {
    status.textContent = t("setup_opening");
    for (let i = 0; i < HANDOVER_TRIES; i += 1) {
      try {
        const response = await get("/api/snapshot", { cache: "no-store" });
        if (response.status === 200) {
          onReady(reported);
          return;
        }
      } catch {
        // The panel may be between its two handlers for a moment; ask again.
      }
      await pause();
    }
    status.textContent = t("setup_no_handover");
  };

  createButton.addEventListener("click", async () => {
    if (createButton.disabled) return;
    const chosen = path.value.trim();
    if (chosen === "") {
      error.textContent = t("setup_path_required");
      return;
    }
    createButton.disabled = true;
    error.textContent = "";
    status.textContent = "";
    try {
      const response = await get("/api/setup", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ path: chosen }),
      });
      const body = await readJSON(response);
      if (response.status !== 200) {
        error.textContent = String(body?.error ?? response.statusText ?? response.status);
        createButton.disabled = false;
        return;
      }
      reported = Array.isArray(body?.steps) ? body.steps : [];
      showSteps(steps, reported);
      if (body?.ok === true) {
        await handOver();
        return;
      }
      status.textContent = t("setup_failed");
      createButton.disabled = false;
    } catch (err) {
      error.textContent = String(err?.message ?? err);
      createButton.disabled = false;
    }
  });
}

// sessionName is what a session is called here: the operator's own label, the
// daemon's name, the short id — never blank, the same order the panel uses.
function sessionName(s) {
  return s.label || s.name || s.short;
}

function contextPercent(s) {
  return s.context?.window ? Math.round((s.context.tokens / s.context.window) * 100) : null;
}

// doing is what a session is busy with, in the daemon's own words: a question
// it is waiting on first, because that is what a person has to act on, then its
// detail. The envelope a fleet message arrives in comes off; nothing else does.
function doing(s) {
  if (s.needs) return `${t("wizard_waiting_for_you")}: ${envelopeText(s.needs)}`;
  return envelopeText(s.detail || "");
}

function renderOrchestratorStep(root, { get, open, fleet = "", every, made = [] }) {
  let preview = null;
  let sessions = [];
  let current = "";
  let chosen = "";
  let busy = false;

  const heading = el("h1", "setup-title", t("wizard_title"));
  const intro = el("p", "setup-intro", t("wizard_intro"));

  // What the folder step did, when this step follows it.
  const done = el("div", "wizard-made");
  done.hidden = made.length === 0;
  if (made.length > 0) {
    const list = el("ul", "setup-steps");
    showSteps(list, made);
    done.append(el("p", "", t("wizard_setup_done")), list);
  }

  // What will be sent and written, the same for both paths, shown once.
  const line = el("code", "wizard-line");
  const briefPath = el("code", "wizard-path");
  const brief = el("pre", "wizard-brief");
  const briefBox = el("details", "wizard-brief-box");
  briefBox.append(el("summary", "", t("wizard_brief_show")), brief);
  const briefLine = el("p", "wizard-brief-line", `${t("wizard_brief")} `);
  briefLine.append(briefPath);
  const message = el("div", "wizard-message");
  message.append(el("p", "", t("wizard_message")), line, briefLine, briefBox);

  const newText = el("p", "wizard-new-text");
  const newUnavailable = el("p", "wizard-unavailable", t("wizard_new_unavailable"));
  newUnavailable.hidden = true;
  const createButton = button("wizard-create", t("wizard_new_button"));
  const newSection = el("section", "wizard-section wizard-new");
  newSection.append(el("h2", "", t("wizard_new_title")), newText, newUnavailable, createButton);

  const list = el("ul", "wizard-sessions");
  const listNote = el("p", "wizard-sessions-note");
  const warning = el("div", "wizard-warning");
  warning.hidden = true;
  const appointButton = button("wizard-appoint", "");
  appointButton.hidden = true;
  const existingSection = el("section", "wizard-section wizard-existing");
  existingSection.append(el("h2", "", t("wizard_existing_title")), el("p", "", t("wizard_existing_text")), listNote, list, warning, appointButton);

  const error = el("div", "wizard-error setup-error");
  const steps = el("ul", "setup-steps");
  const status = el("p", "setup-status");
  const openButton = button("wizard-open", t("wizard_open"));
  openButton.hidden = true;
  const skipButton = button("wizard-skip", t("wizard_skip"));
  const actions = el("div", "setup-row wizard-actions");
  actions.append(openButton, skipButton);
  const rerun = el("p", "wizard-rerun", t("wizard_rerun_hint"));

  root.replaceChildren(done, heading, intro, message, newSection, existingSection, error, steps, status, actions, rerun);

  const enable = () => {
    createButton.disabled = busy || !preview?.canStart;
    appointButton.disabled = busy;
  };

  const drawPreview = () => {
    line.textContent = preview?.message ?? "";
    briefPath.textContent = preview?.path ?? "";
    brief.textContent = preview?.brief ?? "";
    newText.textContent = fill(t("wizard_new_text"), { name: preview?.name ?? "", path: preview?.workspace ?? "" });
    newUnavailable.hidden = preview?.canStart !== false;
    enable();
  };

  const drawWarning = () => {
    const session = sessions.find((s) => s.short === chosen);
    warning.hidden = !session;
    appointButton.hidden = !session;
    if (!session) {
      warning.replaceChildren();
      return;
    }
    const name = sessionName(session);
    const items = el("ul", "wizard-warn-list");
    items.append(el("li", "", t("wizard_warn_kept")), el("li", "", t("wizard_warn_context")));
    const pct = contextPercent(session);
    if (pct !== null) items.append(el("li", "", fill(t("wizard_warn_context_now"), { n: String(pct) })));
    items.append(el("li", "", t("wizard_warn_busy")), el("li", "", t("wizard_warn_final")));
    const parts = [
      el("p", "wizard-warn-title", t("wizard_warn_title")),
      el("p", "wizard-warn-lead", fill(t("wizard_warn_lead"), { name, doing: doing(session) || t("wizard_doing_unknown") })),
      items,
    ];
    if (current && current === chosen) {
      parts.push(el("p", "wizard-warn-note", t("wizard_warn_again")));
    } else if (current) {
      const old = sessions.find((s) => s.short === current);
      parts.push(el("p", "wizard-warn-note", fill(t("wizard_warn_replaces"), { old: old ? sessionName(old) : current })));
    }
    warning.replaceChildren(...parts);
    appointButton.textContent = fill(t("wizard_appoint_button"), { name });
  };

  const drawSessions = (snapshot) => {
    current = snapshot?.orchestratorSession ?? "";
    // Never a session another fleet claims: it is that fleet's work, and its
    // orchestrator cannot lead this one (see fleet.js belongsTo).
    sessions = (snapshot?.sessions ?? []).filter((s) => s.short && !s.dying && belongsTo(s, fleet));
    if (!sessions.some((s) => s.short === chosen)) chosen = "";
    listNote.textContent = snapshot?.daemonError
      ? fill(t("wizard_daemon_down"), { detail: snapshot.daemonError })
      : sessions.length === 0
        ? t("wizard_no_sessions")
        : "";
    list.replaceChildren(
      ...sessions.map((s) => {
        const item = button(s.short === chosen ? "wizard-session wizard-session-chosen" : "wizard-session");
        item.dataset.short = s.short;
        const head = el("span", "wizard-session-name", sessionName(s));
        const parts = [head];
        if (s.short === current) parts.push(el("span", "wizard-session-current", t("wizard_current")));
        const pct = contextPercent(s);
        const meta = [s.cwd, s.state, pct === null ? "" : fill(t("wizard_context"), { n: String(pct) })].filter(Boolean).join(" · ");
        parts.push(el("span", "wizard-session-meta", meta));
        const busyWith = doing(s);
        if (busyWith) parts.push(el("span", s.needs ? "wizard-session-doing wizard-session-waiting" : "wizard-session-doing", busyWith));
        item.append(...parts);
        item.addEventListener("click", () => {
          chosen = s.short;
          drawSessions(snapshot);
        });
        const row = el("li", "");
        row.append(item);
        return row;
      }),
    );
    drawWarning();
  };

  const loadSessions = async () => {
    try {
      const response = await get("/api/snapshot", { cache: "no-store" });
      if (response.status === 200) drawSessions(await readJSON(response));
    } catch {
      // The next ask may find the panel; the list stays as it was.
    }
  };

  const appoint = async (body, working) => {
    if (busy) return;
    busy = true;
    enable();
    error.textContent = "";
    steps.replaceChildren();
    status.textContent = working;
    try {
      const response = await get("/api/orchestrator", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ ...body, lang: langCode }),
      });
      const result = await readJSON(response);
      if (response.status !== 200) {
        error.textContent = String(result?.error ?? response.statusText ?? response.status);
        status.textContent = "";
        return;
      }
      showSteps(steps, Array.isArray(result?.steps) ? result.steps : []);
      status.textContent = result?.ok === true ? t("wizard_done") : t("wizard_failed");
      if (result?.ok === true) openButton.hidden = false;
      await loadSessions();
    } catch (err) {
      error.textContent = String(err?.message ?? err);
      status.textContent = "";
    } finally {
      busy = false;
      enable();
    }
  };

  createButton.addEventListener("click", () => {
    if (createButton.disabled) return;
    appoint({ new: true }, t("wizard_starting"));
  });
  appointButton.addEventListener("click", () => {
    if (appointButton.disabled || !chosen) return;
    appoint({ session: chosen }, t("wizard_working"));
  });
  const leave = () => {
    stop();
    open();
  };
  openButton.addEventListener("click", leave);
  skipButton.addEventListener("click", leave);

  drawPreview();
  get(`/api/orchestrator?lang=${langCode}`)
    .then(async (response) => {
      const body = await readJSON(response);
      if (response.status === 200) {
        preview = body;
        drawPreview();
      } else {
        error.textContent = String(body?.error ?? response.status);
      }
    })
    .catch((err) => {
      error.textContent = String(err?.message ?? err);
    });
  loadSessions();
  const stop = every(loadSessions, SESSIONS_EVERY_MS);
}

// The page itself. Guarded, so a test can import the module without a page.
if (typeof document !== "undefined" && typeof document.getElementById === "function") {
  const root = document.getElementById("setup");
  if (root) {
    const chooser = globalThis.fleetdeckChooseFolder;
    renderSetup(root, { choose: typeof chooser === "function" ? (message, prompt) => chooser(message, prompt) : undefined });
  }
}
