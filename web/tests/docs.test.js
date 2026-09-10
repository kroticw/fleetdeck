// The documentation section, driven from a stubbed fetch.
//
// Everything here is about one of four things this section is easy to get wrong:
// showing an empty list where the server said the directories are unconfigured,
// putting a file name from disk into the page as markup, letting a slow answer
// land on a document the operator has already navigated away from, and losing
// the list when one document fails to open.

import { test, beforeEach, afterEach } from "node:test";
import assert from "node:assert/strict";

import { installDOM, fireEvent, settle } from "./fake-dom.js";
import { t } from "../js/i18n.js";

const DOCS = [
  { path: "/docs/index.md", title: "index.md", root: "/docs" },
  { path: "/docs/guide/install.md", title: "guide/install.md", root: "/docs" },
];

// A stub in the shape the section actually meets: fetch resolves with an object
// carrying ok/status and a json() method, and a route that was never taught to
// the stub is a test's mistake, not a silent undefined.
function stubFetch(routes) {
  const calls = [];
  globalThis.fetch = async (url) => {
    calls.push(url);
    for (const [match, reply] of routes) {
      if (url.startsWith(match)) return reply(url);
    }
    throw new Error(`unstubbed fetch: ${url}`);
  };
  return calls;
}

function ok(body) {
  return () => ({ ok: true, status: 200, statusText: "OK", json: async () => body });
}

function refuse(status, error) {
  return () => ({
    ok: false,
    status,
    statusText: `HTTP ${status}`,
    json: async () => ({ error }),
  });
}

// A reply whose body arrives only when the test releases it, so two requests can
// be put in flight and answered out of order.
function deferred(body) {
  let release;
  const gate = new Promise((resolve) => {
    release = resolve;
  });
  return {
    reply: () => ({ ok: true, status: 200, statusText: "OK", json: async () => (await gate, body) }),
    release: () => release(),
  };
}

let dom;
let realFetch;
let renderDocs;
let root;

beforeEach(async () => {
  dom = installDOM();
  realFetch = globalThis.fetch;
  root = dom.element("div");
  ({ renderDocs } = await import("../js/docs.js"));
});

afterEach(() => {
  globalThis.fetch = realFetch;
  dom.restore();
});

test("the list is built from the server's documents", async () => {
  stubFetch([["/api/docs", ok(DOCS)]]);

  renderDocs(root);
  await settle();

  const links = root.querySelectorAll("[data-path]");
  assert.equal(links.length, 2);
  assert.deepEqual(
    links.map((node) => node.textContent),
    ["index.md", "guide/install.md"],
  );
});

test("nothing is selected until a document is opened", async () => {
  stubFetch([["/api/docs", ok(DOCS)]]);

  renderDocs(root);
  await settle();

  assert.match(root.textContent, new RegExp(t("pick_doc")));
});

test("opening a document renders its markdown", async () => {
  stubFetch([
    ["/api/docs/content", ok({ path: "/docs/index.md", body: "# Handbook\n\n- one\n" })],
    ["/api/docs", ok(DOCS)],
  ]);

  renderDocs(root);
  await settle();
  fireEvent(root.querySelector("[data-path]"), "click");
  await settle();

  const body = root.querySelector(".docs-body");
  assert.match(body.innerHTML, /<h2>Handbook<\/h2>/);
  assert.match(body.innerHTML, /<li>one<\/li>/);
});

// The section has no cards to resolve links against, so every wiki link in a
// document is a link that does not work — shown as one, never as a control that
// silently does nothing when clicked.
test("wiki links in a document are rendered as unresolvable", async () => {
  stubFetch([
    ["/api/docs/content", ok({ path: "/docs/index.md", body: "See [[board-convention]].\n" })],
    ["/api/docs", ok(DOCS)],
  ]);

  renderDocs(root);
  await settle();
  fireEvent(root.querySelector("[data-path]"), "click");
  await settle();

  const body = root.querySelector(".docs-body").innerHTML;
  assert.match(body, /wikilink-missing/);
  assert.doesNotMatch(body, /data-link=/);
});

