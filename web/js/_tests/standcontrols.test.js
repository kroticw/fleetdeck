// web/js/_tests/standcontrols.test.js
//
// Run with: node --test web/js/_tests/standcontrols.test.js

import test from "node:test";
import assert from "node:assert/strict";
import { contrastRatio, controlsReport, parseColor, watchControls } from "../standcontrols.js";

// The dark theme's grounds, as web/app.css states them.
const DARK = { "--bg": "#14161a", "--surface": "#1b1e24", "--surface-raised": "#21252c", "--surface-hover": "#292e36" };
const LIGHT = { "--bg": "#f5f6f8", "--surface": "#ffffff", "--surface-raised": "#ffffff", "--surface-hover": "#edeff2" };

// An element: its box, its computed style, its parent, and whether it is
// disabled. style holds only what the report reads.
function node({ style = {}, parent = null, rect = { top: 0, left: 0, width: 80, height: 24 }, disabled, placeholder } = {}) {
  return {
    parentElement: parent,
    disabled,
    placeholder: placeholder ?? "",
    value: "",
    style: { backgroundColor: "rgba(0, 0, 0, 0)", color: "rgb(0, 0, 0)", borderTopLeftRadius: "0px", backdropFilter: "none", opacity: "1", ...style },
    placeholderStyle: { color: "rgb(169, 169, 169)" },
    getBoundingClientRect: () => rect,
  };
}

// A window whose document answers selectors from elements, its root carrying
// glass and the theme's grounds.
function fakeWindow({ glass = "glass", grounds = DARK, elements = {}, body } = {}) {
  const listeners = {};
  const frames = [];
  const root = {
    dataset: { glass },
    style: { getPropertyValue: (name) => grounds[name] ?? "" },
  };
  return {
    document: { documentElement: root, body, querySelector: (selector) => elements[selector] ?? null },
    // WebKit answers an input's ::placeholder with its own style (measured on
    // macOS 27: the colour a rule gives it, rgb(169, 169, 169) with none).
    getComputedStyle: (el, pseudo) => (el === root ? root.style : pseudo === "::placeholder" ? el.placeholderStyle : el.style),
    addEventListener: (name, fn) => (listeners[name] = fn),
    requestAnimationFrame: (fn) => frames.push(fn),
    listeners,
    frames,
  };
}

test("colours are read as the page's computed styles and the sheet's tokens write them", () => {
  assert.deepEqual(parseColor("#1b1e24"), { r: 27, g: 30, b: 36, a: 1 });
  assert.deepEqual(parseColor("#fff"), { r: 255, g: 255, b: 255, a: 1 });
  assert.deepEqual(parseColor("rgb(193, 199, 207)"), { r: 193, g: 199, b: 207, a: 1 });
  assert.deepEqual(parseColor("rgba(255, 255, 255, 0.09)"), { r: 255, g: 255, b: 255, a: 0.09 });
  assert.deepEqual(parseColor("rgb(255 255 255 / 52%)"), { r: 255, g: 255, b: 255, a: 0.52 });
  assert.deepEqual(parseColor("transparent"), { r: 0, g: 0, b: 0, a: 0 });
  assert.equal(parseColor("var(--x)"), null);
});

test("contrast is WCAG's ratio of relative luminances", () => {
  assert.equal(Math.round(contrastRatio(parseColor("#000"), parseColor("#fff")) * 10) / 10, 21);
  assert.equal(contrastRatio(parseColor("#777"), parseColor("#777")), 1);
});

// A capsule on the island's glass: nothing under it in the page, so its text is
// read against its fill over the lightest and the darkest ground the theme has,
// and the worse of the two is reported.
test("a capsule on glass is measured against the worst ground under it", () => {
  const body = node({ style: { backgroundColor: "rgba(0, 0, 0, 0)" } });
  const picker = node({
    parent: body,
    style: { backgroundColor: "rgba(255, 255, 255, 0.09)", color: "rgb(193, 199, 207)", borderTopLeftRadius: "999px" },
    rect: { top: 14, left: 120, width: 140, height: 24 },
  });
  const report = controlsReport(fakeWindow({ elements: { ".o-pick-select": picker }, body }), "orchestrator");
  assert.equal(report.surface, "orchestrator");
  assert.equal(report.report, "controls");
  assert.equal(report.glass, "glass");
  const [control] = report.controls;
  assert.equal(control.name, "picker");
  assert.equal(control.height, 24);
  assert.equal(control.radius, 999);
  assert.equal(control.fillAlpha, 0.09);
  assert.equal(control.backdrop, "none");
  assert.equal(control.floating, false);
  assert.equal(control.disabled, false);
  // #c1c7cf over 9 % white on #14161a, the darker ground.
  const fill = { r: 255 * 0.09 + 20 * 0.91, g: 255 * 0.09 + 22 * 0.91, b: 255 * 0.09 + 26 * 0.91, a: 1 };
  const onDark = contrastRatio(parseColor("rgb(193, 199, 207)"), fill);
  const fillLight = { r: 255 * 0.09 + 41 * 0.91, g: 255 * 0.09 + 46 * 0.91, b: 255 * 0.09 + 54 * 0.91, a: 1 };
  const onLight = contrastRatio(parseColor("rgb(193, 199, 207)"), fillLight);
  assert.equal(control.contrast, Math.round(Math.min(onDark, onLight) * 100) / 100);
});

