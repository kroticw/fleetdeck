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

import { t } from "./i18n.js";
import { createColumnWidth, MIN_PIXELS } from "./columnwidth.js";

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
 */
export function mountColumnGrip(root, width) {
  const main = root.parentElement;
  if (!main) return null;

  const grip = document.createElement("div");
  grip.className = "col-grip";
  grip.setAttribute("role", "separator");
  grip.setAttribute("aria-orientation", "vertical");
  grip.setAttribute("aria-label", t("column_drag"));
  grip.setAttribute("title", t("column_drag"));
  main.insertBefore(grip, root.nextSibling);

  // A drag that never ends is the classic failure here: the pointer is
  // released over another window, no "up" arrives, and the interface stays
  // in drag mode for good. Pointer capture is what prevents it — the
  // element keeps receiving events wherever the pointer goes, and it gets
  // pointerup and pointercancel both. `finish` is idempotent so every one
  // of the three ways a drag can end lands in the same place.
  let dragging = false;

  const widthFrom = (clientX) => {
    const bounds = main.getBoundingClientRect();
    const available = bounds.width;
    if (available <= 0) return null;
    // Where the column's right edge would be if it followed the pointer.
    const pixels = clientX - root.getBoundingClientRect().left;
    // The pixel floor as well as the percentage one. On a narrow window a
    // percentage floor is a handful of pixels — "not zero" and useless — so
    // the two are applied together and the stricter one wins.
    const floored = Math.max(MIN_PIXELS, pixels);
    return (floored / available) * 100;
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
 * columnwidth.js's ORCHESTRATOR_KEYS/SESSIONS_KEYS); `onChange` runs after
 * the DOM already matches the new state, for side effects beyond the
 * column's own box.
 *
 * Returns `width` — the columnwidth.js handle, for a caller whose own
 * fold/unfold buttons call `.fold()`/`.unfold()` directly and whose other
 * logic reads `.state().folded` — and `paint()`, which applies the
 * column's current state to the DOM. `paint()` is not called
 * automatically: call it once, after root holds whatever the width and
 * folded attribute are meant to act on, since a column folded before it has
 * content to hide has nothing for `data-folded` to hide.
 */
export function mountColumnResize(root, keys, onChange = () => {}) {
  let grip = null;
  const width = createColumnWidth((state) => applyColumnState(root, grip, state, onChange), keys);
  grip = mountColumnGrip(root, width);
  return { width, paint: () => applyColumnState(root, grip, width.state(), onChange) };
}
