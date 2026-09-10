// How wide the orchestrator column is, and whether it is folded away.
//
// The operator asked to be able to make it wider, narrower, or put it aside
// altogether. Two decisions are worth stating rather than reading out of the
// code.
//
// A ladder of fixed steps rather than a drag handle. What was asked for is
// "wider, narrower, folded away", which is three answers, not a continuum; a
// handle is a second interaction to learn, a second thing to get wrong on a
// touchpad, and a value that ends up at 23.4% for no reason anyone chose. The
// steps are percentages so the three zones keep their proportions when the
// window changes, which a pixel width would quietly break.
//
// Remembered in localStorage, under the same conventions web/js/theme.js
// already uses — the same prefix, the same read-through-try/catch, the same
// rule that an absent value is an ordinary first run rather than a failure. A
// second mechanism for the same job would be one more thing to keep in step.
//
// Nothing here touches the DOM or decides what the column looks like: it owns
// the state and the arithmetic, so both can be tested without a browser and so
// the pane keeps owning its own markup.

const WIDTH_KEY = "fleetdeck-orchestrator-width";
const FOLDED_KEY = "fleetdeck-orchestrator-folded";

// The ladder, narrowest first. 25% is what the column was before any of this
// existed, so a person who never touches the controls sees no change at all.
export const STEPS = ["16%", "20%", "25%", "32%", "42%"];
const DEFAULT_STEP = STEPS.indexOf("25%");

function read(key) {
  try {
    return localStorage.getItem(key);
  } catch {
    // A private window, or storage switched off. The column still works; the
    // choice just will not survive a reload this session.
    return null;
  }
}

function write(key, value) {
  try {
    if (value === null) localStorage.removeItem(key);
    else localStorage.setItem(key, value);
  } catch {
    // Same trade as above: a remembered preference is not worth taking the
    // page down for.
  }
}

// storedStep reads the remembered rung, refusing anything that is not one.
// A value from an older version, a hand-edited one, or a number that no longer
// names a step must land on the default rather than on nothing — a column of
// width `undefined` is a column that has disappeared.
export function storedStep() {
  const raw = read(WIDTH_KEY);
  // An empty string is absent, not a rung. Number("") is 0, which is a real
  // step, so a blank value would silently pin the column at its narrowest —
  // caught by the test below rather than by reading this line.
  if (raw === null || raw.trim() === "") return DEFAULT_STEP; // ordinary first run
  const n = Number(raw);
  if (!Number.isInteger(n) || n < 0 || n >= STEPS.length) return DEFAULT_STEP;
  return n;
}

export function storedFolded() {
  return read(FOLDED_KEY) === "1";
}

/**
 * createColumnWidth holds the column's width and folded state and remembers
 * both. `onChange` is called with the current state whenever it changes, and
 * once is never assumed: the caller applies the same state at startup.
 */
export function createColumnWidth(onChange = () => {}) {
  let step = storedStep();
  let folded = storedFolded();

  const announce = () => onChange({ step, width: STEPS[step], folded, canWiden: step < STEPS.length - 1, canNarrow: step > 0 });

  return {
    state() {
      return { step, width: STEPS[step], folded, canWiden: step < STEPS.length - 1, canNarrow: step > 0 };
    },

    // Both movers stop at the end of the ladder rather than wrapping around:
    // a control that jumps from widest to narrowest reads as a misclick, and
    // the button says so by being disabled instead.
    widen() {
      if (step >= STEPS.length - 1) return;
      step += 1;
      write(WIDTH_KEY, String(step));
      announce();
    },

    narrow() {
      if (step <= 0) return;
      step -= 1;
      write(WIDTH_KEY, String(step));
      announce();
    },

    // Folding does not touch the width. Unfolding must put the column back
    // exactly where it was, or a person loses their width every time they use
    // the control that was supposed to be reversible.
    fold() {
      if (folded) return;
      folded = true;
      write(FOLDED_KEY, "1");
      announce();
    },

    unfold() {
      if (!folded) return;
      folded = false;
      write(FOLDED_KEY, null);
      announce();
    },
  };
}
