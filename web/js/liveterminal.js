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
//
// Two more answers are also nothing to measure, read from the addon's source
// rather than measured in a browser. An element with display: none has "auto"
// for its computed width and height, which the addon turns into NaN columns and
// rows; fit() then changes nothing. An element with no room at all gets the
// addon's floor, 2 columns by 1 row. Neither is a size to give a session that
// others are watching.
function terminalFitter(terminal) {
  const Fit = globalThis.FitAddon?.FitAddon;
  if (typeof Fit !== "function") return null;
  const addon = new Fit();
  terminal.loadAddon(addon);
  return () => {
    const proposed = addon.proposeDimensions();
    if (!proposed || !(proposed.cols > 2 && proposed.rows > 1)) return false;
    addon.fit();
    return true;
  };
}

// The widest a session can be: the bridge refuses a geometry past it
// (maxTerminalCols in internal/server/pty.go). A column past it is not a size
// someone gave the session but an application finding the corner of its
// terminal, `ESC[999;999H` followed by a cursor report.
const MAX_SESSION_COLS = 500;

// The tallest a session can be (maxTerminalRows in internal/server/pty.go). A
// cursor moved down by more is an application finding the bottom of its
// terminal, not a taller session.
const MAX_SESSION_ROWS = 200;

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

// Taking the session's size back from another attacher that made it bigger
// (see followSize and takeSizeBack). The first time waits RECLAIM_WAIT_MS, so
// that the rest of the screen, which arrives in the same moment, is read first.
// After each time it waits RECLAIM_EVERY_MS before it can happen again.
//
// RECLAIM_LIMIT take-backs within RECLAIM_WINDOW_MS pause it: for the first of
// RECLAIM_PAUSES_MS, and for each next one the next time it happens again, the
// last repeating. A session made bigger during a pause is taken back once the
// pause is over. A pane that changes size, comes back into view, or a stream
// that reconnects starts over, and so do RECLAIM_CALM_MS without a take-back.
// Whatever keeps making the session bigger again that often is not something
// taking it back fixes, so this bounds any loop nobody foresaw, while a screen
// broken in a pause is not left broken for good: the orchestrator's column
// seldom changes size.
//
// A take-back that comes while xterm is still reading what arrived waits
// READ_WAIT_MS at a time for it to finish, because the session's size is read
// from the whole screen (see takeSizeBack).
const RECLAIM_WAIT_MS = 150;
const RECLAIM_EVERY_MS = 1000;
const RECLAIM_WINDOW_MS = 10000;
const RECLAIM_LIMIT = 3;
const RECLAIM_PAUSES_MS = [10000, 30000, 60000];
const RECLAIM_CALM_MS = 120000;
const READ_WAIT_MS = 50;

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
//   fontSize(size)       — the size of the type: when the terminal is built,
//                          at every step, and null once it is gone. What the
//                          caller's font buttons are painted from.
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
export function createLiveTerminal(host, short, { timers = globalThis, report = {}, reconnect = false, links = null, fontKey = null, page = globalThis } = {}) {
  const say = {
    streamError: report.streamError ?? (() => {}),
    actionError: report.actionError ?? (() => {}),
    standing: report.standing ?? (() => {}),
    ready: report.ready ?? (() => {}),
    fontSize: report.fontSize ?? (() => {}),
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
  // the terminal's element, the settle timer, and the terminal's size the last
  // time it told the session anything — at attach, then by each resize it sent —
  // so a pane that settles where it started sends nothing. After a take-back
  // the session can be smaller than that (see takeSizeBack), and that is not a
  // reason to send it again.
  let refit = null;
  let paneWatcher = null;
  let settle = null;
  let paneSize = null;
  // Taking the size back (see takeSizeBack): the armed timer; whether the stream
  // showed a session wider, and taller, than this terminal since the session was
  // last given a size; the take-backs still inside RECLAIM_WINDOW_MS, by the
  // timers that let them go; the pause the terminal is in, if any, and which of
  // RECLAIM_PAUSES_MS the next one is; and the timer that starts that over.
  let reclaim = null;
  let wider = false;
  let taller = false;
  const recentTakes = new Set();
  let pause = null;
  let pauseStep = 0;
  let calm = null;
  // The pieces of the stream handed to xterm, the last piece xterm has finished
  // reading, and the last piece that had arrived when the session was last given
  // a size (see noticeBigger).
  let received = 0;
  let parsed = 0;
  let staleThrough = 0;
  // The row the stream means the cursor to be on, whatever this terminal clamps
  // or wraps, and the lowest such row since the screen was last cleared or the
  // session last given a size — null while the stream has placed the cursor on
  // no row since (see followSize and takeSizeBack).
  let streamRow = 0;
  let streamLowest = null;
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
    if (terminal) say.fontSize(null);
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
    dropReclaim();
    if (typing) typing.dispose();
    typing = null;
    paneSize = null;
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
    say.fontSize(made.options.fontSize);
    followWheel(made);
    followFontKeys(made);
    if (links && typeof made.registerLinkProvider === "function") made.registerLinkProvider(wikiLinkProvider(made, links));
    followSize(made);
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
    if (!terminal || !socket || socket.readyState !== open) return false;
    const cols = terminal.cols;
    const rows = terminal.rows;
    if (paneSize && paneSize[0] === cols && paneSize[1] === rows) return false;
    sendSize(cols, rows);
    paneSize = [cols, rows];
    // A pane of a new size starts over: a terminal that stopped taking the size
    // back takes it back again.
    standUp();
    return true;
  };

  // sendSize puts a size into the session through the open socket. Any size
  // sent makes the daemon repaint this terminal at that size, so what the stream
  // showed of the size before it is forgotten: a bigger session noticed, the
  // screens still on their way (see noticeBigger), and the lowest row the stream
  // reached.
  const sendSize = (cols, rows) => {
    socket.send(JSON.stringify({ type: "resize", cols, rows }));
    forgetSize();
  };

  const forgetSize = () => {
    wider = false;
    taller = false;
    staleThrough = received;
    streamLowest = null;
  };

  const forgetTakes = () => {
    for (const take of recentTakes) timers.clearTimeout(take);
    recentTakes.clear();
  };

  // standUp starts the take-backs over: no pause, the first pause next.
  const standUp = () => {
    forgetTakes();
    if (pause !== null) timers.clearTimeout(pause);
    pause = null;
    if (calm !== null) timers.clearTimeout(calm);
    calm = null;
    pauseStep = 0;
  };

  // pauseTakes stops the take-backs for the next of RECLAIM_PAUSES_MS, and
  // takes the size back once when it is over if the session was made bigger in
  // the meantime.
  const pauseTakes = () => {
    forgetTakes();
    const ms = RECLAIM_PAUSES_MS[Math.min(pauseStep, RECLAIM_PAUSES_MS.length - 1)];
    pauseStep += 1;
    pause = timers.setTimeout(() => {
      pause = null;
      if ((wider || taller) && reclaim === null) reclaim = timers.setTimeout(takeSizeBack, RECLAIM_WAIT_MS);
    }, ms);
  };

  // widestRow is how far across the screen anything is drawn, in cells.
  const widestRow = () => {
    const buffer = terminal.buffer?.active;
    if (!buffer) return 0;
    let widest = 0;
    // The screen the session drew, not what the viewport shows: scrolled back,
    // the viewport shows history, which can be wider than the session is now.
    for (let y = 0; y < terminal.rows; y += 1) {
      const line = buffer.getLine(buffer.baseY + y);
      if (!line) continue;
      for (let x = terminal.cols - 1; x >= widest; x -= 1) {
        const cell = line.getCell(x);
        const chars = cell?.getChars() ?? "";
        if (chars !== "" && chars !== " ") {
          widest = x + Math.max(1, cell.getWidth());
          break;
        }
      }
    }
    return widest;
  };

  // takeSizeBack gives the session a size this terminal can show after the
  // stream showed another attacher made it bigger (see followSize). The daemon
  // repaints every attacher for the size a session has, so the repaint that
  // follows is drawn to fit this terminal. Measured against CLI 2.1.269, the
  // screen then matched a fresh attach's exactly.
  //
  // It sends not this terminal's size but the smaller of it and the session's in
  // each dimension. Where the session is bigger, that is this terminal's own. Where
  // it is not, sending this terminal's own would make the session bigger there,
  // and break the screen of the attacher that is smaller there — which then takes
  // the size back, and the two take turns for ever (a 76 × 60 terminal beside a
  // 120 × 40 one). The session's size there is read from what it drew:
  //
  //   - columns: how far across the screen anything is drawn (widestRow);
  //   - rows: the lowest row the stream placed the cursor on since the screen was
  //     cleared (streamLowest), not the screen's last drawn row, because a screen
  //     wider than this terminal wraps and reaches rows the session does not have
  //     (measured: 29 rows drawn for a 24-row session).
  //
  // Measured on a disposable session, both matched the session's size on every
  // complete screen, at rest and while it streamed output: Claude Code draws
  // rules across its whole width and its status on the last row. A screen that
  // draws neither gives less than the session's size, and the session stays
  // smaller than it needs to be until a size is set again — a pane of this
  // terminal that changes size, another attacher attaching, resizing or leaving,
  // or this stream reconnecting. With no estimate at all, the terminal's own
  // size stands for it.
  //
  // So a take-back never makes the session bigger in either dimension and makes
  // it smaller in the one it was bigger in. Every take-back by every attacher that
  // does this shrinks the session, which a session cannot do for ever: between
  // sizes set by anything else the take-backs stop, and they stop exactly when
  // the session fits every such attacher.
  //
  // A terminal that cannot be measured, folded or in a hidden tab, takes
  // nothing: it keeps the columns it last had, which are no longer its pane's.
  // The bigger session stays noticed, and followPane takes the size back once the
  // pane can be measured again. A pane still moving is followPane's too: taking
  // the size back in the middle of a drag would send a size the pane only passed
  // through, so the take-back waits for the pane to settle.
  const takeSizeBack = () => {
    reclaim = null;
    if (!(wider || taller) || !terminal || settle !== null || pause !== null) return;
    // Half a screen gives half the session's size: wait for xterm to read all
    // that has arrived.
    if (parsed < received) {
      reclaim = timers.setTimeout(takeSizeBack, READ_WAIT_MS);
      return;
    }
    unfitted = !(refit && refit());
    say.standing();
    if (unfitted) return;
    const open = globalThis.WebSocket?.OPEN ?? 1;
    if (!socket || socket.readyState !== open) return;
    const cols = wider ? terminal.cols : Math.min(terminal.cols, widestRow() || terminal.cols);
    const rows = taller || streamLowest === null ? terminal.rows : Math.min(terminal.rows, streamLowest + 1);
    sendSize(cols, rows);
    paneSize = [terminal.cols, terminal.rows];
    const take = timers.setTimeout(() => recentTakes.delete(take), RECLAIM_WINDOW_MS);
    recentTakes.add(take);
    if (calm !== null) timers.clearTimeout(calm);
    calm = timers.setTimeout(() => {
      calm = null;
      pauseStep = 0;
    }, RECLAIM_CALM_MS);
    if (recentTakes.size >= RECLAIM_LIMIT) pauseTakes();
    else reclaim = timers.setTimeout(takeSizeBack, RECLAIM_EVERY_MS);
  };

  // noticeBigger is xterm reading a screen drawn for a session wider
  // (`dimension` "cols") or taller ("rows") than this terminal. A timer already
  // armed, the first wait or the pause after a take-back, covers it.
  //
  // xterm reads the stream after it arrives, not as it arrives. A screen that
  // arrived before this terminal last gave the session a size is read after
  // that, and the daemon's repaint for that size is already on its way behind
  // it. So what xterm is reading counts only if it arrived after that size was
  // sent. Otherwise every take-back would be followed by a second one, of the
  // same size, a second later.
  //
  // A stream that has closed takes nothing back: xterm can still be reading
  // what it sent after the close, and the next attach sets the size anyway.
  //
  // In a pause it is kept for when the pause is over.
  const noticeBigger = (dimension) => {
    if (!socket || parsed + 1 <= staleThrough) return;
    if (dimension === "cols") wider = true;
    else taller = true;
    if (pause === null && reclaim === null) reclaim = timers.setTimeout(takeSizeBack, RECLAIM_WAIT_MS);
  };

  // followSize watches xterm read the stream for the two ways the daemon draws a
  // screen for a session bigger than this terminal.
  //
  // Wider: the daemon clears the screen and places every word at its absolute
  // column: CHA (ESC[<col>G), HPA (ESC[<col>`) or CUP and HVP
  // (ESC[<row>;<col>H or f). xterm clamps a column past its edge to the last
  // one, so the words run into each other.
  //
  // Taller at the same width: the daemon reaches each next line with a cursor
  // down (ESC[1B), and xterm stops the cursor at the last row, so every row past
  // it is drawn over the last one. Measured on a disposable session (CLI
  // 2.1.269): a 76 × 60 attacher beside a 76 × 40 terminal piled 20 rows onto its
  // last one, and the terminal's own resize repaired it.
  //
  // Nothing else counts. A column equal to the last one fits. An absolute row
  // past the bottom, a move right and a wide character at the edge are all things
  // a terminal of the right size is sent too. A column past MAX_SESSION_COLS or a
  // move down past MAX_SESSION_ROWS is an application finding its corner. The
  // alternate screen is not tracked, so switching to it is no reason by itself.
  //
  // These are hooks in xterm's parser rather than a look at the bytes: xterm
  // reads a sequence cut between two pieces of the stream as one, and hands the
  // hooks each sequence in order. The hooks only watch: xterm still moves the
  // cursor.
  //
  // The hooks keep the row the stream means the cursor to be on: CUP, HVP and
  // VPA place it (counted from 1 in the sequence), CUD and CUU move it. A move
  // down is past the last row when that row is, not when xterm's own cursor is:
  // a screen wider than this terminal wraps, and its wrapping has already moved
  // xterm's cursor down rows the session does not have. takeSizeBack reads the
  // lowest such row since the last full clear (ESC[2J) or the last size sent —
  // moving up does not undo it. The daemon moves the cursor with nothing else: no
  // line feed in any stream measured.
  const followSize = (made) => {
    const parser = made.parser;
    if (typeof parser?.registerCsiHandler !== "function") return;
    const param = (params, i) => (typeof params[i] === "number" && params[i] > 0 ? params[i] : 1);
    const fresh = () => parsed + 1 > staleThrough;
    const column = (col) => {
      if (col > made.cols && col <= MAX_SESSION_COLS) noticeBigger("cols");
      return false;
    };
    // A row past MAX_SESSION_ROWS, or a move by more, is an application finding
    // the bottom and moves nothing here; a row below this terminal's own last
    // row is not an estimate of anything a take-back would send.
    const place = (row) => {
      if (row > MAX_SESSION_ROWS) return;
      streamRow = row - 1;
      if (fresh() && streamRow <= made.rows - 1) streamLowest = Math.max(streamLowest ?? 0, streamRow);
    };
    const move = (rows) => {
      if (Math.abs(rows) > MAX_SESSION_ROWS) return;
      streamRow = Math.max(0, streamRow + rows);
      if (fresh() && streamLowest !== null && streamRow <= made.rows - 1) streamLowest = Math.max(streamLowest, streamRow);
    };
    parser.registerCsiHandler({ final: "G" }, (params) => column(param(params, 0)));
    parser.registerCsiHandler({ final: "`" }, (params) => column(param(params, 0)));
    parser.registerCsiHandler({ final: "H" }, (params) => {
      place(param(params, 0));
      return column(param(params, 1));
    });
    parser.registerCsiHandler({ final: "f" }, (params) => {
      place(param(params, 0));
      return column(param(params, 1));
    });
    parser.registerCsiHandler({ final: "d" }, (params) => {
      place(param(params, 0));
      return false;
    });
    parser.registerCsiHandler({ final: "A" }, (params) => {
      move(-param(params, 0));
      return false;
    });
    parser.registerCsiHandler({ final: "B" }, (params) => {
      const rows = param(params, 0);
      if (rows <= MAX_SESSION_ROWS && streamRow + rows > made.rows - 1) noticeBigger("rows");
      move(rows);
      return false;
    });
    // A full clear starts a new screen: what the old one showed of the size is
    // forgotten, and the new one says again whether the session is bigger.
    parser.registerCsiHandler({ final: "J" }, (params) => {
      if (params[0] === 2 && fresh()) {
        streamLowest = null;
        wider = false;
        taller = false;
      }
      return false;
    });
  };

  const dropReclaim = () => {
    if (reclaim !== null) timers.clearTimeout(reclaim);
    reclaim = null;
    wider = false;
    taller = false;
    standUp();
  };

  // followPane runs once the pane has stopped moving: refit, say whether that
  // worked, and tell the session.
  const followPane = () => {
    settle = null;
    if (!terminal) return;
    const wasUnfitted = unfitted;
    unfitted = !(refit && refit());
    say.standing();
    // A pane back in view starts over, as a pane of a new size does.
    if (wasUnfitted && !unfitted) standUp();
    if (!unfitted) tellSession();
    if (!unfitted && (wider || taller) && reclaim === null) reclaim = timers.setTimeout(takeSizeBack, RECLAIM_WAIT_MS);
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

  // stepFont changes the type by one step: 1 bigger, -1 smaller, 0 back to the
  // default. It is the one way the type changes — Cmd+= / Cmd+- / Cmd+0 come
  // here through followFontKeys, the buttons (web/js/fontcontrols.js) through
  // the terminal's own stepFont — so a key and a press cannot come to differ.
  // The new size is on screen, and said to the caller, at once; the terminal
  // is refitted and the session told once the steps stop, as for a pane that
  // changed size.
  const stepFont = (step) => {
    if (!terminal) return;
    const size = terminal.options.fontSize;
    const next = step === 0 ? DEFAULT_FONT_SIZE : clampFontSize(size + step);
    if (next === size) {
      // Nothing changes. While earlier steps still wait to be fitted, the
      // columns on screen are not the ones they will leave, so the note waits
      // for them and says the end when it goes up.
      if (sizeNoteDue) sizeNoteAtLimit = step !== 0;
      else showSize(step !== 0);
      return;
    }
    terminal.options.fontSize = next;
    rememberFontSize(fontKey, next);
    say.fontSize(next);
    sizeNoteDue = true;
    sizeNoteAtLimit = false;
    settleThenFollow();
  };

  // followFontKeys makes Cmd with = / + / - / 0 a step (see fontStep). The key
  // is taken from xterm, which would send the session nothing for it anyway,
  // and from the page, which in a browser would zoom everything on it.
  const followFontKeys = (made) => {
    if (typeof made.attachCustomKeyEventHandler !== "function") return;
    made.attachCustomKeyEventHandler((event) => {
      const step = fontStep(event);
      if (step === null) return true;
      event.preventDefault();
      stepFont(step);
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
    paneSize = [cols, rows];
    // The attach gives the session this size, so whatever an earlier stream
    // sent that xterm has not read yet is from before it, and a terminal that
    // had stopped taking the size back starts over.
    forgetSize();
    standUp();
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
      // Each piece is numbered as it arrives and marked read once xterm has read
      // it, which is how noticeBigger tells a screen that arrived before the
      // last size sent from one that arrived after.
      received += 1;
      const piece = received;
      term.write(new Uint8Array(event.data), () => {
        parsed = piece;
      });
    };
    ws.onclose = (event) => {
      if (socket !== ws) return;
      socket = null;
      writable = false;
      readOnly = false;
      paneSize = null;
      dropReclaim();
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

  // A page the browser keeps in its back/forward cache keeps its sockets open:
  // measured on a stand, switching fleets left both terminals of the fleet
  // left attached to their sessions, still holding the sessions' size, until
  // Chrome restored that page. So the terminal lets go when the page is hidden
  // and does not come back by itself; a page restored from the cache reloads
  // (store.js), and the reload opens the terminals afresh.
  const onPageHide = () => closeStream();

  return {
    open() {
      page.addEventListener?.("pagehide", onPageHide);
      void openStream();
    },
    stop() {
      page.removeEventListener?.("pagehide", onPageHide);
      closeStream();
      disposeTerminal();
    },
    // Text typed into the session through the stream, as a key button does.
    type(text) {
      sendBytes(encoder.encode(text));
    },
    // One step of the type, as Cmd+= / Cmd+- / Cmd+0 take (see stepFont): what
    // the caller's font buttons press.
    stepFont(step) {
      stepFont(step);
    },
    get readOnly() {
      return readOnly;
    },
    get unfitted() {
      return unfitted;
    },
  };
}
