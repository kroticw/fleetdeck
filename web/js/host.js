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
  return { surface: host.surface, glass: host.glass };
}

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
