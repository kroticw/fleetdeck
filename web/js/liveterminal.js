// web/js/liveterminal.js
//
// A session's live terminal, drawn into an element the caller owns: the
// session panel's screen tab, and the orchestrator column. One implementation
// for both, because the two drawing the same session's terminal with two pieces
// of code is how this project has twice ended up with panes that disagree.
//
// It holds one socket to GET /api/sessions/{id}/pty, which holds one attach on
// the daemon for as long as the terminal is open and passes bytes both ways as
// they happen. A daemon on macOS lets several attachers read one session at
// once — the operator's own `claude attach` included — so holding the stream
// takes nothing from anyone; only a daemon on Windows evicts the previous
// attacher, and for that case the socket closes with its own code (see
// closeMessage). What a held stream does share with every other viewer is the
// terminal's size: an attach sets it for the whole session, so the socket asks
// for exactly the size drawn here, and later sizes follow the pane.
//
// It draws nothing but the terminal, and for a moment after its type changes
// size, the size over it (see showSize). What it has to say — a stream that
// ended, a key that could not be sent, that the terminal cannot type — goes to
// the caller through `report`, because each caller has its own lines to say it
// in.
//
// The document, fetch, WebSocket, ResizeObserver and the terminal and fit addon
// constructors are read from the global object, which is where the browser puts
// them and where a test can put its own. Only the clock is a parameter.

import { fetchTerminalToken } from "./api.js";
import { t } from "./i18n.js";
import { wikiLinkProvider } from "./terminallinks.js";
import { DEFAULT_FONT_SIZE, clampFontSize, fontStep, rememberFontSize, storedFontSize } from "./terminalfont.js";

// The close codes GET /api/sessions/{id}/pty ends a stream with
// (internal/server/pty.go), each turned into what the operator should read.
const STREAM_ENDINGS = {
  4000: "terminal_session_ended",
  4001: "terminal_kicked",
  4002: "terminal_stream_dropped",
  4003: "terminal_stream_unexplained",
  4403: "terminal_token_refused",
  4404: "terminal_no_session",
  4401: "terminal_key_refused",
  4503: "terminal_daemon_unavailable",
};

// closeMessage is what a stream's end says on screen. A kick carries the
// daemon's own words; any code this table does not name is a lost connection,
// with whatever reason came with it.
export function closeMessage(code, reason) {
  const known = STREAM_ENDINGS[code];
  if (code === 4001) {
    const words = String(reason ?? "").replace(/^kicked:\s*/, "");
    return words ? `${t(known)}: ${words}` : t(known);
  }
  if (known) return t(known);
  return reason ? `${t("terminal_connection_lost")}: ${reason}` : t("terminal_connection_lost");
}

// socketURL is the page's own origin with a WebSocket scheme. A page with no
// location (the tests) gets a fixed loopback one; the path is what matters.
function socketURL(path) {
  const loc = globalThis.location;
  if (!loc || !loc.host) return `ws://localhost${path}`;
  return `${loc.protocol === "https:" ? "wss:" : "ws:"}//${loc.host}${path}`;
}

const encoder = new TextEncoder();

// terminalTheme reads the panel's own colour tokens (app.css's :root custom
// properties, already resolved for whichever theme is current) and turns
// them into the object xterm.js's `theme` constructor option wants.
//
// Without this xterm falls back to its own default palette — a light grey on
// black regardless of what the rest of the page is doing — which is
// invisible as a defect for as long as the whole app is dark-only, and is
// exactly what a live run surfaced once light became a real, chosen theme:
// the terminal stayed a solid black rectangle inside an otherwise light
// panel. Read once, at the moment the terminal is built (a terminal is built
// afresh on every open, so this does not need to react to a theme switch
// mid-session — only a fresh open needs to start on the right one).
//
// getComputedStyle and document.documentElement are both real-browser-only:
// web/tests/fake-dom.js's FakeDocument has neither, on purpose — it is a
// wiring test double, not a layout engine. Returning undefined here rather
// than throwing lets those tests construct a terminal exactly as they did
// before this function existed; the real page always has both.
function terminalTheme() {
  if (typeof getComputedStyle !== "function" || !document.documentElement) return undefined;
  const style = getComputedStyle(document.documentElement);
  const token = (name) => style.getPropertyValue(name).trim();
  return {
    background: token("--surface"),
    foreground: token("--text"),
    cursor: token("--accent"),
    cursorAccent: token("--surface"),
    selectionBackground: token("--surface-hover"),
  };
}

