// Where a card keeps the session that wrote its open document: below the
// document or beside it, the choice remembered, and below whatever was chosen
// when the sheet has no room beside it (T-091).

import { test, beforeEach, afterEach } from "node:test";
import assert from "node:assert/strict";

import { installDOM, fireEvent, fireDocumentEvent, settle } from "./fake-dom.js";
import { t } from "../js/i18n.js";
import { sessionMark } from "../js/initials.js";
import {
  DOCK_KEYS,
  DOCK_RIGHT_MIN,
  clampDockSize,
  createCardDock,
  effectiveDock,
  readDockPrefs,
  writeDockPref,
} from "../js/carddock.js";

let dom;
beforeEach(() => {
  dom = installDOM();
});
afterEach(() => dom.restore());

function memoryStorage(initial = {}) {
  const data = new Map(Object.entries(initial));
  return {
    getItem: (k) => (data.has(k) ? data.get(k) : null),
    setItem: (k, v) => data.set(k, String(v)),
    removeItem: (k) => data.delete(k),
    data,
  };
}

const refusing = {
  getItem() {
    throw new Error("denied");
  },
  setItem() {
    throw new Error("denied");
  },
  removeItem() {
    throw new Error("denied");
  },
};

test("beside is taken only from the threshold up", () => {
  assert.equal(DOCK_RIGHT_MIN, 640);
  assert.equal(effectiveDock("right", 639), "bottom");
  assert.equal(effectiveDock("right", 640), "right");
  assert.equal(effectiveDock("bottom", 2000), "bottom");
});

test("anything but right reads as below", () => {
  assert.equal(effectiveDock("left", 2000), "bottom");
  assert.equal(effectiveDock(undefined, 2000), "bottom");
});

test("sizes are held inside their range, and anything not a number gives the default", () => {
  assert.equal(clampDockSize("bottom", 10), 25);
  assert.equal(clampDockSize("bottom", 95), 80);
  assert.equal(clampDockSize("bottom", 60), 60);
  assert.equal(clampDockSize("right", 10), 30);
  assert.equal(clampDockSize("right", 95), 60);
  assert.equal(clampDockSize("right", "40"), 40);
  assert.equal(clampDockSize("bottom", Number.NaN), 55);
  assert.equal(clampDockSize("right", "abc"), 45);
  assert.equal(clampDockSize("bottom", null), 55);
});

test("with nothing stored the session is below, at the default sizes", () => {
  assert.deepEqual(readDockPrefs(memoryStorage()), { place: "bottom", height: 55, width: 45 });
});

test("stored choices come back, sizes clamped", () => {
  const s = memoryStorage({
    "fleetdeck-card-session-dock": "right",
    "fleetdeck-card-session-height-pct": "90",
    "fleetdeck-card-session-width-pct": "40",
  });
  assert.deepEqual(readDockPrefs(s), { place: "right", height: 80, width: 40 });
});

test("a stored place that is neither below nor beside reads as below", () => {
  for (const junk of ["left", "", "RIGHT", "true"]) {
    assert.equal(readDockPrefs(memoryStorage({ "fleetdeck-card-session-dock": junk })).place, "bottom", junk);
  }
});

test("a storage that refuses is not an error: the defaults hold and a write is dropped", () => {
  assert.deepEqual(readDockPrefs(refusing), { place: "bottom", height: 55, width: 45 });
  assert.doesNotThrow(() => writeDockPref("place", "right", refusing));
  assert.deepEqual(readDockPrefs(undefined), { place: "bottom", height: 55, width: 45 });
});

// A browser with site data blocked throws on reading localStorage itself, not
// on getItem: a card must still open, at the defaults.
test("a page whose localStorage cannot even be read still opens the session's place", () => {
  const had = Object.getOwnPropertyDescriptor(globalThis, "localStorage");
  Object.defineProperty(globalThis, "localStorage", {
    configurable: true,
    get() {
      throw new Error("SecurityError: access denied");
    },
  });
  try {
    assert.deepEqual(readDockPrefs(), { place: "bottom", height: 55, width: 45 });
    assert.doesNotThrow(() => writeDockPref("place", "right"));
    const stage = dom.element("div");
    const grip = dom.element("div");
    const host = dom.element("div");
    assert.doesNotThrow(() => createCardDock(host, { stage, grip, observe: () => () => {} }));
  } finally {
    if (had) Object.defineProperty(globalThis, "localStorage", had);
    else delete globalThis.localStorage;
  }
});