test("the document the operator opened is marked as the current one", async () => {
  stubFetch([
    ["/api/docs/content", ok({ path: "/docs/index.md", body: "# Handbook" })],
    ["/api/docs", ok(DOCS)],
  ]);

  renderDocs(root);
  await settle();
  fireEvent(root.querySelectorAll("[data-path]")[0], "click");
  await settle();

  const [first, second] = root.querySelectorAll("[data-path]");
  assert.match(first.className, /\bon\b/);
  assert.doesNotMatch(second.className, /\bon\b/);
});

// The whole point of the server answering 404 rather than [] for unconfigured
// directories: the section must repeat that statement, not show an empty list,
// which reads as "there is no documentation".
test("unconfigured directories are stated, not shown as an empty list", async () => {
  stubFetch([["/api/docs", refuse(404, "no documentation roots are configured")]]);

  renderDocs(root);
  await settle();

  assert.match(root.textContent, /no documentation roots are configured/);
  assert.equal(root.querySelectorAll("[data-path]").length, 0);
});

test("a refused document does not take the list with it", async () => {
  stubFetch([
    ["/api/docs/content", refuse(403, "documents are served only from the configured documentation roots")],
    ["/api/docs", ok(DOCS)],
  ]);

  renderDocs(root);
  await settle();
  fireEvent(root.querySelector("[data-path]"), "click");
  await settle();

  assert.equal(root.querySelectorAll("[data-path]").length, 2, "the list must survive a failed open");
  assert.match(root.textContent, /documents are served only from the configured documentation roots/);
});

// A file name comes off an operator's disk and is not markup. The section builds
// its list with textContent for exactly this reason; assembling an HTML string
// would put whatever is in the name straight into the page.
test("a document title from disk is never treated as markup", async () => {
  const hostile = [{ path: "/docs/x.md", title: '<img src=x onerror=alert(1)>"', root: "/docs" }];
  stubFetch([["/api/docs", ok(hostile)]]);

  renderDocs(root);
  await settle();

  const link = root.querySelector("[data-path]");
  assert.equal(link.textContent, '<img src=x onerror=alert(1)>"');
  assert.equal(link.innerHTML, "", "the title reached the page as markup");
});

test("the path is sent url-encoded, so a name with a space or an ampersand still opens", async () => {
  const odd = [{ path: "/docs/a b&c.md", title: "a b&c.md", root: "/docs" }];
  const calls = stubFetch([
    ["/api/docs/content", ok({ path: "/docs/a b&c.md", body: "# odd" })],
    ["/api/docs", ok(odd)],
  ]);

  renderDocs(root);
  await settle();
  fireEvent(root.querySelector("[data-path]"), "click");
  await settle();

  const content = calls.find((url) => url.startsWith("/api/docs/content"));
  assert.equal(content, "/api/docs/content?path=%2Fdocs%2Fa%20b%26c.md");
});

// Two opens in a row, answered out of order. Without a guard the slower answer
// paints the earlier document over the one the operator is now looking at.
test("a slow answer never lands on a document the operator has moved on from", async () => {
  const slow = deferred({ path: "/docs/index.md", body: "# First" });
  globalThis.fetch = async (url) => {
    if (url === "/api/docs") return ok(DOCS)();
    if (url.includes("index.md")) return slow.reply();
    return ok({ path: "/docs/guide/install.md", body: "# Second" })();
  };

  renderDocs(root);
  await settle();
  const [first, second] = root.querySelectorAll("[data-path]");
  fireEvent(first, "click");
  fireEvent(second, "click");
  await settle();
  slow.release();
  await settle();

  assert.match(root.querySelector(".docs-body").innerHTML, /Second/);
  assert.doesNotMatch(root.querySelector(".docs-body").innerHTML, /First/);
});

test("a network failure is reported rather than leaving the section blank", async () => {
  globalThis.fetch = async () => {
    throw new Error("connection refused");
  };

  renderDocs(root);
  await settle();

  assert.match(root.textContent, /connection refused/);
});

// A list that came back empty is a real answer — the configured directories hold
// no markdown — and is a different sentence from "nothing is configured".
test("configured directories holding no markdown say so", async () => {
  stubFetch([["/api/docs", ok([])]]);

  renderDocs(root);
  await settle();

  assert.equal(root.querySelectorAll("[data-path]").length, 0);
  assert.match(root.textContent, new RegExp(t("docs_empty")));
});
