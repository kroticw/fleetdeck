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
const tabs = document.getElementById("tabs");

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

renderSessions(document.getElementById("sessions"), openSession);
renderHeader(document.getElementById("header"));
renderBoard(document.getElementById("board"), cardPanel.open);
renderOrchestrator(document.getElementById("orchestrator"));

// The centre column's two sections. The documentation one is built on its first
// show rather than here: it fetches when it is built, and a panel with no
// documentation directories configured would otherwise ask for them — and take
// the server's 404 — before the operator had opened that section at all.
createSections(tabs, [
  { id: "board", label: t("tab_board"), root: document.getElementById("board") },
  {
    id: "docs",
    label: t("tab_docs"),
    root: document.getElementById("docs"),
    onFirstShow: () => renderDocs(document.getElementById("docs")),
  },
]);

// Both panels in this column are overlays: they cover whichever section is
// showing rather than being a section themselves, so the switcher — which only
// knows how to hide section roots — cannot take them down. Switching section
// with one open would leave a terminal drawn over the documentation. Asking for
// another section is an answer to "am I still reading this one", so the click
// closes both, and closing the session panel is what stops it polling.
tabs.addEventListener("click", () => {
  closeSession();
  cardPanel.close();
});

connect();
