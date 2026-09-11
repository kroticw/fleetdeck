// web/js/terminalfont.js
//
// How big a live terminal's type is, and the keys that change it: Cmd with =
// or + makes it bigger, Cmd with - smaller, Cmd+0 puts it back. The same keys
// Terminal.app and every browser use, and they are free in the fleetdeck
// window: its menu has no item on them, and WKWebView has no page zoom of its
// own to take them first (both measured, see the PR that added this file).
//
// The size is remembered per place a terminal is drawn, not once for all of
// them. What a bigger type costs is columns, and how many a size leaves
// depends on how wide the place is: the orchestrator column and the session
// panel's screen tab are different widths, so one number for both would be
// too big for one or too small for the other.
//
// What storage holds is treated the way columnwidth.js treats a width:
// absent is its own branch and never reaches Number(), anything that is not
// a finite number is the default, and every size is clamped, so no stored
// value can make a terminal unreadably small or a session absurdly narrow.

// The storage key of each place. Literal strings, because once an operator has
// chosen a size these are the entries holding it.
export const FONT_KEYS = {
  orchestrator: "fleetdeck-terminal-font-orchestrator",
  screen: "fleetdeck-terminal-font-screen",
};

// The size the terminal was built with before it could be changed, so nothing
// moves for anyone who never presses a key.
export const DEFAULT_FONT_SIZE = 12;
export const MIN_FONT_SIZE = 9;
export const MAX_FONT_SIZE = 24;

/** clampFontSize brings any number into the range, in whole pixels. */
export function clampFontSize(size) {
  return Math.min(MAX_FONT_SIZE, Math.max(MIN_FONT_SIZE, Math.round(size)));
}

function read(key) {
  try {
    return localStorage.getItem(key);
  } catch {
    // A private window, storage switched off, or no storage at all. The
    // terminal still works; the size just is not remembered.
    return null;
  }
}

/**
 * storedFontSize is the size remembered under `key`, or the default when there
 * is nothing trustworthy there. A terminal with no key has nowhere to remember
 * anything and always starts at the default.
 */
export function storedFontSize(key) {
  if (!key) return DEFAULT_FONT_SIZE;
  const raw = read(key);
  if (raw === null || String(raw).trim() === "") return DEFAULT_FONT_SIZE;
  const size = Number(String(raw).trim());
  if (!Number.isFinite(size)) return DEFAULT_FONT_SIZE;
  return clampFontSize(size);
}

/**
 * rememberFontSize writes a chosen size. Choosing the default forgets the
 * choice instead of writing it, so Cmd+0 leaves storage as a first run left it.
 */
export function rememberFontSize(key, size) {
  if (!key) return;
  try {
    if (size === DEFAULT_FONT_SIZE) localStorage.removeItem(key);
    else localStorage.setItem(key, String(size));
  } catch {
    // Same trade as reading: a remembered size is not worth an error.
  }
}

/**
 * fontStep is what a key event asks of the type: 1 bigger, -1 smaller, 0 back
 * to the default, or null when it is not one of these keys and belongs to the
 * terminal. Only the key going down counts, and only with Cmd alone: Ctrl
 * with these keys is something a terminal program can be sent, and Option
 * with Cmd is not what anyone presses to zoom.
 */
export function fontStep(event) {
  if (event.type !== "keydown" || !event.metaKey || event.ctrlKey || event.altKey) return null;
  if (event.key === "=" || event.key === "+") return 1;
  if (event.key === "-") return -1;
  if (event.key === "0") return 0;
  return null;
}
