// web/js/start.js
// The start page: the screen the application opens on.
//
// The panel is served at /?fleet=<name>; this page is what / serves when no
// fleet is named. It lists the configured fleets with what is waiting in each,
// and it makes a new one. Choosing a fleet is a navigation into the panel, the
// same navigation the header's menu makes — a fleet lives in the address and
// nowhere else (web/js/fleet.js).
//
// What it deliberately does not do:
//
//   - it does not serve a machine with no fleets. No fleets means no
//     configuration file, and then there is no panel at all: the setup surface
//     (internal/server/setup.go) serves the wizard instead, and this page is
//     never reached. The first fleet is the wizard's, as it always was.
//   - it does not repeat the wizard. Making a fleet here makes a folder and
//     writes a line in the configuration; appointing that fleet's orchestrator
//     is the wizard's, run from the fleet's own orchestrator column.
//   - it does not pretend a new fleet is usable at once. The panel reads the
//     configuration when it starts and never again, so a fleet made here is
//     served after a restart — said before the button and again after it.

import { subscribe as storeSubscribe, connect } from "./store.js";
import { t } from "./i18n.js";
import { initTheme } from "./theme.js";
import { belongsTo, fleetEntries, rememberedFleet, switchFleet } from "./fleet.js";
import { isLive } from "./lifecycle.js";
import { isWaiting } from "./header.js";
import { fleetIconHTML } from "./icon.js";
import { showSteps } from "./steplist.js";
import { pageStorage, takeReloadFleet } from "./buildcheck.js";

// The icon at the size the start page shows it. The header shows the same
// drawing much smaller; nothing else in the page uses it.
const ICON_SIZE = 64;

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

async function readJSON(response) {
  try {
    return await response.json();
  } catch {
    return null;
  }
}

/**
 * renderStart fills root with the start page.
 *
 * Everything that reaches outside the page is passed in, so a test can stand in
 * for it: subscribe (the snapshot), fetch, navigate (leaving for a fleet),
 * lastFleet (which fleet the panel wrote down last) and choose — the window's
 * folder chooser, which exists only inside the fleetdeck window.
 */
