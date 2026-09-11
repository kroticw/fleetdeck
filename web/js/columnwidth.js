// How wide a column is, and whether it is folded away. Started as the
// orchestrator column's own device; the session list now uses it too
// (spec: "the same feel, not a similar one"), which is why the storage keys
// below are a parameter rather than a literal — see createColumnWidth's own
// doc comment. Everything else about the mechanism is shared unchanged:
// there is exactly one implementation of "is this stored value legal",
// whichever column is asking.
//
// It used to be a ladder of five steps moved by two buttons. The operator
// looked at it and asked for the edge instead — grab the border, drag, let go.
// That is his call, but the reason the ladder was chosen has to survive it, and
// the reason turned out not to be what it looked like.
//
// The defect the steps are remembered for — a blank string in storage becoming
// Number("") === 0, and zero being a real rung — was never caught BY the steps.
// It was caught by a test. What the steps did was limit the damage: the worst a
// bad value could do was pin the column to its narrowest rung, which is ugly
// and recoverable. And, less obviously, an enumerable domain makes "is this
// stored value legal?" an obvious question to ask; a continuous range does not
// ask it for you.
//
// So two things are carried over deliberately and a third is added:
//
//   • absent is its own branch, never coerced. A blank string, whitespace or a
//     missing key is a first run, and it does not reach Number() at all;
//   • nothing from storage reaches the layout unchecked — NaN, Infinity, a
//     negative, a word;
//   • and, new here because a range has no "illegal index" to reject, every
//     value is CLAMPED. That is what makes a zero-width column unreachable
//     whatever storage holds and whatever the mouse does.
//
// The width is a percentage rather than pixels for the reason it always was:
// the three zones keep their proportions when the window changes. But a
// percentage floor on a narrow window is a few pixels — "not zero" and
// practically zero — so there is a pixel floor as well, and the two are applied
// together.

// Which column is asking is a set of storage keys, not a second copy of the
// module. The orchestrator column is the default set — these are the exact
// strings it has always used, unchanged, so a caller that passes none (every
// caller before this column had a neighbor) keeps reading the same entries
// it always read. A second column passes its own keys and gets the same
// rules applied to a different corner of storage.
export const ORCHESTRATOR_KEYS = {
  width: "fleetdeck-orchestrator-width-pct",
  folded: "fleetdeck-orchestrator-folded",
  // The key the ladder wrote, holding an index rather than a percentage. It
  // is read once so an operator who had chosen a width keeps it, and then
  // removed. Reusing the same key would have been worse than ignoring it:
  // "3" is a perfectly good number, and read as a percentage it is a
  // three-percent column.
  legacy: "fleetdeck-orchestrator-width",
};

// The session list never had a stepped version, so it has nothing to carry
// across — no `legacy` key, and storedPercent below treats that as "there is
// no migration to attempt", not as a bug.
export const SESSIONS_KEYS = {
  width: "fleetdeck-sessions-width-pct",
  folded: "fleetdeck-sessions-folded",
};

const LEGACY_STEPS = [16, 20, 25, 32, 42];

export const DEFAULT_PERCENT = 25;
export const MIN_PERCENT = 12;
export const MAX_PERCENT = 60;
// Below this the column is a sliver with a scrollbar in it. The fold control
// exists for people who want it out of the way; dragging is not the way to get
// there, because a column dragged to nothing has nothing left to grab.
export const MIN_PIXELS = 220;

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
    // Same trade: a remembered preference is not worth taking the page down for.
  }
}

/** clampPercent brings any number into the range the column may occupy. */
export function clampPercent(percent) {
  return Math.min(MAX_PERCENT, Math.max(MIN_PERCENT, percent));
}

/**
 * storedPercent is the remembered width, or the default when there is nothing
 * trustworthy to remember. `keys` picks which column's entries to read;
 * unset, it reads the orchestrator's, exactly as this function always has.
 *
 * Every rejection here is a real value that has been seen or can be: an absent
 * key on a first run, a blank one from a cleared entry, a step index from the
 * version before this one, and whatever a hand-edited entry contains.
 */
export function storedPercent(keys = ORCHESTRATOR_KEYS) {
  const raw = read(keys.width);

  if (raw === null || String(raw).trim() === "") {
    // Nothing of ours. The ladder may still have left something, and a width
    // the operator chose is worth carrying across one upgrade — but only for
    // a column the ladder could have written to; a column with no `legacy`
    // key never had a ladder, and there is nothing to migrate.
    const legacy = keys.legacy ? read(keys.legacy) : null;
    if (legacy !== null) {
      write(keys.legacy, null); // read once, then it stops existing
      const text = String(legacy).trim();
      // The same guard as above, and it is here because the test below caught
      // it missing: Number("") is 0, 0 is a legal step index, and a cleared
      // entry would have "carried across" a choice nobody made — the very
      // defect this migration exists to remember, reappearing inside the code
      // written to remember it.
      const index = text === "" ? NaN : Number(text);
      if (Number.isInteger(index) && index >= 0 && index < LEGACY_STEPS.length) {
        const carried = clampPercent(LEGACY_STEPS[index]);
        write(keys.width, String(carried));
        return carried;
      }
    }
    return DEFAULT_PERCENT;
  }

  const percent = Number(String(raw).trim());
  // Number.isFinite rejects NaN and both infinities in one go; the range check
  // is what a continuous domain has instead of "is this a legal index".
  if (!Number.isFinite(percent) || percent <= 0) return DEFAULT_PERCENT;
  return clampPercent(percent);
}

export function storedFolded(keys = ORCHESTRATOR_KEYS) {
  return read(keys.folded) === "1";
}

/**
 * createColumnWidth holds the column's width and folded state and remembers
 * both. `onChange` is called whenever either changes; the caller applies the
 * same state at startup, because a state applied only when it changes is a
 * state a reload does not restore.
 *
 * `keys` picks which column's storage entries this instance reads and
 * writes — unset, the orchestrator's, so every call site written before this
 * parameter existed keeps behaving exactly as it did. A second column passes
 * its own `{ width, folded }` (and, only if it ever shipped a stepped
 * version, a `legacy` key too) and gets an entirely separate remembered
 * state, sharing nothing with the orchestrator's but the rules.
 */
export function createColumnWidth(onChange = () => {}, keys = ORCHESTRATOR_KEYS) {
  let percent = storedPercent(keys);
  let folded = storedFolded(keys);

  const state = () => ({ percent, width: `${percent}%`, folded });
  const announce = () => onChange(state());

  return {
    state,

    /**
     * setPercent moves the column, clamping whatever it is given. It is called
     * on every pointer move during a drag, so it does not write to storage:
     * a drag across the screen would otherwise be a hundred writes, and the one
     * that matters is the last.
     */
    setPercent(next) {
      const wanted = Number.isFinite(next) ? clampPercent(next) : percent;
      if (wanted === percent) return;
      percent = wanted;
      announce();
    },

    /** remember writes the width that is on screen. Called when a drag ends. */
    remember() {
      write(keys.width, String(percent));
    },

    // Folding does not touch the width. Unfolding must give back exactly the
    // column that was folded away, or the control is one a person learns not to
    // touch.
    fold() {
      if (folded) return;
      folded = true;
      write(keys.folded, "1");
      announce();
    },

    unfold() {
      if (!folded) return;
      folded = false;
      write(keys.folded, null);
      announce();
    },
  };
}
