// Motion for a box the panel rewrites whole (T-159).
//
// The board and the session list replace their innerHTML on every snapshot,
// so a card that changes column is a new node in a new place, with nothing a
// CSS transition could start from. capture reads where every keyed node is
// before the rewrite; play, after it, starts each node from where it was and
// lets it travel to where it is now (First, Last, Invert, Play).
//
// Only a node whose place changed is moved: another group (a card's column)
// or another position in its group (a session that rose to the top) — or one
// still on its way when the box was rewritten again. The board is drawn again
// right after a drop and by every snapshot; a card mid-flight is read where
// it is on screen and carried on from there, not dropped at its end. A node
// pushed by the layout — a taller card above it, a narrower window — kept its
// place, and following it would be motion that means nothing. A move that
// starts or ends out of sight is not played either: there is nothing to follow
// there. A node that was not there before fades in, except on the first
// drawing, when every node is new. With reduced motion nothing moves at all.

export const MOVE_MS = 220;
export const ENTER_MS = 160;
const EASING = "cubic-bezier(0.2, 0.7, 0.2, 1)";
// The name a move is played under, so a redraw tells it from the page's own
// animations: the bar on a card being written runs the whole time.
const ID = "flip";

// capture is where every node in nodes is: its rect as drawn on screen, its
// group, its position in that group, whether it is still moving, and the box
// it was seen through (clipOf, when given), by key. Nodes without a key are
// left out.
export function capture(nodes, { keyOf, groupOf, clipOf }) {
  const at = new Map();
  const counts = new Map();
  for (const n of nodes) {
    const key = keyOf(n);
    if (!key) continue;
    const group = groupOf(n);
    const index = counts.get(group) ?? 0;
    counts.set(group, index + 1);
    const moving = Boolean(n.getAnimations?.().some((a) => a.id === ID && a.playState === "running"));
    at.set(key, { rect: n.getBoundingClientRect?.(), group, index, moving, clip: n.getBoundingClientRect ? clipOf?.(n) : undefined });
  }
  return at;
}

function overlaps(a, b) {
  return a.left < b.right && a.right > b.left && a.top < b.bottom && a.bottom > b.top;
}

// play moves the nodes of the new drawing from where capture saw them.
// viewport is the window's box; clipOf, when given, the box a node is clipped
// to (its scrolling column). Where a node was is judged by the box it was in
// then, where it is by the box it is in now: a card that changed column
// changed boxes.
export function play(before, nodes, { keyOf, groupOf, viewport, clipOf, reduced }) {
  if (reduced) return;
  const now = capture(nodes, { keyOf, groupOf, clipOf });
  const seen = (rect, clip) => overlaps(rect, viewport) && (!clip || overlaps(rect, clip));
  for (const n of nodes) {
    if (typeof n.animate !== "function") continue;
    const key = keyOf(n);
    if (!key) continue;
    const was = before.get(key);
    const is = now.get(key);
    if (!was) {
      if (before.size > 0 && seen(is.rect, is.clip)) {
        n.animate([{ opacity: 0, transform: "scale(0.96)" }, { opacity: 1, transform: "none" }], { duration: ENTER_MS, easing: "ease-out" });
      }
      continue;
    }
    if (was.group === is.group && was.index === is.index && !was.moving) continue;
    if (!was.rect || !seen(was.rect, was.clip) || !seen(is.rect, is.clip)) continue;
    const dx = was.rect.left - is.rect.left;
    const dy = was.rect.top - is.rect.top;
    if (Math.abs(dx) < 1 && Math.abs(dy) < 1) continue;
    n.animate([{ transform: `translate(${dx}px, ${dy}px)` }, { transform: "none" }], { id: ID, duration: MOVE_MS, easing: EASING });
  }
}

// reducedMotion is the system's answer, asked each time: it can change while
// the page is open.
export function reducedMotion() {
  return Boolean(globalThis.matchMedia?.("(prefers-reduced-motion: reduce)").matches);
}

// viewportOf is the window's own box.
export function viewportOf() {
  return { left: 0, top: 0, right: globalThis.innerWidth ?? Infinity, bottom: globalThis.innerHeight ?? Infinity };
}
