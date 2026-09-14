// web/js/surfaces.js — which part of the panel a web view is.
//
// In the fleetdeck window the board, the orchestrator column and the sessions
// column are three web views on the same address. Each mounts only its own
// regions; the board alone tells the window it is a panel page it can frame.
import { HOST_VERSION } from "./host.js";

const ALL = ["header", "orchestrator", "center", "sessions"];
const BY_SURFACE = {
  board: ["center"],
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