// defaultTerminalFactory builds an xterm.js terminal in `host`.
//
// window.Terminal is what web/vendor/xterm.js assigns when index.html loads it
// with a plain <script> tag — the UMD bundle exports exactly one name. A script
// tag that 404s reports nothing to anyone, which is precisely the kind of
// silent failure this project keeps finding, so its absence is turned into a
// message on the screen instead of a terminal that stays mysteriously blank.
function defaultTerminalFactory(host, fontSize) {
  const Terminal = globalThis.Terminal;
  if (typeof Terminal !== "function") return null;
  const terminal = new Terminal({
    convertEol: true,
    fontSize,
    scrollback: 2000,
    theme: terminalTheme(),
    // A Claude Code session turns on mouse tracking (the stream carries
    // ?1000h/?1002h/?1003h/?1006h), so a plain drag is reported to the session
    // instead of selecting text. Option+drag is the way to select on macOS.
    macOptionClickForcesSelection: true,
  });
  terminal.open(host);
  return terminal;
}

// terminalFitter loads the fit addon into an opened terminal and returns a
// function that sizes the terminal to the element it was opened into, reporting
// whether it could — or null when the addon is not there at all.
//
// The measuring is web/vendor/addon-fit.js's, not this file's: the addon reads
// the cell size from xterm's own renderer, which is why it is pinned to the
// same xterm release (web/vendor/README.md). It is loaded the way xterm is,
// with a plain script tag that assigns FitAddon.FitAddon onto the global object.
//
// Two ways it cannot, and both must be said rather than swallowed. The script
// did not load; or the addon has nothing to measure — an element with no
// layout — in which case proposeDimensions answers nothing and fit() on its own
// would return without a word, leaving the terminal at whatever size it had in
// a pane that no longer matches it.
function terminalFitter(terminal) {
  const Fit = globalThis.FitAddon?.FitAddon;
  if (typeof Fit !== "function") return null;
  const addon = new Fit();
  terminal.loadAddon(addon);
  return () => {
    if (!addon.proposeDimensions()) return false;
    addon.fit();
    return true;
  };
}

// wheelLines is how far a wheel event asks to move, in terminal lines: the
// distance the browser would scroll any page by for the same event, measured
// in the terminal's own cells. deltaMode says what deltaY counts — pixels (0),
// lines (1) or pages (2). The browser has already put the device's own
// acceleration and a trackpad's inertia into deltaY, so nothing here needs to
// know which device sent it.
export function wheelLines(event, cellHeight, rows) {
  if (event.deltaMode === 1) return event.deltaY;
  if (event.deltaMode === 2) return event.deltaY * rows;
  return event.deltaY / cellHeight;
}

// followWheel makes the wheel over a terminal whose application tracks the
// mouse move that application's view by the distance the event asks for.
//
// Claude Code turns mouse tracking on, so xterm does not scroll anything when
// the wheel turns over its terminal: it sends the session a wheel report, and
// the session moves its own view one line per report (measured). xterm sends
// at most ONE report per wheel event, however far the event asks to go, and
// damps pixel steps under 50 px to a third. Measured on a live session, a
// 100 px step — a wheel notch — moved the view one line where a page moves
// seven, and a stream of small steps — a trackpad — moved it a third of the
// way. That is the sluggishness the operator felt.
//
// So the event is taken over: its distance is counted in lines, what is left
// of a line is kept for the next event, and xterm is handed one single-line
// wheel event per whole line, which it turns into one report each — in
// whatever encoding the application asked for, at the pointer, with the keys
// held. One event moves a screen at most.
//
// Left to xterm: a terminal whose application does not track the mouse (xterm
// scrolls its own scrollback, by the event's distance already), shift held
// (xterm reports nothing for it), and a terminal with nothing to measure.
function followWheel(terminal) {
  if (typeof terminal.attachCustomWheelEventHandler !== "function") return;
  const Wheel = globalThis.WheelEvent;
  if (typeof Wheel !== "function") return;
  // The single-line events this makes itself, which xterm hands back to this
  // handler on their way to becoming reports.
  const replayed = new WeakSet();
  // The part of a line asked for and not yet moved, signed.
  let owed = 0;
  terminal.attachCustomWheelEventHandler((event) => {
    if (replayed.has(event)) return true;
    if (terminal.modes?.mouseTrackingMode === "none" || event.shiftKey || !event.deltaY) return true;
    const screen = terminal.element?.querySelector?.(".xterm-screen");
    const cellHeight = screen ? screen.getBoundingClientRect().height / terminal.rows : 0;
    if (!(cellHeight > 0)) return true;
    const asked = wheelLines(event, cellHeight, terminal.rows);
    // A hand that turns back starts from nothing: what was owed the other
    // way is not a debt the new direction has to pay off first.
    if (Math.sign(asked) !== Math.sign(owed)) owed = 0;
    owed += asked;
    const whole = Math.trunc(owed);
    owed -= whole;
    const count = Math.min(Math.abs(whole), terminal.rows);
    for (let i = 0; i < count; i += 1) {
      const line = new Wheel("wheel", {
        deltaY: Math.sign(whole),
        deltaMode: 1,
        clientX: event.clientX,
        clientY: event.clientY,
        ctrlKey: event.ctrlKey,
        altKey: event.altKey,
        metaKey: event.metaKey,
        bubbles: true,
        cancelable: true,
      });
      replayed.add(line);
      event.target.dispatchEvent(line);
    }
    return false;
  });
}