test("writing the place stores exactly that key", () => {
  const s = memoryStorage();
  writeDockPref("place", "right", s);
  assert.deepEqual([...s.data], [["fleetdeck-card-session-dock", "right"]]);
});

test("the keys are the ones the spec names", () => {
  assert.deepEqual(DOCK_KEYS, {
    place: "fleetdeck-card-session-dock",
    height: "fleetdeck-card-session-height-pct",
    width: "fleetdeck-card-session-width-pct",
  });
});

// --- the place itself ----------------------------------------------------------

const ASK = "a41c09d2";
const WORK = "909bf9b2";
const ORCH = "0c7e1a2b";

function snap(overrides = {}) {
  return {
    orchestratorSession: ORCH,
    sessions: [
      { short: ASK, name: "cruises: booking review", needs: "answer: fold by double click, by a button, or both?", lifecycle: "live" },
      { short: WORK, name: "reconciliation notes", label: "the operator's label", needs: "", lifecycle: "live" },
      { short: ORCH, needs: "", lifecycle: "live" },
      { short: "5e55a0ff", lifecycle: "stopped" },
      { short: "deadbeef", lifecycle: "dead" },
    ],
    ...overrides,
  };
}

// A terminal that records what the place asks of it.
function terminals() {
  const made = [];
  const factory = (host, short, opts) => {
    const term = {
      host,
      short,
      opts,
      opened: 0,
      stopped: 0,
      typed: [],
      steps: [],
      open() {
        this.opened += 1;
      },
      stop() {
        this.stopped += 1;
      },
      type(bytes) {
        this.typed.push(bytes);
      },
      stepFont(step) {
        this.steps.push(step);
      },
    };
    made.push(term);
    return term;
  };
  return { made, factory };
}

// A stage whose width the test sets, the way a ResizeObserver would report it.
function mount(options = {}) {
  const stage = dom.element("div");
  const grip = dom.element("div");
  const host = dom.element("div");
  stage.append(grip, host);
  dom.document.body.appendChild(stage);
  let widthListener = null;
  let unobserved = 0;
  const storage = options.storage ?? memoryStorage();
  const terms = terminals();
  const calls = { resume: [], orchestrator: 0 };
  const dock = createCardDock(host, {
    stage,
    grip,
    storage,
    terminal: terms.factory,
    observe: (el, fn) => {
      widthListener = fn;
      fn(options.width ?? 800);
      return () => {
        unobserved += 1;
      };
    },
    rect: () => ({ left: 0, top: 0, right: 1000, bottom: 600, width: 1000, height: 600 }),
    resume: options.resume ?? (async (short) => calls.resume.push(short)),
    toOrchestrator: () => {
      calls.orchestrator += 1;
    },
    ...options.extra,
  });
  return {
    dock,
    stage,
    grip,
    host,
    storage,
    terms,
    calls,
    resize: (w) => widthListener(w),
    get unobserved() {
      return unobserved;
    },
  };
}

const openButton = (host) => host.querySelector(".card-dock-open");

test("the session's place starts folded: its handle says who and what, and no terminal is attached", () => {
  const m = mount();
  m.dock.show({ short: ASK, from: "document" }, snap());
  assert.equal(m.host.hidden, false);
  assert.equal(m.host.dataset.open, "false");
  assert.equal(m.terms.made.length, 0, "a folded handle holds no attach and resizes nobody's terminal");
  assert.ok(m.host.textContent.includes(ASK));
  assert.ok(m.host.textContent.includes(t("dock_state_waiting")));
  assert.ok(m.host.textContent.includes("fold by double click"), "the question the session is on is on the handle");
  assert.equal(m.host.querySelector(".card-dock-dot").dataset.state, "waiting");
});

test("the place says whose session it holds and where the sheet learnt who that is", () => {
  const m = mount();
  m.dock.show({ short: ASK, from: "document" }, snap());
  assert.equal(m.host.dataset.short, ASK);
  assert.equal(m.host.dataset.from, "document");
  m.dock.show({ short: WORK, from: "card" }, snap());
  assert.equal(m.host.dataset.from, "card");
});

