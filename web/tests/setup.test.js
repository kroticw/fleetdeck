// The page a panel with no configuration shows: choose the workspace, make it,
// then hand over to the panel. These pin what it sends, that every step's
// outcome is shown (a refused one included), and that it moves on to the panel
// only once the panel actually answers.

import { test, beforeEach, afterEach } from "node:test";
import assert from "node:assert/strict";

import { installDOM, fireEvent, settle } from "./fake-dom.js";

let dom;
let root;
let renderSetup;
let requests;
let routes;
let reloaded;

function reply(status, body) {
  return { status, ok: status >= 200 && status < 300, async json() { return body; } };
}

async function fakeFetch(url, init = {}) {
  requests.push({ url, method: init.method ?? "GET", body: init.body ? JSON.parse(init.body) : undefined });
  const route = routes[`${init.method ?? "GET"} ${url}`];
  if (!route) return reply(404, { error: "no route" });
  return typeof route === "function" ? route() : route;
}

const OK_STEPS = {
  ok: true,
  steps: [
    { name: "config", note: "/h/.config/fleetdeck/config.yaml (created, board: /h/fleetdeck/board)" },
    { name: "workspace", note: "/h/fleetdeck (board created, docs created)" },
    { name: "statusline", error: "no fleetdeck-status found beside /app/fleetdeck" },
    { name: "permissions", note: "/h/.claude/settings.json additionalDirectories -> /h/fleetdeck (added)" },
  ],
};

async function render(extra = {}) {
  renderSetup(root, { fetch: fakeFetch, reload: () => (reloaded += 1), wait: async () => {}, ...extra });
  await settle();
}

function create() {
  fireEvent(root.querySelector("button.setup-create"), "click");
  return settle();
}

beforeEach(async () => {
  dom = installDOM();
  root = dom.element("main");
  dom.document.body.appendChild(root);
  requests = [];
  reloaded = 0;
  routes = {
    "GET /api/setup": reply(200, { default: "/h/fleetdeck" }),
    "POST /api/setup": reply(200, OK_STEPS),
    "GET /api/snapshot": reply(200, {}),
  };
  ({ renderSetup } = await import("../js/setup.js"));
});

afterEach(() => {
  dom.restore();
});

test("setup: the path starts as the workspace the panel proposes", async () => {
  await render();
  assert.equal(root.querySelector("input.setup-path").value, "/h/fleetdeck");
});

test("setup: says what will be written outside the chosen folder before anything is", async () => {
  await render();
  const text = root.textContent;
  assert.match(text, /additionalDirectories/);
  assert.match(text, /settings\.json/);
  assert.equal(requests.filter((r) => r.method === "POST").length, 0);
});

test("setup: no folder chooser in a browser, where nothing provides one", async () => {
  await render();
  assert.equal(root.querySelector("button.setup-choose"), null);
});

test("setup: the window's chooser picks the folder the workspace is made in", async () => {
  await render({ choose: async () => "/Users/me/Documents/" });
  fireEvent(root.querySelector("button.setup-choose"), "click");
  await settle();
  assert.equal(root.querySelector("input.setup-path").value, "/Users/me/Documents/fleetdeck");
});

test("setup: a chooser closed without a folder leaves the path as it was", async () => {
  await render({ choose: async () => "" });
  fireEvent(root.querySelector("button.setup-choose"), "click");
  await settle();
  assert.equal(root.querySelector("input.setup-path").value, "/h/fleetdeck");
});

test("setup: create sends the path and shows every step, a refused one with its reason", async () => {
  await render();
  root.querySelector("input.setup-path").value = "  /srv/fleet  ";
  await create();
  const post = requests.find((r) => r.method === "POST");
  assert.deepEqual(post.body, { path: "/srv/fleet" });
  const items = root.querySelectorAll("li.setup-step");
  assert.equal(items.length, 4);
  assert.match(items[2].textContent, /statusline/);
  assert.match(items[2].textContent, /no fleetdeck-status found/);
  assert.ok(items[2].className.includes("setup-step-skipped"));
});

test("setup: once made, the page waits for the panel to answer, then opens it", async () => {
  let asked = 0;
  routes["GET /api/snapshot"] = () => {
    asked += 1;
    return asked < 3 ? reply(503, { error: "this panel is not set up yet" }) : reply(200, {});
  };
  await render();
  await create();
  await settle();
  assert.equal(asked, 3);
  assert.equal(reloaded, 1);
});

test("setup: a setup that could not make the board stays on this page", async () => {
  routes["POST /api/setup"] = reply(200, { ok: false, steps: [{ name: "workspace", error: "permission denied" }] });
  await render();
  await create();
  assert.equal(reloaded, 0);
  assert.equal(root.querySelector("button.setup-create").disabled, false, "the person can choose another folder");
  assert.match(root.textContent, /permission denied/);
});

test("setup: a refused path shows the panel's words and can be corrected", async () => {
  routes["POST /api/setup"] = reply(400, { error: "give an absolute path or one starting with ~/" });
  await render();
  root.querySelector("input.setup-path").value = "fleet";
  await create();
  assert.match(root.querySelector("div.setup-error").textContent, /absolute path/);
  assert.equal(root.querySelector("button.setup-create").disabled, false);
  assert.equal(root.querySelector("input.setup-path").value, "fleet");
});

test("setup: an empty path sends nothing", async () => {
  await render();
  root.querySelector("input.setup-path").value = "  ";
  await create();
  assert.equal(requests.filter((r) => r.method === "POST").length, 0);
  assert.notEqual(root.querySelector("div.setup-error").textContent, "");
});
