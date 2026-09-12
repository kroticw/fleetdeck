import { connect, subscribe, get } from "./store.js";
import { renderSessions } from "./sessions.js";
import { renderHeader } from "./header.js";
import { renderBoard } from "./board.js";
import { renderOrchestrator } from "./orchestrator.js";
import { createCardPanel, cardPathForLink } from "./card.js";
import { createSections } from "./sections.js";
import { createNewCard } from "./newcard.js";
import { renderDocs } from "./docs.js";
import { renderSession } from "./session.js";
import { renderBuildBanner, pageStorage, rememberOpenSession, takeOpenSession } from "./buildcheck.js";
import { rememberFleet } from "./fleet.js";
import { t } from "./i18n.js";

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
const cardPanel = createCardPanel(document.getElementById("card-panel"));

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
    cardPanel.open(path);
  },
};

function openSession(short) {
  closeSession();
  // The session panel and the card panel are two overlays over the same column,
  // and the card panel is opened from the board underneath. Only one of them may
  // be up, or they cover each other in whichever order they happened to open.
  cardPanel.close();
  stopSession = renderSession(sessionPanel, short, closeSession, { links: terminalLinks });
  rememberOpenSession(storage, short);
}

// The card control in a session row opens that session's card, through the same
// panel the board opens. Before this it opened the session instead, because it
// had no handler of its own and the click reached the row.
renderSessions(document.getElementById("sessions"), openSession, cardPanel.open);
renderHeader(document.getElementById("header"));
renderBuildBanner(document.getElementById("build-banner"), subscribe);
renderBoard(document.getElementById("board"), cardPanel.open);
renderOrchestrator(document.getElementById("orchestrator"), { links: terminalLinks });

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
createSections(document.getElementById("tabs"), [
  { id: "board", label: t("tab_board"), root: document.getElementById("board") },
  {
    id: "docs",
    label: t("tab_docs"),
    root: document.getElementById("docs"),
    onFirstShow: () => renderDocs(document.getElementById("docs")),
  },
]);

// After the tabs, not before: createSections replaces the row's children.
createNewCard(document.getElementById("tabs"));

connect();

// The session that was open when this page was last loaded, reopened once.
// Nothing above opens or closes a session while the module loads, so the
// stored value is still the one the previous page left.
const reopen = takeOpenSession(storage);
if (reopen) openSession(reopen);
