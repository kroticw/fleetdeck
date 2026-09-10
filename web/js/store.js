// One snapshot arrives per second over the socket. Every module reads from
// here; no module fetches state on its own.

const listeners = new Set();
let snapshot = null;
let connected = false;

export function subscribe(fn) {
  listeners.add(fn);
  if (snapshot) fn(snapshot, connected);
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

export function connect() {
  const url = `ws://${location.host}/ws`;
  let socket;

  const open = () => {
    socket = new WebSocket(url);
    socket.onmessage = (ev) => {
      try {
        snapshot = JSON.parse(ev.data);
      } catch (err) {
        console.error("snapshot is not JSON", err);
        return;
      }
      connected = true;
      emit();
    };
    socket.onclose = () => {
      // A dropped socket is a visible state, not a silent one: a stale board
      // that looks live is worse than an empty one that says it is stale.
      connected = false;
      emit();
      setTimeout(open, 1000);
    };
    socket.onerror = () => socket.close();
  };

  open();
}
