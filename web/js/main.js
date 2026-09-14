import { connect, subscribe, get } from "./store.js";
import { renderSessions } from "./sessions.js";
import { renderHeader } from "./header.js";
import { renderBoard } from "./board.js";
import { renderOrchestrator } from "./orchestrator.js";
import { createCardPanel, cardPathForLink } from "./card.js";
import { createReader } from "./reader.js";
import { createSections } from "./sections.js";
import { createNewCard } from "./newcard.js";
import { renderDocs } from "./docs.js";
import { renderSession } from "./session.js";
import { renderBuildBanner, pageStorage, rememberOpenSession, takeOpenSession } from "./buildcheck.js";
import { rememberFleet } from "./fleet.js";
import { t } from "./i18n.js";
import { readHost, callHost } from "./host.js";
import { regionsFor, layoutReport } from "./surfaces.js";
import { wireHostActions } from "./hostactions.js";
import { applyTheme, cycleTheme } from "./theme.js";

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
  header: regions.has("header") || regions.has("brand"),
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
  onOpenSession: (short) => openSession(short),
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

// The card control in a session row opens that session's card, through the same
// panel the board opens. Before this it opened the session instead, because it
// had no handler of its own and the click reached the row.
if (regions.has("sessions")) renderSessions(document.getElementById("sessions"), openSession, cardPanel.open);
if (regions.has("header")) renderHeader(document.getElementById("header"));
renderBuildBanner(document.getElementById("build-banner"), subscribe);
if (regions.has("center")) renderBoard(document.getElementById("board"), cardPanel.open);
if (regions.has("orchestrator")) renderOrchestrator(document.getElementById("orchestrator"), { links: terminalLinks });

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

// What the fleetdeck window asks of this surface (web/js/hostactions.js). Wired
// while the module runs, before the page's load event: the window sends nothing
// until that event says the page is up, and by then this is listening.
if (host) {
  const page = document.documentElement;
  const column = document.getElementById(host.surface);
  wireHostActions(window, host, {
    openCard: (path) => terminalLinks.open(path),
    openDoc: (path) => {
      closeSession();
      cardPanel.close();
      reader.open(path);
    },
    openSession,
    showSection: (id) => sections?.show(id),
    openNewCard: () => newCard?.open(),
    cycleTheme: () => cycleTheme() ?? "auto",
    applyTheme,
    setInsets: (insets) => {
      for (const side of ["top", "left", "right"]) page.style.setProperty(`--host-inset-${side}`, `${insets[side]}px`);
    },
    setGlass: (glass) => {
      page.dataset.glass = glass;
    },
    setFolded: (folded) => {
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
