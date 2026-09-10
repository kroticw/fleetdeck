import { connect, subscribe } from "./store.js";
import { renderSessions } from "./sessions.js";
import { renderHeader } from "./header.js";
import { renderBoard } from "./board.js";
import { renderOrchestrator } from "./orchestrator.js";

subscribe((snap, connected) => {
  document.title = connected ? `fleetdeck (${snap?.sessions?.length ?? 0})` : "fleetdeck — offline";
});

// The real session panel lands in a later task; for now, opening a session
// just logs its short id.
function openSession(short) {
  console.log("open session", short);
}

// Placeholder until a later task adds the card panel: opening a card just
// logs its path for now.
function onOpenCard(path) {
  console.log("open card", path);
}

renderSessions(document.getElementById("sessions"), openSession);
renderHeader(document.getElementById("header"));
renderBoard(document.getElementById("board"), onOpenCard);
renderOrchestrator(document.getElementById("orchestrator"));

connect();
