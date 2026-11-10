// web/js/theme.js
//
// The panel already reads a light or dark system theme correctly — app.css's
// :root carries `color-scheme: light dark` and every surface is a token, not
// a hardcoded colour. This module is the one thing that theme mechanism does
// not give a person on its own: a way to pin one theme regardless of what the
// system says. The choice is the panel's (config ui.theme, set in the
// settings); it is kept here too only so a reload opens on it before the
// first snapshot arrives.
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

// The rest of the look (config ui.accent, ui.glass, ui.terminal_background):
// attributes on <html> app.css reads. Unset is the default — indigo, the
// window's own glass, the theme's terminal ground — and a value the page does
// not know is the default too, so a typo never paints half a theme.
const LOOK_KEY = "fleetdeck-look";
export const ACCENTS = ["indigo", "blue", "teal", "violet", "rose", "graphite"];
const LOOK = [
  { field: "accent", attr: "data-accent", values: ACCENTS, fallback: ACCENTS[0] },
  { field: "glass", attr: "data-glass-level", values: ["reduced", "opaque"], fallback: null },
  { field: "terminalBackground", attr: "data-terminal-bg", values: ["dark"], fallback: null },
];

function lookOf(look) {
  const out = {};
  for (const l of LOOK) out[l.field] = l.values.includes(look?.[l.field]) ? look[l.field] : l.fallback;
  return out;
}

function readLook() {
  try {
    return lookOf(JSON.parse(localStorage.getItem(LOOK_KEY) ?? "{}"));
  } catch {
    return lookOf({});
  }
}

function applyLook(look) {
  const root = document.documentElement;
  for (const l of LOOK) {
    if (look[l.field]) root.setAttribute(l.attr, look[l.field]);
    else root.removeAttribute(l.attr);
  }
}

/**
 * setConfiguredLook shows the accent, glass level and terminal ground the
 * configuration holds ({accent, glass, terminalBackground}, each unset for its
 * default), keeps them for the next load, and reports whether that changed
 * what was shown.
 */
export function setConfiguredLook(look) {
  const next = lookOf(look);
  const root = document.documentElement;
  const changed = LOOK.some((l) => root.getAttribute(l.attr) !== next[l.field]);
  try {
    localStorage.setItem(LOOK_KEY, JSON.stringify(next));
  } catch {
    // Shown now; the next load opens on the default until a snapshot says.
  }
  applyLook(next);
  return changed;
}

/** currentAccent is the accent preset shown now. */
export function currentAccent() {
  return document.documentElement.getAttribute("data-accent") ?? ACCENTS[0];
}

export function initTheme() {
  apply(readStored());
  applyLook(readLook());
}

export function currentTheme() {
  return readStored();
}

export function applyTheme(choice) {
  apply(choice === "light" || choice === "dark" ? choice : null);
}

/**
 * setConfiguredTheme takes the theme the panel's configuration holds (ui.theme:
 * "light", "dark", or empty to follow the system), shows it, and keeps it as
 * the one the next page load opens on before its first snapshot. It reports
 * whether that changed what was shown.
 */
export function setConfiguredTheme(theme) {
  const choice = theme === "light" || theme === "dark" ? theme : null;
  const changed = choice !== readStored() || document.documentElement.getAttribute("data-theme") !== choice;
  writeStored(choice);
  apply(choice);
  return changed;
}
