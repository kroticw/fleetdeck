// Test doubles for the live terminal (web/js/liveterminal.js) and what it reads
// from the global object: a clock, fetch answers, xterm, the fit addon, the
// browser's ResizeObserver and WebSocket. Shared by every test file that draws
// a terminal, so the panel and the orchestrator column are tested against the
// same stand-ins rather than two copies that can drift apart.

import { settle } from "./fake-dom.js";

// A clock the test advances by hand.
//
// It models setInterval as well as setTimeout, which is not decoration: the
// defect this file exists to pin is a re-armed interval, and a clock that could
// only express timeouts would make that defect unrepresentable and the test that
// "catches" it meaningless.
export function fakeTimers() {
  let nextId = 1;
  const pending = new Map();
  const cancel = (id) => pending.delete(id);
  return {
    setTimeout(fn) {
      const id = nextId++;
      pending.set(id, { fn, repeating: false });
      return id;
    },
    setInterval(fn) {
      const id = nextId++;
      pending.set(id, { fn, repeating: true });
      return id;
    },
    clearTimeout: cancel,
    clearInterval: cancel,
    count() {
      return pending.size;
    },
    // Fire every armed timer once. A timeout is removed before it runs; an
    // interval stays, exactly as a real clock behaves.
    async tick() {
      for (const [id, timer] of [...pending.entries()]) {
        if (!timer.repeating) pending.delete(id);
        timer.fn();
      }
      await settle();
    },
  };
}

export function answer({ status = 200, body, statusText = "" } = {}) {
  return {
    status,
    ok: status >= 200 && status < 300,
    statusText,
    async json() {
      if (body === undefined) throw new SyntaxError("Unexpected end of JSON input");
      return body;
    },
  };
}

// The terminal the panel finds on the global object, which is where
// web/vendor/xterm.js puts the real one.
export function installTerminal({ cols = 80, rows = 24 } = {}) {
  const made = [];
  globalThis.Terminal = class {
    constructor(options) {
      this.options = options;
      this.writes = [];
      this.resets = 0;
      this.disposed = 0;
      this.host = null;
      // xterm's own default geometry, which is what the panel's terminal is built
      // with while it has no fit to a container.
      this.cols = cols;
      this.rows = rows;
      this.dataListeners = [];
      made.push(this);
    }
    open(host) {
      this.host = host;
    }
    // xterm hands the terminal to an addon's activate() when it is loaded.
    loadAddon(addon) {
      addon.activate(this);
    }
    resize(cols, rows) {
      this.cols = cols;
      this.rows = rows;
    }
    onData(fn) {
      this.dataListeners.push(fn);
      return { dispose: () => (this.dataListeners = this.dataListeners.filter((f) => f !== fn)) };
    }
    // What xterm does when a person types into it: hands the characters to every
    // onData listener.
    type(data) {
      for (const fn of this.dataListeners) fn(data);
    }
    reset() {
      this.resets += 1;
    }
    write(data) {
      this.writes.push(data);
    }
    dispose() {
      this.disposed += 1;
    }
  };
  return made;
}

// The fit addon the panel finds on the global object, which is where
// web/vendor/addon-fit.js puts the real one. `pane` is the size the real addon
// would measure the terminal's element at: "own" answers with the terminal's
// current size, so every test that is not about fitting sees no change; null is
// a pane with nothing to measure yet, which the real addon answers with nothing.
export function installFit(pane = "own") {
  const made = [];
  globalThis.FitAddon = {
    FitAddon: class {
      constructor() {
        this.terminal = null;
        made.push(this);
      }
      activate(terminal) {
        this.terminal = terminal;
      }
      dispose() {}
      proposeDimensions() {
        // Like the real one: a terminal not yet opened into an element has no
        // size to propose.
        // A pane with `hidden` set is one that stopped having a layout — a
        // folded column — after the terminal was opened into it.
        if (!this.terminal?.host || pane === null || pane?.hidden) return undefined;
        return pane === "own" ? { cols: this.terminal.cols, rows: this.terminal.rows } : { ...pane };
      }
      fit() {
        const dims = this.proposeDimensions();
        if (dims) this.terminal.resize(dims.cols, dims.rows);
      }
    },
  };
  return made;
}

// The browser's ResizeObserver, driven by hand: resize() is the pane changing
// size, and delivers what a real observer would — one callback per change.
export function installObserver() {
  const made = [];
  globalThis.ResizeObserver = class {
    constructor(callback) {
      this.callback = callback;
      this.targets = [];
      this.disconnected = false;
      made.push(this);
    }
    observe(target) {
      this.targets.push(target);
    }
    disconnect() {
      this.disconnected = true;
    }
    resize() {
      if (!this.disconnected) this.callback(this.targets.map((target) => ({ target })));
    }
  };
  return made;
}

// The socket the screen tab opens for its live terminal: a browser's WebSocket
// reduced to what the panel touches, with the server's side driven by hand.
export function installSocket() {
  const opened = [];
  globalThis.WebSocket = class {
    static CONNECTING = 0;
    static OPEN = 1;
    static CLOSING = 2;
    static CLOSED = 3;
    constructor(url) {
      this.url = url;
      this.readyState = 0;
      this.binaryType = "blob";
      this.sent = [];
      this.closedWith = null;
      this.onopen = null;
      this.onmessage = null;
      this.onclose = null;
      opened.push(this);
    }
    send(data) {
      this.sent.push(data);
    }
    close(code, reason) {
      if (this.readyState === 3) return;
      this.closedWith = { code, reason };
      this.readyState = 3;
    }
    // The server's side.
    serverOpen() {
      this.readyState = 1;
      this.onopen?.({});
    }
    serverSend(data) {
      this.onmessage?.({ data });
    }
    serverClose(code, reason = "") {
      this.readyState = 3;
      this.onclose?.({ code, reason });
    }
  };
  return opened;
}

// What the bridge says first on a socket it has attached.
export function ready(socket, writable = true) {
  socket.serverOpen();
  socket.serverSend(JSON.stringify({ type: "ready", writable }));
}

// A frame the panel sent, as text. Keystrokes go out as bytes.
export const asText = (data) => new TextDecoder().decode(data instanceof ArrayBuffer ? new Uint8Array(data) : data);

// Bytes as the server sends them: an ArrayBuffer, because the panel asks for one.
export const frame = (s) => new TextEncoder().encode(s).buffer;
