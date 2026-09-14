import { connect, subscribe, get } from "./store.js";
import { renderSessions } from "./sessions.js";
import { renderHeader, HEADER_PARTS, limitsOf, themeLabelText } from "./header.js";
import { renderBoard } from "./board.js";
import { renderOrchestrator } from "./orchestrator.js";
import { createCardPanel, cardPathForLink } from "./card.js";
import { createReader } from "./reader.js";
import { createSections } from "./sections.js";
import { createNewCard } from "./newcard.js";
import { renderDocs } from "./docs.js";
import { renderSession } from "./session.js";
import { renderBuildBanner, pageStorage, rememberOpenSession, takeOpenSession } from "./buildcheck.js";
import { rememberFleet, switchFleet } from "./fleet.js";
import { t } from "./i18n.js";
import { readHost, callHost } from "./host.js";
import { regionsFor, layoutReport } from "./surfaces.js";
import { wireHostActions } from "./hostactions.js";
import { routesFor } from "./hostroutes.js";
import { applyTheme, cycleTheme, initTheme } from "./theme.js";
import { capsuleModel } from "./capsules.js";

// In the fleetdeck window this page is one of three web views, and mounts only
// its own part of the panel (surfaces.js). The side surfaces keep the centre
// column's overlays, hidden, until opening goes through the window.
const host = readHost(window);
const regions = regionsFor(host);
if (host) {
  document.documentElement.dataset.surface = host.surface;
  document.documentElement.dataset.glass = host.glass;
}
const center = regions.has("center");
const kept = {
  header: regions.has("header") || regions.has("brand") || regions.has("counters"),
  orchestrator: regions.has("orchestrator"),
  sessions: regions.has("sessions"),
  tabs: center,
  board: center,
  docs: center,
};
for (const [id, keep] of Object.entries(kept)) {
  if (!keep) document.getElementById(id)?.remove();
}
document.getElementById("center").hidden = !center;

let layoutReported = false;
subscribe((snap) => {
  const report = layoutReport(host, snap);
  if (!report || layoutReported) return;
  layoutReported = true;
  callHost(window, "fleetdeckLayout", report);
});

// This module and every one it imports have arrived. The window reads this
// once the page has loaded, and a page loaded without it -- its scripts cut off
// by a panel restarting under it -- is asked for again
// (cmd/fleetdeck-window/owner.go, pageLoadScript).
// Asked of globalThis: the tests import this module with no document at all.
if (globalThis.document?.documentElement) document.documentElement.dataset.fleetdeckPage = "running";

subscribe((snap, connected) => {
  document.title = connected ? `fleetdeck (${snap?.sessions?.length ?? 0})` : "fleetdeck — offline";
});

// Which fleet this panel is showing, written down for the start page to mark
// on its list next launch. Taken from the snapshot rather than the address:
// an address with no fleet is served the first one, and the snapshot is the
// only place that says which one that turned out to be.
// Written once, not on every snapshot. Which fleet a tab shows is decided by
// its address and never changes while the page lives, so writing it each
// second would be a storage write a second in every open tab — and with two
// tabs on two fleets the key would flip between them, making the start page's
// mark whichever tab ticked last rather than where the operator was.
let fleetRemembered = false;
subscribe((snap) => {
  const fleet = snap?.fleet ?? "";
  if (!fleet || fleetRemembered) return;
  fleetRemembered = true;
  rememberFleet(fleet);
});

const sessionPanel = document.getElementById("session-panel");
// The card panel and the document reader are two overlays over the same column,
// and each opens the other: a card lists its documents, and a document names the
// cards linking to it. Only one of them may be up, so each closes before the
// other opens. The same holds for the session panel a card jumps to:
// openSession closes the card panel before it opens the session.
const cardPanel = createCardPanel(document.getElementById("card-panel"), {
  onOpenSession: (short) => showSession(short),
  onOpenDoc: (path) => {
    cardPanel.close();
    reader.open(path);
  },
});
const reader = createReader(document.getElementById("reader-panel"), {
  onOpenCard: (path) => {
    reader.close();
    cardPanel.open(path);
  },
});

