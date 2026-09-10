// Attaching an image to a session, from the panel's side.
//
// The mechanics of a paste live in pasteimage.js and are pinned in
// paste-image.test.js. What is pinned here is the wiring: that the session panel
// listens on its own box, that it uploads to the session it is pointing at, that
// its two message lines carry the outcome — and that the button which used to
// stand next to the input is gone, which is what the operator asked for.

import { test, beforeEach, afterEach } from "node:test";
import assert from "node:assert/strict";

import { installDOM, fireEvent, settle } from "./fake-dom.js";
import { renderSession } from "../js/session.js";

const SHORT = "sess-1";
const FULL = "sess-1-4f2c-11ee-9d3a-0242ac120002";

const PNG = new Uint8Array([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 13]);

function fakeFile(bytes, { type = "image/png", size = null } = {}) {
  return {
    name: "clipboard.png",
    type,
    size: size ?? bytes.length,
    async arrayBuffer() {
      return bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength);
    },
  };
}

function pasteEvent({ file = null, text = null } = {}) {
  const items = [];
  if (text !== null) items.push({ kind: "string", type: "text/plain", getAsFile: () => null });
  if (file) items.push({ kind: "file", type: file.type, getAsFile: () => file });
  let prevented = false;
  return {
    type: "paste",
    clipboardData: { items, files: file ? [file] : [] },
    preventDefault() {
      prevented = true;
    },
    get defaultPrevented() {
      return prevented;
    },
  };
}

let dom;
let realFetch;
let realTerminal;
let uploads;

beforeEach(() => {
  dom = installDOM();
  realFetch = globalThis.fetch;
  realTerminal = globalThis.Terminal;
  globalThis.Terminal = undefined;
  uploads = [];
  globalThis.fetch = async (url, options = {}) => {
    const target = String(url);
    if (target.endsWith("/image")) {
      uploads.push({ url: target, body: JSON.parse(options.body ?? "{}") });
      return {
        ok: true,
        status: 200,
        statusText: "OK",
        json: async () => ({ path: "/store/sess-1/pasted.png" }),
      };
    }
    return { ok: true, status: 200, statusText: "OK", json: async () => ({ steps: [], screen: "" }) };
  };
});

afterEach(() => {
  globalThis.fetch = realFetch;
  if (realTerminal === undefined) delete globalThis.Terminal;
  else globalThis.Terminal = realTerminal;
  dom.restore();
});

// A clock this file can advance by hand. It used to be four stubs that swallowed
// every timer, which made the panel's background poll unrepresentable here — and
// the poll is exactly what was erasing the messages these tests are about.
function fakeTimers() {
  let nextId = 1;
  const pending = new Map();
  const cancel = (id) => pending.delete(id);
  return {
    setTimeout(fn) {
      const id = nextId++;
      pending.set(id, { fn, repeating: false });
      return id;
    },
    setInterval(fn) {
      const id = nextId++;
      pending.set(id, { fn, repeating: true });
      return id;
    },
    clearTimeout: cancel,
    clearInterval: cancel,
    async tick() {
      for (const [id, timer] of [...pending.entries()]) {
        if (!timer.repeating) pending.delete(id);
        timer.fn();
      }
      await settle();
      await settle();
    },
  };
}

async function mount() {
  const root = dom.element("div");
  const timers = fakeTimers();
  const stop = renderSession(root, SHORT, () => {}, {
    timers,
    lookup: () => ({ short: SHORT, sessionId: FULL }),
  });
  await settle();

  return {
    root,
    timers,
    stop,
    input: () => root.querySelector(".s-input"),
    errorText: () => {
      const line = root.querySelector(".s-error");
      return line?.hidden ? "" : (line?.textContent ?? "");
    },
    noticeText: () => {
      const line = root.querySelector(".s-notice");
      return line?.hidden ? "" : (line?.textContent ?? "");
    },
    async paste(event) {
      root.querySelector(".s-input").dispatchEvent(event);
      await settle();
      return event;
    },
  };
}

