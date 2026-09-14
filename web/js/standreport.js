// web/js/standreport.js
//
// What the board says of itself in the fleetdeck window's log on a CI stand, and
// nowhere else (window.fleetdeckHost.stand): numbers a screenshot cannot prove.
// The first is whether the strip under the board is #board's horizontal
// scrollbar -- how much wider its content is than it, and how tall the bar
// under it is. The second is where that box and its bar are, against the
// sessions panel's left edge, and whether the last column can be scrolled out
// from under that panel -- answered by the board itself, as true or false.

// Sub-pixel layout rounding, not a column under the panel.
const EDGE_SLACK_PX = 1;

const tenth = (n) => Math.round(n * 10) / 10;

// boardScrollReport is what board (#board) in win says of its horizontal
// scrolling. The sessions panel's left edge is the page's width less the inset
// the window sends for it; a page the window sent no inset leaves it, and every
// answer that needs it, null. So does a board with no columns for the last one.
export function boardScrollReport(win, board) {
  const style = win.getComputedStyle(board);
  const root = win.document.documentElement;
  const border = (parseFloat(style.borderTopWidth) || 0) + (parseFloat(style.borderBottomWidth) || 0);
  const box = board.getBoundingClientRect();
  const inset = parseFloat(win.getComputedStyle(root).getPropertyValue("--host-inset-content-right"));
  const sessionsLeft = Number.isFinite(inset) ? root.clientWidth - inset : null;
  // Scrolling moves every column by the same distance, so where the last one
  // ends at the end of the scroll is where it ends now, moved by what is left.
  const columns = board.querySelectorAll(":scope > .kcol");
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
  };
}

// listScrollReport is what the sessions surface's list (#sessions) in win says
// of its vertical scrolling: how much taller its content is than it, and how wide
// the bar beside it is. macOS draws a classic 15 px bar for a mouse; the islands
// ask WebKit for 6 (web/app.css).
export function listScrollReport(win, list) {
  const style = win.getComputedStyle(list);
  const border = (parseFloat(style.borderLeftWidth) || 0) + (parseFloat(style.borderRightWidth) || 0);
  return {
    surface: "sessions",
    scrollHeight: list.scrollHeight,
    clientHeight: list.clientHeight,
    scrollbarWidth: list.offsetWidth - list.clientWidth - border,
    overflowY: style.overflowY,
  };
}

// watchListScroll reports list's scrolling as watchBoardScroll does the
// board's: once laid out, on a resize, whenever the list draws its rows, and
// when the returned function is called; at most once a frame, and only when the
// report changed.
export function watchListScroll(win, list, report) {
  let last = "";
  let queued = false;
  const measure = () => {
    queued = false;
    const now = listScrollReport(win, list);
    const text = JSON.stringify(now);
    if (text === last) return;
    last = text;
    report(now);
  };
  const later = () => {
    if (queued) return;
    queued = true;
    win.requestAnimationFrame(measure);
  };
  win.addEventListener("resize", later);
  if (typeof win.MutationObserver === "function") new win.MutationObserver(later).observe(list, { childList: true });
  later();
  return later;
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
  const style = win.getComputedStyle(viewport);
  const border = (parseFloat(style.borderLeftWidth) || 0) + (parseFloat(style.borderRightWidth) || 0);
  return {
    surface: "orchestrator",
    viewportScrollbarWidth: viewport.offsetWidth - viewport.clientWidth - border,
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

// watchBoardScroll reports board's scrolling once the page has laid it out,
// whenever the window is resized, whenever the board draws its columns, and
// whenever the returned function is called -- after the window's insets change
// -- at most once a frame, and only when the report differs from the last one.
export function watchBoardScroll(win, board, report) {
  let last = "";
  let queued = false;
  const measure = () => {
    queued = false;
    const now = boardScrollReport(win, board);
    const text = JSON.stringify(now);
    if (text === last) return;
    last = text;
    report(now);
  };
  const later = () => {
    if (queued) return;
    queued = true;
    win.requestAnimationFrame(measure);
  };
  win.addEventListener("resize", later);
  if (typeof win.MutationObserver === "function") new win.MutationObserver(later).observe(board, { childList: true });
  later();
  return later;
}
