import { connect, subscribe } from "./store.js";
import { renderSessions } from "./sessions.js";
import { renderHeader } from "./header.js";

subscribe((snap, connected) => {
  document.title = connected ? `fleetdeck (${snap?.sessions?.length ?? 0})` : "fleetdeck — offline";
});

// The real session panel lands in a later task; for now, opening a session
// just logs its short id.
function openSession(short) {
  console.log("open session", short);
}

renderSessions(document.getElementById("sessions"), openSession);
renderHeader(document.getElementById("header"));

connect();
