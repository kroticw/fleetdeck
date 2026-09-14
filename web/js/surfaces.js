// web/js/surfaces.js — which part of the panel a web view is.
//
// In the fleetdeck window the board, the orchestrator column and the sessions
// column are three web views on the same address. Each mounts only its own
// regions; the board alone tells the window it is a panel page it can frame.
import { HOST_VERSION } from "./host.js";

// "build" is the check of the page's build against the panel's: one web view
// makes it. In the window that is the board, whose reload is the window's and
// reloads all three, so its reload ceiling stays in one web view's storage.
const ALL = ["header", "orchestrator", "center", "sessions", "build"];
const BY_SURFACE = {
  board: ["center", "build"],
  orchestrator: ["orchestrator", "brand"],
  sessions: ["sessions", "counters"],
};

export function regionsFor(host) {
  return new Set(host ? BY_SURFACE[host.surface] : ALL);
}

export function layoutReport(host, snapshot) {
  if (!host || host.surface !== "board") return null;
  if (!snapshot || typeof snapshot.fleet !== "string") return null;
  return { version: HOST_VERSION, mode: "panel", fleet: snapshot.fleet };
}