// Exactly one session panel at a time, and its stop function held here.
//
// Opening a second session without stopping the first would leave the first
// still polling a session nobody is looking at — the panel is a page that
// stays open for hours, so a leak here is not academic.
let stopSession = null;

// Which session is open is written down as it changes, so a reload -- the
// window reloads the page by itself when the panel under it is replaced --
// opens it again instead of closing it under the operator. See buildcheck.js.
const storage = pageStorage();

function closeSession() {
  if (stopSession) stopSession();
  stopSession = null;
  sessionPanel.hidden = true;
  sessionPanel.replaceChildren();
  rememberOpenSession(storage, "");
}

// A [[link]] a session prints into a live terminal opens the card it names,
// through the same panel the board opens and by the same rule a card's own
// links follow. The session panel goes first: the two panels are overlays over
// the same column, and only one of them may be up.
const terminalLinks = {
  resolve: (name) => cardPathForLink(get()?.cards, name),
  open: (path) => {
    closeSession();
    reader.close();
    cardPanel.open(path);
  },
};

function openSession(short) {
  closeSession();
  // The session panel and the card panel are two overlays over the same column,
  // and the card panel is opened from the board underneath. Only one of them may
  // be up, or they cover each other in whichever order they happened to open.
  cardPanel.close();
  reader.close();
  // A card in the session's history opens the way a [[link]] in its terminal
  // does: the session panel goes, the card panel comes up.
  stopSession = renderSession(sessionPanel, short, closeSession, { links: terminalLinks, onOpenCard: terminalLinks.open });
  rememberOpenSession(storage, short);
}

// Where an action goes (web/js/hostroutes.js): in a browser tab, here; in the
// fleetdeck window a side surface hands opening to the board through the window,
// and every surface switches fleet and folds its panel through it.
const routes = routesFor(window, host, {
  openCard: terminalLinks.open,
  openDoc: (path) => {
    closeSession();
    cardPanel.close();
    reader.open(path);
  },
  openSession,
  switchFleet: (name) => switchFleet(name, { storage }),
  fold: () => {},
  openOrchestrator: () => {},
});

// In the window the orchestrator's pinned session is its own panel, always on
// screen, so opening it focuses that panel instead of a second copy in a sheet.
function showSession(short) {
  if (host && short === get()?.orchestratorSession) routes.openOrchestrator();
  else routes.openSession(short);
}

// The card control in a session row opens that session's card, through the same
// panel the board opens. Before this it opened the session instead, because it
// had no handler of its own and the click reached the row.
if (regions.has("sessions")) {
  renderSessions(document.getElementById("sessions"), showSession, routes.openCard, { switchFleet: routes.switchFleet });
}
// The orchestrator surface draws the header's brand row (with the update button
// beside it), the sessions surface its counters, the board none of it.
const headerParts = regions.has("header")
  ? HEADER_PARTS
  : HEADER_PARTS.filter((part) => regions.has(part === "update" ? "brand" : part));
if (headerParts.length > 0) {
  renderHeader(document.getElementById("header"), { switchFleet: routes.switchFleet, parts: headerParts });
} else {
  initTheme();
}
// One web view checks the build: in the window, the board. Its reload is the
// window's, which reloads all three, and its reload ceiling stays in one place.
if (regions.has("build")) renderBuildBanner(document.getElementById("build-banner"), subscribe);
if (regions.has("center")) renderBoard(document.getElementById("board"), cardPanel.open);
if (regions.has("orchestrator")) {
  renderOrchestrator(document.getElementById("orchestrator"), { links: { resolve: terminalLinks.resolve, open: routes.openCard } });
}