// The stand takes its frame once the terminal has attached, and its gate holds
// the terminal to it: a box of the right size with nothing in it passed before.
test("the place says when its terminal has attached, and stops saying it when it lets go", () => {
  const m = mount({ extra: { expand: true } });
  m.dock.show({ short: ASK, from: "document" }, snap());
  assert.notEqual(m.host.dataset.attached, "true", "not before the bridge says so");
  m.terms.made[0].opts.report.ready();
  assert.equal(m.host.dataset.attached, "true");
  fireEvent(openButton(m.host), "click");
  assert.equal(m.host.dataset.attached, "false");
});

test("opening attaches the author's terminal; folding lets it go", () => {
  const m = mount();
  m.dock.show({ short: ASK, from: "document" }, snap());
  fireEvent(openButton(m.host), "click");
  assert.equal(m.terms.made.length, 1);
  assert.equal(m.terms.made[0].short, ASK);
  assert.equal(m.terms.made[0].opened, 1);
  assert.equal(m.host.dataset.open, "true");
  fireEvent(openButton(m.host), "click");
  assert.equal(m.terms.made[0].stopped, 1);
  assert.equal(m.host.dataset.open, "false");
});

test("the same author shown again and again keeps the one terminal", () => {
  const m = mount({ extra: { expand: true } });
  for (let i = 0; i < 10; i += 1) m.dock.show({ short: ASK, from: "document" }, snap());
  assert.equal(m.terms.made.length, 1);
  assert.equal(m.terms.made[0].opened, 1);
  assert.equal(m.terms.made[0].stopped, 0);
});

test("another author while open: the old terminal goes, the new author's comes", () => {
  const m = mount({ extra: { expand: true } });
  m.dock.show({ short: ASK, from: "document" }, snap());
  m.dock.show({ short: WORK, from: "document" }, snap());
  assert.equal(m.terms.made.length, 2);
  assert.equal(m.terms.made[0].stopped, 1);
  assert.equal(m.terms.made[1].short, WORK);
  assert.ok(m.host.textContent.includes(WORK));
});

test("no author, no place: hidden, and a terminal it held is let go", () => {
  const m = mount({ extra: { expand: true } });
  m.dock.show({ short: ASK, from: "card" }, snap());
  m.dock.show(null, snap());
  assert.equal(m.host.hidden, true);
  assert.equal(m.grip.hidden, true);
  assert.equal(m.terms.made[0].stopped, 1);
});

test("the orchestrator's terminal is never opened a second time: the place sends to it", () => {
  const m = mount({ extra: { expand: true } });
  m.dock.show({ short: ORCH, from: "document" }, snap());
  assert.equal(m.terms.made.length, 0);
  const go = m.host.querySelector(".card-dock-orchestrator");
  assert.ok(go && !go.hidden);
  assert.equal(go.textContent, t("dock_to_orchestrator"));
  fireEvent(go, "click");
  assert.equal(m.calls.orchestrator, 1);
});

test("a gone session, or one the panel does not know, sends to the orchestrator too", () => {
  for (const short of ["deadbeef", "0000beef"]) {
    const m = mount({ extra: { expand: true } });
    m.dock.show({ short, from: "document" }, snap());
    assert.equal(m.terms.made.length, 0, short);
    const go = m.host.querySelector(".card-dock-gone");
    assert.ok(go && !go.hidden, short);
    fireEvent(go.querySelector("button"), "click");
    assert.equal(m.calls.orchestrator, 1, short);
  }
});

test("a stopped session is brought back from the place, and its terminal comes once it is live", async () => {
  let release;
  const m = mount({
    extra: { expand: true },
    resume: (short) =>
      new Promise((resolve) => {
        m.calls.resume.push(short);
        release = resolve;
      }),
  });
  m.dock.show({ short: "5e55a0ff", from: "card" }, snap());
  assert.equal(m.terms.made.length, 0);
  const button = m.host.querySelector(".card-dock-resume");
  assert.ok(button && !button.hidden);
  assert.ok(m.host.textContent.includes(t("dock_stopped_note")));
  fireEvent(button, "click");
  assert.deepEqual(m.calls.resume, ["5e55a0ff"]);
  assert.equal(button.disabled, true);
  assert.equal(button.textContent, t("dock_resuming"));
  release();
  await settle();
  const live = snap();
  live.sessions.find((s) => s.short === "5e55a0ff").lifecycle = "live";
  m.dock.show({ short: "5e55a0ff", from: "card" }, live);
  assert.equal(m.terms.made.length, 1);
  assert.equal(m.terms.made[0].short, "5e55a0ff");
});

