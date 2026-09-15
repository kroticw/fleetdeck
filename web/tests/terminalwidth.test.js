// A session's size is one size for everyone attached to it. When another
// attacher makes it bigger than this terminal — the operator's own `claude
// attach`, a second window, a tool that attaches at a fixed 120 × 40 — the
// daemon repaints every attacher for the new size: it clears the screen, places
// each word at an absolute column and reaches each next line with a cursor
// down. Drawn into a narrower xterm, every column past the edge is clamped to
// the last one and the lines run into each other; drawn into a shorter one, the
// rows past the bottom pile onto the last. Measured against CLI 2.1.269 with two
// attachers on a disposable session: 39 rows of 40 wrong until this terminal's
// own resize, after which the screen matched a fresh attach exactly.
//
// These cases replay the daemon's way of drawing through xterm's own core
// (web/tests/vendor/xterm-headless.cjs, the same 6.0.0 release the page loads)
// and compare what the terminal ends up showing with what a fresh attach at
// its size shows.

import { test, beforeEach, afterEach } from "node:test";
import assert from "node:assert/strict";
import { createRequire } from "node:module";

import { installDOM, settle } from "./fake-dom.js";
import { fakeTimers, answer, installFit, installObserver, installSocket, frame } from "./terminal-fakes.js";
import { createLiveTerminal } from "../js/liveterminal.js";

const { Terminal: Headless } = createRequire(import.meta.url)("./vendor/xterm-headless.cjs");

// Paragraphs of the kind an orchestrator's terminal shows, longer than the
// orchestrator column is wide and shorter than a wide terminal.
const TEXT = [
  "● T-065 закончила #179: полный make test на Xcode 27 зелёный без ручных настроек, CI прошёл, 8 задач из 8",
  "[m-2eeace] This is a message from another agent, not from your user. It did not interrupt anything at all",
  "  - проверка на машине пройдена до запуска dev-окна, во время работы и после закрытия; повторная сверка",
  "  - anything and nobody is blocked on it — answer when the work you are doing allows, not before that",
  "● Пункты приёмки из карточки выполнены, мержу. Перед мержем проверяю три вещи: PR, CI и описание ветки",
  "  1. Готова PR — a20d6f2, ревью пройдено; the branch is up to date with release/v0.10.2 and nothing else",
  "  2. Все проверки CI зелёные: test, test-web, lint, window stand on macOS 26 and the Linux cross build",
  "  3. Описание PR сверено с шаблоном, чекбоксы отмечены только за сделанное, private details left out",
  "⎿ Interrupted · What should Claude do instead? The orchestrator waits for the operator to say what next",
];

