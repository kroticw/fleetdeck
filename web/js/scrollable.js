// The scroll indicator, shared.
//
// macOS draws an overlay scrollbar that stays invisible until the pointer is
// already over the box, so a container whose content runs past its right edge
// looks, at rest, exactly like a container whose content was cut short. The
// reader sees a clipped last column and has nothing telling them there is more
// of it. This adds a class the stylesheet turns into a fade at that edge, and
// only while scrolling right would genuinely reveal something new.
//
// It lives here rather than in board.js because the panel has more than one
// place that scrolls sideways. It was written for the board, applied to the
// board, and left the other four -- a markdown table and three code blocks --
// with the same gap it was written to close. A mechanism that exists once and
// is applied once is not the same as a mechanism that is applied everywhere it
// belongs.

export const SCROLLABLE_CLASS = "is-scrollable";

// Sub-pixel layout rounding, not a real gap: at the far end scrollWidth can sit
// a fraction of a pixel past scrollLeft + clientWidth with nothing there.
const SCROLL_END_SLACK_PX = 1;

// markScrollable is a measurement, not a guess about typical widths: "scrolling
// right now would show something new", which is narrower than "the content is
// wider than the box" -- the latter stays true however far you have scrolled,
// and would fade the final column forever.
export function markScrollable(el) {
  const more = el.scrollLeft + el.clientWidth < el.scrollWidth - SCROLL_END_SLACK_PX;
  el.classList.toggle(SCROLLABLE_CLASS, more);
  return more;
}

// Every match, not the first: a card body holds as many tables and code blocks
// as its author wrote, and each one scrolls on its own.
export function markScrollablesWithin(root, selector) {
  for (const el of root.querySelectorAll(selector)) markScrollable(el);
}

const watched = new WeakMap();

// watchScrollables keeps the mark current for boxes that come and go with each
// render. Two events matter and neither is a render: scrolling one of them to
// its end is the thing that takes the mark off, and a window resize crosses the
// fits/doesn't boundary with no new snapshot behind it.
//
// The scroll listener sits on the container in the capture phase because scroll
// does not bubble -- a listener per box would have to be re-attached on every
// render, and re-attaching is how a panel ends up firing the same handler N
// times after N renders. One listener per (root, selector), recorded here so a
// second call is a no-op rather than a duplicate.
export function watchScrollables(root, selector) {
  const seen = watched.get(root);
  if (seen?.has(selector)) return;
  if (seen) seen.add(selector);
  else watched.set(root, new Set([selector]));

  root.addEventListener("scroll", () => markScrollablesWithin(root, selector), true);
  window.addEventListener("resize", () => markScrollablesWithin(root, selector));
}

// watchSelf is the same for a container that scrolls itself rather than holding
// boxes that do -- the board.
export function watchSelf(el) {
  el.addEventListener("scroll", () => markScrollable(el));
  window.addEventListener("resize", () => markScrollable(el));
}
