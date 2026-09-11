// The part of a resizable, foldable column that touches the DOM: the drag
// grip and applying web/js/columnwidth.js's state to the column element.
// columnwidth.js owns the width and folded state and where it is
// remembered; this owns turning that state into a CSS variable, an
// attribute, and a grip a pointer can drag — the same mechanism for every
// column that has one, first built for the orchestrator column and now
// shared with the session list, because two near-identical copies of a drag
// handle are worse than a list that does not resize at all: they drift on
// the very next edit to either one, in a way no test catches, and a person
// feels the mismatch under their hand before anyone reads a diff.
//
// The fold/unfold buttons themselves (.col-size in web/app.css) are NOT
// built here. The orchestrator column builds its whole frame as DOM nodes
// and can hold one permanently; the session list rebuilds its entire
// innerHTML on every snapshot (web/js/sessions.js), and a node parked
// inside root would be destroyed the next time that string is written —
// the innerHTML setter replaces every child, not only the ones the module
// itself put there. So each caller builds that strip in whatever shape its
// own rendering already uses, sharing the class names and i18n keys rather
// than the construction: web/app.css's rules act on the class names alone
// and do not care which way a node carrying them came to exist.
//
// `side` is the one thing that is NOT the same between columns, because a
// column anchored to the window's left edge and one anchored to its right
// edge are mirror images of each other, not two instances of the same
// shape: which edge stays put while the other moves, which way the drag
// handle's pixels are measured, and which way "away" points are all
// reversed. This module takes `side` as a parameter of the one mechanism
// rather than becoming two mechanisms — the operator's own words, after
// the session list first reused the orchestrator's controls unmirrored and
// every one of them pointed and sat the wrong way.

import { t } from "./i18n.js";
import { createColumnWidth, MIN_PIXELS } from "./columnwidth.js";

// widthPercentFrom is the pure arithmetic behind a drag: given where the
// pointer is and where the track and the column's own edges are, the
// percentage width that keeps the column's edge anchored to its own side of
// the window fixed while the edge under the pointer follows it.
//
// Pulled out of the pointermove handler and exported so the direction for
// each side can be proven with plain numbers in a test — this project's
// test harness has no real layout engine behind it (see
// web/tests/fake-dom.js's own header), so `getBoundingClientRect` inside a
// test returns nothing meaningful and the sign of this formula was, until
// now, answerable only by a browser. A left column's LEFT edge is the one
// anchored to the window, so its width is measured from that edge to the
// pointer — moving the pointer right widens it. A right column's RIGHT edge
// is anchored instead, so its width is measured from the pointer to THAT
// edge — moving the pointer LEFT widens it. Getting this sign wrong is
// silent: the grip drags, the column resizes, and it resizes backwards.
export function widthPercentFrom({ side, clientX, mainWidth, rootLeft, rootRight }) {
  if (mainWidth <= 0) return null;
  const pixels = side === "right" ? rootRight - clientX : clientX - rootLeft;
  // The pixel floor as well as the percentage one. On a narrow window a
  // percentage floor is a handful of pixels — "not zero" and useless — so
  // the two are applied together and the stricter one wins.
  const floored = Math.max(MIN_PIXELS, pixels);
  return (floored / mainWidth) * 100;
}

// applyColumnState is what every column does with its own width/folded
// state once columnwidth.js decides it: the state a browser is told to
// paint. It never decides the state itself and never decides what happens
// beyond the column's own box — `onChange` is for that (the orchestrator
// holds a terminal open only while its column is not folded, and closes it
// from there; a caller with nothing further to do can leave it out).
function applyColumnState(root, grip, state, onChange) {
  const { width: value, folded } = state;
  root.style.setProperty("--col-width", value);
  // An attribute rather than a class, so the stylesheet says what a folded
  // column looks like in one place and this module never decides that.
  if (folded) root.dataset.folded = "1";
  else delete root.dataset.folded;
  // The grip is not a child of root — see mountColumnGrip — so it is told
  // separately, and it is what hides itself when there is no edge to pull.
  if (grip) grip.hidden = folded;
  onChange(state);
}