export function renderStart(root, { subscribe = storeSubscribe, fetch: get = globalThis.fetch, navigate, lastFleet, choose, openNew = false, carryBackTo } = {}) {
  const leave = navigate ?? ((name, keepSession) => switchFleet(name, { storage: pageStorage(), keepSession }));
  const remembered = lastFleet ?? (() => rememberedFleet());
  // A page that reloaded itself lands here: the window reloads by navigating
  // to a fixed address, and that address is this page now. It was in a fleet a
  // moment ago, possibly with a session panel open, and it goes straight back
  // rather than making the operator find their way again — the reload was the
  // application's doing, not theirs (web/js/buildcheck.js writes the fleet
  // down before reloading). Taken once, so only that one load carries back;
  // every other visit stays on the list.
  const carryBack = carryBackTo ?? takeReloadFleet(pageStorage());

  const icon = el("div", "start-icon");
  icon.innerHTML = fleetIconHTML(ICON_SIZE);
  const head = el("div", "start-head");
  head.append(icon, el("h1", "start-title", "fleetdeck"), el("p", "start-intro", t("start_intro")));

  const offline = el("p", "start-offline", t("start_offline"));
  offline.hidden = true;
  const list = el("ul", "start-fleets");

  const newButton = button("start-new", t("start_new"));
  const newRow = el("div", "start-row");
  newRow.append(newButton);

  // The new-fleet form. Closed until it is asked for: the page's own work is
  // choosing a fleet, and a form standing open under the list reads as a step
  // everyone has to take.
  const name = el("input", "setup-path start-new-name");
  name.setAttribute("type", "text");
  name.setAttribute("aria-label", t("start_new_name"));
  name.setAttribute("spellcheck", "false");

  const path = el("input", "setup-path start-new-path");
  path.setAttribute("type", "text");
  path.setAttribute("aria-label", t("start_new_path"));
  path.setAttribute("spellcheck", "false");

  // Labelled, not only aria-labelled: two bare boxes one under the other do
  // not say which is the name and which is the folder, and the first live run
  // of this form could not be filled in without guessing.
  const nameRow = el("label", "setup-row start-field");
  nameRow.append(el("span", "start-field-name", t("start_new_name")), name);
  const pathRow = el("label", "setup-row start-field");
  pathRow.append(el("span", "start-field-name", t("start_new_path")), path);
  if (typeof choose === "function") {
    const chooseButton = button("start-choose", t("setup_choose"));
    chooseButton.addEventListener("click", async () => {
      // The window has no dictionary of its own, so the chooser's words go
      // with the call. The fleet's own folder is made inside what was picked,
      // under the name being given — the same shape the wizard offers.
      const picked = String((await choose(t("setup_choose_message"), t("setup_choose_prompt"))) ?? "").replace(/\/+$/, "");
      if (picked === "") return;
      const named = name.value.trim();
      path.value = named ? `${picked}/${named}` : picked;
    });
    pathRow.append(chooseButton);
  }

  const createButton = button("wizard-create start-new-create", t("start_new_create"));
  const cancelButton = button("wizard-skip start-new-cancel", t("start_new_cancel"));
  const formRow = el("div", "setup-row");
  formRow.append(createButton, cancelButton);

  const unavailable = el("p", "start-new-unavailable wizard-unavailable", t("start_new_unavailable"));
  unavailable.hidden = true;

  const form = el("section", "start-new-form wizard-section");
  // Open from the start when the header's menu sent the person here to make
  // one: "start a fleet" in the panel promises the form, not another button
  // to find.
  form.hidden = !openNew;
  form.append(
    el("h2", "", t("start_new_title")),
    el("p", "", t("start_new_text")),
    // What is written outside the folder being chosen, before the button
    // rather than in the report after it. Making a fleet rewrites
    // ~/.claude/settings.json as formatted JSON, which a hand-ordered file
    // does not survive — found by making a fleet on a stand and reading what
    // the steps had done.
    el("p", "setup-outside", t("start_new_outside")),
    nameRow,
    pathRow,
    unavailable,
    formRow,
  );

  const error = el("div", "setup-error");
  const steps = el("ul", "setup-steps");
  const status = el("p", "setup-status");
  const footer = el("p", "start-footer", t("start_footer"));

  root.replaceChildren(head, offline, list, newRow, form, error, steps, status, footer);

  // What a fleet's row says: the sessions running in it — its own and the ones
  // no fleet claims (fleet.js belongsTo) — and, separately, the questions
  // waiting in it, counted on the fleet that claims them alone, so one
  // unclaimed question is not shown as waiting everywhere.
  //
  // Running ones only, the same rule the header's counters follow
  // (fleet.js headerSessions): a stopped session is paused work, and a number
  // here that counted it would send someone into a fleet on the strength of
  // sessions that are not doing anything.
  let carried = false;
  const draw = (snap, connected) => {
    const fleets = snap?.fleets ?? [];
    // Only into a fleet this panel still serves: one dropped from the
    // configuration would answer 404 for its snapshot, and the page would have
    // sent the operator to a dead address instead of showing them the list.
    if (carryBack && !carried && fleets.includes(carryBack)) {
      carried = true;
      // Keeping the open session: the tab is going back where it was, not
      // leaving a fleet, and the session panel that was up belongs to this
      // fleet (web/js/fleet.js switchFleet).
      leave(carryBack, true);
      return;
    }
    // Said only when the panel is not answering. A connected panel whose
    // snapshot carries no fleets yet is the ordinary first paint —
    // state.ForFleet serves the zero snapshot until the first collect cycle
    // has run — and this page is the first screen the application opens, so
    // reading an empty list as a dead panel would put that sentence on
    // screen at almost every launch.
    offline.hidden = connected;
    const waitingBy = new Map(fleetEntries(snap, isWaiting).map((e) => [e.name, e.waiting]));
    const last = remembered();
    list.replaceChildren(
      ...fleets.map((fleet) => {
        const entry = button("start-fleet");
        entry.dataset.fleet = fleet;
        const head = el("span", "start-fleet-head");
        head.append(el("span", "start-fleet-name", fleet));
        if (fleet === last) head.append(el("span", "start-fleet-last", t("start_last")));
        const sessions = (snap?.sessions ?? []).filter((s) => isLive(s) && belongsTo(s, fleet)).length;
        const meta = el("span", "start-fleet-meta");
        meta.append(el("span", "start-fleet-sessions", `${t("fleet_sessions")}: ${sessions}`));
        const waiting = waitingBy.get(fleet) ?? 0;
        if (waiting > 0) meta.append(el("span", "start-fleet-waiting", `${t("fleet_waiting")}: ${waiting}`));
        entry.append(head, meta);
        entry.addEventListener("click", () => leave(fleet));
        const row = el("li", "");
        row.append(entry);
        return row;
      }),
    );
  };

  newButton.addEventListener("click", () => {
    form.hidden = false;
    name.focus?.();
  });
  cancelButton.addEventListener("click", () => {
    form.hidden = true;
    error.textContent = "";
  });

  let busy = false;
  createButton.addEventListener("click", async () => {
    if (busy || createButton.disabled) return;
    const named = name.value.trim();
    const folder = path.value.trim();
    // Everything the last press left on screen goes before this one is
    // judged, not after it: refused here, the steps and the verdict of a
    // fleet made a minute ago would stand above "Name the fleet" and read as
    // this press's own outcome.
    error.textContent = "";
    steps.replaceChildren();
    status.textContent = "";
    if (named === "") {
      error.textContent = t("start_name_required");
      return;
    }
    if (folder === "") {
      error.textContent = t("setup_path_required");
      return;
    }
    busy = true;
    createButton.disabled = true;
    try {
      const response = await get("/api/fleets", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name: named, path: folder }),
      });
      const body = await readJSON(response);
      if (response.status !== 200) {
        // A panel that starts no fleets is a stand, and saying so is worth more
        // than a button that answers 503 every time it is pressed.
        if (response.status === 503) unavailable.hidden = false;
        error.textContent = String(body?.error ?? response.statusText ?? response.status);
        return;
      }
      showSteps(steps, Array.isArray(body?.steps) ? body.steps : []);
      // The list is deliberately not grown here. This panel read the
      // configuration when it started and will not read it again, so the new
      // fleet is not one this page can open — an entry for it would be a row
      // that answers 404.
      status.textContent = body?.ok === true ? t("start_made") : t("start_failed");
    } catch (err) {
      error.textContent = String(err?.message ?? err);
    } finally {
      busy = false;
      if (unavailable.hidden) createButton.disabled = false;
    }
  });

  subscribe((snap, connected) => draw(snap, connected));
}

// The page itself. Guarded, so a test can import the module without a page.
if (typeof document !== "undefined" && typeof document.getElementById === "function") {
  const root = document.getElementById("start");
  if (root) {
    initTheme();
    const chooser = globalThis.fleetdeckChooseFolder;
    renderStart(root, {
      choose: typeof chooser === "function" ? (message, prompt) => chooser(message, prompt) : undefined,
      // The fragment the header's menu leaves for (web/js/header.js
      // nextMenuState). A fragment rather than a query parameter: it never
      // reaches the server, and it cannot be mistaken for the fleet parameter
      // that decides what the root serves.
      openNew: globalThis.location?.hash === "#new",
    });
    connect();
  }
}

export default renderStart;
