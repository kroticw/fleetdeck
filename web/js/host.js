// web/js/host.js — the window around the page, when there is one.
//
// The fleetdeck window injects window.fleetdeckHost before the page loads and
// tells the page which part of itself to be. A page without it — a browser tab,
// or an older window — is the three-column panel it has always been.

export const HOST_VERSION = 1;

const SURFACES = new Set(["board", "orchestrator", "sessions"]);
const GLASSES = new Set(["glass", "vibrancy", "opaque"]);

export function readHost(win) {
  const host = win?.fleetdeckHost;
  if (!host || host.version !== HOST_VERSION) return null;
  if (!SURFACES.has(host.surface) || !GLASSES.has(host.glass)) return null;
  // stand only when the window says so: a CI stand (cmd/fleetdeck-window,
  // hostOnStand), never a person's window.
  if (host.stand !== true) return { surface: host.surface, glass: host.glass };
  // open: what a stand's frame is taken with open, without a press
  // (FLEETDECK_STAND_OPEN): the new card form, the fleet menu, or both. Anything
  // else asked opens nothing.
  const asked = typeof host.standOpen === "string" ? host.standOpen.split(",") : [];
  const open = asked.length > 0 && asked.every((name) => STAND_OPENS.has(name)) ? { open: asked } : {};
  return { surface: host.surface, glass: host.glass, stand: true, ...open };
}

const STAND_OPENS = new Set(["newcard", "fleetmenu"]);

export function callHost(win, name, payload) {
  const fn = win?.[name];
  if (typeof fn !== "function") return null;
  return Promise.resolve(fn(payload));
}

export function onHostMessage(win, handler) {
  const host = win?.fleetdeckHost;
  if (!host) return () => {};
  const previous = host.receive;
  host.receive = (message) => {
    if (message && typeof message.type === "string") handler(message);
  };
  return () => {
    host.receive = previous;
  };
}
