// The centre column's sections: the board and the documentation (spec section 4,
// "Разделы": both are sections of the centre column).
//
// index.html has carried <nav id="tabs"> and a hidden #docs since the shell was
// laid down, and until now nothing filled either: the documentation section
// existed in the markup with no way to reach it. This module is that way.
//
// Sections are built lazily, on first show. The documentation section fetches
// when it is built, and building it at startup would make every panel ask for
// documentation directories — and, on a panel that configured none, take a 404 —
// before the operator had shown any interest in that section.

function el(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}

/**
 * createSections fills `tabs` with one control per section and shows one section
 * at a time.
 *
 * Each section is {id, label, root, onFirstShow?}. `root` is the element holding
 * that section's content; `onFirstShow` is called the first time the section
 * becomes visible, and never again.
 *
 * A section whose root element is missing is dropped with a message rather than
 * taking the switcher down: the remaining sections stay usable, which is the
 * difference between a page that lost one view and a page that lost its centre
 * column.
 *
 * Returns {show(id)} so the rest of the page can move between sections.
 */
export function createSections(tabs, sections) {
  if (!tabs) {
    console.error("fleetdeck: the section tabs are not wired — the page has no #tabs");
    return { show() {} };
  }

  const usable = sections.filter((section) => {
    if (section.root) return true;
    console.error(`fleetdeck: section "${section.id}" has no element in the page and was skipped`);
    return false;
  });

  const built = new Set();
  const buttons = new Map();

  const show = (id) => {
    for (const section of usable) {
      const current = section.id === id;
      section.root.hidden = !current;
      const button = buttons.get(section.id);
      if (button) {
        button.className = current ? "tab on" : "tab";
        // aria-selected, not only a class: which section is current has to be
        // available to a screen reader, not only visible as a colour.
        button.setAttribute("aria-selected", current ? "true" : "false");
      }
      if (current && !built.has(section.id)) {
        built.add(section.id);
        section.onFirstShow?.();
      }
    }
  };

  tabs.setAttribute("role", "tablist");
  const controls = usable.map((section) => {
    const button = el("button", "tab", section.label);
    button.setAttribute("type", "button");
    button.setAttribute("role", "tab");
    button.dataset.section = section.id;
    button.addEventListener("click", () => show(section.id));
    buttons.set(section.id, button);
    return button;
  });
  tabs.replaceChildren(...controls);

  if (usable.length > 0) show(usable[0].id);

  return { show };
}
