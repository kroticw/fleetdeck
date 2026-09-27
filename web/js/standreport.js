// web/js/standreport.js
//
// What the board says of itself in the fleetdeck window's log on a CI stand, and
// nowhere else (window.fleetdeckHost.stand): numbers a screenshot cannot prove.
// The first is whether the strip under the board is #board's horizontal
// scrollbar -- how much wider its content is than it, and how tall the bar
// under it is. The second is where that box and its bar are, against the
// sessions panel's left edge, and whether the last column can be scrolled out
// from under that panel -- answered by the board itself, as true or false. The
// third is the bars down the board's height: its own, at the sessions glass's
// edge, and its columns'.
//
// The side surfaces say theirs: the sessions list's bar, the edges down the
// orchestrator's terminal, and the grounds and corners of the boxes the
// sessions list lies in.

import { TOP_BAND_MAX, isGround } from "./topband.js";

// Sub-pixel layout rounding, not a column under the panel.
const EDGE_SLACK_PX = 1;

const tenth = (n) => Math.round(n * 10) / 10;

// barWidth is how wide the bar down el's right side is: what its box is wider
// than the room it leaves its content, less its borders.
function barWidth(win, el) {
  const style = win.getComputedStyle(el);
  const border = (parseFloat(style.borderLeftWidth) || 0) + (parseFloat(style.borderRightWidth) || 0);
  return (el.offsetWidth ?? 0) - (el.clientWidth ?? 0) - border;
}

// boardScrollReport is what board (#board) in win says of its scrolling. The
// sessions panel's left edge is the page's width less the inset the window
// sends for it; a page the window sent no inset leaves it, and every answer
// that needs it, null. So does a board with no columns for the last one.
export function boardScrollReport(win, board) {
  const style = win.getComputedStyle(board);
  const root = win.document.documentElement;
  const border = (parseFloat(style.borderTopWidth) || 0) + (parseFloat(style.borderBottomWidth) || 0);
  const box = board.getBoundingClientRect();
  const inset = parseFloat(win.getComputedStyle(root).getPropertyValue("--host-inset-content-right"));
  const sessionsLeft = Number.isFinite(inset) ? root.clientWidth - inset : null;
  // Scrolling moves every column by the same distance, so where the last one
  // ends at the end of the scroll is where it ends now, moved by what is left.
  const columns = [...board.querySelectorAll(":scope > .kcol")];
  const last = columns.length ? columns[columns.length - 1] : null;
  const end = board.scrollWidth - board.clientWidth;
  const lastRight = last ? last.getBoundingClientRect().right - (end - board.scrollLeft) : null;
  const clear = (edge) => (edge === null || sessionsLeft === null ? null : edge <= sessionsLeft + EDGE_SLACK_PX);
  return {
    scrollWidth: board.scrollWidth,
    clientWidth: board.clientWidth,
    scrollbarHeight: board.offsetHeight - board.clientHeight - border,
    overflowX: style.overflowX,
    boardLeft: tenth(box.left),
    boardRight: tenth(box.right),
    sessionsLeft: sessionsLeft === null ? null : tenth(sessionsLeft),
    lastColumnRightAtEnd: lastRight === null ? null : tenth(lastRight),
    lastColumnClear: clear(lastRight),
    boardClearOfSessions: clear(box.right),
    // Down the board's height: whether anything on it is taller than the room
    // it has -- without that, no bar is no proof -- and the bars that draws.
    scrollHeight: board.scrollHeight,
    clientHeight: board.clientHeight,
    overflowY: style.overflowY,
    scrollbarWidth: barWidth(win, board),
    columnScrollbarWidth: columns.reduce((widest, c) => Math.max(widest, barWidth(win, c)), 0),
    contentTallerThanRoom: board.scrollHeight > board.clientHeight || columns.some((c) => c.scrollHeight > c.clientHeight),
  };
}

