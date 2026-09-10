import { connect, subscribe } from "./store.js";
import { renderSessions } from "./sessions.js";
import { renderHeader } from "./header.js";
import { renderBoard } from "./board.js";
import { renderOrchestrator } from "./orchestrator.js";
import { createCardPanel } from "./card.js";
import { createSections } from "./sections.js";
import { renderDocs } from "./docs.js";
import { t } from "./i18n.js";

subscribe((snap, connected) => {
  document.title = connected ? `fleetdeck (${snap?.sessions?.length ?? 0})` : "fleetdeck — offline";
});

// The real session panel lands in a later task; for now, opening a session
// just logs its short id.
function openSession(short) {
  console.log("open session", short);
}

const cardPanel = createCardPanel(document.getElementById("card-panel"));

renderSessions(document.getElementById("sessions"), openSession);
renderHeader(document.getElementById("header"));
renderBoard(document.getElementById("board"), cardPanel.open);
renderOrchestrator(document.getElementById("orchestrator"));

// The centre column's two sections. The documentation one is built on its first
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
