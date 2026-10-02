// web/js/windowdrag.js
//
// In the fleetdeck window a side surface's header is the window's title bar:
// the window's own is transparent, the surface's page runs under it, and the
// band the window is dragged by (topband.js) lies under the surface's web view,
// which takes every press first. So the page tells the window of a press on
// the header's ground -- the header itself, or what it marks as ground -- and
// the window drags itself as a title bar would, or, on two clicks, does what
// the system's "Double-click a window's title bar to" says
// (cmd/fleetdeck-window, frame_darwin.c). A press on a control in the header
// stays the control's. In a browser tab the window's binding is not defined,
// and a press is a press.

import { isGround } from "./topband.js";

export const WINDOW_DRAG_BINDING = "fleetdeckWindowDrag";

// titleBarPress is whether event, a mousedown on the header, is the window's:
// the main button, one or two clicks, on the header's ground, with drag -- the
// window's binding -- there to take it. Taken, the press is kept from the page:
// it would otherwise start a text selection under the moving window.
export function titleBarPress(event, drag) {
  if (typeof drag !== "function") return false;
  if (event.button !== 0 || (event.detail !== 1 && event.detail !== 2)) return false;
  if (!isGround(event.target)) return false;
  event.preventDefault();
  drag(event.detail);
  return true;
}