test("a return that fails says why and can be tried again", async () => {
  const m = mount({
    extra: { expand: true },
    resume: async () => {
      throw new Error("the session's directory is gone");
    },
  });
  m.dock.show({ short: "5e55a0ff", from: "card" }, snap());
  fireEvent(m.host.querySelector(".card-dock-resume"), "click");
  await settle();
  assert.ok(m.host.textContent.includes("the session's directory is gone"));
  assert.equal(m.host.querySelector(".card-dock-resume").disabled, false);
});

test("beside when chosen and there is room; below when there is not; the choice kept either way", () => {
  const m = mount({ storage: memoryStorage({ "fleetdeck-card-session-dock": "right" }), width: 639 });
  m.dock.show({ short: ASK, from: "document" }, snap());
  const right = m.host.querySelector(".card-dock-place-right");
  assert.equal(m.stage.dataset.dock, "bottom");
  assert.equal(m.stage.dataset.chosen, "right");
  assert.equal(right.disabled, true);
  assert.equal(right.getAttribute("title"), t("dock_right_no_room"));
  m.resize(640);
  assert.equal(m.stage.dataset.dock, "right");
  assert.equal(right.disabled, false);
  m.resize(639);
  assert.equal(m.stage.dataset.dock, "bottom");
  assert.equal(m.storage.getItem("fleetdeck-card-session-dock"), "right", "a narrow sheet does not forget the choice");
});

test("the place buttons move the session and remember the choice; a stand's place is not remembered", () => {
  const m = mount({ width: 900 });
  m.dock.show({ short: ASK, from: "document" }, snap());
  fireEvent(m.host.querySelector(".card-dock-place-right"), "click");
  assert.equal(m.stage.dataset.dock, "right");
  assert.equal(m.storage.getItem("fleetdeck-card-session-dock"), "right");
  assert.equal(m.host.querySelector(".card-dock-place-right").getAttribute("aria-pressed"), "true");
  fireEvent(m.host.querySelector(".card-dock-place-bottom"), "click");
  assert.equal(m.storage.getItem("fleetdeck-card-session-dock"), "bottom");

  const stand = mount({ width: 900, extra: { place: "right" } });
  stand.dock.show({ short: ASK, from: "document" }, snap());
  assert.equal(stand.stage.dataset.dock, "right");
  assert.equal(stand.storage.getItem("fleetdeck-card-session-dock"), null);
});

test("dragging the grip sizes the session as it moves and remembers it once, where it was let go", () => {
  const m = mount({ extra: { expand: true } });
  m.dock.show({ short: ASK, from: "document" }, snap());
  assert.equal(m.grip.hidden, false);
  fireEvent(m.grip, "pointerdown", { clientX: 500, clientY: 300, preventDefault() {} });
  fireDocumentEvent(dom.document, "pointermove", { clientX: 500, clientY: 240 });
  assert.equal(m.stage.style.getPropertyValue("--card-dock-size"), "60%");
  assert.equal(m.storage.getItem("fleetdeck-card-session-height-pct"), null, "nothing is written while dragging");
  fireDocumentEvent(dom.document, "pointermove", { clientX: 500, clientY: 10 });
  fireDocumentEvent(dom.document, "pointerup", {});
  assert.equal(m.stage.style.getPropertyValue("--card-dock-size"), "80%", "held to its most");
  assert.equal(m.storage.getItem("fleetdeck-card-session-height-pct"), "80");
  fireDocumentEvent(dom.document, "pointermove", { clientX: 500, clientY: 500 });
  assert.equal(m.stage.style.getPropertyValue("--card-dock-size"), "80%", "a move after letting go sizes nothing");
});