// How long the pane must stay still before the terminal follows it and the
// session is told its new size. A drag of the column's edge moves the pane on
// every pointer event — one per display frame, 8 to 17 ms apart at 120 or
// 60 Hz — and every resize reshapes the session for everyone watching it and
// makes it repaint. 150 ms is nine frames even at 60 Hz: a hand still moving
// never goes that long between events, and a person who has let go waits less
// than a blink for the terminal to follow.
const PANE_SETTLE_MS = 150;

// How long the size stays over the terminal after its type last changed:
// long enough to read a line of a dozen characters, short enough to be gone
// before the next thing on screen needs reading.
const SIZE_NOTE_MS = 2000;

// How long a terminal that reconnects waits before each attempt, by attempt;
// the last value repeats. The first is short because the common case is the
// panel restarting under the page — replaced by a newer build, or brought back
// by its supervisor — which is over in a moment. The rest back off so that a
// panel that is down for good costs one request every ten seconds rather than
// a stream of them. A connection that gets through starts the count over.
const RECONNECT_DELAYS_MS = [1000, 2000, 5000, 10000];

// Endings a reconnect cannot fix, so a terminal that reconnects does not try
// after them: the session ended (4000) or is not there (4404) — its caller
// opens it again if it comes back — or another attacher took the terminal over
// (4001; on Windows that is the operator's own terminal, and taking it back
// would evict them in a loop).
//
// A refused token (4403) is not among them. The token lives exactly as long as
// the panel's process, and the one moment a page presents a token the panel
// does not know is a restart between reading the token and opening the socket —
// the very case reconnecting is for. Every attempt reads the token afresh, so
// the next one presents the new process's.
const FINAL_ENDINGS = new Set([4000, 4001, 4404]);

// What an ending that is being retried says. The words closeMessage gives the
// screen tab end in advice to reopen the tab, which a terminal that reconnects
// by itself must not give; only a daemon that is away and a token that was
// refused say something more useful than that the connection went.
const RETRIED_ENDINGS = { 4403: "terminal_token_stale", 4503: "terminal_daemon_unavailable" };

