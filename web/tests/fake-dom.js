// A DOM small enough to be read in one sitting and large enough to run the card
// panel. Node has no DOM and this project has no dependencies, so the choice was
// between this and not testing the panel's behaviour at all.
//
// It is deliberately not a DOM implementation. It supports exactly what
// web/js/card.js touches: element creation, text, a stored innerHTML string,
// child lists, dataset, attributes, a three-form selector, and bubbling events.
// In particular innerHTML is stored and never parsed, so nodes the panel inserts
// as markup — the rendered card body — are not addressable here. That is why the
// wiki-link navigation test clicks a backlink, which the panel builds as a real
// node, and why the body is asserted on as a string, which is also how
// markdown.js's own tests read it.

// One selector form: an optional tag, an optional single class, an optional
// attribute with an optional exact value. Everything the panel queries fits.
const SELECTOR = /^([a-zA-Z][\w-]*)?(?:\.([\w-]+))?(?:\[([\w-]+)(?:=(?:"([^"]*)"|([\w-]+)))?\])?$/;

function attributeName(name) {
  return name.startsWith("data-")
    ? name.slice(5).replace(/-([a-z])/g, (_, ch) => ch.toUpperCase())
    : null;
}

function matchesSelector(node, selector) {
  // A comma is a list of selectors, and a node matches if it matches any of
  // them — the panel asks for its scrolling boxes as ".md-table, pre", one
  // query for two kinds of box.
  if (selector.includes(",")) {
    return selector.split(",").some((one) => matchesSelector(node, one));
  }
  const parts = SELECTOR.exec(selector.trim());
  if (!parts) throw new Error(`fake-dom: unsupported selector ${selector}`);
  const [, tag, className, attribute, quoted, bare] = parts;
  const attributeValue = quoted ?? bare;
  if (tag && node.tagName !== tag.toUpperCase()) return false;
  if (className && !String(node.className).split(/\s+/).includes(className)) return false;
  if (attribute) {
    const dataKey = attributeName(attribute);
    const held = dataKey ? node.dataset[dataKey] : node.getAttribute(attribute);
    if (held === undefined || held === null) return false;
    if (attributeValue !== undefined && String(held) !== attributeValue) return false;
  }
  return true;
}

class FakeEvent {
  constructor(type) {
    this.type = type;
    this.target = null;
    this.defaultPrevented = false;
  }

  preventDefault() {
    this.defaultPrevented = true;
  }
}

class FakeNode {
  constructor(tag) {
    this.tagName = String(tag).toUpperCase();
    this.children = [];
    this.parentNode = null;
    this.dataset = {};
    this.attributes = {};
    this.className = "";
    this.hidden = false;
    this.disabled = false;
    this.selected = false;
    this.value = "";
    this.focused = false;
    this.listeners = new Map();
    this._text = "";
    this._html = null;

    // Scrolling and text selection, for the modules whose whole defect was
    // about them: a column that scrolls its thread to the bottom on every
    // redraw is unreadable past one screen, and a textarea rebuilt under the
    // cursor loses the caret mid-word. Neither is observable without these,
    // and neither is laid out here -- a test sets scrollHeight/clientHeight to
    // describe the situation it means, and reads scrollTop to see what the
    // module did about it.
    this.scrollTop = 0;
    this.scrollHeight = 0;
    this.clientHeight = 0;
    // The same three across, for the scroll indicator: whether there is more
    // content to the right is a measurement, and a test states the situation by
    // setting these rather than by laying anything out.
    this.scrollLeft = 0;
    this._scrollWidth = 0;
    this._clientWidth = 0;
    this.selectionStart = 0;
    this.selectionEnd = 0;

    // Two counters, because two of the defects these tests exist for are
    // invisible in the resulting tree: a module that rewrites a node's text
    // with the same string leaves an identical DOM behind, and one that runs
    // its whole render for a snapshot that changed nothing leaves an identical
    // DOM too. In a browser the first drops the selection inside that text and
    // the second costs the work; neither can be seen by comparing the tree
    // before and after, so they are counted instead.
    this.textWrites = 0;
    this.htmlWrites = 0;
    this.queries = 0;
  }

  // A node is connected when its chain of parents reaches the document. The
  // browser is the authority here and it is unforgiving: a node that is not in
  // the document has no layout at all, so clientWidth and scrollWidth are both
  // zero and every measurement taken from them is false. A module that measures
  // before inserting gets a confident, wrong answer — which is exactly the
  // defect this models, and which this fake could not express while any node
  // could carry any width.
  get isConnected() {
    let node = this;
    while (node) {
      if (node.isDocumentRoot) return true;
      node = node.parentNode;
    }
    return false;
  }

  // Only the horizontal pair is gated. The vertical pair belongs to tests
  // written before this and describes scroll position rather than layout;
  // changing what those report is a separate question from this one.
  get clientWidth() {
    return this.isConnected ? this._clientWidth : 0;
  }

  set clientWidth(value) {
    this._clientWidth = value;
  }

  get scrollWidth() {
    return this.isConnected ? this._scrollWidth : 0;
  }

  set scrollWidth(value) {
    this._scrollWidth = value;
  }

  // classList over className, so a module can toggle one class without
  // knowing what else the node carries -- which is the whole point of using it
  // in the first place.
  get classList() {
    const node = this;
    const names = () => String(node.className).split(/\s+/).filter(Boolean);
    const write = (list) => {
      node.className = list.join(" ");
    };
    return {
      contains: (name) => names().includes(name),
      add: (name) => {
        if (!names().includes(name)) write([...names(), name]);
      },
      remove: (name) => write(names().filter((n) => n !== name)),
      toggle: (name, on) => {
        if (on === undefined) on = !names().includes(name);
        if (on) {
          if (!names().includes(name)) write([...names(), name]);
        } else {
          write(names().filter((n) => n !== name));
        }
        return on;
      },
    };
  }

  setSelectionRange(start, end) {
    this.selectionStart = start;
    this.selectionEnd = end;
  }

  // Setting textContent replaces the children with one text node, and appending
  // an element after that leaves both in place — so the text of a node is its
  // own text followed by its descendants', exactly as in a browser.
  get textContent() {
    return this._text + this.children.map((child) => child.textContent).join("");
  }

  set textContent(value) {
    this.textWrites += 1;
    this._text = String(value);
    this._html = null;
    this.children = [];
  }

  get innerHTML() {
    return this._html ?? "";
  }

  set innerHTML(value) {
    this.htmlWrites += 1;
    this._html = String(value);
    this._text = "";
    this.children = [];
  }

  appendChild(child) {
    child.parentNode = this;
    this.children.push(child);
    return child;
  }

  append(...children) {
    for (const child of children) this.appendChild(child);
  }

  // insertBefore and remove exist because a column that updates in place has to
  // put a row back where it belongs and take one away again, rather than
  // rebuilding the list around it.
  insertBefore(child, reference) {
    child.parentNode = this;
    const at = reference ? this.children.indexOf(reference) : -1;
    if (at < 0) this.children.push(child);
    else this.children.splice(at, 0, child);
    return child;
  }

  remove() {
    const parent = this.parentNode;
    if (!parent) return;
    const at = parent.children.indexOf(this);
    if (at >= 0) parent.children.splice(at, 1);
    this.parentNode = null;
  }

  replaceChildren(...children) {
    for (const child of this.children) child.parentNode = null;
    this.children = [];
    this._text = "";
    this._html = null;
    for (const child of children) this.appendChild(child);
  }

  setAttribute(name, value) {
    this.attributes[name] = String(value);
  }

  getAttribute(name) {
    return Object.hasOwn(this.attributes, name) ? this.attributes[name] : null;
  }

  focus() {
    this.focused = true;
    if (this.ownerDocument) this.ownerDocument.activeElement = this;
  }

  contains(node) {
    for (let walk = node; walk; walk = walk.parentNode) {
      if (walk === this) return true;
    }
    return false;
  }

  closest(selector) {
    for (let walk = this; walk; walk = walk.parentNode) {
      if (matchesSelector(walk, selector)) return walk;
    }
    return null;
  }

  querySelectorAll(selector) {
    this.queries += 1;
    // Recorded with the one fact a count cannot carry: whether the node being
    // searched was in the page at the time. A module that measures layout has
    // to do it on a node the browser has laid out, and asking too early is
    // silent — every width reads zero and the answer is a confident "it fits".
    // The tree that comes back looks the same either way, so the moment is
    // logged rather than inferred.
    this.ownerDocument?.searches?.push({ selector, connected: this.isConnected });
    const found = [];
    const visit = (node) => {
      for (const child of node.children) {
        if (matchesSelector(child, selector)) found.push(child);
        visit(child);
      }
    };
    visit(this);
    return found;
  }

  querySelector(selector) {
    return this.querySelectorAll(selector)[0] ?? null;
  }

  addEventListener(type, fn) {
    if (!this.listeners.has(type)) this.listeners.set(type, new Set());
    this.listeners.get(type).add(fn);
  }

  removeEventListener(type, fn) {
    this.listeners.get(type)?.delete(fn);
  }

  // Bubbles the way a real event does: from the target up through every
  // ancestor, which is what the panel's click delegation relies on.
  dispatchEvent(event) {
    event.target = event.target ?? this;
    for (let walk = this; walk; walk = walk.parentNode) {
      for (const fn of walk.listeners.get(event.type) ?? []) fn(event);
    }
    return !event.defaultPrevented;
  }
}

class FakeSelect extends FakeNode {
  get value() {
    const chosen = this.children.find((option) => option.selected);
    // A real select always has a selection: with nothing marked, it shows its
    // first option, and reading .value returns that option's value.
    return chosen ? chosen.value : (this.children[0]?.value ?? "");
  }

  set value(next) {
    for (const option of this.children) option.selected = option.value === String(next);
  }
}

class FakeDocument {
  constructor() {
    this.listeners = new Map();
    // The document's own root. A test that cares whether a node is in the page
    // appends to it; one that does not, does not have to.
    // Every querySelectorAll in the page, in order, each with whether its node
    // was connected. See FakeNode.querySelectorAll.
    this.searches = [];
    this.body = new FakeNode("body");
    this.body.isDocumentRoot = true;
    this.body.ownerDocument = this;
  }

  // The node focus() last moved to, as document.activeElement.
  activeElement = null;

  createElement(tag) {
    const node = String(tag).toLowerCase() === "select" ? new FakeSelect(tag) : new FakeNode(tag);
    // So focus() can report itself to the document the way a browser does; a
    // module that asks "is my textarea the active element" is asking the
    // document, not the node.
    node.ownerDocument = this;
    return node;
  }

  addEventListener(type, fn) {
    if (!this.listeners.has(type)) this.listeners.set(type, new Set());
    this.listeners.get(type).add(fn);
  }

  removeEventListener(type, fn) {
    this.listeners.get(type)?.delete(fn);
  }

  dispatchEvent(event) {
    for (const fn of this.listeners.get(event.type) ?? []) fn(event);
  }
}

// installDOM puts a document on the global object, which is where the panel
// looks for one, and hands back a restore function so a test file cannot leak
// its document into the next one.
export function installDOM() {
  const previous = Object.hasOwn(globalThis, "document") ? globalThis.document : undefined;
  const previousWindow = Object.hasOwn(globalThis, "window") ? globalThis.window : undefined;
  const document = new FakeDocument();
  globalThis.document = document;
  // A window with nothing on it but events. Resize is a real input to the
  // panel -- the same content crosses the fits/doesn't-fit boundary with no
  // new snapshot behind it -- and without a window here that path is untestable.
  const window = new FakeNode("window");
  globalThis.window = window;
  return {
    document,
    window,
    element(tag) {
      return document.createElement(tag);
    },
    restore() {
      if (previous === undefined) delete globalThis.document;
      else globalThis.document = previous;
      if (previousWindow === undefined) delete globalThis.window;
      else globalThis.window = previousWindow;
    },
  };
}

export function fireEvent(node, type, extra = {}) {
  const event = Object.assign(new FakeEvent(type), extra);
  node.dispatchEvent(event);
  return event;
}

// fireDocumentEvent is separate because the panel's Escape and click-outside
// listeners sit on the document, whose events have a target somewhere else in
// the page entirely.
export function fireDocumentEvent(document, type, extra = {}) {
  const event = Object.assign(new FakeEvent(type), extra);
  document.dispatchEvent(event);
  return event;
}

// settle lets every pending promise — the panel's write, and everything chained
// after it — run to completion before a test asserts on the DOM.
export function settle() {
  return new Promise((resolve) => setTimeout(resolve, 0));
}
