// The panel's dialog (web/js/dialog.js): the modal the page did not have.
//
// What is pinned here is the behaviour the three hand-made stand-ins never had
// — a focus trap, a way out by a press beside the window, and a stack so that
// Escape closes the innermost dialog rather than all of them. The fake DOM
// carries focus(), document.activeElement and contains(), which is everything
// this needs.
//
// Every test closes what it opens: the stack of open dialogs is the module's
// own, and a dialog left open in one test is still on it in the next.

import { test, beforeEach, afterEach } from "node:test";
import assert from "node:assert/strict";

import { installDOM, fireEvent, fireDocumentEvent } from "./fake-dom.js";
import { createDialog } from "../js/dialog.js";

let dom;

beforeEach(() => {
  dom = installDOM();
});

afterEach(() => {
  dom.restore();
});

// A dialog in the page with two controls in its footer, and the button that
// opened it left focused — the situation every one of these starts from.
function mount({ title = "Move the card", ...options } = {}) {
  const opener = dom.element("button");
  dom.document.body.appendChild(opener);
  opener.focus();
  const dialog = createDialog({ title, ...options });
  const cancel = dom.element("button");
  const confirm = dom.element("button");
  dialog.foot.append(cancel, confirm);
  dom.document.body.appendChild(dialog.element);
  return { dialog, opener, cancel, confirm };
}

test("a dialog opens over the page with the focus on its first control", () => {
  const { dialog, cancel } = mount();
  assert.equal(dialog.element.hidden, true);
  dialog.open();
  assert.equal(dialog.element.hidden, false);
  assert.equal(dom.document.activeElement, cancel);
  dialog.close();
});

test("a dialog says what it is to a screen reader", () => {
  const { dialog } = mount({ title: "Move the card" });
  const win = dialog.element.querySelector(".dialog");
  assert.equal(win.getAttribute("role"), "dialog");
  assert.equal(win.getAttribute("aria-modal"), "true");
  const title = win.querySelector(".dialog-title");
  assert.equal(title.textContent, "Move the card");
  assert.equal(win.getAttribute("aria-labelledby"), title.getAttribute("id"));
  assert.notEqual(title.getAttribute("id"), null);
});

test("two dialogs never share the name their title is addressed by", () => {
  const first = createDialog({ title: "One" });
  const second = createDialog({ title: "Two" });
  const id = (dialog) => dialog.element.querySelector(".dialog-title").getAttribute("id");
  assert.notEqual(id(first), id(second));
});

test("closing a dialog hides it and gives the focus back to what opened it", () => {
  const { dialog, opener } = mount();
  dialog.open();
  dialog.close();
  assert.equal(dialog.element.hidden, true);
  assert.equal(dom.document.activeElement, opener);
});

test("the dialog's own close button closes it", () => {
  const { dialog, opener } = mount();
  dialog.open();
  fireEvent(dialog.element.querySelector(".dialog-close"), "click");
  assert.equal(dialog.element.hidden, true);
  assert.equal(dom.document.activeElement, opener);
});

test("a dialog tells the page it closed, whichever way it was closed", () => {
  const closed = [];
  const { dialog } = mount({ onClose: () => closed.push("closed") });
  dialog.open();
  fireDocumentEvent(dom.document, "keydown", { key: "Escape" });
  assert.deepEqual(closed, ["closed"]);
  // A dialog already closed does not say so twice.
  dialog.close();
  assert.deepEqual(closed, ["closed"]);
});

test("Escape closes the dialog opened last and leaves the one under it", () => {
  const outer = mount();
  const inner = mount();
  outer.dialog.open();
  inner.dialog.open();
  fireDocumentEvent(dom.document, "keydown", { key: "Escape" });
  assert.equal(inner.dialog.element.hidden, true);
  assert.equal(outer.dialog.element.hidden, false);
  fireDocumentEvent(dom.document, "keydown", { key: "Escape" });
  assert.equal(outer.dialog.element.hidden, true);
});