// The operator's own words about the button were "ужас": it took room in the
// writing area for something that should just happen. This fails if it returns.
test("there is no attach button beside the input", async () => {
  const panel = await mount();

  assert.equal(panel.root.querySelector(".s-attach"), null, "the attach button is back");
  assert.equal(panel.root.querySelector(".s-attach-input"), null, "the file picker is back");
  assert.equal(panel.root.querySelector(".s-attach-row"), null, "the attach row is back");
});

test("pasting an image uploads it to this session and puts the path in the box", async () => {
  const panel = await mount();

  await panel.paste(pasteEvent({ file: fakeFile(PNG) }));

  assert.equal(uploads.length, 1);
  assert.equal(uploads[0].url, `/api/sessions/${SHORT}/image`);
  assert.match(panel.input().value, /\/store\/sess-1\/pasted\.png/);
});

// The control case, and the one that would hurt most: Cmd+V on text is what a
// person does constantly.
test("pasting text is left alone entirely", async () => {
  const panel = await mount();

  const event = await panel.paste(pasteEvent({ text: "обычный текст" }));

  assert.equal(event.defaultPrevented, false, "an ordinary text paste was cancelled");
  assert.equal(uploads.length, 0);
  assert.equal(panel.input().value, "");
});

test("pasting sends nothing into the session", async () => {
  const panel = await mount();

  await panel.paste(pasteEvent({ file: fakeFile(PNG) }));

  assert.equal(uploads.filter((u) => u.url.endsWith("/text")).length, 0);
});

test("the permission notice appears in the panel's own notice line", async () => {
  const panel = await mount();

  await panel.paste(pasteEvent({ file: fakeFile(PNG) }));

  assert.notEqual(panel.noticeText(), "", "nothing told the operator a prompt is coming");
});

test("a refused paste reports into the panel's error line", async () => {
  const panel = await mount();
  globalThis.fetch = async () => ({
    ok: false,
    status: 415,
    statusText: "Unsupported Media Type",
    json: async () => ({ error: "only PNG, JPEG, GIF and WebP images are accepted" }),
  });

  await panel.paste(pasteEvent({ file: fakeFile(PNG) }));

  assert.match(panel.errorText(), /only PNG, JPEG, GIF and WebP/);
});

// The textarea is rebuilt on every tab switch, and the handler is attached to
// the node. Attached again without disposing the previous one, a single paste
// would upload the same image twice.
test("switching tabs does not leave a second handler behind", async () => {
  const panel = await mount();

  // .s-tab, not a descendant selector: the stand-in DOM matches one level, and
  // asking it for ".s-tabs button" throws rather than returning nothing.
  for (const tab of panel.root.querySelectorAll(".s-tab")) {
    tab.dispatchEvent({ type: "click", target: tab, preventDefault() {} });
    await settle();
  }

  await panel.paste(pasteEvent({ file: fakeFile(PNG) }));

  assert.equal(uploads.length, 1, `one paste produced ${uploads.length} uploads`);
});

// And the same on the way out: a closed panel must stop listening.
test("a stopped panel stops accepting pastes", async () => {
  const panel = await mount();
  const input = panel.input();
  panel.stop();

  input.dispatchEvent(pasteEvent({ file: fakeFile(PNG) }));
  await settle();

  assert.equal(uploads.length, 0);
});

// --- the two message lines, compared against the other pane -----------------
//
// Everything below came out of pasting the same things into this panel and into
// the orchestrator column and printing the two answers side by side. All of it
// was invisible to the tests above, which asked whether this panel says the
// right thing and never whether it goes on saying it.

