// A session's size is one size for everyone attached to it. When another
// attacher makes it wider than this terminal — the operator's own `claude
// attach`, a second window, a tool that attaches at a fixed 120 × 40 — the
// daemon repaints every attacher for the new width: it clears the screen and
// places each word at an absolute column. Drawn into a narrower xterm, every
// column past the edge is clamped to the last one and the lines run into each
// other. Measured against CLI 2.1.269 with two attachers on a disposable
// session: 39 rows of 40 wrong until this terminal's own resize, after which
// the screen matched a fresh attach exactly.
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

// What a fresh attach at this size shows in a terminal of `termCols` columns.
async function freshAttach(cols, rows, termCols = cols) {
  const term = new Headless({ cols: termCols, rows, convertEol: true, allowProposedApi: true });
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
    passing(cols, rows) {
      daemon.another(cols, rows);
      const last = attached.at(-1);
      if (!last) return;
      const query = new URL(last.socket.url).searchParams;
      const told = resizes({ sent: last.socket.sent.slice(0, last.read) }).at(-1);
      daemon.cols = told ? told.cols : Number(query.get("cols"));
      daemon.rows = told ? told.rows : Number(query.get("rows"));
      daemon.repaint();
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
async function attach(daemon, pane) {
  const timers = fakeTimers();
  const armed = [];
  const setTimeout = timers.setTimeout;
  timers.setTimeout = (fn, ms) => {
    armed.push(ms);
    return setTimeout(fn, ms);
  };
  const host = dom.element("div");
  host.pane = pane;
  dom.document.body.appendChild(host);
  const live = createLiveTerminal(host, "orchestrator", { timers });
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

test("while another attacher keeps widening the session, the size is taken back at most once a second", async () => {
  const daemon = fakeDaemon();
  const column = await attach(daemon, { cols: 76, rows: 40 });

  daemon.another(120, 40);
  await drawn(column.terminal);
  await round(daemon, column);
  assert.equal(resizes(column.socket).length, 1);

  for (let i = 2; i <= 4; i++) {
    daemon.another(120, 40);
    await drawn(column.terminal);
    assert.equal(resizes(column.socket).length, i - 1, "nothing is sent before a timer fires");
    await round(daemon, column);
    assert.equal(resizes(column.socket).length, i, "and one resize when it does");
  }
  const afterFirst = column.armed.slice(1);
  assert.ok(
    afterFirst.length > 0 && afterFirst.every((ms) => ms >= 1000),
    `every take-back after the first waits at least a second: armed ${column.armed.join(", ")} ms`,
  );
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
  const daemon = fakeDaemon();
  const column = await attach(daemon, { cols: 76, rows: 40 });
  column.socket.serverSend(frame("\x1b[1"));
  column.socket.serverSend(frame("20Gx"));
  await drawn(column.terminal);
  await round(daemon, column);
  assert.deepEqual(resizes(column.socket), [{ type: "resize", cols: 76, rows: 40 }]);
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
