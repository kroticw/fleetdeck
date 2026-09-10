import { connect, subscribe } from "./store.js";
import { renderSessions } from "./sessions.js";
import { renderHeader } from "./header.js";
import { renderBoard } from "./board.js";
import { renderOrchestrator } from "./orchestrator.js";
import { renderCard } from "./card.js";

subscribe((snap, connected) => {
  document.title = connected ? `fleetdeck (${snap?.sessions?.length ?? 0})` : "fleetdeck — offline";
});

// The real session panel lands in a later task; for now, opening a session
// just logs its short id.
function openSession(short) {
  console.log("open session", short);
}

// The board calls back with the path of the card that was clicked; the panel is
// opened from that callback rather than from a listener of its own, so one click
// cannot open it twice.
let openCard = () => {};
const board = document.getElementById("board");
const cardPanel = document.getElementById("card-panel");

if (board && cardPanel) {
  let disposeCard = null;

  const closeCard = () => {
    if (disposeCard) {
      disposeCard();
      disposeCard = null;
    }
    cardPanel.replaceChildren();
    cardPanel.hidden = true;
  };

  openCard = (path) => {
    closeCard();
    disposeCard = renderCard(cardPanel, path, closeCard);
  };
}

renderSessions(document.getElementById("sessions"), openSession);
renderHeader(document.getElementById("header"));
renderBoard(document.getElementById("board"), (path) => openCard(path));
renderOrchestrator(document.getElementById("orchestrator"));

connect();
