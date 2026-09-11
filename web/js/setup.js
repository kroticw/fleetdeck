// The page of a panel that has no configuration yet: the person chooses the
// folder fleetdeck keeps its board and documentation in, the panel makes it,
// and this page hands over to the panel.
//
// It says, before the button is pressed, what is written outside that folder:
// Claude Code's settings get the folder as an allowed directory and fleetdeck's
// status line. Every step's outcome is shown afterwards, a refused one with its
// reason — the same lines `fleetdeck init` prints.

import { t } from "./i18n.js";

// What the panel names the workspace folder inside a directory the person
// picked with the window's chooser.
const WORKSPACE_NAME = "fleetdeck";

// How long the page waits for the panel to take over after setup: tries times
// the pause. The panel swaps itself in right after the setup answers; this is
// the margin, not the expected wait.
const HANDOVER_TRIES = 50;
const HANDOVER_PAUSE_MS = 200;

function el(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}

async function readJSON(response) {
  try {
    return await response.json();
  } catch {
    return null;
  }
}

/**
 * renderSetup fills root with the setup page.
 *
 * Everything that reaches outside the page is passed in, so a test can stand in
 * for it: fetch, reload (open the panel), wait (the pause between asking the
 * panel whether it has taken over), and choose — the window's folder chooser,
 * which exists only inside the fleetdeck window.
 */
export function renderSetup(root, { fetch: get = globalThis.fetch, reload, wait, choose } = {}) {
  const pause = wait ?? (() => new Promise((resolve) => setTimeout(resolve, HANDOVER_PAUSE_MS)));
  const open = reload ?? (() => globalThis.location.replace("/"));

  const heading = el("h1", "setup-title", t("setup_title"));
  const intro = el("p", "setup-intro", t("setup_intro"));
  const made = el("ul", "setup-made");
  made.append(el("li", "", t("setup_board")), el("li", "", t("setup_docs")));
  const outside = el("p", "setup-outside", t("setup_outside"));

  const path = el("input", "setup-path");
  path.setAttribute("type", "text");
  path.setAttribute("aria-label", t("setup_path"));
  path.setAttribute("spellcheck", "false");

  const row = el("div", "setup-row");
  row.append(path);
  if (typeof choose === "function") {
    const chooseButton = el("button", "setup-choose", t("setup_choose"));
    chooseButton.setAttribute("type", "button");
    chooseButton.addEventListener("click", async () => {
      // The window has no dictionary of its own, so the chooser's words go
      // with the call.
      const picked = String((await choose(t("setup_choose_message"), t("setup_choose_prompt"))) ?? "").replace(/\/+$/, "");
      if (picked !== "") path.value = `${picked}/${WORKSPACE_NAME}`;
    });
    row.append(chooseButton);
  }
  const createButton = el("button", "setup-create", t("setup_create"));
  createButton.setAttribute("type", "button");
  row.append(createButton);

  const error = el("div", "setup-error");
  const steps = el("ul", "setup-steps");
  const status = el("p", "setup-status");

  root.replaceChildren(heading, intro, made, outside, row, error, steps, status);

  get("/api/setup")
    .then(readJSON)
    .then((body) => {
      if (body?.default && path.value === "") path.value = String(body.default);
    })
    .catch(() => {});

  const showSteps = (list) => {
    steps.replaceChildren(
      ...list.map((step) => {
        const item = el("li", step.error ? "setup-step setup-step-skipped" : "setup-step");
        const line = step.error ? `${step.name}: ${t("setup_skipped")}: ${step.error}` : `${step.name}: ${step.note}`;
        item.textContent = line;
        if (step.detail) item.append(el("div", "setup-step-detail", step.detail));
        return item;
      }),
    );
  };

  const handOver = async () => {
    status.textContent = t("setup_opening");
    for (let i = 0; i < HANDOVER_TRIES; i += 1) {
      try {
        const response = await get("/api/snapshot", { cache: "no-store" });
        if (response.status === 200) {
          open();
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
      showSteps(Array.isArray(body?.steps) ? body.steps : []);
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

// The page itself. Guarded, so a test can import the module without a page.
if (typeof document !== "undefined" && typeof document.getElementById === "function") {
  const root = document.getElementById("setup");
  if (root) {
    const chooser = globalThis.fleetdeckChooseFolder;
    renderSetup(root, { choose: typeof chooser === "function" ? (message, prompt) => chooser(message, prompt) : undefined });
  }
}