// The worst of the three, and the one a person would report as "nothing
// happened": the panel showed why an image was refused, and the next digest
// poll — a few seconds later, at most — wiped it. Break it by pointing
// digestPass's success back at showError and this test fails with an empty line.
test("a refused paste survives the background poll that follows it", async () => {
  const panel = await mount();
  globalThis.fetch = async (url) => {
    if (String(url).endsWith("/image")) {
      return { ok: false, status: 415, statusText: "", json: async () => ({ error: "not an image the panel can attach" }) };
    }
    return { ok: true, status: 200, statusText: "OK", json: async () => ({ steps: [], screen: "" }) };
  };

  await panel.paste(pasteEvent({ file: fakeFile(PNG) }));
  assert.match(panel.errorText(), /not an image/, "precondition: the refusal was shown");

  await panel.timers.tick();

  assert.match(panel.errorText(), /not an image/, "a successful background poll erased the refusal");
});

// The other direction of the same rule: a failing poll must not be silenced by
// an old operator error either, and neither may clear the other's slot.
test("a failing poll reports without waiting for the operator to do something", async () => {
  const panel = await mount();
  globalThis.fetch = async () => {
    throw new Error("the daemon went away");
  };

  await panel.timers.tick();

  assert.match(panel.errorText(), /daemon went away/);
});

// It describes a path that has just left the box, so it must not outlive it —
// and the orchestrator column already behaved this way, which is how the
// difference was found.
test("the permission notice goes away when the message is sent", async () => {
  const panel = await mount();

  await panel.paste(pasteEvent({ file: fakeFile(PNG) }));
  assert.notEqual(panel.noticeText(), "", "precondition: the notice was shown");

  panel.input().dispatchEvent({ type: "keydown", key: "Enter", shiftKey: false, preventDefault() {} });
  await settle();
  await settle();

  assert.equal(panel.noticeText(), "", "the notice outlived the path it was about");
});

// But only when the message actually went. A failed send puts the path back in
// the box, and the sentence about the permission prompt is true again with it.
test("a failed send keeps both the path and the notice", async () => {
  const panel = await mount();
  await panel.paste(pasteEvent({ file: fakeFile(PNG) }));
  const withPath = panel.input().value;
  assert.notEqual(withPath, "", "precondition: the path is in the box");

  globalThis.fetch = async () => {
    throw new Error("the daemon refused it");
  };
  panel.input().dispatchEvent({ type: "keydown", key: "Enter", shiftKey: false, preventDefault() {} });
  await settle();
  await settle();

  assert.equal(panel.input().value, withPath, "the path was lost with the failed send");
  assert.notEqual(panel.noticeText(), "", "the notice went away while the path it describes stayed");
});

// The panel rebuilds both lines on a tab switch, and switching to the screen tab
// to see what a session is actually asking is exactly when there is a message
// worth keeping. Break it by removing the repaint after replaceChildren and this
// test fails with an empty line.
test("a message survives a tab switch, like the half-written text beside it", async () => {
  const panel = await mount();
  globalThis.fetch = async (url) => {
    if (String(url).endsWith("/image")) {
      return { ok: false, status: 415, statusText: "", json: async () => ({ error: "not an image the panel can attach" }) };
    }
    return { ok: true, status: 200, statusText: "OK", json: async () => ({ steps: [], screen: "" }) };
  };
  await panel.paste(pasteEvent({ file: fakeFile(PNG) }));
  assert.match(panel.errorText(), /not an image/, "precondition: the refusal was shown");
  const errorLineBefore = panel.root.querySelector(".s-error");

  // The screen tab by name, not "every tab in turn": selectTab does nothing
  // when the tab asked for is the one already open, so a loop that starts on
  // the digest tab can end back on it having rebuilt nothing — which is how
  // this test first passed against a panel that did throw the message away.
  const screen = panel.root.querySelector('[data-tab="screen"]');
  assert.ok(screen, "no screen tab to switch to");
  fireEvent(screen, "click");
  await settle();
  await settle();

  // The switch has to have happened, or this test proves nothing: the line it
  // reads would simply be the one that was never rebuilt.
  assert.notEqual(panel.root.querySelector(".s-error"), errorLineBefore, "the tab switch did not rebuild the panel");
  assert.match(panel.errorText(), /not an image/, "the message was thrown away with the line it sat in");
});
