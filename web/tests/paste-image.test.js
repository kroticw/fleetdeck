// Pasting an image into a panel's own input.
//
// The first test in this file is the one that matters most, and it is about
// text: Cmd+V is what a person uses constantly, and attaching an image is what
// they do occasionally. A handler that swallowed an ordinary paste would break
// the common thing for the rare one, so the rule is that nothing happens at all
// unless the clipboard actually carries an image.
//
// Everything else here is the same two refusals the button had — too large, not
// an image — called from the new place rather than rewritten.

import { test, beforeEach, afterEach } from "node:test";
import assert from "node:assert/strict";

import { installDOM, settle } from "./fake-dom.js";
import { wireImagePaste } from "../js/pasteimage.js";
import { MAX_IMAGE_BYTES } from "../js/imagefile.js";

const PNG = new Uint8Array([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 13]);
const NOT_AN_IMAGE = new Uint8Array([0x23, 0x21, 0x2f, 0x62, 0x69, 0x6e]);

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

// A paste event shaped like the browser's: clipboardData.items, each with a
// kind, and getAsFile() on the ones that are files. Text pastes carry a "string"
// item and no file at all.
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
let input;
let uploads;
let errors;
let realFetch;

beforeEach(() => {
  dom = installDOM();
  input = dom.element("textarea");
  input.value = "";
  uploads = [];
  errors = [];
  realFetch = globalThis.fetch;
  globalThis.fetch = async (url, options = {}) => {
    uploads.push({ url: String(url), body: JSON.parse(options.body ?? "{}") });
    return {
      ok: true,
      status: 200,
      statusText: "OK",
      json: async () => ({ path: "/store/sess-1/pasted.png" }),
    };
  };
});

afterEach(() => {
  globalThis.fetch = realFetch;
  dom.restore();
});

function wire({ session = () => "sess-1" } = {}) {
  return wireImagePaste(input, session, {
    onError: (message) => errors.push(message),
    onNotice: () => {},
  });
}

// The control case, first and deliberately: an ordinary text paste must be left
// completely alone.
test("a text paste is not touched at all", async () => {
  wire();
  const event = pasteEvent({ text: "просто текст" });

  input.dispatchEvent(event);
  await settle();

  assert.equal(event.defaultPrevented, false, "the handler cancelled an ordinary text paste");
  assert.equal(uploads.length, 0, "a text paste caused an upload");
  assert.equal(input.value, "", "the handler wrote into the box on a text paste");
  assert.deepEqual(errors, []);
});

// The same for an empty clipboard and for one carrying a non-image file: neither
// is an image, so neither is this handler's business.
test("a paste with no image is not touched either", async () => {
  wire();
  for (const event of [pasteEvent({}), pasteEvent({ file: fakeFile(NOT_AN_IMAGE, { type: "application/zip" }) })]) {
    input.dispatchEvent(event);
    await settle();
    assert.equal(event.defaultPrevented, false);
  }
  assert.equal(uploads.length, 0);
});

test("an image paste is uploaded and its path lands in the box", async () => {
  wire();
  const event = pasteEvent({ file: fakeFile(PNG) });

  input.dispatchEvent(event);
  await settle();

  assert.equal(event.defaultPrevented, true, "the browser was left to paste the image as well");
  assert.equal(uploads.length, 1);
  assert.equal(uploads[0].url, "/api/sessions/sess-1/image");
  assert.match(input.value, /\/store\/sess-1\/pasted\.png/);
});

test("the path joins what is already typed", async () => {
  wire();
  input.value = "что тут не так?";

  input.dispatchEvent(pasteEvent({ file: fakeFile(PNG) }));
  await settle();

  assert.match(input.value, /что тут не так\?/);
  assert.match(input.value, /pasted\.png/);
});

// The session is resolved when the paste happens, not when the handler is wired:
// the orchestrator column re-points at another session while the same textarea
// stays in place, and an image pasted after that must go to the new one.
test("the session is resolved at paste time", async () => {
  let current = "first";
  wireImagePaste(input, () => current, { onError: (m) => errors.push(m), onNotice: () => {} });

  input.dispatchEvent(pasteEvent({ file: fakeFile(PNG) }));
  await settle();
  current = "second";
  input.dispatchEvent(pasteEvent({ file: fakeFile(PNG) }));
  await settle();

  assert.deepEqual(
    uploads.map((u) => u.url),
    ["/api/sessions/first/image", "/api/sessions/second/image"],
  );
});

// With nowhere to send it, the upload must not happen at all — a file written
// under a session id of "undefined" is rubbish nobody will ever look at.
test("no session, no upload", async () => {
  wire({ session: () => "" });

  const event = pasteEvent({ file: fakeFile(PNG) });
  input.dispatchEvent(event);
  await settle();

  assert.equal(uploads.length, 0);
  assert.notEqual(errors.length, 0, "the operator was not told why nothing happened");
});

// The two refusals are the button's own, called from here rather than rewritten.
test("an image past the ceiling is refused before it is uploaded", async () => {
  wire();

  input.dispatchEvent(pasteEvent({ file: fakeFile(PNG, { size: MAX_IMAGE_BYTES + 1 }) }));
  await settle();

  assert.equal(uploads.length, 0);
  assert.notEqual(errors.length, 0);
  assert.equal(input.value, "");
});

test("something that is not an image by its bytes is refused before it is uploaded", async () => {
  wire();

  // Everything about it claims PNG except the content.
  input.dispatchEvent(pasteEvent({ file: fakeFile(NOT_AN_IMAGE, { type: "image/png" }) }));
  await settle();

  assert.equal(uploads.length, 0);
  assert.notEqual(errors.length, 0);
});

test("a failed upload reports the server's words and leaves the box alone", async () => {
  wire();
  input.value = "уже написано";
  globalThis.fetch = async () => ({
    ok: false,
    status: 413,
    statusText: "Payload Too Large",
    json: async () => ({ error: "an image must not exceed 8388608 bytes" }),
  });

  input.dispatchEvent(pasteEvent({ file: fakeFile(PNG) }));
  await settle();

  assert.match(errors.join(" "), /8388608/);
  assert.equal(input.value, "уже написано");
});

// Two images in one paste is a thing a file manager will do. Both belong in the
// message, not just whichever came first.
test("several images in one paste all land in the box", async () => {
  wire();
  let n = 0;
  globalThis.fetch = async (url, options = {}) => {
    uploads.push({ url: String(url), body: JSON.parse(options.body ?? "{}") });
    n += 1;
    return { ok: true, status: 200, statusText: "OK", json: async () => ({ path: `/store/p${n}.png` }) };
  };

  const first = fakeFile(PNG);
  const second = fakeFile(PNG);
  const event = pasteEvent({ file: first });
  event.clipboardData.items.push({ kind: "file", type: second.type, getAsFile: () => second });

  input.dispatchEvent(event);
  await settle();

  assert.equal(uploads.length, 2);
  assert.match(input.value, /p1\.png/);
  assert.match(input.value, /p2\.png/);
});

// The handler is removable: a panel torn down and rebuilt would otherwise leave
// one behind on every open, and a single paste would upload the same image
// several times.
test("the handler can be removed", async () => {
  const dispose = wire();
  dispose();

  input.dispatchEvent(pasteEvent({ file: fakeFile(PNG) }));
  await settle();

  assert.equal(uploads.length, 0);
});
