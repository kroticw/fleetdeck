// web/js/cardtabs.js — a card's tabs: the card itself, then every document the
// card links to, in the order its body links them (T-091).
//
// A document's tab carries a dot for the state of the session that wrote it
// (web/js/docauthor.js), so a session waiting on its document's question is seen
// from whatever tab is open. Tabs that do not fit the row are counted on a
// button that lists them all; the row itself scrolls sideways.
import { documentsOf, docTitle, noteName } from "./docnames.js";
import { t } from "./i18n.js";

export const CARD_TAB = "card";

const keyListeners = new WeakMap();

// tabsOf is the card's tab, then one per document: {key, doc}, the key a
// document's path. Before the documents are listed there is only the card.
export function tabsOf(card, cards, docs) {
  const tabs = [{ key: CARD_TAB }];
  for (const doc of documentsOf(card, cards, docs)) tabs.push({ key: doc.path, doc });
  return tabs;
}

function label(tab) {
  return tab.doc ? noteName(tab.doc.path) : t("card_tab_card");
}

// The default measure, off the page's own layout: the row's visible width, and
// where each tab ends within it at the row's current scroll.
function layoutOf(root, buttons) {
  return {
    row: root.clientWidth,
    tabs: buttons.map((b) => ({ right: b.offsetLeft + b.offsetWidth - (root.scrollLeft ?? 0) })),
  };
}

/**
 * renderTabs draws tabs into root, the card's tab row.
 *
 * options.active is the open tab's key; options.stateOf(tab) the state of a
 * document's author (docauthor.js authorState); options.onPick(key) is called
 * for a tab picked by a press, an arrow key or the list of every tab.
 * options.measure replaces the page's layout, for a test.
 *
 * Returns { remeasure() }, for when the row changes width with no new tabs.
 */
export function renderTabs(root, tabs, options = {}) {
  const { active = CARD_TAB, stateOf = () => null, onPick = () => {} } = options;
  root.className = "card-tabs";
  root.setAttribute("role", "tablist");

  const buttons = tabs.map((tab) => {
    const b = document.createElement("button");
    b.type = "button";
    b.className = "card-tab";
    b.dataset.key = tab.key;
    b.setAttribute("role", "tab");
    b.setAttribute("aria-selected", String(tab.key === active));
    const name = label(tab);
    let spoken = name;
    if (tab.doc) {
      const state = stateOf(tab);
      if (state) {
        const dot = document.createElement("span");
        dot.className = "card-tab-dot";
        dot.dataset.state = state;
        dot.setAttribute("aria-hidden", "true");
        b.append(dot);
        spoken = `${name} — ${t(`author_state_${state}`)}`;
      }
      b.setAttribute("title", docTitle(tab.doc));
    }
    const text = document.createElement("span");
    text.className = "card-tab-name";
    text.textContent = name;
    b.append(text);
    b.setAttribute("aria-label", spoken);
    b.addEventListener("click", () => onPick(tab.key));
    return b;
  });

  const more = document.createElement("button");
  more.type = "button";
  more.className = "card-tabs-more";
  more.setAttribute("aria-haspopup", "true");
  const menu = document.createElement("div");
  menu.className = "card-tabs-menu";
  menu.setAttribute("role", "menu");
  menu.hidden = true;
  for (const tab of tabs) {
    const item = document.createElement("button");
    item.type = "button";
    item.className = "card-tabs-item";
    item.setAttribute("role", "menuitem");
    item.textContent = label(tab);
    item.addEventListener("click", () => {
      menu.hidden = true;
      onPick(tab.key);
    });
    menu.append(item);
  }
  more.addEventListener("click", () => {
    menu.hidden = !menu.hidden;
    // The menu is fixed on the page (app.css: the row scrolls sideways and
    // would clip it), so it is placed under the button, its right edges on it.
    const box = more.getBoundingClientRect?.();
    if (!menu.hidden && box) {
      menu.style.setProperty("top", `${box.bottom}px`);
      menu.style.setProperty("right", `${Math.max(0, (globalThis.innerWidth ?? box.right) - box.right)}px`);
    }
  });

  root.replaceChildren(...buttons);
  // The row is drawn again on every change to its tabs; one key listener
  // stays on it, the latest drawing's.
  const index = tabs.findIndex((tab) => tab.key === active);
  if (keyListeners.has(root)) root.removeEventListener("keydown", keyListeners.get(root));
  const onKey = (event) => {
    const step = event.key === "ArrowRight" ? 1 : event.key === "ArrowLeft" ? -1 : 0;
    const next = index + step;
    if (step === 0 || next < 0 || next >= tabs.length) return;
    event.preventDefault?.();
    onPick(tabs[next].key);
  };
  keyListeners.set(root, onKey);
  root.addEventListener("keydown", onKey);

  const measure = options.measure ?? (() => layoutOf(root, buttons));
  function remeasure() {
    const { row, tabs: ends } = measure();
    const hidden = ends.filter((end) => end.right > row).length;
    if (hidden > 0) {
      more.textContent = t("card_tabs_more").replace("{n}", String(hidden));
      if (!more.parentNode) root.append(more, menu);
    } else if (more.parentNode) {
      more.remove();
      menu.remove();
    }
  }
  remeasure();
  buttons[index]?.scrollIntoView?.({ block: "nearest", inline: "nearest" });
  return { remeasure };
}
