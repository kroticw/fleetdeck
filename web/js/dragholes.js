// web/js/dragholes.js — a side panel's controls in the window's top strip.
//
// In the fleetdeck window the capsule row's strip drags the window across its
// whole width, over the panels too (cmd/fleetdeck-window, the top band). A
// press there reaches the panel's page only where it has a control, and this
// is where: the rectangles of its buttons, links and fields that reach into the
// strip, in the page's own coordinates, told to the window whenever they move.

// The strip's height in points, the window's topDragHeight.
export const STRIP_HEIGHT = 52;

const CONTROLS = 'button, a[href], input, select, textarea, summary, [role="button"], [role="tab"], [contenteditable="true"]';

// controlRects is every visible control reaching into the top `height` points,
// cut to them, rounded out to whole points.
export function controlRects(doc, height = STRIP_HEIGHT) {
  const out = [];
  for (const node of doc.querySelectorAll(CONTROLS)) {
    const r = node.getBoundingClientRect();
    if (r.width <= 0 || r.height <= 0 || r.top >= height || r.top + r.height <= 0) continue;
    const top = Math.max(0, Math.floor(r.top));
    const bottom = Math.min(height, Math.ceil(r.top + r.height));
    out.push({ x: Math.floor(r.left), y: top, w: Math.ceil(r.width), h: bottom - top });
  }
  return out;
}

// watchDragHoles tells send the rectangles now and whenever the page changes
// or resizes, each set once. It returns the report, for a caller that knows of
// a change the observers do not see.
export function watchDragHoles(doc, send, { height = STRIP_HEIGHT, observe = true } = {}) {
  let told = "";
  const report = () => {
    const rects = controlRects(doc, height);
    const json = JSON.stringify(rects);
    if (json === told) return;
    told = json;
    send(rects);
  };
  if (observe) {
    let queued = false;
    const soon = () => {
      if (queued) return;
      queued = true;
      requestAnimationFrame(() => {
        queued = false;
        report();
      });
    };
    new MutationObserver(soon).observe(doc.documentElement, { subtree: true, childList: true, attributes: true });
    new ResizeObserver(soon).observe(doc.documentElement);
  }
  report();
  return report;
}
