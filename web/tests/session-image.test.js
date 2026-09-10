// Attaching an image to a session, from the panel's side.
//
// The route this drives writes a file and hands back its path; it sends nothing.
// The panel must not send either: the operator attaches, then types what the
// image is about, then presses send. Two images and one question is an ordinary
// thing to want, and a panel that sent on attach could not express it.
//
// The rest of what is pinned here is about refusing early. A file too large, or
// of a type the server will not keep, must be refused before it is uploaded —
// an interface that sends 9 MiB to be told the limit is 8 has made the same
// mistake as a message naming the wrong limit.

import { test, beforeEach, afterEach } from "node:test";
import assert from "node:assert/strict";

import { installDOM, fireEvent, settle } from "./fake-dom.js";
import { renderSession } from "../js/session.js";
import { t } from "../js/i18n.js";
import { MAX_IMAGE_BYTES, detectImageType } from "../js/imagefile.js";

const SHORT = "sess-1";
const FULL = "sess-1-4f2c-11ee-9d3a-0242ac120002";

// Real signatures. What the browser calls the file is a claim; these are what
// the server will look at, so the panel looks at them too.
const PNG = new Uint8Array([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 13]);
const JPEG = new Uint8Array([0xff, 0xd8, 0xff, 0xe0, 0, 0x10, 0x4a, 0x46, 0x49, 0x46, 0]);
const GIF = new Uint8Array([0x47, 0x49, 0x46, 0x38, 0x39, 0x61, 0, 0, 0, 0, 0, 0]);
const NOT_AN_IMAGE = new Uint8Array([0x23, 0x21, 0x2f, 0x62, 0x69, 0x6e, 0x2f, 0x73, 0x68]);

// A stand-in for the browser's File. Only what the panel touches: a name, the
// type the browser guessed, a size, and the bytes.
function fakeFile(bytes, { name = "shot.png", type = "image/png", size = null } = {}) {
  return {
    name,
    type,
    size: size ?? bytes.length,
    async arrayBuffer() {
      return bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength);
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
    if (String(url).endsWith("/image")) {
      uploads.push({ url: String(url), body: JSON.parse(options.body ?? "{}"), options });
      return {
        ok: true,
        status: 200,
        statusText: "OK",
        json: async () => ({ path: "/store/sess-1/deadbeef.png" }),
      };
    }
    // Every other route the panel polls: enough to keep it drawing.
    return { ok: true, status: 200, statusText: "OK", json: async () => ({ steps: [], screen: "" }) };
  };
});

afterEach(() => {
  dom.restore();
  globalThis.fetch = realFetch;
  if (realTerminal === undefined) delete globalThis.Terminal;
  else globalThis.Terminal = realTerminal;
});

async function mount() {
  const root = dom.element("div");
  const timers = {
    setTimeout: () => 1,
    setInterval: () => 2,
    clearTimeout() {},
    clearInterval() {},
  };
  const stop = renderSession(root, SHORT, () => {}, {
    timers,
    lookup: () => ({ short: SHORT, sessionId: FULL }),
  });
  await settle();

  return {
    root,
    stop,
    input: () => root.querySelector(".s-input"),
    picker: () => root.querySelector(".s-attach-input"),
    errorText: () => {
      const line = root.querySelector(".s-error");
      return line?.hidden ? "" : (line?.textContent ?? "");
    },
    async attach(file) {
      const picker = root.querySelector(".s-attach-input");
      picker.files = [file];
      fireEvent(picker, "change");
      await settle();
    },
  };
}

test("the form offers a way to attach an image", async () => {
  const panel = await mount();

  assert.ok(panel.root.querySelector(".s-attach"), "no attach control in the session form");
  assert.ok(panel.picker(), "no file input to pick an image with");
});

test("attaching uploads the file and puts the path in the box", async () => {
  const panel = await mount();

  await panel.attach(fakeFile(PNG));

  assert.equal(uploads.length, 1, "the image was not uploaded");
  assert.equal(uploads[0].url, `/api/sessions/${SHORT}/image`);
  assert.match(panel.input().value, /\/store\/sess-1\/deadbeef\.png/);
});

// The whole point of the route not sending: the operator types what the image
// is about, then presses send.
test("attaching sends nothing into the session", async () => {
  const panel = await mount();

  await panel.attach(fakeFile(PNG));

  const sends = uploads.filter((call) => call.url.endsWith("/text"));
  assert.equal(sends.length, 0, "attaching an image sent it into the session on its own");
});

