// web/js/standreport.js
//
// What the board says of itself in the fleetdeck window's log on a CI stand, and
// nowhere else (window.fleetdeckHost.stand): numbers a screenshot cannot prove.
// The first is whether the strip under the board is #board's horizontal
// scrollbar -- how much wider its content is than it, and how tall the bar
// under it is.

// boardScrollReport is what board (#board) says of its horizontal scrolling;
// style is its computed style.
export function boardScrollReport(board, style) {
  const border = (parseFloat(style.borderTopWidth) || 0) + (parseFloat(style.borderBottomWidth) || 0);
  return {
    scrollWidth: board.scrollWidth,
    clientWidth: board.clientWidth,
    scrollbarHeight: board.offsetHeight - board.clientHeight - border,
    overflowX: style.overflowX,
  };
}

// watchBoardScroll reports board's scrolling once the page has laid it out,
// whenever the window is resized, and whenever the returned function is called
// -- after the window's insets change -- at most once a frame, and only when
// the report differs from the last one.
export function watchBoardScroll(win, board, report) {
  let last = "";
  let queued = false;
  const measure = () => {
    queued = false;
    const now = boardScrollReport(board, win.getComputedStyle(board));
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
  later();
  return later;
}
