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
