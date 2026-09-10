// One transcript step, drawn the same way wherever it is drawn.
//
// Two panes show a digest — the orchestrator column and the session panel — and
// until this module they each drew a step with their own code. The orchestrator column learned to render markdown, unwrap the
// envelopes the fleet wraps messages in, and leave an unchanged step alone; the
// session panel did not, and no test could notice, because both sides were
// green about their own half. The operator found it by looking.
//
// So this is not a helper that happens to be shared: it is the answer to "where
// is a step drawn", and there must be exactly one. A pane brings its own class
// names and its own container; everything about what a step IS lives here.
//
// It is deliberately not part of markdown.js. That module is about markdown —
// a format the world defines. This one is about the shape of our own fleet's
// conversation: which tags wrap a message, which of their fields a person needs
// to see. Mixing the two would leave nobody able to say a month from now which
// half a change belongs to.

import { renderMarkdown } from "./markdown.js";
import { markScrollablesWithin, watchScrollables } from "./scrollable.js";
import { stripToolNote, unwrapEnvelope } from "./envelope.js";



// stepKey is what tells an unchanged step from a changed one. It is the step's
// own role and text, not the markup they render into: the rendered form is
// derived, and comparing derived output would make the diff depend on the
// renderer as well as on the data.
export function stepKey(step) {
  return `${step.role}\u0000${step.text}`;
}

// How close to the end counts as "reading the newest message". A person who
// has scrolled up even slightly is reading, and their position is theirs; a
// person sitting at the bottom is following along and wants to keep following.
// A few dozen pixels of slack absorbs a part-line offset and a sub-pixel
// rounding difference without turning either into a decision.
export const STICK_THRESHOLD_PX = 48;

// atBottom must be asked BEFORE the DOM changes, because appending to a thread
// changes scrollHeight and so changes the answer. An element that does not
// scroll at all (nothing in it yet, or shorter than its box) is at the bottom
// by definition, which is what puts a freshly opened conversation at its newest
// message.
export function atBottom(el, threshold = STICK_THRESHOLD_PX) {
  if (!el) return true;
  return el.scrollHeight - el.scrollTop - el.clientHeight <= threshold;
}

// An empty Set of known cards, deliberately: a conversation has no card names
// to resolve links against, so a [[link]] renders as a link that does not work
// rather than one that goes somewhere wrong.
const NO_CARDS = new Set();

function element(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}

// fillStep writes one step into a row, whether the row is new or is being
// updated. Only a step whose own data changed is ever refilled, so reusing the
// node keeps the thread stable for everything around it.
//
// The body is the one place markup is assigned, and it comes from markdown.js,
// which escapes its whole input before assembling anything. A step's text
// arrives from the daemon and the fleet is open (spec 3.1), so it is untrusted
// exactly as a card body is. Everything else here — the attribution, the
// identifiers — is set as text or as an attribute value.
export function fillStep(row, step, classFor) {
  row.className = classFor(step.role);
  row.dataset.stepKey = stepKey(step);
  row.replaceChildren();

  const wrapper = unwrapEnvelope(step.text);
  if (wrapper) {
    const from = element("div", "step-from", wrapper.label);
    // The identifiers hang here rather than in the reading line: out of the
    // way, and one hover from being read when someone needs to chase a lead.
    if (wrapper.detail) from.setAttribute("title", wrapper.detail);
    row.appendChild(from);
  }

  const body = element("div", "step-body");
  // Applied to the step either way, envelope or not: one of the two notes
  // arrives on a message that has no envelope at all, so keying this to
  // unwrapping would have left that one on screen.
  const text = stripToolNote(wrapper ? wrapper.body : step.text);
  // A wrapper whose whole content was the envelope leaves nothing to render;
  // the attribution line is then the entire step, which is honest — that is all
  // the notification actually said.
  if (text) body.innerHTML = renderMarkdown(text, NO_CARDS);
  row.appendChild(body);
  return row;
}

// syncSteps brings a container in line with a list of steps, touching as little
// as it can.
//
// Two rules, both of them the answer to a complaint from the operator. A step
// whose role and text are unchanged is not touched at all, so a selection made
// with the mouse survives a poll that returned the same twenty steps it
// returned three seconds ago. And the container is scrolled to the newest step
// only if it was already there — measured BEFORE the DOM changes, because
// appending changes the answer. Someone who scrolled up is reading, and taking
// the screen back makes a conversation longer than one screen unreadable.
export function syncSteps(container, steps, classFor) {
  // The container outlives the steps inside it — steps are diffed, not rebuilt
  // — so the listeners go here, once. watchScrollables is idempotent per
  // (container, selector), which is what makes calling it on every sync safe.
  watchScrollables(container, ".md-table, pre");
  const stick = atBottom(container);
  const rows = container.children;

  for (let i = 0; i < steps.length; i += 1) {
    const step = steps[i];
    const existing = rows[i];
    if (!existing) {
      container.appendChild(fillStep(document.createElement("div"), step, classFor));
      continue;
    }
    // The comparison is on the step's own data, kept on the node, not on the
    // markup it produced: a rendered form is derived, and diffing derived
    // output would make an unchanged step depend on the renderer holding still
    // as well as on the data.
    if (existing.dataset.stepKey === stepKey(step)) continue;
    fillStep(existing, step, classFor);
  }
  while (container.children.length > steps.length) {
    container.children[container.children.length - 1].remove();
  }

  // Once, over the whole container, and only here: a step's body is measured
  // for scrolling, and a node that is not yet in the page has no layout to
  // measure — every box would read as fitting. fillStep builds a body and
  // attaches it afterwards, so measuring there answered about nothing. Walking
  // the container is also what catches the rows this sync left untouched.
  markScrollablesWithin(container, ".md-table, pre");

  if (stick) container.scrollTop = container.scrollHeight;
}
