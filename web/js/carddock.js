// web/js/carddock.js — where a card keeps the session that wrote its open
// document (T-091).
//
// Below the document (A) or beside it (B), the operator's choice, one for the
// whole window and remembered like the theme. Beside needs room: a sheet
// narrower than DOCK_RIGHT_MIN keeps the session below, and the choice is not
// forgotten for it -- the session goes back beside the document once the sheet
// is wide enough again. The sizes are shares of the sheet's stage, dragged by the
// grip between the two and written down only when the drag ends, the way
// columnwidth.js writes a column's width.

// DOCK_RIGHT_MIN is the narrowest stage, in CSS px, the session is kept beside
// the document in: about 340 for the document and 300 for the terminal.
export const DOCK_RIGHT_MIN = 640;

export const DOCK_KEYS = {
  place: "fleetdeck-card-session-dock",
  height: "fleetdeck-card-session-height-pct",
  width: "fleetdeck-card-session-width-pct",
};

// The share of the stage the session takes, in percent: [least, most, default].
const RANGE = { bottom: [25, 80, 55], right: [30, 60, 45] };

// effectiveDock is where the session is: beside only when chosen and the stage
// has room for it.
export function effectiveDock(chosen, stageWidth) {
  return chosen === "right" && stageWidth >= DOCK_RIGHT_MIN ? "right" : "bottom";
}

// clampDockSize holds a share to its place's range; anything that is not a
// number is the place's default.
export function clampDockSize(place, pct) {
  const [least, most, fallback] = RANGE[place === "right" ? "right" : "bottom"];
  const n = typeof pct === "number" ? pct : Number.parseFloat(pct);
  if (!Number.isFinite(n)) return fallback;
  return Math.min(most, Math.max(least, n));
}

function read(storage, key) {
  try {
    return storage?.getItem(key) ?? null;
  } catch {
    return null;
  }
}

// readDockPrefs is the remembered choice and sizes. A storage that refuses --
// a private window, a page without one -- gives the defaults.
export function readDockPrefs(storage = globalThis.localStorage, keys = DOCK_KEYS) {
  return {
    place: read(storage, keys.place) === "right" ? "right" : "bottom",
    height: clampDockSize("bottom", read(storage, keys.height)),
    width: clampDockSize("right", read(storage, keys.width)),
  };
}

export function writeDockPref(name, value, storage = globalThis.localStorage, keys = DOCK_KEYS) {
  try {
    storage?.setItem(keys[name], String(value));
  } catch {
    // A storage that refuses keeps the choice for this page only.
  }
}