test("the path joins what is already typed instead of replacing it", async () => {
  const panel = await mount();
  panel.input().value = "что тут не так?";

  await panel.attach(fakeFile(PNG));

  assert.match(panel.input().value, /что тут не так\?/);
  assert.match(panel.input().value, /deadbeef\.png/);
});

test("two images make two paths", async () => {
  const panel = await mount();

  await panel.attach(fakeFile(PNG, { name: "one.png" }));
  globalThis.fetch = async (url, options = {}) => {
    uploads.push({ url: String(url), body: JSON.parse(options.body ?? "{}"), options });
    return { ok: true, status: 200, statusText: "OK", json: async () => ({ path: "/store/sess-1/second.png" }) };
  };
  await panel.attach(fakeFile(PNG, { name: "two.png" }));

  assert.match(panel.input().value, /deadbeef\.png/);
  assert.match(panel.input().value, /second\.png/);
});

// Refused here, not by the server: an interface that uploads 9 MiB to be told
// the limit is 8 has made the same mistake as a message naming the wrong limit.
test("a file past the ceiling never leaves the browser", async () => {
  const panel = await mount();

  await panel.attach(fakeFile(PNG, { size: MAX_IMAGE_BYTES + 1 }));

  assert.equal(uploads.length, 0, "an oversized file was uploaded anyway");
  assert.notEqual(panel.errorText(), "", "the refusal was not shown to anyone");
  assert.equal(panel.input().value, "", "a refused attach wrote into the box");
});

// The browser's own type is a claim, and the file name is a weaker one. The
// server decides by the bytes, so the panel checks the same end.
test("a file whose bytes are not an image never leaves the browser", async () => {
  const panel = await mount();

  // Everything about it says PNG except the bytes.
  await panel.attach(fakeFile(NOT_AN_IMAGE, { name: "innocent.png", type: "image/png" }));

  assert.equal(uploads.length, 0, "a non-image was uploaded on the strength of its name");
  assert.notEqual(panel.errorText(), "");
});

test("a real image whose name lies is still accepted, because the bytes decide", async () => {
  const panel = await mount();

  // A JPEG saved as .png with the wrong type on it: the server will keep it and
  // name it correctly, so the panel must not stand in the way.
  await panel.attach(fakeFile(JPEG, { name: "shot.png", type: "text/plain" }));

  assert.equal(uploads.length, 1, "a real image was refused for what it was called");
});

test("a failed upload says so and leaves what was typed alone", async () => {
  const panel = await mount();
  panel.input().value = "уже написано";
  globalThis.fetch = async () => ({
    ok: false,
    status: 415,
    statusText: "Unsupported Media Type",
    json: async () => ({ error: "only PNG, JPEG, GIF and WebP images are accepted" }),
  });

  await panel.attach(fakeFile(PNG));

  assert.match(panel.errorText(), /only PNG, JPEG, GIF and WebP/);
  assert.equal(panel.input().value, "уже написано", "a failed upload disturbed the box");
});

// The cost of keeping files out of the operator's repository: the session's
// first read from the store asks permission. Saying so where the path appears
// is the difference between a person who waits for an answer and a person who
// thinks the panel has hung.
test("the panel says the session may ask for permission", async () => {
  const panel = await mount();

  await panel.attach(fakeFile(PNG));

  assert.match(panel.root.textContent, new RegExp(t("image_may_ask_permission")));
});

test("detectImageType reads the signature, not the extension", () => {
  assert.equal(detectImageType(PNG), "image/png");
  assert.equal(detectImageType(JPEG), "image/jpeg");
  assert.equal(detectImageType(GIF), "image/gif");
  assert.equal(detectImageType(NOT_AN_IMAGE), "");
  assert.equal(detectImageType(new Uint8Array([0x89, 0x50])), "", "a truncated signature is not a match");
  assert.equal(detectImageType(new Uint8Array(0)), "");
});

// WebP is the one signature with a gap in it: "RIFF", four bytes of length that
// are anything at all, then "WEBP". A check that only looked at the front would
// accept every RIFF container — a .wav among them.
test("detectImageType checks both ends of a WebP header", () => {
  const webp = new Uint8Array([
    0x52, 0x49, 0x46, 0x46, 0x24, 0x00, 0x00, 0x00, 0x57, 0x45, 0x42, 0x50,
  ]);
  assert.equal(detectImageType(webp), "image/webp");

  const wav = new Uint8Array([
    0x52, 0x49, 0x46, 0x46, 0x24, 0x00, 0x00, 0x00, 0x57, 0x41, 0x56, 0x45,
  ]);
  assert.equal(detectImageType(wav), "", "a RIFF container that is not WebP was accepted");
});