// listScrollReport is what the sessions surface's list (#sessions) in win says
// of its vertical scrolling: how much taller its content is than it, and how wide
// the bar beside it is. macOS draws a classic 15 px bar for a mouse; the islands
// ask WebKit for 6 (web/app.css).
export function listScrollReport(win, list) {
  return {
    surface: "sessions",
    scrollHeight: list.scrollHeight,
    clientHeight: list.clientHeight,
    scrollbarWidth: barWidth(win, list),
    overflowY: win.getComputedStyle(list).overflowY,
  };
}

// The boxes the sessions list lies in, outermost first. On glass none paints a
// ground or rounds a corner of its own: the glass is the island. Opaque, the
// body paints the panel, square, and the window's frame rounds it.
const GROUND_SELECTORS = ["html", "body", "main", "#sessions", "#header", ".col-size", ".slist-head", ".fleet-group-head"];

// groundsReport is what surface's page in win says of those boxes: each one
// there is, with its computed ground, image, corners and shadow, and the
// material the window said the page lies on (data-glass), null if none.
export function groundsReport(win, surface) {
  const doc = win.document;
  const elements = [];
  for (const selector of GROUND_SELECTORS) {
    for (const el of doc.querySelectorAll(selector)) {
      const style = win.getComputedStyle(el);
      elements.push({
        selector,
        background: style.backgroundColor,
        image: style.backgroundImage,
        radius: style.borderRadius,
        shadow: style.boxShadow,
      });
    }
  }
  return { surface, report: "grounds", glass: doc.documentElement.dataset.glass ?? null, elements };
}

// watch reports what measure says of target once laid out, on a resize,
// whenever target's children change as observed, and when the returned
// function is called; at most once a frame, and only when the report changed.
function watch(win, target, observed, measure, report) {
  let last = "";
  let queued = false;
  const run = () => {
    queued = false;
    const now = measure();
    const text = JSON.stringify(now);
    if (text === last) return;
    last = text;
    report(now);
  };
  const later = () => {
    if (queued) return;
    queued = true;
    win.requestAnimationFrame(run);
  };
  win.addEventListener("resize", later);
  if (typeof win.MutationObserver === "function") new win.MutationObserver(later).observe(target, observed);
  later();
  return later;
}

// watchListScroll reports list's scrolling whenever the list draws its rows.
export function watchListScroll(win, list, report) {
  return watch(win, list, { childList: true }, () => listScrollReport(win, list), report);
}

// watchGrounds reports the sessions surface's grounds whenever anything in its
// list is drawn: a head or a group heading comes with the first rows.
export function watchGrounds(win, list, report) {
  return watch(win, list, { childList: true, subtree: true }, () => groundsReport(win, "sessions"), report);
}

// How long after a change the terminal is measured again: xterm's own bar fades
// out over 800 ms (web/vendor/xterm.css), and a report taken while it fades
// would be the last one the window hears.
const TERMINAL_SETTLE_MS = 1500;

// terminalScrollReport is what the orchestrator surface's column in win says of
// the edges its terminal draws down its right side: the native bar under
// xterm's viewport, which WebKit draws for overflow-y: scroll at all times, and
// how visible xterm's own bar is. null while the column has no terminal.
export function terminalScrollReport(win, column) {
  const viewport = column.querySelector(".xterm-viewport");
  const bar = column.querySelector(".xterm-scrollable-element > .scrollbar.vertical");
  if (!viewport || !bar) return null;
  return {
    surface: "orchestrator",
    viewportScrollbarWidth: barWidth(win, viewport),
    ownBarOpacity: Number(win.getComputedStyle(bar).opacity),
  };
}