// Light text on a light ground is what a single ground would have hidden: in the
// light theme the lightest ground is white, and white text on it fails.
test("the worse ground decides: pale text on a pale capsule is reported as the failure it is", () => {
  const body = node();
  const pencil = node({ parent: body, style: { backgroundColor: "rgba(255, 255, 255, 0.52)", color: "rgb(240, 240, 240)", borderTopLeftRadius: "999px" } });
  const [control] = controlsReport(fakeWindow({ grounds: LIGHT, elements: { ".o-name-edit": pencil }, body }), "orchestrator").controls;
  assert.ok(control.contrast < 1.2, `contrast ${control.contrast}`);
});

// A field in the frosted form is read against the form's fill over the worst
// ground: the form blurs whatever is under it, and the page cannot know what.
test("a field in a frosted panel is measured against the panel over the worst ground", () => {
  const body = node({ style: { backgroundColor: "rgb(20, 22, 26)" } });
  const form = node({
    parent: body,
    style: { backgroundColor: "rgba(27, 30, 36, 0.8)", color: "rgb(231, 233, 236)", borderTopLeftRadius: "18px", backdropFilter: "blur(24px) saturate(160%)" },
    rect: { top: 56, left: 400, width: 448, height: 90 },
  });
  const title = node({ parent: form, style: { backgroundColor: "rgba(255, 255, 255, 0.09)", color: "rgb(231, 233, 236)", borderTopLeftRadius: "999px" }, rect: { top: 66, left: 410, width: 428, height: 30 } });
  const report = controlsReport(fakeWindow({ elements: { ".newcard": form, ".newcard-title": title }, body }), "board");
  const byName = Object.fromEntries(report.controls.map((c) => [c.name, c]));
  assert.equal(byName.newCard.floating, true);
  assert.equal(byName.newCard.backdrop, "blur(24px) saturate(160%)");
  assert.equal(byName.newCard.fillAlpha, 0.8);
  assert.equal(byName.newCardTitle.floating, false);
  // Over the lightest dark ground (#292e36), not the board's body under it.
  const panel = { r: 27 * 0.8 + 41 * 0.2, g: 30 * 0.8 + 46 * 0.2, b: 36 * 0.8 + 54 * 0.2 };
  const field = { r: 255 * 0.09 + panel.r * 0.91, g: 255 * 0.09 + panel.g * 0.91, b: 255 * 0.09 + panel.b * 0.91, a: 1 };
  const expected = contrastRatio(parseColor("rgb(231, 233, 236)"), field);
  assert.ok(byName.newCardTitle.contrast <= Math.round(expected * 100) / 100 + 0.01, `${byName.newCardTitle.contrast} vs ${expected}`);
});

// With no glass a side surface's page is solid: what is under a capsule is the
// page's own ground, and no theme ground is guessed at.
test("with no glass a capsule is measured against the solid page under it", () => {
  const body = node({ style: { backgroundColor: "rgb(255, 255, 255)" } });
  const button = node({ parent: body, style: { backgroundColor: "rgb(255, 255, 255)", color: "rgb(26, 29, 34)", borderTopLeftRadius: "999px" } });
  const [control] = controlsReport(fakeWindow({ glass: "opaque", grounds: LIGHT, elements: { ".fleet-menu-button": button }, body }), "orchestrator").controls;
  assert.equal(control.name, "fleetButton");
  assert.equal(control.fillAlpha, 1);
  assert.equal(control.contrast, Math.round(contrastRatio(parseColor("#1a1d22"), parseColor("#fff")) * 100) / 100);
});

// A solid fill anywhere under a see-through capsule is what the capsule is read
// against: the theme's grounds lie under it, out of sight.
test("a see-through capsule over a solid fill is measured against that fill, not the theme's grounds", () => {
  const body = node({ style: { backgroundColor: "rgb(0, 0, 0)" } });
  const button = node({ parent: body, style: { backgroundColor: "rgba(255, 255, 255, 0.1)", color: "rgb(255, 255, 255)", borderTopLeftRadius: "999px" } });
  const [control] = controlsReport(fakeWindow({ glass: "opaque", grounds: LIGHT, elements: { ".o-name-edit": button }, body }), "orchestrator").controls;
  const onBlack = { r: 25.5, g: 25.5, b: 25.5, a: 1 };
  assert.equal(control.contrast, Math.round(contrastRatio(parseColor("#fff"), onBlack) * 100) / 100);
});

