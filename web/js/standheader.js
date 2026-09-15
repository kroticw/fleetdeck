// web/js/standheader.js
//
// What the orchestrator surface says of its header in the fleetdeck window's log
// on a CI stand, and nowhere else (window.fleetdeckHost.stand): where its row and
// its brand are centred, from the surface's top, and whether the window is in
// full screen. The window measures the line its buttons are centred on itself;
// scripts/standcheck holds the two together. A screenshot shows a brand a couple
// of points off that line and cannot prove it is not.

const tenth = (n) => Math.round(n * 10) / 10;
const middle = (rect) => tenth(rect.top + rect.height / 2);

// headerLineReport is what header (#header) in win says of where it is centred.
// A header with no brand yet leaves brandCenter null.
export function headerLineReport(win, header) {
  const brand = header.querySelector(".brand");
  return {
    surface: "orchestrator",
    headerRowCenter: middle(header.getBoundingClientRect()),
    brandCenter: brand ? middle(brand.getBoundingClientRect()) : null,
    fullscreen: win.document.documentElement.dataset.fullscreen === "1",
  };
}

// watchHeaderLine reports header once laid out, on a resize, whenever the header
// draws its parts or the page's own attributes change -- the window's word on
// its buttons and on full screen -- and when the returned function is called;
// at most once a frame, and only when the report changed.
export function watchHeaderLine(win, header, report) {
  let last = "";
  let queued = false;
  const measure = () => {
    queued = false;
    const now = headerLineReport(win, header);
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
    new win.MutationObserver(later).observe(header, { childList: true, subtree: true });
    new win.MutationObserver(later).observe(win.document.documentElement, { attributes: true });
  }
  later();
  return later;
}
