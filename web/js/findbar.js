// The find bar over a document: Cmd+F in the docs tab and in a card's document
// tab. The matches are drawn with the CSS Custom Highlight API rather than by
// wrapping them in <mark>: the document is rendered markdown, and rewriting its
// nodes to mark a search would have to be undone exactly, or the next render
// diff, link handler or selection would meet nodes it did not make.

import { t } from "./i18n.js";

// matchOffsets is every match of query in text, case-insensitive, as [start,
// end] pairs that never overlap.
export function matchOffsets(text, query) {
  if (!query) return [];
  const hay = text.toLocaleLowerCase();
  const needle = query.toLocaleLowerCase();
  const out = [];
  for (let at = hay.indexOf(needle); at !== -1; at = hay.indexOf(needle, at + needle.length)) {
    out.push([at, at + needle.length]);
  }
  return out;
}

// cssHighlighter draws matches as two named highlights, every match and the
// current one (web/app.css, ::highlight(find-hit) and ::highlight(find-current)).
// Where the API is missing the bar still counts and scrolls, without colour.
export function cssHighlighter() {
  let ranges = [];
  const registry = globalThis.CSS?.highlights;
  const Highlight = globalThis.Highlight;
  const paint = (name, list) => {
    if (!registry || !Highlight) return;
    if (list.length === 0) registry.delete(name);
    else registry.set(name, new Highlight(...list));
  };
  return {
    search(root, query) {
      ranges = [];
      if (root && query) {
        const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
        for (let node = walker.nextNode(); node; node = walker.nextNode()) {
          for (const [start, end] of matchOffsets(node.nodeValue, query)) {
            const range = document.createRange();
            range.setStart(node, start);
            range.setEnd(node, end);
            ranges.push(range);
          }
        }
      }
      paint("find-hit", ranges);
      return ranges.length;
    },
    show(index) {
      const range = ranges[index];
      paint("find-current", range ? [range] : []);
      range?.startContainer.parentElement?.scrollIntoView({ block: "center" });
    },
    clear() {
      ranges = [];
      paint("find-hit", []);
      paint("find-current", []);
    },
  };
}

function el(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}

// createFindBar builds a closed bar. target() is the element searched, active()
// whether its document is on screen, so that Cmd+F is taken only then; onKey
// is the page's keydown handler to call with every key.
export function createFindBar({ target, highlighter = cssHighlighter(), active = () => true }) {
  const element = el("div", "find-bar");
  element.setAttribute("role", "search");
  element.setAttribute("aria-label", t("find_label"));
  element.hidden = true;
  const input = el("input", "find-input");
  input.type = "search";
  input.setAttribute("aria-label", t("find_label"));
  input.placeholder = t("find_placeholder");
  const count = el("span", "find-count", "");
  const button = (cls, text, label) => {
    const b = el("button", `btn btn-icon btn-sm ${cls}`, text);
    b.type = "button";
    b.setAttribute("aria-label", label);
    b.title = label;
    return b;
  };
  const prev = button("find-prev", "↑", t("find_prev"));
  const next = button("find-next", "↓", t("find_next"));
  const close = button("find-close", "×", t("find_close"));
  element.append(input, count, prev, next, close);

  let total = 0;
  let current = 0;

  const say = () => {
    count.textContent = total === 0 ? (input.value ? "0" : "") : t("find_count").replace("{n}", String(current + 1)).replace("{total}", String(total));
  };
  const run = () => {
    total = highlighter.search(target(), input.value);
    current = 0;
    if (total > 0) highlighter.show(current);
    say();
  };
  const step = (by) => {
    if (total === 0) return;
    current = (current + by + total) % total;
    highlighter.show(current);
    say();
  };
  const hide = () => {
    element.hidden = true;
    highlighter.clear();
  };

  input.addEventListener("input", run);
  input.addEventListener("keydown", (event) => {
    if (event.key === "Enter") {
      event.preventDefault?.();
      step(event.shiftKey ? -1 : 1);
    } else if (event.key === "Escape") {
      event.preventDefault?.();
      event.stopPropagation?.();
      hide();
    }
  });
  prev.addEventListener("click", () => step(-1));
  next.addEventListener("click", () => step(1));
  close.addEventListener("click", hide);

  const open = () => {
    element.hidden = false;
    input.focus?.();
    input.select?.();
    if (input.value) run();
  };

  return {
    element,
    open,
    close: hide,
    // The document changed under the bar: search it again.
    refresh: () => {
      if (!element.hidden && input.value) run();
    },
    // Takes Cmd+F (Ctrl+F) while active, and says whether it did.
    onKey(event) {
      if (!(event.metaKey || event.ctrlKey) || event.key?.toLowerCase() !== "f" || !active()) return false;
      event.preventDefault();
      open();
      return true;
    },
  };
}