/**
 * mountColumnGrip creates the drag handle for `root` as a sibling inside
 * `root.parentElement`, wires it to `width` (a columnwidth.js handle), and
 * returns the grip element — or null if root has no parent to hang a
 * sibling on, in which case the column still works; it just has no edge to
 * grab.
 *
 * A sibling, not a child of root: .col carries overflow: auto, and an
 * absolutely positioned child of a scrolling box scrolls away with the
 * content. Nothing else in the page looks for this element; if a caller
 * stops calling this, the grip stops existing, and the column simply loses
 * its handle rather than half of something staying behind.
 *
 * `side` decides which edge of `root` the grip sits at, not only the sign
 * of the drag math above: a left column's resizable edge faces the centre,
 * i.e. is the edge AFTER it in `main`; a right column's faces the centre
 * from the other direction, i.e. is the edge BEFORE it. Put the grip at the
 * wrong one and it ends up pinned against the outer edge of the window,
 * where a person still finds it in the DOM but has nothing to grab it
 * against — this is exactly the defect the operator reported ("no resize
 * handle") before this parameter existed.
 */
export function mountColumnGrip(root, width, { side = "left" } = {}) {
  const main = root.parentElement;
  if (!main) return null;

  const grip = document.createElement("div");
  grip.className = "col-grip";
  grip.setAttribute("role", "separator");
  grip.setAttribute("aria-orientation", "vertical");
  grip.setAttribute("aria-label", t("column_drag"));
  grip.setAttribute("title", t("column_drag"));
  if (side === "right") main.insertBefore(grip, root);
  else main.insertBefore(grip, root.nextSibling);

  // A drag that never ends is the classic failure here: the pointer is
  // released over another window, no "up" arrives, and the interface stays
  // in drag mode for good. Pointer capture is what prevents it — the
  // element keeps receiving events wherever the pointer goes, and it gets
  // pointerup and pointercancel both. `finish` is idempotent so every one
  // of the three ways a drag can end lands in the same place.
  let dragging = false;

  const widthFrom = (clientX) => {
    const mainBounds = main.getBoundingClientRect();
    const rootBounds = root.getBoundingClientRect();
    return widthPercentFrom({
      side,
      clientX,
      mainWidth: mainBounds.width,
      rootLeft: rootBounds.left,
      rootRight: rootBounds.right,
    });
  };

  const finish = (event) => {
    if (!dragging) return;
    dragging = false;
    grip.classList.remove("is-dragging");
    document.body.classList.remove("is-resizing");
    if (event && grip.hasPointerCapture?.(event.pointerId)) grip.releasePointerCapture(event.pointerId);
    // Storage is written once, at the end. A drag across the screen is a
    // hundred moves and one outcome.
    width.remember();
  };

  grip.addEventListener("pointerdown", (event) => {
    if (event.button !== undefined && event.button !== 0) return; // left button only
    dragging = true;
    grip.classList.add("is-dragging");
    // On <body> rather than on the grip: the cursor has to stay col-resize
    // while the pointer is anywhere on the page, and text must not select
    // under it mid-drag.
    document.body.classList.add("is-resizing");
    grip.setPointerCapture?.(event.pointerId);
    event.preventDefault?.();
  });

  grip.addEventListener("pointermove", (event) => {
    if (!dragging) return;
    const percent = widthFrom(event.clientX);
    if (percent !== null) width.setPercent(percent);
    event.preventDefault?.();
  });

  grip.addEventListener("pointerup", finish);
  grip.addEventListener("pointercancel", finish);
  // Belt and braces for the case pointer capture does not cover: the window
  // itself losing focus mid-drag. Ending the drag is always safe — the
  // width on screen is already the width being kept.
  window.addEventListener("blur", () => finish(null));

  return grip;
}

/**
 * mountColumnResize is the whole device for one column: columnwidth.js's
 * state, the grip that drags it, and applying both to `root`. `keys` picks
 * which column's storage this instance reads and writes (see
 * columnwidth.js's ORCHESTRATOR_KEYS/SESSIONS_KEYS); `side` — `"left"`
 * (the default, and the orchestrator column's own — unchanged behaviour)
 * or `"right"` — picks which edge of the window this column is anchored to,
 * deciding the grip's position and drag direction (see mountColumnGrip and
 * widthPercentFrom above); `onChange` runs after the DOM already matches
 * the new state, for side effects beyond the column's own box.
 *
 * Returns `width` — the columnwidth.js handle, for a caller whose own
 * fold/unfold buttons call `.fold()`/`.unfold()` directly and whose other
 * logic reads `.state().folded` — and `paint()`, which applies the
 * column's current state to the DOM. `paint()` is not called
 * automatically: call it once, after root holds whatever the width and
 * folded attribute are meant to act on, since a column folded before it has
 * content to hide has nothing for `data-folded` to hide.
 */
export function mountColumnResize(root, keys, { side = "left", onChange = () => {} } = {}) {
  let grip = null;
  const width = createColumnWidth((state) => applyColumnState(root, grip, state, onChange), keys);
  grip = mountColumnGrip(root, width, { side });
  return { width, paint: () => applyColumnState(root, grip, width.state(), onChange) };
}
