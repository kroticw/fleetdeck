// web/js/standoverflow.js
//
// What the sessions surface says of its fit in the fleetdeck window's log on a
// CI stand, and nowhere else (window.fleetdeckHost.stand): which of its boxes
// do not fit. Folded, the surface is a 48 px rail, and a screenshot of cut text
// cannot say which box is cut or by how much.

// Layout rounds to fractions of a pixel: a box that much past an edge, or that
// much wider than its room, is not cut text.
const SLACK_PX = 1;

const tenth = (n) => Math.round(n * 10) / 10;

// describe names a box the way a stylesheet would: its tag and its classes.
function describe(el) {
  const classes = String(el.className || "").trim().split(/\s+/).filter(Boolean);
  return [el.tagName.toLowerCase(), ...classes].join(".");
}

// overflowReport is what column's surface in win says of its fit: whether the
// column is folded, how wide the surface is, and every drawn box that is wider
// than the room it gives its content or that reaches past the surface's left or
// right edge.
export function overflowReport(win, column) {
  const doc = win.document;
  const width = doc.documentElement.clientWidth;
  const overflowing = [];
  for (const el of doc.body.querySelectorAll("*")) {
    if (el.getClientRects().length === 0) continue;
    const box = el.getBoundingClientRect();
    const wider = el.scrollWidth > el.clientWidth + SLACK_PX;
    const past = box.left < -SLACK_PX || box.right > width + SLACK_PX;
    if (!wider && !past) continue;
    overflowing.push({
      element: describe(el),
      scrollWidth: el.scrollWidth,
      clientWidth: el.clientWidth,
      left: tenth(box.left),
      right: tenth(box.right),
    });
  }
  return { surface: "sessions", report: "overflow", folded: column.dataset.folded === "1", width, overflowing };
}

// watchOverflow reports the surface's fit once laid out, on a resize, whenever
// anything in the page is drawn anew or the column folds or unfolds; at most
// once a frame, and only when the report changed.
export function watchOverflow(win, column, report) {
  let last = "";
  let queued = false;
  const measure = () => {
    queued = false;
    const now = overflowReport(win, column);
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
  if (typeof win.MutationObserver === "function") {
    new win.MutationObserver(later).observe(win.document.body, { childList: true, subtree: true, attributes: true, attributeFilter: ["data-folded"] });
  }
  later();
  return later;
}
