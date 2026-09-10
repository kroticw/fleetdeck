import { connect, subscribe } from "./store.js";
import { renderSessions } from "./sessions.js";
import { renderHeader } from "./header.js";
import { renderBoard } from "./board.js";
import { renderOrchestrator } from "./orchestrator.js";
import { createCardPanel } from "./card.js";
import { createSections } from "./sections.js";
import { renderDocs } from "./docs.js";
import { renderSession } from "./session.js";
import { t } from "./i18n.js";

subscribe((snap, connected) => {
  document.title = connected ? `fleetdeck (${snap?.sessions?.length ?? 0})` : "fleetdeck — offline";
});

const sessionPanel = document.getElementById("session-panel");
const cardPanel = createCardPanel(document.getElementById("card-panel"));

// Exactly one session panel at a time, and its stop function held here.
//
// Opening a second session without stopping the first would leave the first
// still polling a session nobody is looking at — the panel is a page that
// stays open for hours, so a leak here is not academic.
let stopSession = null;

function closeSession() {
  if (stopSession) stopSession();
  stopSession = null;
  sessionPanel.hidden = true;
  sessionPanel.replaceChildren();
}

function openSession(short) {
  closeSession();
  // The session panel and the card panel are two overlays over the same column,
  // and the card panel is opened from the board underneath. Only one of them may
  // be up, or they cover each other in whichever order they happened to open.
  cardPanel.close();
  stopSession = renderSession(sessionPanel, short, closeSession);
}

// The card control in a session row opens that session's card, through the same
// panel the board opens. Before this it opened the session instead, because it
// had no handler of its own and the click reached the row.
renderSessions(document.getElementById("sessions"), openSession, cardPanel.open);
renderHeader(document.getElementById("header"));
renderBoard(document.getElementById("board"), cardPanel.open);
renderOrchestrator(document.getElementById("orchestrator"));

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

connect();
