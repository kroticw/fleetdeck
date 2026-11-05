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

// The cycle a click walks: no override -> light -> dark -> no override.
export function initTheme() {
  apply(readStored());
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
