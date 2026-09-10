// Attaching an image to a session, from the panel's side.
//
// The mechanics of a paste live in pasteimage.js and are pinned in
// paste-image.test.js. What is pinned here is the wiring: that the session panel
// listens on its own box, that it uploads to the session it is pointing at, that
// its two message lines carry the outcome — and that the button which used to
// stand next to the input is gone, which is what the operator asked for.

import { test, beforeEach, afterEach } from "node:test";
import assert from "node:assert/strict";

import { installDOM, settle } from "./fake-dom.js";
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

async function mount() {
  const root = dom.element("div");
  const timers = { setTimeout: () => 1, setInterval: () => 2, clearTimeout() {}, clearInterval() {} };
  const stop = renderSession(root, SHORT, () => {}, {
    timers,
    lookup: () => ({ short: SHORT, sessionId: FULL }),
  });
  await settle();

  return {
    root,
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