test("a control not on screen is not reported, and a disabled one says so", () => {
  const body = node();
  const hidden = node({ parent: body, rect: { top: 0, left: 0, width: 0, height: 0 } });
  const bigger = node({ parent: body, disabled: true, style: { borderTopLeftRadius: "999px" } });
  const report = controlsReport(fakeWindow({ elements: { ".fleet-menu-list": hidden, ".term-font-bigger": bigger }, body }), "orchestrator");
  assert.deepEqual(report.controls.map((c) => c.name), ["fontBigger"]);
  assert.equal(report.controls[0].disabled, true);
});

// Review of #185: a stand opens the form empty, and what the title shows is its
// placeholder, not its text.
test("an empty field says how its placeholder reads, over the same grounds as its text", () => {
  const body = node();
  const form = node({ parent: body, style: { backgroundColor: "rgba(255, 255, 255, 0.8)", backdropFilter: "blur(24px) saturate(160%)" } });
  const title = node({ parent: form, placeholder: "Card title", style: { backgroundColor: "rgba(255, 255, 255, 0.52)", color: "rgb(26, 29, 34)", borderTopLeftRadius: "999px" } });
  const zone = node({ parent: form, style: { borderTopLeftRadius: "999px" } });
  const win = fakeWindow({ grounds: LIGHT, elements: { ".newcard": form, ".newcard-title": title, ".newcard-zone": zone }, body });
  const byName = Object.fromEntries(controlsReport(win, "board").controls.map((c) => [c.name, c]));
  assert.ok(byName.newCardTitle.placeholderContrast < 4.5, `WebKit's default grey on a pale field: ${byName.newCardTitle.placeholderContrast}`);
  title.placeholderStyle = { color: "rgb(91, 100, 112)" };
  const muted = Object.fromEntries(controlsReport(win, "board").controls.map((c) => [c.name, c])).newCardTitle;
  assert.ok(muted.placeholderContrast >= 4.5, `--text-muted: ${muted.placeholderContrast}`);
  assert.equal(byName.newCardZone.placeholderContrast, null, "a select has no placeholder");
  title.value = "T-070";
  const typed = Object.fromEntries(controlsReport(win, "board").controls.map((c) => [c.name, c])).newCardTitle;
  assert.equal(typed.placeholderContrast, null, "a field with text shows no placeholder");
});

// Review of #185: an element's opacity, or an ancestor's, makes its fill as
// see-through as its colour's alpha does.
test("opacity on a control or above it is part of how see-through it is and how its text reads", () => {
  const body = node({ style: { opacity: "0.5" } });
  const button = node({ parent: body, style: { backgroundColor: "rgb(255, 255, 255)", color: "rgb(0, 0, 0)", opacity: "0.8", borderTopLeftRadius: "999px" } });
  const win = fakeWindow({ grounds: DARK, elements: { ".o-name-edit": button }, body });
  const [dimmed] = controlsReport(win, "orchestrator").controls;
  assert.equal(dimmed.opacity, 0.4);
  assert.equal(dimmed.fillAlpha, 0.4);
  body.style.opacity = "1";
  button.style.opacity = "1";
  const [clear] = controlsReport(win, "orchestrator").controls;
  assert.equal(clear.fillAlpha, 1);
  assert.ok(dimmed.contrast < clear.contrast, `${dimmed.contrast} under ${clear.contrast}`);
});

test("the board and the orchestrator surface report their own controls only", () => {
  const body = node();
  const elements = { ".o-pick-select": node({ parent: body }), ".newcard-title": node({ parent: body }), ".col-size-unfold": node({ parent: body }) };
  assert.deepEqual(controlsReport(fakeWindow({ elements, body }), "board").controls.map((c) => c.name), ["newCardTitle"]);
  assert.deepEqual(controlsReport(fakeWindow({ elements, body }), "orchestrator").controls.map((c) => c.name), ["picker"]);
});

test("the window hears the controls once laid out, and again only when they change", () => {
  const body = node();
  const picker = node({ parent: body, style: { borderTopLeftRadius: "999px" } });
  const win = fakeWindow({ elements: { ".o-pick-select": picker }, body });
  const heard = [];
  const again = watchControls(win, "orchestrator", (report) => heard.push(report));
  win.frames.shift()();
  assert.equal(heard.length, 1);
  again();
  win.frames.shift()();
  assert.equal(heard.length, 1, "the same controls again are not heard again");
  picker.style.borderTopLeftRadius = "4px";
  win.listeners.resize();
  win.frames.shift()();
  assert.equal(heard.length, 2);
  assert.equal(heard[1].controls[0].radius, 4);
});
