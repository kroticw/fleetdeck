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

import { authorState } from "./docauthor.js";
import { t } from "./i18n.js";
import { resumeSession } from "./api.js";
import { buildFontControls } from "./fontcontrols.js";
import { sessionMark } from "./initials.js";
import { createLiveTerminal } from "./liveterminal.js";
import { KEYS } from "./session.js";
import { FONT_KEYS } from "./terminalfont.js";

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

// pageStorage is the page's localStorage, or null where even reading it throws
// -- a browser with site data blocked does, on the property itself.
function pageStorage() {
  try {
    return globalThis.localStorage ?? null;
  } catch {
    return null;
  }
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
export function readDockPrefs(storage = pageStorage(), keys = DOCK_KEYS) {
  return {
    place: read(storage, keys.place) === "right" ? "right" : "bottom",
    height: clampDockSize("bottom", read(storage, keys.height)),
    width: clampDockSize("right", read(storage, keys.width)),
  };
}

export function writeDockPref(name, value, storage = pageStorage(), keys = DOCK_KEYS) {
  try {
    storage?.setItem(keys[name], String(value));
  } catch {
    // A storage that refuses keeps the choice for this page only.
  }
}

// --- the place ------------------------------------------------------------------

// The states in which the author has a terminal to show: a live session that is
// not the orchestrator (web/js/docauthor.js authorState).
const WITH_TERMINAL = new Set(["waiting", "working", "stalled"]);

function el(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}

function button(className, text) {
  const b = el("button", className, text);
  b.type = "button";
  return b;
}

// The width a ResizeObserver reports for el, to fn, now and on every change.
function observeWidth(el, fn) {
  fn(el.clientWidth ?? 0);
  const Observer = globalThis.ResizeObserver;
  if (typeof Observer !== "function") return () => {};
  const observer = new Observer((entries) => fn(entries[0]?.contentRect?.width ?? el.clientWidth ?? 0));
  observer.observe(el);
  return () => observer.disconnect();
}

/**
 * createCardDock mounts the place a card keeps its author's session in, in
 * host (the sheet's .card-dock), and keeps it until dispose.
 *
 * options:
 *   stage          the sheet's .card-stage: its width decides the place, and it
 *                  carries the place (data-dock), the choice (data-chosen) and
 *                  the size (--card-dock-size) for the page's CSS
 *   grip           the stage's .card-dock-grip, dragged to size the session
 *   storage, storageKeys  where the choice and the sizes are remembered (DOCK_KEYS)
 *   observe(el, fn) -> stop   the stage's width, ResizeObserver by default
 *   rect(el)       a box's place on the page, for the grip
 *   terminal(host, short, o)  createLiveTerminal by default
 *   pressKeys      the key row under the terminal (session.js KEYS)
 *   resume(short) -> Promise  bring a stopped session back (api.js resumeSession)
 *   toOrchestrator()          to the orchestrator's own terminal
 *   links, fontKey            handed to the terminal, as session.js does
 *   place, expand  a stand's forced choice and the session open; the place is
 *                  not remembered
 *
 * Returns { show(author, snapshot), dispose() }. show is called on every redraw
 * of the sheet and for every tab picked; it attaches a terminal only when the
 * place is open and the author is live, and again only when the author changes:
 * a folded handle holds no attach, so it resizes nobody's terminal.
 */
export function createCardDock(host, options) {
  const { stage, grip, pressKeys = KEYS, toOrchestrator = () => {}, resume = resumeSession } = options;
  const terminal = options.terminal ?? createLiveTerminal;
  const storage = options.storage ?? pageStorage();
  const keyNames = options.storageKeys ?? DOCK_KEYS;
  const rect = options.rect ?? ((node) => node.getBoundingClientRect());
  const observe = options.observe ?? observeWidth;
  const prefs = readDockPrefs(storage, keyNames);

  let chosen = options.place === "right" || options.place === "bottom" ? options.place : prefs.place;
  const sizes = { bottom: prefs.height, right: prefs.width };
  let width = 0;
  let open = options.expand === true;
  let author = null;
  let latest = null;
  let state = null;
  let live = null;
  let attached = "";
  let resuming = false;
  let resumeError = "";
  let streamError = "";

  // The frame, built once.
  const handle = el("div", "card-dock-handle");
  const dot = el("span", "card-dock-dot");
  dot.setAttribute("aria-hidden", "true");
  const who = el("span", "card-dock-id");
  // The name the session list shows, the operator's label first: the id alone
  // does not say which session this is.
  const named = el("span", "card-dock-name");
  const doing = el("span", "card-dock-state");
  const needs = el("span", "card-dock-needs");
  const places = el("div", "card-dock-places");
  places.setAttribute("role", "group");
  const toBottom = button("btn btn-sm card-dock-place card-dock-place-bottom", "⬓");
  toBottom.setAttribute("aria-label", t("dock_bottom"));
  toBottom.dataset.place = "bottom";
  const toRight = button("btn btn-sm card-dock-place card-dock-place-right", "◨");
  toRight.setAttribute("aria-label", t("dock_right"));
  toRight.dataset.place = "right";
  places.append(toBottom, toRight);
  const openClose = button("btn btn-sm card-dock-open");
  // The type's size, as in the session panel and the orchestrator column
  // (web/js/fontcontrols.js), there only while a terminal is.
  const fontButtons = buildFontControls({ onStep: (step) => live?.stepFont(step), buttonClass: "card-dock-font" });
  // Folded beside the document the place is drawn like the folded session
  // list: the round button that unfolds it and the session's two-letter mark
  // (web/js/initials.js), which opens it too. The page's CSS shows them only
  // there.
  const unfold = button("card-dock-unfold", "«");
  unfold.setAttribute("aria-label", t("dock_open"));
  const mark = button("card-dock-mark");
  // Who and what on one line that clips, the controls on another when the two
  // do not fit side by side: a handle that wrapped item by item took four rows
  // on a narrow sheet and left the terminal 55 px (run 36379262459).
  const about = el("div", "card-dock-about");
  about.append(dot, who, named, doing, needs);
  const controls = el("div", "card-dock-controls");
  controls.append(fontButtons.node, places, openClose);
  handle.append(unfold, mark, about, controls);

  const body = el("div", "card-dock-body");
  const errorLine = el("p", "card-dock-error");
  const term = el("div", "card-dock-term");
  term.dataset.terminal = "";
  const keyRow = el("div", "card-dock-keys");
  keyRow.append(el("span", "s-keys-label", t("keys_to_session")));
  for (const key of pressKeys) {
    const b = button("btn btn-sm s-key", key.labelKey ? t(key.labelKey) : key.label);
    b.dataset.key = key.id;
    if (key.hintKey) b.title = t(key.hintKey);
    b.addEventListener("click", () => live?.type(key.bytes));
    keyRow.append(b);
  }
  const stoppedNote = el("div", "card-dock-note card-dock-stopped");
  const stoppedText = el("p", "card-dock-note-text", t("dock_stopped_note"));
  const resumeButton = button("btn btn-sm btn-primary card-dock-resume", t("dock_resume"));
  const resumeLine = el("p", "card-dock-error");
  stoppedNote.append(stoppedText, resumeButton, resumeLine);
  const goneNote = el("div", "card-dock-note card-dock-gone");
  goneNote.append(el("p", "card-dock-note-text", t("dock_gone")), button("btn btn-primary card-dock-write", t("dock_write_orchestrator")));
  const orchestratorButton = button("btn btn-sm btn-primary card-dock-orchestrator", t("dock_to_orchestrator"));
  body.append(errorLine, term, keyRow, stoppedNote, goneNote, orchestratorButton);
  host.replaceChildren(handle, body);

  const place = () => effectiveDock(chosen, width);

  const stopTerminal = () => {
    if (live) live.stop();
    live = null;
    attached = "";
    streamError = "";
    host.dataset.attached = "false";
    fontButtons.paint(null);
  };

  const attach = (short) => {
    live = terminal(term, short, {
      links: options.links ?? null,
      fontKey: options.fontKey ?? FONT_KEYS.screen,
      report: {
        streamError: (message) => {
          streamError = message ?? "";
          paint();
        },
        actionError: (message) => {
          streamError = message ?? "";
          paint();
        },
        // The bridge has attached: said on the place, for the stand's report
        // (web/js/standreport.js) -- a terminal that never attached is a box
        // of the right size with nothing in it.
        ready: () => {
          host.dataset.attached = "true";
        },
        fontSize: (size) => fontButtons.paint(size),
      },
    });
    attached = short;
    live.open();
  };

  // paint puts the place's state on screen, and attaches or lets go of the
  // terminal as the state asks.
  function paint() {
    if (!author) {
      host.hidden = true;
      grip.hidden = true;
      stopTerminal();
      return;
    }
    host.hidden = false;
    const where = place();
    stage.dataset.dock = where;
    stage.dataset.chosen = chosen;
    stage.style?.setProperty?.("--card-dock-size", `${sizes[where]}%`);
    host.dataset.open = String(open);
    host.dataset.place = where;
    host.dataset.state = state;
    host.dataset.short = author.short;
    host.dataset.from = author.from;
    grip.hidden = !open;

    dot.dataset.state = state;
    who.textContent = author.short;
    doing.textContent = t(`dock_state_${state}`);
    const session = (latest?.sessions ?? []).find((s) => s.short === author.short);
    const name = session?.label || session?.name || "";
    named.textContent = name;
    named.hidden = !name;
    mark.textContent = sessionMark(session ?? { short: author.short });
    mark.dataset.state = state;
    mark.title = `${name || author.short} — ${doing.textContent}`;
    needs.textContent = state === "waiting" && session?.needs ? `— ${session.needs}` : "";
    toBottom.setAttribute("aria-pressed", String(where === "bottom"));
    toRight.setAttribute("aria-pressed", String(where === "right"));
    const room = width >= DOCK_RIGHT_MIN;
    toRight.disabled = !room;
    if (room) toRight.removeAttribute?.("title");
    else toRight.setAttribute("title", t("dock_right_no_room"));
    openClose.textContent = open ? t("dock_close") : t("dock_open");
    openClose.setAttribute("aria-expanded", String(open));

    const withTerminal = WITH_TERMINAL.has(state);
    fontButtons.node.hidden = !(open && withTerminal);
    body.hidden = !open;
    term.hidden = !withTerminal;
    keyRow.hidden = !withTerminal;
    errorLine.hidden = !(withTerminal && streamError);
    errorLine.textContent = streamError;
    stoppedNote.hidden = state !== "stopped";
    resumeButton.disabled = resuming;
    resumeButton.textContent = resuming ? t("dock_resuming") : t("dock_resume");
    resumeLine.hidden = !resumeError;
    resumeLine.textContent = resumeError;
    goneNote.hidden = state !== "dead" && state !== "unknown";
    orchestratorButton.hidden = state !== "orchestrator";

    if (open && withTerminal) {
      if (attached !== author.short) {
        stopTerminal();
        attach(author.short);
      }
    } else {
      stopTerminal();
    }
  }

  openClose.addEventListener("click", () => {
    open = !open;
    paint();
  });
  for (const b of [unfold, mark]) {
    b.addEventListener("click", () => {
      open = true;
      paint();
    });
  }
  for (const b of [toBottom, toRight]) {
    b.addEventListener("click", () => {
      if (b.disabled) return;
      chosen = b.dataset.place;
      writeDockPref("place", chosen, storage, keyNames);
      paint();
    });
  }
  orchestratorButton.addEventListener("click", () => toOrchestrator());
  goneNote.querySelector("button").addEventListener("click", () => toOrchestrator());
  resumeButton.addEventListener("click", async () => {
    if (resuming || !author) return;
    const short = author.short;
    resuming = true;
    resumeError = "";
    paint();
    try {
      await resume(short);
    } catch (err) {
      resumeError = err?.message ?? String(err);
    }
    resuming = false;
    paint();
  });

  // The grip: the size follows the pointer while it is down and is written
  // down once, where the drag ends.
  let dragging = false;
  const sizeAt = (event) => {
    const where = place();
    const box = rect(stage);
    const pct =
      where === "right"
        ? ((box.right - event.clientX) / box.width) * 100
        : ((box.bottom - event.clientY) / box.height) * 100;
    sizes[where] = Math.round(clampDockSize(where, pct));
    stage.style?.setProperty?.("--card-dock-size", `${sizes[where]}%`);
  };
  const onMove = (event) => {
    if (dragging) sizeAt(event);
  };
  const onUp = () => {
    if (!dragging) return;
    dragging = false;
    const where = place();
    writeDockPref(where === "right" ? "width" : "height", sizes[where], storage, keyNames);
  };
  grip.addEventListener("pointerdown", (event) => {
    event.preventDefault?.();
    dragging = true;
  });
  document.addEventListener("pointermove", onMove);
  document.addEventListener("pointerup", onUp);
  // A drag the system takes away (a gesture, a window losing the pointer) ends
  // where it was, as if let go: otherwise plain moves go on sizing the session.
  document.addEventListener("pointercancel", onUp);

  const unobserve = observe(stage, (w) => {
    width = w;
    if (author) paint();
  });

  return {
    show(next, snap) {
      latest = snap ?? null;
      author = next ?? null;
      state = author ? authorState(author.short, latest) : null;
      paint();
    },
    dispose() {
      stopTerminal();
      unobserve();
      document.removeEventListener("pointermove", onMove);
      document.removeEventListener("pointerup", onUp);
      document.removeEventListener("pointercancel", onUp);
    },
  };
}
