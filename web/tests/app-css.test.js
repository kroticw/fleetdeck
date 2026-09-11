// web/app.css is a shared file that every frontend task appends its own block
// to, and a stylesheet fails silently: an unclosed rule earlier in the file
// swallows everything appended after it into error recovery (or, where CSS
// Nesting is supported, turns it into descendant rules of whatever was left
// open), and the page still loads, still parses, and simply stops looking the
// way it should. Nothing else in this repository would notice.
//
// So: the braces must balance, and the panel's rules must be top-level rules,
// not nested inside somebody else's.
//
// The scanner below counts braces outside comments. A brace inside a CSS string
// or a url() would fool it; there is none in this file, and the failure mode
// would be a loud false positive rather than a silent pass.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const css = readFileSync(new URL("../app.css", import.meta.url), "utf8");

function scan(source) {
  const stripped = source.replace(/\/\*[\s\S]*?\*\//g, "");
  const topLevel = [];
  let depth = 0;
  let deepest = 0;
  let buffer = "";
  for (const ch of stripped) {
    if (ch === "{") {
      if (depth === 0) topLevel.push(buffer.trim().replace(/\s+/g, " "));
      depth += 1;
      deepest = Math.max(deepest, depth);
      buffer = "";
    } else if (ch === "}") {
      depth -= 1;
      buffer = "";
      if (depth < 0) return { topLevel, depth, deepest };
    } else {
      buffer += ch;
    }
  }
  return { topLevel, depth, deepest };
}

test("every rule in app.css is closed", () => {
  const { depth } = scan(css);
  assert.equal(
    depth,
    0,
    depth > 0
      ? `${depth} rule(s) in web/app.css are never closed, so everything after them is dead`
      : "web/app.css closes a rule that was never opened",
  );
});

test("the card panel's rules are top-level rules", () => {
  const { topLevel } = scan(css);
  const selectors = new Set(topLevel.flatMap((rule) => rule.split(",").map((s) => s.trim())));

  // #card-panel is the overlay itself: nested, the panel stops covering the
  // board and becomes a transparent block below it.
  assert.ok(selectors.has("#card-panel"), "#card-panel is not a top-level rule in web/app.css");

  // These four carry meaning, not decoration. Without them a refusal and a
  // written-but-not-committed notice look identical, a dead session reads as an
  // ordinary one, and a link to a card that does not exist looks like one that
  // does.
  for (const selector of [".card-error", ".card-notice", ".card-session-dead", ".wikilink-missing"]) {
    assert.ok(selectors.has(selector), `${selector} is not a top-level rule in web/app.css`);
  }
});

test("the documentation section's rules are top-level rules", () => {
  const { topLevel } = scan(css);
  const selectors = new Set(topLevel.flatMap((rule) => rule.split(",").map((s) => s.trim())));

  // .docs is the section's own two-pane layout: nested, the list and the body
  // stop being side by side and the document falls below the list.
  assert.ok(selectors.has(".docs"), ".docs is not a top-level rule in web/app.css");

  // These carry meaning rather than decoration. Without them the current tab and
  // the open document are indistinguishable from the rest, and a section that is
  // empty looks exactly like one that failed.
  for (const selector of ["#tabs .tab.on", ".docs-entry.on", ".docs-empty", ".docs-error"]) {
    assert.ok(selectors.has(selector), `${selector} is not a top-level rule in web/app.css`);
  }
});

// The switcher hides a section with the hidden attribute, and `hidden` is only
// display:none from the user-agent stylesheet: any author rule outranks it.
// #board carries display:flex for its columns, so hiding it did nothing until
// this rule existed — the board stayed on screen with the documentation section
// stacked under it, which no test saw and one screenshot did.
test("hidden actually hides, whatever display a section's own rule sets", () => {
  const stripped = css.replace(/\/\*[\s\S]*?\*\//g, "");
  const block = /\[hidden\]\s*\{([^}]*)\}/.exec(stripped);
  assert.ok(block, "web/app.css has no [hidden] rule, so hiding a section is at the mercy of its own display");
  assert.match(
    block[1].replace(/\s+/g, " "),
    /display\s*:\s*none\s*!important/,
    "[hidden] must force display:none, or a section with its own display stays on screen",
  );

  // The rule only matters for elements that have a display of their own. Both
  // sections do, which is exactly why the guarantee has to be unconditional.
  for (const selector of ["#board", "#docs"]) {
    assert.ok(
      new RegExp(`${selector}\\s*\\{`).test(stripped),
      `${selector} is not a top-level rule in web/app.css`,
    );
  }
});

test("the session panel's rules are top-level rules", () => {
  const { topLevel } = scan(css);
  const selectors = new Set(topLevel.flatMap((rule) => rule.split(",").map((s) => s.trim())));

  // #session-panel is the second overlay over the same column: nested, it stops
  // covering the board and the terminal is drawn behind a kanban.
  assert.ok(selectors.has("#session-panel"), "#session-panel is not a top-level rule in web/app.css");

  // .s-error is the only place the panel says a write did not happen — that the
  // text in the box was not sent, that the screen could not be read. Unstyled it
  // is a line of body text among the terminal's own output.
  assert.ok(selectors.has(".session-panel .s-error"), ".session-panel .s-error is not a top-level rule in web/app.css");

  // The keys write into a live session and the close button writes nothing
  // anywhere; these two rules are what keeps them apart on screen. Nested, the
  // keys fall back into the header's top-right corner beside ✕ — read as one
  // row of window controls, which is the complaint this panel was fixed for.
  // .s-who names the open session in the header. Unstyled it has no ellipsis,
  // and a long session name pushes the close button off the row.
  for (const selector of [
    ".session-panel .s-keys",
    ".session-panel .s-keys-label",
    ".session-panel .s-close",
    ".session-panel .s-who",
  ]) {
    assert.ok(selectors.has(selector), `${selector} is not a top-level rule in web/app.css`);
  }
});

// The edge a person drags to resize the orchestrator column has to be seen.
//
// An invisible strip that responds to dragging is, for anyone who has not been
// told it is there, a strip that does not exist — and a browser is the only
// thing that can say whether it reads as a control, which is what the
// acceptance screenshots are for. What can be kept here is the floor: it is
// painted, it says which way it moves, and the column it sizes cannot be
// squeezed below a usable width by any percentage.
test("the resize edge is painted and says which way it moves", () => {
  const stripped = css.replace(/\/\*[\s\S]*?\*\//g, "");
  const body = (selector) => {
    const match = new RegExp(`(^|\\})\\s*${selector}\\s*\\{([^}]*)\\}`, "m").exec(stripped);
    assert.ok(match, `${selector} has no rule in web/app.css`);
    return match[2];
  };

  const edge = body("\\.col-grip");
  assert.match(edge, /cursor:\s*col-resize/, "the edge does not say it can be dragged sideways");
  assert.match(edge, /background:\s*var\(--/, "the edge is not painted, so nobody can find it");

  // The pixel floor, alongside the percentage one in web/js/columnwidth.js. A
  // percentage minimum on a narrow window is a handful of pixels — "not zero"
  // and practically zero. Both resizable columns carry it now, not only the
  // orchestrator's.
  for (const selector of ["\\.col-orchestrator", "\\.col-sessions"]) {
    assert.match(body(selector), /min-width:\s*\d+px/, `${selector} lost its pixel floor`);
  }
});

// A left column's fold/unfold controls belong on its own right (the edge
// facing the centre of the screen); a right column's belong on its own
// left, mirrored. This checks the two are actually opposite values, not
// merely that both rules exist — two rules with the same justify-content
// would pass a presence check while sitting both controls on the same side.
test("a left column's controls and a right column's sit on opposite sides, not the same one", () => {
  const stripped = css.replace(/\/\*[\s\S]*?\*\//g, "");
  const body = (selector) => {
    const match = new RegExp(`(^|\\})\\s*${selector}\\s*\\{([^}]*)\\}`, "m").exec(stripped);
    assert.ok(match, `${selector} has no rule in web/app.css`);
    return match[2];
  };

  assert.match(body("\\.col-size-left"), /justify-content:\s*flex-end/, "a left column's controls are not toward its centre-facing edge");
  assert.match(body("\\.col-size-right"), /justify-content:\s*flex-start/, "a right column's controls are not toward its centre-facing edge");
});

// The orchestrator column is a terminal now, and a terminal is sized by what it
// measures: the fit addon reads the width and height of the terminal's parent,
// .o-term, and divides them into cells. Two ways that goes wrong without a
// browser noticing anything. A padding on .o-term is counted as room the
// terminal does not have, so its last column or row is cut. And a box that does
// not take the height the head leaves — no flex: 1, or no min-height: 0 to let
// it shrink — hands the addon a height of its content, which for a terminal not
// yet drawn is nothing.
//
// The third is the colour. xterm.css paints the viewport black, which in the
// light theme is a black rectangle in a light column: the defect the session
// panel was fixed for once, and the column needs the same override.
test("the orchestrator's terminal takes the room the head leaves, and is painted by the theme", () => {
  const stripped = css.replace(/\/\*[\s\S]*?\*\//g, "");
  const body = (selector) => {
    const match = new RegExp(`(^|\\})\\s*${selector}\\s*\\{([^}]*)\\}`, "m").exec(stripped);
    assert.ok(match, `${selector} has no rule in web/app.css`);
    return match[2];
  };

  for (const selector of ["\\.o-screen", "\\.o-term"]) {
    assert.match(body(selector), /flex:\s*1\b/, `${selector} does not take the room the head leaves`);
    assert.match(body(selector), /min-height:\s*0/, `${selector} cannot shrink below its content`);
  }
  assert.doesNotMatch(body("\\.o-term"), /padding/, ".o-term has a padding the fit addon counts as room");

  const { topLevel } = scan(css);
  const selectors = new Set(topLevel.flatMap((rule) => rule.split(",").map((s) => s.trim())));
  assert.ok(selectors.has("#orchestrator .xterm-viewport"), "the column's terminal keeps xterm's black viewport");
});

// The size shown after the type changes (web/js/liveterminal.js's showSize) is
// put over the terminal, inside the element the terminal was opened into. It
// has to stay out of the layout — a note that takes room makes the pane smaller
// and the session narrower, the very thing it is there to report — and it has
// to be placed against that element, not against whatever positioned ancestor
// happens to be further up.
test("the terminal's size note sits over the terminal, takes no room and no clicks", () => {
  const stripped = css.replace(/\/\*[\s\S]*?\*\//g, "");
  const body = (selector) => {
    const match = new RegExp(`(^|\\})\\s*${selector}\\s*\\{([^}]*)\\}`, "m").exec(stripped);
    assert.ok(match, `${selector} has no rule in web/app.css`);
    return match[2];
  };

  const note = body("\\.term-size");
  assert.match(note, /position:\s*absolute/, ".term-size takes room in the pane");
  assert.match(note, /pointer-events:\s*none/, ".term-size catches clicks meant for the terminal");
  assert.match(note, /z-index:\s*\d+/, ".term-size can end up under xterm's own layers");
  for (const selector of ["\\.o-term", "\\.session-panel \\.s-term"]) {
    assert.match(body(selector), /position:\s*relative/, `${selector} does not place the note over its own terminal`);
  }
});

// Found live on the screen tab: for the moment between a bigger type and the
// refit, the terminal is wider and taller than its element. The screen tab's
// body scrolls, so it grew scrollbars, the refit measured the room the
// scrollbars left, the scrollbars went away, and the pane refitted again: two
// resizes of the session for one step, and a note showing the first, wrong
// size. The terminal's element keeps what spills out of it to itself, as the
// orchestrator column's always has.
test("a terminal's element keeps a terminal bigger than itself from scrolling what it sits in", () => {
  const stripped = css.replace(/\/\*[\s\S]*?\*\//g, "");
  const body = (selector) => {
    const match = new RegExp(`(^|\\})\\s*${selector}\\s*\\{([^}]*)\\}`, "m").exec(stripped);
    assert.ok(match, `${selector} has no rule in web/app.css`);
    return match[2];
  };
  for (const selector of ["\\.o-term", "\\.session-panel \\.s-term"]) {
    assert.match(body(selector), /overflow:\s*hidden/, `${selector} lets a terminal bigger than itself scroll its parent`);
  }
});

// The mark on the operator's own messages is the whole of the distinction
// between their words and an agent's, in both themes. One pane draws steps now:
// the orchestrator column is a terminal, and the session panel's digest tab is
// what is left of the feed.
test("the operator's own messages are marked, in colours that follow the theme", () => {
  const { topLevel } = scan(css);
  const selectors = new Set(topLevel.flatMap((rule) => rule.split(",").map((s) => s.trim())));

  for (const selector of [".step-typed", '.session-panel .s-step[data-typed="1"]']) {
    assert.ok(selectors.has(selector), `${selector} is not a top-level rule in web/app.css`);
  }

  // A literal colour here is the failure this is guarding: the two themes do
  // not share a palette, and a shade picked against the light surface can come
  // out all but invisible against the dark one — which is how the tint it
  // replaces failed. Every colour must be a token, so it changes with the theme
  // rather than being chosen for one of them.
  const stripped = css.replace(/\/\*[\s\S]*?\*\//g, "");
  for (const selector of [".step-typed", '\\.session-panel \\.s-step\\[data-typed="1"\\]']) {
    const block = new RegExp(`${selector.replace(/^\.step-typed$/, "\\.step-typed")}\\s*\\{([^}]*)\\}`).exec(stripped);
    assert.ok(block, `${selector} has no rule body to check`);
    assert.ok(
      !/#[0-9a-fA-F]{3,8}\b|\brgba?\(|\bhsla?\(/.test(block[1]),
      `${selector} names a colour outright instead of a theme token: ${block[1].trim()}`,
    );
  }
});
