// One snapshot arrives per second over the socket. Every module reads from
// here; no module fetches state on its own.
//
// A subscriber is called once immediately, and again on every change after
// that. The snapshot argument passed to it may be `null` — before the first
// successful connection, while the daemon is still starting, or after a
// dropped or unparseable frame — and every module must render that state
// rather than assume a snapshot is already there. `get()` already returns
// `null` in the same situations, so callers had to handle it either way.

import { fleetFromSearch, withFleet } from "./fleet.js";

const INITIAL_BACKOFF_MS = 1000;
const MAX_BACKOFF_MS = 5000;

const listeners = new Set();
let snapshot = null;
let connected = false;
let socket = null;
let started = false;
let backoff = INITIAL_BACKOFF_MS;

export function subscribe(fn) {
  listeners.add(fn);
  fn(snapshot, connected);
  return () => listeners.delete(fn);
}

export function get() {
  return snapshot;
}

export function isConnected() {
  return connected;
}

function emit() {
  for (const fn of listeners) fn(snapshot, connected);
}

function open() {
  // wss:// from an https:// page, ws:// from an http:// one: a browser
  // refuses a plain ws:// connection from a secure page as mixed content, and
  // that refusal feeds onerror straight into the reconnect loop with no
  // visible cause.
  const scheme = location.protocol === "https:" ? "wss" : "ws";
  // The snapshot is of the fleet this tab's address names (see fleet.js).
  const url = `${scheme}://${location.host}${withFleet("/ws", fleetFromSearch(location.search))}`;

  // Captured per attempt so a handler always knows which socket it belongs
  // to. socket itself changes as soon as this attempt's onclose fires and
  // schedules the next one.
  const current = new WebSocket(url);
  socket = current;

  current.onmessage = (ev) => {
    let parsed;
    try {
      parsed = JSON.parse(ev.data);
    } catch (err) {
      console.error("snapshot is not JSON", err);
      // A frame that does not parse is the same visible failure as a dropped
      // socket: the page must never keep claiming to be connected on top of
      // a frame it could not read.
      connected = false;
      emit();
      return;
    }
    snapshot = parsed;
    connected = true;
    backoff = INITIAL_BACKOFF_MS; // a good frame means the backoff has done its job
    emit();
  };

  current.onclose = () => {
    // A dropped socket is a visible state, not a silent one: a stale board
    // that looks live is worse than an empty one that says it is stale.
    connected = false;
    emit();

    // Detach so a stale event from this socket — one the spec does not
    // strictly rule out on every platform — can never reach the store once a
    // new attempt has taken over.
    current.onmessage = null;
    current.onclose = null;
    current.onerror = null;
    if (socket === current) socket = null;

    setTimeout(open, backoff);
    backoff = Math.min(backoff * 2, MAX_BACKOFF_MS);
  };

  // onerror fires on whatever socket it is attached to — current, not
  // whatever the module-level `socket` binding happens to hold by the time it
  // runs — so closing `current` here always closes the socket that actually
  // errored.
  current.onerror = () => current.close();
}

// A page the browser keeps in its back/forward cache keeps its sockets open:
// measured on a stand, a switch to another fleet left the page of the fleet
// left alive in Chrome's cache, its terminals still attached to that fleet's
// sessions. The page therefore lets go of its socket when it is hidden, with
// no reconnect — the terminals do the same (liveterminal.js) — and a page the
// cache brings back reloads, so it is opened afresh rather than resumed on
// sockets it no longer has.
function releaseWhenHidden() {
  globalThis.addEventListener?.("pagehide", () => {
    const current = socket;
    socket = null;
    if (!current) return;
    current.onmessage = null;
    current.onclose = null;
    current.onerror = null;
    current.close();
  });
  globalThis.addEventListener?.("pageshow", (event) => {
    if (event.persisted) location.reload();
  });
}

export function connect() {
  // Re-entrant by construction otherwise: connect() is exported and every
  // module imports it. A second call must be a no-op, or two independent
  // sockets end up pushing into the same store.
  if (started) return;
  started = true;
  releaseWhenHidden();
  open();
}