// The centre column's two sections.
//
// Neither panel is closed when a section is switched, and does not need to be:
// both are absolute overlays over the whole column, tab bar included, so while
// one is up the switcher cannot be reached — checked in a browser, not deduced.
// The way out of a panel is its own close button. If the overlays are ever moved
// to cover only the section area, a section switch has to close them, or a
// terminal ends up drawn over the documentation.
//
// The documentation section is built on its first
// show rather than here: it fetches when it is built, and a panel with no
// documentation directories configured would otherwise ask for them — and take
// the server's 404 — before the operator had opened that section at all.
let sections = null;
let newCard = null;
if (regions.has("center")) {
  sections = createSections(document.getElementById("tabs"), [
    { id: "board", label: t("tab_board"), root: document.getElementById("board") },
    {
      id: "docs",
      label: t("tab_docs"),
      root: document.getElementById("docs"),
      onFirstShow: () => renderDocs(document.getElementById("docs"), { onOpenCard: cardPanel.open }),
    },
  ]);

  // After the tabs, not before: createSections replaces the row's children.
  newCard = createNewCard(document.getElementById("tabs"));
}

// The window's capsules (web/js/capsules.js), handed over by the board whenever
// what they show changes: the section, the theme, the limits.
let section = "board";
let capsulesSent = "";
function publishCapsules() {
  if (host?.surface !== "board") return;
  const style = getComputedStyle(document.documentElement);
  const token = (name) => style.getPropertyValue(name).trim();
  const colors = { cool: token("--ok"), warm: token("--attn"), hot: token("--danger"), stale: token("--text-muted"), off: token("--text-faint") };
  const model = capsuleModel({ section, themeLabel: themeLabelText(), limits: limitsOf(get() ?? {}, Date.now()), t, colors });
  const json = JSON.stringify(model);
  if (json === capsulesSent) return;
  capsulesSent = json;
  callHost(window, "fleetdeckCapsules", model);
}
subscribe(publishCapsules);

// What the fleetdeck window asks of this surface (web/js/hostactions.js). Wired
// while the module runs, before the page's load event: the window sends nothing
// until that event says the page is up, and by then this is listening.
if (host) {
  const page = document.documentElement;
  const column = document.getElementById(host.surface);
  // The window's panel folds with the column: a fold the column makes itself
  // (its own button) is passed on, and one the window sends is not passed back.
  let panelFolded = column?.dataset.folded === "1";
  if (column && host.surface !== "board") {
    new MutationObserver(() => {
      const folded = column.dataset.folded === "1";
      if (folded === panelFolded) return;
      panelFolded = folded;
      routes.fold(host.surface, folded);
    }).observe(column, { attributes: true, attributeFilter: ["data-folded"] });
  }
  wireHostActions(window, host, {
    openCard: (path) => terminalLinks.open(path),
    openDoc: (path) => {
      closeSession();
      cardPanel.close();
      reader.open(path);
    },
    openSession: showSession,
    showSection: (id) => {
      sections?.show(id);
      section = id;
      publishCapsules();
    },
    openNewCard: () => newCard?.open(),
    cycleTheme: () => {
      const choice = cycleTheme() ?? "auto";
      publishCapsules();
      return choice;
    },
    applyTheme,
    setInsets: (insets) => {
      const names = { top: "top", left: "left", right: "right", contentRight: "content-right" };
      for (const [key, name] of Object.entries(names)) page.style.setProperty(`--host-inset-${name}`, `${insets[key]}px`);
    },
    setGlass: (glass) => {
      page.dataset.glass = glass;
    },
    setFolded: (folded) => {
      panelFolded = folded;
      if (folded) column.dataset.folded = "1";
      else delete column.dataset.folded;
    },
    focusTerminal: () => document.querySelector("#orchestrator .xterm-helper-textarea")?.focus(),
    setFullscreen: (on) => {
      if (on) page.dataset.fullscreen = "1";
      else delete page.dataset.fullscreen;
    },
  });
}

connect();

// The session that was open when this page was last loaded, reopened once.
// Nothing above opens or closes a session while the module loads, so the
// stored value is still the one the previous page left. Only the board shows
// sessions; a side surface leaves the stored value to it.
const reopen = regions.has("center") ? takeOpenSession(storage) : "";
if (reopen) openSession(reopen);
