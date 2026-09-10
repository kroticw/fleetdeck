// web/js/theme.js
//
// The panel already reads a light or dark system theme correctly — app.css's
// :root carries `color-scheme: light dark` and every surface is a token, not
// a hardcoded colour. This module is the one thing that theme mechanism does
// not give a person on its own: a way to pin one theme regardless of what the
// system says, remembered across a reload.
//
// Nothing here decides what light or dark actually look like. That is
// app.css's [data-theme="light"|"dark"] blocks; this module only ever writes
// or clears that attribute on <html> and remembers the choice.

const STORAGE_KEY = "fleetdeck-theme";

// null means "no override, follow the system" — the third state alongside
// "light" and "dark", not an error case. A private window or a browser with
// storage disabled throws on either call; both are caught silently, because
// losing the remembered choice for this one session is a fair trade for not
// taking the whole page down over a preference.
function readStored() {
  try {
    return localStorage.getItem(STORAGE_KEY);
  } catch {
    return null;
  }
}

function writeStored(theme) {
  try {
    if (theme) localStorage.setItem(STORAGE_KEY, theme);
    else localStorage.removeItem(STORAGE_KEY);
  } catch {
    // The choice just will not survive a reload this session.
  }
}

function apply(theme) {
  if (theme) document.documentElement.setAttribute("data-theme", theme);
  else document.documentElement.removeAttribute("data-theme");
}

// The cycle a click walks: no override -> light -> dark -> no override.
const NEXT = { light: "dark", dark: null };

// Applies whatever choice was already remembered. Called once at startup, so
// a reload lands on the same theme instead of flashing the system default
// for one frame before a click handler runs.
export function initTheme() {
  apply(readStored());
}

// The remembered override (null | "light" | "dark"), for a caller that wants
// to render a label matching the state without walking the cycle itself.
export function currentTheme() {
  return readStored();
}

// Advances the cycle, applies it, remembers it, and returns the new state
// (null | "light" | "dark") so a caller can update a label immediately
// rather than waiting for the next redraw.
//
// `?? "light"` would be wrong here: NEXT.dark is the legitimate value
// `null` (dark's successor is "no override"), and `??` cannot tell that
// apart from "current is not a key of NEXT at all" (the no-override state
// itself, where current is also `null`) — both would fall through to the
// same default and dark could never cycle back to auto. The `in` check
// keeps those two nulls apart.
export function cycleTheme() {
  const current = readStored();
  const next = current && current in NEXT ? NEXT[current] : "light";
  writeStored(next);
  apply(next);
  return next;
}
