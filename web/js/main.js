import { connect, subscribe } from "./store.js";

subscribe((snap, connected) => {
  document.title = connected ? `fleetdeck (${snap.sessions?.length ?? 0})` : "fleetdeck — offline";
});

connect();