// Escape is contested in this page: it interrupts a Claude Code turn in a live
// terminal and closes an open card. While a dialog is up it is the dialog's.
test("an Escape that closes a dialog goes no further", () => {
  const { dialog } = mount();
  dialog.open();
  const event = fireDocumentEvent(dom.document, "keydown", { key: "Escape" });
  assert.equal(event.propagationStopped, true);
  assert.equal(event.defaultPrevented, true);
});

test("the page carries no dialog listeners once the last dialog has closed", () => {
  const { dialog } = mount();
  dialog.open();
  assert.ok((dom.document.listeners.get("keydown")?.size ?? 0) > 0);
  dialog.close();
  assert.equal(dom.document.listeners.get("keydown")?.size ?? 0, 0);
  assert.equal(dom.document.listeners.get("mousedown")?.size ?? 0, 0);
});

// mousedown rather than click, and the press that opened the dialog is the
// reason: see web/js/dialog.js.
test("a press beside the dialog closes it, and a press inside it does not", () => {
  const { dialog, cancel } = mount();
  dialog.open();
  fireDocumentEvent(dom.document, "mousedown", { target: cancel });
  assert.equal(dialog.element.hidden, false);
  fireDocumentEvent(dom.document, "mousedown", { target: dialog.element });
  assert.equal(dialog.element.hidden, true);
});

test("a dialog that refuses to be dismissed by a press beside it stays open", () => {
  const { dialog } = mount({ dismissOnOutside: false });
  dialog.open();
  fireDocumentEvent(dom.document, "mousedown", { target: dom.document.body });
  assert.equal(dialog.element.hidden, false);
  dialog.close();
});

test("a click that opens a dialog does not close it again", () => {
  const { dialog, opener } = mount();
  // The press is over before the dialog exists; the click of the same gesture
  // is still travelling when it opens.
  fireDocumentEvent(dom.document, "mousedown", { target: opener });
  opener.addEventListener("click", () => dialog.open());
  fireEvent(opener, "click");
  assert.equal(dialog.element.hidden, false);
  dialog.close();
});

test("Tab at the dialog's last control comes back to its first", () => {
  const { dialog, confirm } = mount();
  dialog.open();
  confirm.focus();
  const event = fireEvent(confirm, "keydown", { key: "Tab" });
  assert.equal(dom.document.activeElement, dialog.element.querySelector(".dialog-close"));
  assert.equal(event.defaultPrevented, true);
  dialog.close();
});

// The first control of the window is its close button; the footer's buttons
// come after it.
test("Shift+Tab at the dialog's first control goes to its last", () => {
  const { dialog, confirm } = mount();
  dialog.open();
  const close = dialog.element.querySelector(".dialog-close");
  close.focus();
  const event = fireEvent(close, "keydown", { key: "Tab", shiftKey: true });
  assert.equal(dom.document.activeElement, confirm);
  assert.equal(event.defaultPrevented, true);
  dialog.close();
});

test("Tab between the dialog's own controls is left to the browser", () => {
  const { dialog, cancel } = mount();
  dialog.open();
  cancel.focus();
  const event = fireEvent(cancel, "keydown", { key: "Tab" });
  assert.equal(event.defaultPrevented, false);
  assert.equal(dom.document.activeElement, cancel);
  dialog.close();
});

test("a disabled control is no stop on the dialog's way round", () => {
  const { dialog, cancel, confirm } = mount();
  confirm.disabled = true;
  dialog.open();
  cancel.focus();
  fireEvent(cancel, "keydown", { key: "Tab" });
  assert.equal(dom.document.activeElement, dialog.element.querySelector(".dialog-close"));
  dialog.close();
});

test("a dialog is wide only when it is asked to be", () => {
  const narrow = createDialog({ title: "One" });
  const wide = createDialog({ title: "Two", size: "wide" });
  assert.equal(narrow.element.querySelector(".dialog").dataset.size, "narrow");
  assert.equal(wide.element.querySelector(".dialog").dataset.size, "wide");
});