// paint is a screen as the daemon draws it for a size: the screen cleared,
// every word placed at its absolute column, each line reached from the one
// above by a carriage return and a cursor down — the sequences a measured frame
// used (ESC[2J, ESC[<col>G, CR ESC[1B).
function paint(cols, rows) {
  const lines = [];
  for (const paragraph of TEXT) {
    let line = [];
    let col = 1;
    for (const word of paragraph.split(" ").filter(Boolean)) {
      if (col > 3 && col + word.length - 1 > cols) {
        lines.push(line);
        line = [];
        col = 3;
      }
      line.push([col, word]);
      col += word.length + 1;
    }
    lines.push(line);
  }
  // A rule across the whole width and a status line under it, the way Claude
  // Code draws round its input.
  lines.push([[1, "─".repeat(cols)]]);
  lines.push([[3, "⏸"], [5, "manual"], [12, "mode"]]);
  // A screen as tall as the session, the last line on its last row, the way a
  // terminal application keeps its input at the bottom.
  while (lines.length < rows) lines.unshift([]);
  return `\x1b[2J\x1b[H${lines
    .slice(-rows)
    .map((line) => line.map(([col, word]) => `\x1b[${col}G${word}`).join(""))
    .join("\r\x1b[1B")}`;
}

// The rows a terminal shows, as text.
function screen(term) {
  const buffer = term.buffer.active;
  return Array.from({ length: term.rows }, (_, y) => buffer.getLine(buffer.viewportY + y)?.translateToString(true) ?? "");
}

// xterm parses what it is given later, in order; this waits for all of it.
const drawn = (term) => new Promise((resolve) => term.write("", resolve));

// What a fresh attach at this size shows in a terminal of `termCols` × `termRows`.
async function freshAttach(cols, rows, termCols = cols, termRows = rows) {
  const term = new Headless({ cols: termCols, rows: termRows, convertEol: true, allowProposedApi: true });
  await new Promise((resolve) => term.write(paint(cols, rows), resolve));
  const shown = screen(term);
  term.dispose();
  return shown;
}

const resizes = (socket) =>
  socket.sent
    .filter((data) => typeof data === "string")
    .map((data) => JSON.parse(data))
    .filter((msg) => msg.type === "resize");

// The daemon behind the bridge, reduced to the part this is about: one size for
// the session, set by an attach and by a resize, and a repaint for every
// attacher whenever it changes, cut into pieces of `piece` bytes the way a
// stream arrives.
function fakeDaemon() {
  const encoder = new TextEncoder();
  const attached = [];
  // An attacher's recorded size: the last resize it sent that the daemon took
  // in, or its attach's.
  const restoreLast = () => {
    const last = attached.at(-1);
    if (!last) return;
    const query = new URL(last.socket.url).searchParams;
    const told = resizes({ sent: last.socket.sent.slice(0, last.read) }).at(-1);
    daemon.cols = told ? told.cols : Number(query.get("cols"));
    daemon.rows = told ? told.rows : Number(query.get("rows"));
    daemon.repaint();
  };
  const daemon = {
    cols: 0,
    rows: 0,
    piece: 4096,
    repaints: 0,
    repaint() {
      daemon.repaints += 1;
      const bytes = encoder.encode(paint(daemon.cols, daemon.rows));
      for (const { socket } of attached) {
        for (let at = 0; at < bytes.length; at += daemon.piece) socket.serverSend(bytes.slice(at, at + daemon.piece).buffer);
      }
    },
    attach(socket) {
      const query = new URL(socket.url).searchParams;
      daemon.cols = Number(query.get("cols"));
      daemon.rows = Number(query.get("rows"));
      attached.push({ socket, read: 0 });
      socket.serverOpen();
      socket.serverSend(JSON.stringify({ type: "ready", writable: true }));
      daemon.repaint();
    },
    // Another attacher, drawn nowhere here, that sets the session's size.
    another(cols, rows) {
      daemon.cols = cols;
      daemon.rows = rows;
      daemon.repaint();
    },
    // An attacher that comes and goes, as a tool typing keys into the session
    // does. When it leaves, the daemon puts the session at the size of the
    // attacher that attached last (measured on 2.1.269), not of the last resize.
    // visit attaches such an attacher and returns its leaving; passing is a visit
    // with nothing in between.
    visit(cols, rows) {
      daemon.another(cols, rows);
      return restoreLast;
    },
    passing(cols, rows) {
      daemon.visit(cols, rows)();
    },
    // A terminal attached here leaving: the session goes to the size of the
    // attacher that attached last among those left.
    leave(socket) {
      const at = attached.findIndex((entry) => entry.socket === socket);
      if (at >= 0) attached.splice(at, 1);
      restoreLast();
    },
    // Takes in what the terminals sent since the last call.
    pump() {
      let resized = false;
      for (const entry of attached) {
        for (; entry.read < entry.socket.sent.length; entry.read += 1) {
          const data = entry.socket.sent[entry.read];
          if (typeof data !== "string") continue;
          const msg = JSON.parse(data);
          if (msg.type !== "resize") continue;
          daemon.cols = msg.cols;
          daemon.rows = msg.rows;
          resized = true;
        }
      }
      if (resized) daemon.repaint();
    },
  };
  return daemon;
}

let dom;
let sockets;
let terminals;
const saved = {};

beforeEach(() => {
  for (const name of ["fetch", "WebSocket", "Terminal", "FitAddon", "ResizeObserver"]) {
    saved[name] = Object.hasOwn(globalThis, name) ? globalThis[name] : undefined;
  }
  delete globalThis.ResizeObserver;
  dom = installDOM();
  sockets = installSocket();
  terminals = [];
  const made = terminals;
  globalThis.Terminal = class extends Headless {
    constructor(options) {
      super({ ...options, allowProposedApi: true });
      made.push(this);
    }
    open(host) {
      this.host = host;
    }
  };
  // Each terminal measures the pane its host carries.
  installFit((terminal) => {
    const pane = terminal.host.pane;
    return pane.hidden ? undefined : { cols: pane.cols, rows: pane.rows };
  });
  globalThis.fetch = async () => answer({ body: { token: "token" } });
});

afterEach(() => {
  for (const term of terminals) term.dispose();
  dom.restore();
  for (const [name, value] of Object.entries(saved)) {
    if (value === undefined) delete globalThis[name];
    else globalThis[name] = value;
  }
});

// A terminal in a pane of `pane` cells, attached to the daemon. `armed` is the
// delay of every timer it arms, in order.
async function attach(daemon, pane, { timers = fakeTimers(), report = {}, reconnect = false } = {}) {
  const armed = [];
  const setTimeout = timers.setTimeout;
  timers.setTimeout = (fn, ms) => {
    armed.push(ms);
    return setTimeout(fn, ms);
  };
  const host = dom.element("div");
  host.pane = pane;
  dom.document.body.appendChild(host);
  const live = createLiveTerminal(host, "orchestrator", { timers, report, reconnect });
  live.open();
  await settle();
  const socket = sockets.at(-1);
  const terminal = terminals.at(-1);
  daemon.attach(socket);
  await drawn(terminal);
  return { live, timers, armed, socket, terminal, pane };
}

// Time passing: every armed timer fires, the daemon takes in what was sent,
// and every terminal draws what came back.
async function round(daemon, ...clients) {
  for (const client of clients) await client.timers.tick();
  daemon.pump();
  for (const client of clients) await drawn(client.terminal);
}

test("a session another attacher made wider is taken back to this terminal's size, and the screen is what a fresh attach draws", async () => {
  const daemon = fakeDaemon();
  const column = await attach(daemon, { cols: 76, rows: 40 });
  const expected = await freshAttach(76, 40);
  assert.deepEqual(screen(column.terminal), expected, "the terminal starts right");

  daemon.another(120, 40);
  await drawn(column.terminal);
  assert.notDeepEqual(screen(column.terminal), expected, "a frame for 120 columns drawn into 76 is wrong — the defect this is about");
  assert.deepEqual(resizes(column.socket), [], "nothing is sent while the frame is still arriving");
  assert.equal(column.armed.length, 1, "one timer is armed to take the size back");
  assert.ok(column.armed[0] <= 2000, `and it fires within two seconds, not ${column.armed[0]} ms`);

  await round(daemon, column);
  assert.deepEqual(resizes(column.socket), [{ type: "resize", cols: 76, rows: 40 }], "the terminal's own size is sent again");
  assert.deepEqual([daemon.cols, daemon.rows], [76, 40]);
  assert.deepEqual(screen(column.terminal), expected, "and the repaint that follows is exactly a fresh attach's screen");

  for (let i = 0; i < 3; i++) await round(daemon, column);
  assert.equal(resizes(column.socket).length, 1, "a screen that fits asks for nothing more");
  assert.equal(column.timers.count(), 0, "and leaves no timer behind");
});

// Taller at the same width breaks the bottom instead: the daemon reaches each
// next line with a cursor down, xterm stops at the last row, and the rows past it
// are drawn over the last one. Measured on a disposable session with a 76 × 60
// attacher beside a 76 × 40 terminal.
test("a session another attacher made taller is taken back too, and the bottom is what a fresh attach draws", async () => {
  const daemon = fakeDaemon();
  const column = await attach(daemon, { cols: 76, rows: 40 });
  const expected = await freshAttach(76, 40);

  daemon.another(76, 60);
  await drawn(column.terminal);
  assert.notDeepEqual(screen(column.terminal), expected, "a frame for 60 rows drawn into 40 is wrong at the bottom");

  await round(daemon, column);
  assert.deepEqual(resizes(column.socket), [{ type: "resize", cols: 76, rows: 40 }]);
  assert.deepEqual(screen(column.terminal), expected);
});

// xterm reads the stream a moment after it arrives. A wide screen already on its
// way when the size is sent back is covered by the repaint that follows.
test("a screen that arrived before the size was sent back is not a reason to send it again", async () => {
  const daemon = fakeDaemon();
  const column = await attach(daemon, { cols: 76, rows: 40 });
  daemon.another(120, 40);
  await drawn(column.terminal);

  daemon.another(120, 40);
  await column.timers.tick();
  assert.equal(resizes(column.socket).length, 1, "the take-back fired before xterm read the second screen");
  daemon.pump();
  await drawn(column.terminal);
  for (let i = 0; i < 3; i++) await round(daemon, column);
  assert.equal(resizes(column.socket).length, 1, "the screen that was on its way when the size went back asked for it again");
  assert.deepEqual(screen(column.terminal), await freshAttach(76, 40));
});

test("a wide frame that arrives in many pieces takes the size back once, not once per piece", async () => {
  const daemon = fakeDaemon();
  const column = await attach(daemon, { cols: 76, rows: 40 });
  daemon.piece = 64;
  const before = daemon.repaints;

  daemon.another(120, 40);
  await drawn(column.terminal);
  const pieces = Math.ceil(new TextEncoder().encode(paint(120, 40)).length / daemon.piece);
  await round(daemon, column);
  assert.equal(
    resizes(column.socket).length,
    1,
    `a frame of ${pieces} pieces sent ${resizes(column.socket).length} resizes and made the session repaint ${daemon.repaints - before} times`,
  );
});

// Something that makes the session bigger again every second is not something
// taking it back fixes: three take-backs, a second apart, and then none until
// the pane changes.
test("while another attacher keeps making the session bigger, the size is taken back once a second at most, three times, and not again until the pane changes", async () => {
  const observers = installObserver();
  const daemon = fakeDaemon();
  const timers = clockTimers();
  const column = await attach(daemon, { cols: 76, rows: 40 }, { timers });

  const bySecond = [];
  for (let second = 0; second < 20; second++) {
    daemon.another(120, 40);
    await drawn(column.terminal);
    await timers.advance(1000);
    daemon.pump();
    await drawn(column.terminal);
    bySecond.push(resizes(column.socket).length);
  }
  assert.deepEqual(bySecond.slice(0, 4), [1, 2, 3, 3], `resizes sent by the end of each second: ${bySecond.join(", ")}`);
  assert.equal(resizes(column.socket).length, 3, `resizes sent by the end of each second: ${bySecond.join(", ")}`);

  column.pane.cols = 70;
  observers[0].resize();
  await timers.advance(1000);
  daemon.pump();
  await drawn(column.terminal);
  assert.deepEqual(resizes(column.socket).at(-1), { type: "resize", cols: 70, rows: 40 }, "the pane's new size is sent");
  daemon.another(120, 40);
  await drawn(column.terminal);
  await timers.advance(1000);
  daemon.pump();
  await drawn(column.terminal);
  assert.equal(resizes(column.socket).length, 5, "and a pane of a new size takes the session back again");
});

// Each of these puts nothing past the terminal's last column that a wider
// session would: they are what applications and the daemon send to a terminal
// of the right size.
const FITTING = [
  ["the last column itself, absolute and by row and column", "\x1b[5;76H|\x1b[76G|"],
  ["a row past the bottom, as a status line placed at row 999", "\x1b[41;1Hx\x1b[999;1Hstatus"],
  ["an application on the alternate screen", "\x1b[?1049h\x1b[2J\x1b[1;1Htop\x1b[40;76Hz\x1b[?1049l"],
  ["wide characters at the right edge", "\x1b[1;75H中文🟢\x1b[2;76H🟢"],
  ["an application asking where the corner is", "\x1b[999;999H\x1b[6n"],
  ["a cursor moved right past the edge, relatively", "\x1b[1G\x1b[200Cx"],
  ["a cursor moved down onto the last row and no further", "\x1b[38;1H\x1b[1Bx\x1b[1;1H\x1b[39B"],
  ["an application finding the bottom", "\x1b[1;1H\x1b[999B"],
  ["colours and private modes", "\x1b[38;2;255;120;0mred\x1b[0m\x1b[?2026h\x1b[?25l\x1b[?2026l"],
];

test("positions that fit the terminal are not a reason to take the size back", async () => {
  const daemon = fakeDaemon();
  const column = await attach(daemon, { cols: 76, rows: 40 });
  for (const [what, bytes] of FITTING) {
    column.socket.serverSend(frame(bytes));
    await drawn(column.terminal);
    await round(daemon, column);
    await round(daemon, column);
    assert.deepEqual(resizes(column.socket), [], `${what} sent a resize`);
    assert.equal(column.timers.count(), 0, `${what} left a timer armed`);
  }
});

test("a column past the edge split across two pieces of the stream is still read", async () => {
  const pieces = [
    ["CHA", "\x1b[1", "20Gx"],
    ["CUP", "\x1b[1;1", "20Hx"],
    ["HVP", "\x1b[1;1", "20fx"],
    ["HPA", "\x1b[12", "0`x"],
  ];
  for (const [what, first, second] of pieces) {
    const daemon = fakeDaemon();
    const column = await attach(daemon, { cols: 76, rows: 40 });
    column.socket.serverSend(frame(first));
    column.socket.serverSend(frame(second));
    await drawn(column.terminal);
    await round(daemon, column);
    assert.deepEqual(resizes(column.socket), [{ type: "resize", cols: 76, rows: 40 }], `${what} split as ${JSON.stringify([first, second])}`);
    column.live.stop();
  }
});

test("two terminals of different widths on one session settle on the narrower one without taking turns", async () => {
  const daemon = fakeDaemon();
  const column = await attach(daemon, { cols: 76, rows: 40 });
  const wide = await attach(daemon, { cols: 120, rows: 40 });
  assert.deepEqual([daemon.cols, daemon.rows], [120, 40], "the wider one attached last and set the session's size");

  for (let i = 0; i < 8; i++) await round(daemon, column, wide);
  const counts = `narrow ${resizes(column.socket).length}, wide ${resizes(wide.socket).length}, repaints ${daemon.repaints}`;
  assert.equal(resizes(column.socket).length, 1, `resizes over eight rounds: ${counts}`);
  assert.equal(resizes(wide.socket).length, 0, `resizes over eight rounds: ${counts}`);
  assert.deepEqual([daemon.cols, daemon.rows], [76, 40]);
  assert.deepEqual(screen(column.terminal), await freshAttach(76, 40), "the narrow terminal shows a fresh attach's screen");
  assert.deepEqual(screen(wide.terminal), await freshAttach(76, 40, 120), "and a narrower screen inside the wide one is drawn right too");

  // A tool that attaches at 120 × 40 and leaves, three times. Each time it
  // leaves, the daemon gives the session the wide terminal's size, because the
  // wide one attached last: one take-back per visit, and quiet in between.
  for (let visit = 1; visit <= 3; visit++) {
    daemon.passing(120, 40);
    for (let i = 0; i < 4; i++) await round(daemon, column, wide);
    const now = `narrow ${resizes(column.socket).length}, wide ${resizes(wide.socket).length}, repaints ${daemon.repaints}`;
    assert.equal(resizes(column.socket).length, 1 + visit, `after visit ${visit}: ${now}`);
    assert.equal(resizes(wide.socket).length, 0, `after visit ${visit}: ${now}`);
    assert.deepEqual(screen(column.terminal), await freshAttach(76, 40), `after visit ${visit} the narrow terminal is right again`);
  }

  // A visit that lasts longer than the wait: the narrow terminal takes the size
  // back while the visitor is attached, and again when it leaves and the daemon
  // gives the session the wide terminal's size. Two take-backs for such a visit;
  // what bounds them is the one-a-second pace, not the visit.
  const leave = daemon.visit(120, 40);
  for (let i = 0; i < 4; i++) await round(daemon, column, wide);
  assert.equal(resizes(column.socket).length, 5, "taken back while the visitor is still attached");
  leave();
  for (let i = 0; i < 4; i++) await round(daemon, column, wide);
  assert.equal(resizes(column.socket).length, 6, "and again once it has left");
  assert.equal(resizes(wide.socket).length, 0);
  assert.deepEqual(screen(column.terminal), await freshAttach(76, 40));

  // The other way round: the narrow one attaching last takes nothing back.
  const second = fakeDaemon();
  const wideFirst = await attach(second, { cols: 120, rows: 40 });
  const narrowLast = await attach(second, { cols: 76, rows: 40 });
  for (let i = 0; i < 8; i++) await round(second, wideFirst, narrowLast);
  assert.equal(resizes(wideFirst.socket).length + resizes(narrowLast.socket).length, 0);
});

// A folded column or a hidden tab has no layout; its terminal keeps the columns
// it last had, which are not the pane's any more. Taking the session's size for
// such a terminal would reshape the session for a screen nobody sees.
test("a terminal that cannot be measured does not take the size back, and never sends a size it cannot show", async () => {
  const measures = [
    ["a pane with no layout", { hidden: true }],
    // addon-fit's answer for an element with display: none, whose computed width
    // and height are "auto".
    ["a pane measured as not a number", { cols: Number.NaN, rows: Number.NaN }],
    // addon-fit's floor, which is what an element with no room at all gets.
    ["a pane with no room", { cols: 2, rows: 1 }],
  ];
  for (const [what, measured] of measures) {
    const daemon = fakeDaemon();
    const column = await attach(daemon, { cols: 76, rows: 40 });
    Object.assign(column.pane, measured);
    daemon.another(120, 40);
    await drawn(column.terminal);
    for (let i = 0; i < 3; i++) await round(daemon, column);
    assert.deepEqual(resizes(column.socket), [], `${what} sent a resize`);
    assert.equal(column.terminal.cols, 76, `${what} changed the terminal's columns`);
    column.live.stop();
  }
});

test("a terminal that comes back into view takes back a size it could not take while hidden", async () => {
  const observers = installObserver();
  const daemon = fakeDaemon();
  const column = await attach(daemon, { cols: 76, rows: 40 });
  column.pane.hidden = true;
  daemon.another(120, 40);
  await drawn(column.terminal);
  for (let i = 0; i < 3; i++) await round(daemon, column);
  assert.deepEqual(resizes(column.socket), []);

  column.pane.hidden = false;
  observers[0].resize();
  for (let i = 0; i < 3; i++) await round(daemon, column);
  assert.deepEqual(resizes(column.socket), [{ type: "resize", cols: 76, rows: 40 }]);
  assert.deepEqual(screen(column.terminal), await freshAttach(76, 40));
});

test("a pane with no room is not sent to the session as a size", async () => {
  const observers = installObserver();
  const daemon = fakeDaemon();
  const column = await attach(daemon, { cols: 76, rows: 40 });
  Object.assign(column.pane, { cols: 2, rows: 1 });
  observers[0].resize();
  for (let i = 0; i < 3; i++) await round(daemon, column);
  assert.deepEqual(resizes(column.socket), [], "a 2 × 1 pane reshaped the session for everyone");
});

test("a stream that ends drops a take-back it had armed", async () => {
  const daemon = fakeDaemon();
  const column = await attach(daemon, { cols: 76, rows: 40 });
  daemon.another(120, 40);
  await drawn(column.terminal);
  assert.equal(column.timers.count(), 1);
  column.socket.serverClose(4002, "dropped");
  assert.equal(column.timers.count(), 0, "a take-back outlived its stream");
});

// xterm reads what a stream sent after the stream has closed.
test("a wide screen read after the stream closed arms no take-back", async () => {
  const daemon = fakeDaemon();
  const column = await attach(daemon, { cols: 76, rows: 40 });
  daemon.another(120, 40);
  column.socket.serverClose(4002, "dropped");
  await drawn(column.terminal);
  assert.equal(column.timers.count(), 0, `a take-back was armed for a closed stream: armed ${column.armed.join(", ")} ms`);
});

// A reconnect opens its socket before xterm has read what the last one sent. The
// attach gives the session this terminal's size, so that screen is from before it.
test("a wide screen from before a reconnect does not make the reconnected terminal send a size", async () => {
  const daemon = fakeDaemon();
  const column = await attach(daemon, { cols: 76, rows: 40 }, { reconnect: true });
  const first = column.socket;
  daemon.another(120, 40);
  first.serverClose(1006, "");
  await column.timers.tick();
  const second = sockets.at(-1);
  assert.notEqual(second, first, "the terminal reconnected");
  daemon.attach(second);
  await drawn(column.terminal);
  for (let i = 0; i < 3; i++) {
    await column.timers.tick();
    daemon.pump();
    await drawn(column.terminal);
  }
  assert.deepEqual(resizes(second), [], "the reconnected terminal sent a size for a screen from before its attach");
  assert.deepEqual(screen(column.terminal), await freshAttach(76, 40));
});

// A clock whose timers can also be fired one at a time, oldest first, for a case
// about what happens between two of them.
function steppedTimers() {
  let nextId = 1;
  const pending = new Map();
  return {
    setTimeout(fn) {
      const id = nextId++;
      pending.set(id, fn);
      return id;
    },
    clearTimeout(id) {
      pending.delete(id);
    },
    count() {
      return pending.size;
    },
    async fireOldest() {
      const oldest = pending.entries().next().value;
      if (!oldest) return;
      pending.delete(oldest[0]);
      oldest[1]();
      await settle();
    },
    async tick() {
      for (const [id, fn] of [...pending.entries()]) {
        pending.delete(id);
        fn();
      }
      await settle();
    },
  };
}

// A drag of the column's edge moves the pane on every pointer event, and the
// take-back's wait can end in the middle of it.
test("a widening noticed while the pane is still moving is taken back once, at the size the pane stops at", async () => {
  const observers = installObserver();
  const daemon = fakeDaemon();
  const timers = steppedTimers();
  const column = await attach(daemon, { cols: 76, rows: 40 }, { timers });
  daemon.another(120, 40);
  await drawn(column.terminal);

  column.pane.cols = 72;
  observers[0].resize();
  await timers.fireOldest();
  assert.deepEqual(resizes(column.socket), [], "the take-back's wait ended mid-drag and sent a size the pane only passed through");

  column.pane.cols = 70;
  observers[0].resize();
  for (let i = 0; i < 3; i++) await round(daemon, column);
  assert.deepEqual(resizes(column.socket), [{ type: "resize", cols: 70, rows: 40 }]);
  assert.deepEqual(screen(column.terminal), await freshAttach(70, 40));
});

test("a take-back that finds the pane cannot be measured says the terminal does not follow it", async () => {
  const daemon = fakeDaemon();
  let standing = 0;
  const column = await attach(daemon, { cols: 76, rows: 40 }, { report: { standing: () => (standing += 1) } });
  const before = standing;
  column.pane.hidden = true;
  daemon.another(120, 40);
  await drawn(column.terminal);
  await round(daemon, column);
  assert.equal(column.live.unfitted, true, "the terminal still claims to follow a pane it cannot measure");
  assert.ok(standing > before, "and its caller was not told to read that again");
});

// A clock that keeps time, for cases about how often something happens.
function clockTimers() {
  let now = 0;
  let nextId = 1;
  const pending = new Map();
  const timers = {
    setTimeout(fn, ms) {
      const id = nextId++;
      pending.set(id, { fn, at: now + ms });
      return id;
    },
    clearTimeout(id) {
      pending.delete(id);
    },
    count() {
      return pending.size;
    },
    // Moves the clock on by `ms`, firing every timer that falls due on the way,
    // earliest first.
    async advance(ms) {
      const end = now + ms;
      for (;;) {
        let due = null;
        for (const [id, timer] of pending) {
          if (timer.at <= end && (!due || timer.at < due[1].at)) due = [id, timer];
        }
        if (!due) break;
        pending.delete(due[0]);
        now = due[1].at;
        due[1].fn();
        await settle();
      }
      now = end;
    },
    async tick() {
      await timers.advance(1000);
    },
  };
  return timers;
}

// Another terminal can differ from this one in both dimensions at once. Taking
// the whole of this terminal's size back would make the session bigger in the
// dimension the other is smaller in, and the two would take turns for ever. A
// take-back sends the smaller of this terminal's size and the session's in each
// dimension, so the two settle on what fits both.
test("two terminals that differ in width, height or both settle on the smaller of each, without taking turns", async () => {
  const cases = [
    ["wider and taller", 120, 60],
    ["wider and shorter", 120, 24],
    ["narrower and taller", 60, 60],
    ["narrower and shorter", 60, 24],
  ];
  for (const [what, cols, rows] of cases) {
    const daemon = fakeDaemon();
    const column = await attach(daemon, { cols: 76, rows: 40 });
    const other = await attach(daemon, { cols, rows });
    const history = [];
    for (let i = 0; i < 8; i++) {
      await round(daemon, column, other);
      history.push(`${daemon.cols}×${daemon.rows}`);
    }
    const fit = [Math.min(76, cols), Math.min(40, rows)];
    const said = `${what}: session by round ${history.join(", ")}; this sent ${JSON.stringify(resizes(column.socket))}, the other ${JSON.stringify(resizes(other.socket))}`;
    assert.deepEqual([daemon.cols, daemon.rows], fit, said);
    assert.ok(resizes(column.socket).length + resizes(other.socket).length <= 1, said);
    assert.deepEqual(screen(column.terminal), await freshAttach(fit[0], fit[1], 76, 40), said);
    assert.deepEqual(screen(other.terminal), await freshAttach(fit[0], fit[1], cols, rows), said);
    column.live.stop();
    other.live.stop();
  }
});

test("a 76 × 60 terminal and a 120 × 40 one on one session settle on 76 × 40", async () => {
  const daemon = fakeDaemon();
  const tall = await attach(daemon, { cols: 76, rows: 60 });
  const wide = await attach(daemon, { cols: 120, rows: 40 });
  const history = [];
  for (let i = 0; i < 12; i++) {
    await round(daemon, tall, wide);
    history.push(`${daemon.cols}×${daemon.rows}`);
  }
  const said = `session by round ${history.join(", ")}; the tall one sent ${resizes(tall.socket).length}, the wide one ${resizes(wide.socket).length}`;
  assert.deepEqual(history.slice(0, 2), ["76×40", "76×40"], said);
  assert.equal(resizes(tall.socket).length + resizes(wide.socket).length, 1, said);
  assert.deepEqual(screen(tall.terminal), await freshAttach(76, 40, 76, 60));
  assert.deepEqual(screen(wide.terminal), await freshAttach(76, 40, 120, 40));
});

test("three terminals settle on the smallest of each dimension, and settle again when the last to attach leaves", async () => {
  const daemon = fakeDaemon();
  const terms = [await attach(daemon, { cols: 76, rows: 60 }), await attach(daemon, { cols: 120, rows: 40 }), await attach(daemon, { cols: 100, rows: 50 })];
  const sent = () => terms.map((t) => resizes(t.socket).length);
  const history = [];
  for (let i = 0; i < 8; i++) {
    await round(daemon, ...terms);
    history.push(`${daemon.cols}×${daemon.rows}`);
  }
  let said = `session by round ${history.join(", ")}; resizes sent ${sent().join(", ")}`;
  assert.deepEqual([daemon.cols, daemon.rows], [76, 40], said);
  assert.ok(sent().reduce((a, b) => a + b, 0) <= 3, said);
  for (const t of terms) assert.deepEqual(screen(t.terminal), await freshAttach(76, 40, t.pane.cols, t.pane.rows), said);
  assert.deepEqual(history.slice(-4), ["76×40", "76×40", "76×40", "76×40"], said);

  const before = sent().slice(0, 2).reduce((a, b) => a + b, 0);
  const last = terms.pop();
  daemon.leave(last.socket);
  last.live.stop();
  history.length = 0;
  for (let i = 0; i < 8; i++) {
    await round(daemon, ...terms);
    history.push(`${daemon.cols}×${daemon.rows}`);
  }
  said = `after the last one left, session by round ${history.join(", ")}; resizes sent ${sent().join(", ")}`;
  assert.deepEqual([daemon.cols, daemon.rows], [76, 40], said);
  assert.ok(sent().reduce((a, b) => a + b, 0) - before <= 1, said);
  for (const t of terms) assert.deepEqual(screen(t.terminal), await freshAttach(76, 40, t.pane.cols, t.pane.rows), said);
});

// A terminal whose pane changes from 70 to 76 columns (or any size) tells the
// session; the screens the stream sent for the size before that are no guide to
// the rows the session has now.
test("the rows a take-back reads start over when this terminal sends a size, not only when the screen is cleared", async () => {
  const observers = installObserver();
  const daemon = fakeDaemon();
  const column = await attach(daemon, { cols: 76, rows: 80 });
  daemon.another(76, 60);
  await drawn(column.terminal);
  column.pane.rows = 70;
  observers[0].resize();
  await column.timers.tick();
  assert.deepEqual(resizes(column.socket), [{ type: "resize", cols: 76, rows: 70 }]);

  // A screen for 40 rows with no clear before it, then a column past the edge.
  column.socket.serverSend(frame(`\x1b[H${"x\r\x1b[1B".repeat(39)}x\x1b[1;100Hx`));
  await drawn(column.terminal);
  await column.timers.tick();
  assert.deepEqual(resizes(column.socket).at(-1), { type: "resize", cols: 76, rows: 40 }, "rows from a screen for 60 rows outlived the size sent after it");
});

async function afterOwnSize() {
  const observers = installObserver();
  const daemon = fakeDaemon();
  const column = await attach(daemon, { cols: 76, rows: 40 });
  column.pane.cols = 70;
  observers[0].resize();
  await column.timers.tick();
  assert.deepEqual(resizes(column.socket), [{ type: "resize", cols: 70, rows: 40 }]);
  return column;
}

test("with nothing that placed the cursor since the size was sent, a take-back keeps this terminal's own rows", async () => {
  const column = await afterOwnSize();
  column.socket.serverSend(frame("\x1b[120Gx"));
  await drawn(column.terminal);
  await column.timers.tick();
  assert.deepEqual(resizes(column.socket).at(-1), { type: "resize", cols: 70, rows: 40 });
});

test("the rows a take-back reads count a placed row from 1 and keep the lowest, whatever moves up after it", async () => {
  const column = await afterOwnSize();
  // Rows 10, 15, 12, 20 and 18, counted from 1: the lowest is 20.
  column.socket.serverSend(frame("\x1b[10;1Hx\x1b[5Bx\x1b[3Ax\x1b[20dx\x1b[2Ax\x1b[120Gx"));
  await drawn(column.terminal);
  await column.timers.tick();
  assert.deepEqual(resizes(column.socket).at(-1), { type: "resize", cols: 70, rows: 20 });
});
