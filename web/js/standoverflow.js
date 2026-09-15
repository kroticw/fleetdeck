// web/js/standoverflow.js
//
// What a side surface says of its fit in the fleetdeck window's log on a CI
// stand, and nowhere else (window.fleetdeckHost.stand): which of its boxes do
// not fit, and whether the page itself is wider than its web view. Folded, the
// surface is a 48 px strip, and it says too what the strip shows and where its
// unfold control is. v0.10.2's dev build ran the sessions counters off that
// strip's edge, and scrolled the orchestrator's page sideways under its strip;
// a screenshot shows cut text or a scroll bar, and only the page can say which
// box is at fault.

// Layout rounds to fractions of a pixel: a box that much past an edge, or that
// much wider than its room, is not cut text.
const SLACK_PX = 1;

const tenth = (n) => Math.round(n * 10) / 10;

// describe names a box the way a stylesheet would: its tag and its classes. An
// SVG element's className is not a string but an animated one.
function describe(el) {
  const name = typeof el.className === "string" ? el.className : (el.className?.baseVal ?? "");
  const classes = name.trim().split(/\s+/).filter(Boolean);
  return [el.tagName.toLowerCase(), ...classes].join(".");
}

// overflowReport is what column's surface, named surface, in win says of its
// fit: whether the column is folded, how wide the surface and its page are,
// and every drawn box that is wider than the room it gives its content or that
// reaches past the surface's left or right edge. Folded, also every drawn box
// the strip shows, and the unfold control's box and whether a press at its
// middle reaches it (null with none drawn).
export function overflowReport(win, column, surface) {
  const doc = win.document;
  const width = doc.documentElement.clientWidth;
  const folded = column.dataset.folded === "1";
  const overflowing = [];
  const shown = [];
  for (const el of doc.body.querySelectorAll("*")) {
    if (el.getClientRects().length === 0) continue;
    const box = el.getBoundingClientRect();
    const wider = el.scrollWidth > el.clientWidth + SLACK_PX;
    const past = box.left < -SLACK_PX || box.right > width + SLACK_PX;
    if (wider || past) {
      overflowing.push({
        element: describe(el),
        scrollWidth: el.scrollWidth,
        clientWidth: el.clientWidth,
        left: tenth(box.left),
        right: tenth(box.right),
      });
    }
    if (folded && box.right > box.left && box.bottom > box.top && box.right > 0 && box.left < width) shown.push(describe(el));
  }
  const report = { surface, report: "overflow", folded, width, scrollWidth: doc.documentElement.scrollWidth, overflowing };
  if (!folded) return report;
  report.shown = shown;
  report.unfold = null;
  const unfold = column.querySelector(".col-size-unfold");
  if (unfold && unfold.getClientRects().length > 0) {
    const box = unfold.getBoundingClientRect();
    const hit = doc.elementFromPoint((box.left + box.right) / 2, (box.top + box.bottom) / 2);
    report.unfold = {
      left: tenth(box.left),
      top: tenth(box.top),
      right: tenth(box.right),
      bottom: tenth(box.bottom),
      reachable: hit !== null && (hit === unfold || unfold.contains(hit)),
    };
  }
  return report;
}

// watchOverflow reports the surface's fit once laid out, on a resize, whenever
// anything in the page is drawn anew or the column folds or unfolds; at most
// once a frame, and only when the report changed.
export function watchOverflow(win, column, surface, report) {
  let last = "";
  let queued = false;
  const measure = () => {
    queued = false;
    const now = overflowReport(win, column, surface);
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