// createLiveTerminal draws the live terminal of session `short` into `host`.
// Nothing happens until open(); stop() leaves no socket, timer, observer or
// terminal behind.
//
// With `reconnect`, a stream that ends for a reason that can pass — a lost
// connection, a panel restarting, a daemon briefly away — is opened again by
// itself after RECONNECT_DELAYS_MS, into the same terminal, which keeps what it
// showed meanwhile. Without it, which is the default, an ended stream stays
// ended and the caller decides what reopens it.
//
// `report` is how it speaks, every member optional:
//   streamError(message) — the stream's own state: it ended and why, a token
//                          that could not be had, a library that did not load;
//                          "" once the stream is up again.
//   actionError(message) — what a key or a bridge refusal came to; "" once a
//                          key goes through.
//   standing()           — readOnly or unfitted may have changed; read them.
//   ready()              — the bridge has attached.
//
// `links`, when given, makes the wiki links a session prints into something to
// click (web/js/terminallinks.js): { resolve(name) → card path or null,
// open(path) }. Without it, which is the default, the terminal links nothing.
//
// `fontKey` is where the size of the type is remembered (FONT_KEYS in
// web/js/terminalfont.js, one per place a terminal is drawn). Cmd with = / + /
// - / 0 inside the terminal changes it. A bigger type in the same pane is fewer
// columns, and the columns are the session's, shared with everyone watching it
// — the operator's own Terminal.app included. A step therefore goes the way a
// pane that changed size goes: the terminal is refitted and the session is
// told once the keys stop, and the size the session now has is shown over the
// terminal, so it is never changed without a word. Without a key, which is the
// default, the keys still work and nothing is remembered.
export function createLiveTerminal(host, short, { timers = globalThis, report = {}, reconnect = false, links = null, fontKey = null } = {}) {
  const say = {
    streamError: report.streamError ?? (() => {}),
    actionError: report.actionError ?? (() => {}),
    standing: report.standing ?? (() => {}),
    ready: report.ready ?? (() => {}),
  };

  let terminal = null;
  // The stream, and whether it may type. A socket that is not this one —
  // closed on the way out, answering late — is ignored when it speaks.
  let socket = null;
  let writable = false;
  let typing = null;
  // Which opening is current. Opening reads the token first, which takes a
  // round trip; closing, or opening again, inside it moves this on, and the
  // opening that was waiting finds it has been superseded and opens nothing.
  let opening = 0;
  // Following the pane: the fitter from terminalFitter, the observer watching
  // the terminal's element, the settle timer, and the size the session was last
  // given — at attach, then by each resize message — so a pane that settles
  // where it started sends nothing.
  let refit = null;
  let paneWatcher = null;
  let settle = null;
  let sessionSize = null;
  // The size over the terminal (see showSize): its element, the timer that
  // hides it, and whether the next time the terminal follows its pane is one
  // a change of type asked for, which is the time to show it — and whether the
  // last key of that change went past the end of the range.
  let sizeNote = null;
  let sizeNoteTimer = null;
  let sizeNoteDue = false;
  let sizeNoteAtLimit = false;
  // What the terminal says about itself, for as long as it holds a stream and
  // a terminal: that the stream cannot type, and that the terminal is not the
  // size of its pane.
  let readOnly = false;
  let unfitted = false;
  // Reconnecting: the armed attempt, and how many have failed since the last
  // connection that got through.
  let retry = null;
  let failures = 0;

  // tryAgain says why the stream is down and that it is being tried again,
  // and arms the attempt.
  const tryAgain = (why) => {
    const delay = RECONNECT_DELAYS_MS[Math.min(failures, RECONNECT_DELAYS_MS.length - 1)];
    failures += 1;
    say.streamError(`${why} — ${t("terminal_reconnecting")}`);
    retry = timers.setTimeout(() => {
      retry = null;
      void openStream();
    }, delay);
  };

  const disposeTerminal = () => {
    // xterm holds a renderer, listeners and a resize observer. Dropping the
    // reference without disposing leaks all three for the life of the page.
    if (terminal && typeof terminal.dispose === "function") terminal.dispose();
    terminal = null;
    refit = null;
    if (paneWatcher) paneWatcher.disconnect();
    paneWatcher = null;
    if (settle !== null) timers.clearTimeout(settle);
    settle = null;
    if (sizeNoteTimer !== null) timers.clearTimeout(sizeNoteTimer);
    sizeNoteTimer = null;
    if (sizeNote) sizeNote.remove();
    sizeNote = null;
    sizeNoteDue = false;
    sizeNoteAtLimit = false;
  };

  // closeStream ends this terminal's own socket. Its handlers go first: a
  // browser reports a close the page asked for exactly like one it did not, and
  // a caller putting its terminal away is not a lost connection.
  const closeStream = () => {
    opening += 1;
    if (retry !== null) timers.clearTimeout(retry);
    retry = null;
    if (typing) typing.dispose();
    typing = null;
    sessionSize = null;
    if (!socket) return;
    const ws = socket;
    socket = null;
    writable = false;
    ws.onopen = null;
    ws.onmessage = null;
    ws.onclose = null;
    try {
      ws.close(1000, "panel closed");
    } catch {
      // Already closed or never opened; either way it is gone.
    }
  };

  const ensureTerminal = () => {
    if (terminal) return terminal;
    const made = defaultTerminalFactory(host, storedFontSize(fontKey));
    if (!made) {
      say.streamError(t("terminal_missing"));
      return null;
    }
    terminal = made;
    followWheel(made);
    followFontKeys(made);
    if (links && typeof made.registerLinkProvider === "function") made.registerLinkProvider(wikiLinkProvider(made, links));
    refit = terminalFitter(made);
    // Before the socket exists, because the socket asks for this size.
    unfitted = !(refit && refit());
    say.standing();
    watchPane();
    return terminal;
  };

  // tellSession sends the terminal's size to the session when it differs from
  // the size the session was last given, through an open socket. One that is
  // still connecting cannot carry it, so ready calls this again. One that is
  // open but not yet attached can: the bridge reads the token first and the
  // rest only once it has attached, so the resize lands after the attach — the
  // order the session needs.
  const tellSession = () => {
    const open = globalThis.WebSocket?.OPEN ?? 1;
    if (!terminal || !socket || socket.readyState !== open) return;
    const cols = terminal.cols;
    const rows = terminal.rows;
    if (sessionSize && sessionSize[0] === cols && sessionSize[1] === rows) return;
    socket.send(JSON.stringify({ type: "resize", cols, rows }));
    sessionSize = [cols, rows];
  };

  // followPane runs once the pane has stopped moving: refit, say whether that
  // worked, and tell the session.
  const followPane = () => {
    settle = null;
    if (!terminal) return;
    unfitted = !(refit && refit());
    say.standing();
    if (!unfitted) tellSession();
    if (sizeNoteDue) {
      sizeNoteDue = false;
      showSize(sizeNoteAtLimit);
    }
  };

  // settleThenFollow restarts the settle timer, so a run of changes becomes
  // one resize when it stops, however many there were.
  const settleThenFollow = () => {
    if (settle !== null) timers.clearTimeout(settle);
    settle = timers.setTimeout(followPane, PANE_SETTLE_MS);
  };

  // watchPane follows the terminal's element for as long as the terminal
  // lives: a drag becomes one resize when it stops, however many pointer moves
  // it took.
  const watchPane = () => {
    const Observer = globalThis.ResizeObserver;
    if (typeof Observer !== "function") return;
    paneWatcher = new Observer(settleThenFollow);
    paneWatcher.observe(host);
  };

  // showSize puts the type's size and the session's over the terminal for
  // SIZE_NOTE_MS, the way Terminal.app shows a window's size while it is
  // resized. Over the terminal and not in a row beside it: a row that comes
  // and goes would make the pane shorter and taller again, and every change of
  // the pane is a resize of the session. `atLimit` says the step asked for
  // went past the end of the range, which is why nothing changed.
  const showSize = (atLimit) => {
    if (!terminal) return;
    if (!sizeNote) {
      sizeNote = globalThis.document.createElement("div");
      sizeNote.className = "term-size";
      host.appendChild(sizeNote);
    }
    const type = `${terminal.options.fontSize} px${atLimit ? ` (${t("terminal_font_limit")})` : ""}`;
    sizeNote.textContent = `${type} · ${t("terminal_font_session")} ${terminal.cols} × ${terminal.rows}`;
    sizeNote.hidden = false;
    if (sizeNoteTimer !== null) timers.clearTimeout(sizeNoteTimer);
    sizeNoteTimer = timers.setTimeout(() => {
      sizeNoteTimer = null;
      if (sizeNote) sizeNote.hidden = true;
    }, SIZE_NOTE_MS);
  };

  // followFontKeys makes Cmd with = / + / - / 0 change the type (see fontStep).
  // The key is taken from xterm, which would send the session nothing for it
  // anyway, and from the page, which in a browser would zoom everything on it.
  // The new size is on screen at once; the terminal is refitted and the
  // session told once the keys stop, as for a pane that changed size.
  const followFontKeys = (made) => {
    if (typeof made.attachCustomKeyEventHandler !== "function") return;
    made.attachCustomKeyEventHandler((event) => {
      const step = fontStep(event);
      if (step === null) return true;
      event.preventDefault();
      const size = made.options.fontSize;
      const next = step === 0 ? DEFAULT_FONT_SIZE : clampFontSize(size + step);
      if (next === size) {
        // Nothing changes. While earlier steps still wait to be fitted, the
        // columns on screen are not the ones they will leave, so the note
        // waits for them and says the end when it goes up.
        if (sizeNoteDue) sizeNoteAtLimit = step !== 0;
        else showSize(step !== 0);
        return false;
      }
      made.options.fontSize = next;
      rememberFontSize(fontKey, next);
      sizeNoteDue = true;
      sizeNoteAtLimit = false;
      settleThenFollow();
      return false;
    });
  };

  // sendBytes puts bytes into the session through the stream — typed keys and
  // a caller's key buttons alike. Refused on screen when there is nothing to
  // send through, never dropped in silence.
  const sendBytes = (bytes) => {
    const open = globalThis.WebSocket?.OPEN ?? 1;
    if (!socket || socket.readyState !== open) {
      say.actionError(t("terminal_not_connected"));
      return;
    }
    if (!writable) {
      say.actionError(t("terminal_read_only"));
      return;
    }
    socket.send(bytes);
    say.actionError("");
  };

  const onControl = (text) => {
    let msg;
    try {
      msg = JSON.parse(text);
    } catch {
      return;
    }
    if (msg?.type === "ready") {
      writable = msg.writable === true;
      readOnly = !writable;
      failures = 0;
      say.streamError("");
      say.standing();
      say.ready();
      // The pane may have moved while the bridge was attaching.
      tellSession();
    } else if (msg?.type === "error") {
      say.actionError(String(msg.error ?? ""));
    }
  };

  // openStream is one socket for as long as the caller keeps the terminal open.
  // It is reopened by itself only for a caller that asked to reconnect.
  //
  // The socket must prove the panel's terminal token before the bridge attaches
  // to anything (internal/server/pty.go), so the token is read first, fresh for
  // this socket (see fetchTerminalToken), and sent as the first message the
  // moment the socket opens.
  const openStream = async () => {
    const term = ensureTerminal();
    if (!term) return; // the library is missing; ensureTerminal already said so
    const Socket = globalThis.WebSocket;
    if (typeof Socket !== "function") {
      say.streamError(t("terminal_missing"));
      return;
    }
    const mine = ++opening;
    let token;
    try {
      token = await fetchTerminalToken();
    } catch (err) {
      if (mine !== opening) return;
      const why = `${t("terminal_token_unavailable")}: ${err.message}`;
      // A panel that is restarting answers nothing for a moment, and the token
      // is the first thing asked of it.
      if (reconnect) tryAgain(why);
      else say.streamError(why);
      return;
    }
    if (mine !== opening) return;
    // The terminal's own size — fitted to the pane by ensureTerminal — because
    // attaching at it sets the size of the session for everyone watching it.
    const cols = term.cols || 80;
    const rows = term.rows || 24;
    const ws = new Socket(socketURL(`/api/sessions/${encodeURIComponent(short)}/pty?cols=${cols}&rows=${rows}`));
    ws.binaryType = "arraybuffer";
    socket = ws;
    sessionSize = [cols, rows];
    ws.onopen = () => {
      if (socket !== ws) return;
      ws.send(JSON.stringify({ type: "auth", token }));
    };
    ws.onmessage = (event) => {
      if (socket !== ws) return;
      if (typeof event.data === "string") {
        onControl(event.data);
        return;
      }
      // Appended, never redrawn: what arrived is the session's own output, in order.
      term.write(new Uint8Array(event.data));
    };
    ws.onclose = (event) => {
      if (socket !== ws) return;
      socket = null;
      writable = false;
      readOnly = false;
      sessionSize = null;
      // The terminal stays as it was: the last thing the session said is often
      // exactly what the operator needs while reading why it stopped.
      if (reconnect && !FINAL_ENDINGS.has(event.code)) tryAgain(t(RETRIED_ENDINGS[event.code] ?? "terminal_link_lost"));
      else say.streamError(closeMessage(event.code, event.reason));
      say.standing();
    };
    // One wiring at a time: an opening after a reconnect replaces the last one
    // rather than adding to it, or every key would be typed once per attempt.
    if (typing) typing.dispose();
    typing = term.onData((data) => sendBytes(encoder.encode(data)));
  };

  return {
    open() {
      void openStream();
    },
    stop() {
      closeStream();
      disposeTerminal();
    },
    // Text typed into the session through the stream, as a key button does.
    type(text) {
      sendBytes(encoder.encode(text));
    },
    get readOnly() {
      return readOnly;
    },
    get unfitted() {
      return unfitted;
    },
  };
}
