// web/js/fontcontrols.js
//
// The buttons that size a live terminal's type: A−, the size itself (which
// puts it back to the default), and A+. They do nothing of their own. Each
// press asks for a step — -1, 0 or +1, the same steps web/js/terminalfont.js's
// fontStep reads from Cmd+- / Cmd+0 / Cmd+= — and the caller hands it to the
// terminal's stepFont, the one function both the keys and the buttons go
// through (web/js/liveterminal.js). Two ways in, one way through, so the two
// cannot drift apart on the next edit to either.
//
// Where the buttons sit is the caller's: the orchestrator column puts them in
// its strip of column controls, the session panel in its header beside the
// tabs. Both are rows that exist before the terminal does and do not change
// height when the buttons are there, because a row that grows or appears
// under a terminal makes its pane shorter, and every change of the pane is a
// resize of the session. `buttonClass` is the class of the controls they sit
// among, so they look like the neighbours they are.

import { t } from "./i18n.js";
import { DEFAULT_FONT_SIZE, MAX_FONT_SIZE, MIN_FONT_SIZE } from "./terminalfont.js";

/**
 * buildFontControls returns `node`, the three buttons in a span, and
 * `paint(size)`, which shows the terminal's current size and turns off the
 * button that cannot do anything at it: A− at the smallest size, A+ at the
 * largest, the reset at the default. `paint(null)` is a place with no terminal
 * to size, and turns all three off. Until the first paint they are off too:
 * there is no size to claim before a terminal has one.
 */
export function buildFontControls({ onStep, buttonClass }) {
  const node = document.createElement("span");
  node.className = "term-font";
  // Pressing the buttons leaves the focus where it was — in the terminal the
  // person is typing into, where a focused button would take their next Space
  // or Enter as another press. On the group rather than on each button: a
  // browser sends no mousedown to a disabled button at all, so a press on the
  // one that is off at the end of the range would take the focus to the page
  // (found live). A disabled button lets the pointer through to this group
  // (web/app.css), and the press lands here either way.
  node.addEventListener("mousedown", (event) => event.preventDefault());

  const button = (className, text, label, step) => {
    const b = document.createElement("button");
    b.className = `${buttonClass} ${className}`;
    b.setAttribute("type", "button");
    b.setAttribute("aria-label", label);
    b.setAttribute("title", label);
    b.textContent = text;
    b.disabled = true;
    b.addEventListener("click", () => onStep(step));
    return b;
  };

  const smaller = button("term-font-smaller", "A−", t("terminal_font_smaller"), -1);
  const reset = button("term-font-reset", "— px", t("terminal_font_reset"), 0);
  const bigger = button("term-font-bigger", "A+", t("terminal_font_bigger"), 1);
  node.append(smaller, reset, bigger);

  const paint = (size) => {
    const known = Number.isFinite(size);
    reset.textContent = `${known ? size : "—"} px`;
    smaller.disabled = !known || size <= MIN_FONT_SIZE;
    reset.disabled = !known || size === DEFAULT_FONT_SIZE;
    bigger.disabled = !known || size >= MAX_FONT_SIZE;
  };

  return { node, paint };
}