// watchTerminalScroll reports the terminal's edges once there is a terminal,
// whenever the column's content or the window's size changes, and again once
// the last change has settled; only when the report changed.
export function watchTerminalScroll(win, column, report) {
  let last = "";
  let queued = false;
  let settle = null;
  const measure = () => {
    queued = false;
    const now = terminalScrollReport(win, column);
    if (!now) return;
    const text = JSON.stringify(now);
    if (text === last) return;
    last = text;
    report(now);
  };
  const later = () => {
    if (!queued) {
      queued = true;
      win.requestAnimationFrame(measure);
    }
    // Timed from the last change, not the first: a terminal that goes on
    // writing keeps its bar in sight, and only its quiet has to be measured.
    if (settle !== null && typeof win.clearTimeout === "function") win.clearTimeout(settle);
    settle = win.setTimeout(measure, TERMINAL_SETTLE_MS);
  };
  win.addEventListener("resize", later);
  if (typeof win.MutationObserver === "function") new win.MutationObserver(later).observe(column, { childList: true, subtree: true });
  later();
  return later;
}

// The column the stand scrolls, how far, and after how many more snapshots
// drawn it is measured: the panel sends one a second (internal/server/ws.go).
const PROBED_STAGE = "done";
const PROBED_TOP = 120;
const PROBED_RENDERS = 2;

// probeColumnScroll scrolls board's done column once it is drawn taller than its
// room, and reports where that column is after the board has drawn its columns
// twice more: a column drawn anew at its top is one no one can read to its end.
// Nothing is reported for a board whose column never grows taller than its room.
export function probeColumnScroll(win, board, report) {
  let asked = false;
  let renders = 0;
  const column = () => board.querySelector(`:scope > .kcol[data-stage="${PROBED_STAGE}"]`);
  const observer = new win.MutationObserver(() => {
    const c = column();
    if (!asked) {
      if (!c || c.scrollHeight <= c.clientHeight) return;
      c.scrollTop = PROBED_TOP;
      asked = true;
      return;
    }
    renders += 1;
    if (renders < PROBED_RENDERS) return;
    observer.disconnect();
    report({ report: "columnScroll", stage: PROBED_STAGE, asked: PROBED_TOP, renders, scrollTop: c ? c.scrollTop : null });
  });
  observer.observe(board, { childList: true });
}

// --- the band the window is dragged by ----------------------------------------
//
// The window's own word on the band is in its frame report (standcheck): a
// height, and whether a press in the middle of it lands on the band. That says
// the band is gone but never what took it, and on v0.12.0 what took it was one
// element nobody suspected -- #sheet-scrim, the dimmed board under an open
// sheet, covering the whole column from y = 0 (T-079). This is the page's side
// of the same number: the height it reports, and for every row the window looks
// at, the first point across the width that is not the page's ground, with its
// x and what stands there.

// The same grid web/js/topband.js sweeps, so the report names the points the
// window's own measurement stops at and no others.
const BAND_ROW = 4;
const BAND_COLUMN = 24;

export function topBandReport(win, { max = TOP_BAND_MAX } = {}) {
  const doc = win.document;
  const width = win.innerWidth;
  const rows = [];
  let height = max;
  for (let y = 0; y < max; y += BAND_ROW) {
    let stood = null;
    for (let x = BAND_COLUMN / 2; x < width; x += BAND_COLUMN) {
      const el = doc.elementFromPoint(x, y);
      if (isGround(el)) continue;
      stood = { y, x, tag: el.tagName, id: el.id || "", class: typeof el.className === "string" ? el.className : "" };
      break;
    }
    if (!stood) continue;
    if (rows.length === 0) height = y;
    rows.push(stood);
  }
  const open = ["session-panel", "card-panel", "reader-panel"].filter((id) => doc.getElementById(id)?.hidden === false);
  return { report: "topband", width, max, height, sheetOpen: open, rows };
}

// watchTopBand reports the band whenever anything in the centre column changes:
// a sheet opening is what this is here for.
export function watchTopBandOnStand(win, centre, report) {
  return watch(win, centre, { childList: true, subtree: true, attributes: true }, () => topBandReport(win), report);
}

// watchBoardScroll reports board's scrolling whenever the board draws its
// columns, and whenever the returned function is called -- after the window's
// insets change.
export function watchBoardScroll(win, board, report) {
  return watch(win, board, { childList: true }, () => boardScrollReport(win, board), report);
}
