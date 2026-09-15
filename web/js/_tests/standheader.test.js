// web/js/_tests/standheader.test.js
//
// Run with: node --test web/js/_tests/standheader.test.js

import test from "node:test";
import assert from "node:assert/strict";
import { headerLineReport, watchHeaderLine } from "../standheader.js";

function fakeWindow({ fullscreen = false } = {}) {
  const listeners = {};
  const frames = [];
  return {
    document: { documentElement: { dataset: fullscreen ? { fullscreen: "1" } : {} } },
    addEventListener: (name, fn) => (listeners[name] = fn),
    requestAnimationFrame: (fn) => frames.push(fn),
    listeners,
    frames,
  };
}

// A header top..top+height from the surface's top, its brand likewise.
function fakeHeader({ top, height, brand }) {
  return {
    getBoundingClientRect: () => ({ top, height }),
    querySelector: (selector) => {
      assert.equal(selector, ".brand");
      return brand ? { getBoundingClientRect: () => ({ top: brand.top, height: brand.height }) } : null;
    },
  };
}

// The line the window's buttons are centred on is 18 pt down the panel: a 36 pt
// header whose brand line is 20 pt tall.
test("the orchestrator's header says where its row and its brand are centred", () => {
  const report = headerLineReport(fakeWindow(), fakeHeader({ top: 0, height: 36, brand: { top: 8.2, height: 20 } }));
  assert.deepEqual(report, { surface: "orchestrator", headerRowCenter: 18, brandCenter: 18.2, fullscreen: false });
});

// v0.10.1: the header's padding put its row lower than the buttons.
test("a header lower than the buttons' line says so", () => {
  const report = headerLineReport(fakeWindow(), fakeHeader({ top: 0, height: 44, brand: { top: 12, height: 20 } }));
  assert.equal(report.headerRowCenter, 22);
  assert.equal(report.brandCenter, 22);
});

test("in full screen the header says it is", () => {
  assert.equal(headerLineReport(fakeWindow({ fullscreen: true }), fakeHeader({ top: 0, height: 36 })).fullscreen, true);
});

test("a header with no brand yet cannot say where the brand is", () => {
  assert.equal(headerLineReport(fakeWindow(), fakeHeader({ top: 0, height: 36 })).brandCenter, null);
});

test("the window hears the header once laid out, and again only when it changes", () => {
  const win = fakeWindow();
  const box = { top: 0, height: 36, brand: { top: 8, height: 20 } };
  const header = {
    getBoundingClientRect: () => ({ top: box.top, height: box.height }),
    querySelector: () => ({ getBoundingClientRect: () => ({ top: box.brand.top, height: box.brand.height }) }),
  };
  const heard = [];
  const again = watchHeaderLine(win, header, (report) => heard.push(report));
  win.frames.shift()();
  assert.equal(heard.length, 1);
  again();
  win.frames.shift()();
  assert.equal(heard.length, 1, "the same header again is not heard again");
  box.height = 44;
  box.brand.top = 12;
  win.listeners.resize();
  win.frames.shift()();
  assert.equal(heard.length, 2);
  assert.equal(heard[1].headerRowCenter, 22);
});