test("a drag the system cancels ends there: later moves size nothing", () => {
  const m = mount({ extra: { expand: true } });
  m.dock.show({ short: ASK, from: "document" }, snap());
  fireEvent(m.grip, "pointerdown", { clientX: 500, clientY: 300, preventDefault() {} });
  fireDocumentEvent(dom.document, "pointermove", { clientX: 500, clientY: 240 });
  fireDocumentEvent(dom.document, "pointercancel", {});
  fireDocumentEvent(dom.document, "pointermove", { clientX: 500, clientY: 500 });
  assert.equal(m.stage.style.getPropertyValue("--card-dock-size"), "60%");
});

test("the keys under the terminal press into it", () => {
  const m = mount({ extra: { expand: true } });
  m.dock.show({ short: ASK, from: "document" }, snap());
  const enter = [...m.host.querySelectorAll(".s-key")].find((b) => b.dataset.key === "enter");
  fireEvent(enter, "click");
  assert.deepEqual(m.terms.made[0].typed, ["\r"]);
});

test("letting the place go stops its terminal and its watch on the width", () => {
  const m = mount({ extra: { expand: true } });
  m.dock.show({ short: ASK, from: "document" }, snap());
  m.dock.dispose();
  assert.equal(m.terms.made[0].stopped, 1);
  assert.equal(m.unobserved, 1);
});

// The operator's report after the dev build (T-091): the type is sized here as
// in every other place a session's terminal is shown (web/js/fontcontrols.js).
test("the open place sizes its terminal's type with the session panel's buttons", () => {
  const m = mount({ extra: { expand: true } });
  m.dock.show({ short: ASK, from: "document" }, snap());
  const bigger = m.host.querySelector(".term-font-bigger");
  assert.ok(bigger, "the buttons are on the place");
  assert.equal(bigger.disabled, true, "no size to claim before the terminal has one");
  m.terms.made[0].opts.report.fontSize(12);
  assert.equal(m.host.querySelector(".term-font-reset").textContent, "12 px");
  fireEvent(bigger, "click");
  fireEvent(m.host.querySelector(".term-font-smaller"), "click");
  assert.deepEqual(m.terms.made[0].steps, [1, -1]);
  fireEvent(openButton(m.host), "click");
  assert.equal(m.host.querySelector(".term-font-bigger").disabled, true, "folded, there is no terminal to size");
  assert.equal(m.host.querySelector(".term-font").hidden, true);
});

// The id alone does not say which session it is: the name the session list
// shows goes beside it, the operator's label first.
test("the handle names the session beside its id, the way the session list does", () => {
  const m = mount();
  m.dock.show({ short: ASK, from: "document" }, snap());
  assert.equal(m.host.querySelector(".card-dock-name").textContent, "cruises: booking review");
  assert.equal(m.host.querySelector(".card-dock-id").textContent, ASK);
  m.dock.show({ short: WORK, from: "card" }, snap());
  assert.equal(m.host.querySelector(".card-dock-name").textContent, "the operator's label");
  m.dock.show({ short: "5e55a0ff", from: "card" }, snap());
  assert.equal(m.host.querySelector(".card-dock-name").textContent, "", "an unnamed session is its id alone");
  assert.equal(m.host.querySelector(".card-dock-name").hidden, true);
});

// Folded beside the document, the place is drawn like the folded session list:
// the round button that unfolds it and the session's two-letter mark, which
// opens it too. The mark carries the state for the page's colours and the whole
// name under the pointer.
test("folded beside the document, the place is a mark and an unfold button, both of which open it", () => {
  const m = mount({ width: 900, extra: { place: "right" } });
  m.dock.show({ short: ASK, from: "document" }, snap());
  const mark = m.host.querySelector(".card-dock-mark");
  assert.equal(mark.textContent, sessionMark(snap().sessions[0]));
  assert.equal(mark.dataset.state, "waiting");
  assert.ok(mark.title.includes("cruises: booking review"));
  fireEvent(mark, "click");
  assert.equal(m.host.dataset.open, "true");
  assert.equal(m.terms.made.length, 1);
  fireEvent(openButton(m.host), "click");
  fireEvent(m.host.querySelector(".card-dock-unfold"), "click");
  assert.equal(m.host.dataset.open, "true");
  assert.equal(m.terms.made.length, 2);
});
