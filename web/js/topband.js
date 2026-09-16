// web/js/topband.js
//
// The fleetdeck window has no title bar of its own: its content runs under the
// transparent one, so a person drags the window by the empty band at the top of
// the page. The window cannot see the page, so the page tells it how far down
// from its top nothing is -- nothing to click, nothing to read -- and the
// window is dragged by that band and no more (cmd/fleetdeck-window,
// frame_darwin.c). A sheet, a banner, the new card form, a dimmed board, a
// document or cards scrolled up to the top: whatever is at the top shortens the
// band, down to nothing.
//
// The window runs this file in every page it shows -- the board, the start
// page, its own pages -- as a plain script with its exports taken off
// (topBandScript in cmd/fleetdeck-window). In a browser tab nothing runs it.

// TOP_BAND_MAX is the tallest the band gets: the board's top inset, the height
// of the capsule row and the space around it (cmd/fleetdeck-window,
// geometry.go, boardInsetTop).
export const TOP_BAND_MAX = 64;

// Looked at every ROW points down and every COLUMN points across: a control is
// taller and wider than that.
const ROW = 4;
const COLUMN = 24;

// isGround is whether el is the page's own ground -- what a point shows when
// nothing is on it: the document, the body, main, or an element the page marks
// as ground. Anything else is something a person may mean to click or read.
export function isGround(el) {
  if (!el) return true;
  const tag = el.tagName;
  if (tag === "HTML" || tag === "BODY" || tag === "MAIN") return true;
  return typeof el.hasAttribute === "function" && el.hasAttribute("data-window-ground");
}

// freeTopHeight is how far down from the top, up to max, every point across
// width is ground. elementAt(x, y) is what is at a point, as
// document.elementFromPoint says.
export function freeTopHeight(elementAt, width, max = TOP_BAND_MAX) {
  for (let y = 0; y < max; y += ROW) {
    for (let x = COLUMN / 2; x < width; x += COLUMN) {
      if (!isGround(elementAt(x, y))) return y;
    }
  }
  return max;
}

// UNDRAWN_MS is how long a measurement waits for a frame that may never come
// before it is taken anyway: long enough that a page being drawn is measured in
// its frame and not twice, short enough that the band is there before a person
// reaches for the window.
const UNDRAWN_MS = 100;

// watchTopBand measures the band whenever the page may have changed under it,
// at most once a frame, and reports the height when it is not the one last
// reported. It returns the function that asks for a measurement.
//
// The measurement is asked for in a frame and on a timer, and taken by whichever
// comes first. A page that is not being drawn runs no frame callback: the window
// that takes over from an updated one comes up behind it, and its board page
// loads and stays with nothing of it on screen. Measured only in a frame, that
// page never measured at all and never asked again -- the frame it was waiting
// for stood in the way of every later measurement -- so the window was left with
// no band and could not be dragged anywhere (v0.11.0, T-076).
export function watchTopBand(win, report) {
  const doc = win.document;
  let last = -1;
  let queued = false;
  const measure = () => {
    if (!queued) return;
    queued = false;
    const height = freeTopHeight((x, y) => doc.elementFromPoint(x, y), win.innerWidth);
    if (height !== last) {
      last = height;
      report(height);
    }
  };
  const later = () => {
    if (queued) return;
    queued = true;
    win.requestAnimationFrame(measure);
    win.setTimeout(measure, UNDRAWN_MS);
  };
  win.addEventListener("resize", later);
  win.addEventListener("scroll", later, { capture: true, passive: true });
  win.addEventListener("transitionend", later);
  new win.MutationObserver(later).observe(doc, { subtree: true, childList: true, attributes: true, characterData: true });
  if (doc.readyState === "loading") {
    doc.addEventListener("DOMContentLoaded", later);
  } else {
    later();
  }
  return later;
}
