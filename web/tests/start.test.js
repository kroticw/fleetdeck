// The start page (web/js/start.js): the screen the application opens on.
//
// Everything that reaches outside the page is passed in, the same way the
// wizard's tests stand in for it: the snapshot subscription, fetch, the
// navigation into a fleet, and the window's folder chooser.
//
// The fake DOM stores innerHTML as a string and never parses it, so nothing
// here asserts on the icon — it is markup. Every part a person clicks is built
// as a real node for exactly that reason: a button made from an innerHTML
// string, and its handler, would be invisible to these tests (see
// docs/engineering/multiple-fleets.md §7).

import { test, beforeEach, afterEach } from "node:test";
import assert from "node:assert/strict";

import { installDOM, fireEvent, settle } from "./fake-dom.js";
import { t } from "../js/i18n.js";
import { renderStart } from "../js/start.js";

let dom;
let root;
let requests;
let routes;
let navigated;
let listeners;

function reply(status, body) {
  return { status, ok: status >= 200 && status < 300, async json() { return body; } };
}

async function fakeFetch(url, init = {}) {
  requests.push({ url, method: init.method ?? "GET", body: init.body ? JSON.parse(init.body) : undefined });
  const route = routes[`${init.method ?? "GET"} ${url}`];
  if (!route) return reply(404, { error: "no route" });
  return typeof route === "function" ? route() : route;
}

// A snapshot subscription under the test's control: renderStart is handed this
// instead of store.js, and the test pushes snapshots through it.
function fakeSubscribe(fn) {
  listeners.push(fn);
  fn(null, false);
  return () => {};
}

function push(snapshot, connected = true) {
  for (const fn of listeners) fn(snapshot, connected);
}

function snapshot(fleets, sessions = []) {
  return { fleet: fleets[0] ?? "", fleets, sessions };
}

function start(extra = {}) {
  renderStart(root, { subscribe: fakeSubscribe, fetch: fakeFetch, navigate: (name) => navigated.push(name), ...extra });
}

function texts(selector) {
  return root.querySelectorAll(selector).map((node) => node.textContent);
}

beforeEach(() => {
  dom = installDOM();
  root = dom.element("main");
  dom.document.body.appendChild(root);
  requests = [];
  routes = {};
  navigated = [];
  listeners = [];
});

afterEach(() => {
  dom.restore();
});

test("every configured fleet is listed, in the order the configuration has them", () => {
  start();
  push(snapshot(["fleetdeck", "vpn", "freshman"]));
  assert.deepEqual(texts(".start-fleet-name"), ["fleetdeck", "vpn", "freshman"]);
});

test("a fleet's own sessions and the sessions no fleet claims are counted together", () => {
  start();
  push(
    snapshot(
      ["fleetdeck", "vpn"],
      [
        { short: "a1", fleets: ["fleetdeck"] },
        { short: "a2", fleets: [] },
        { short: "a3", fleets: ["vpn"] },
      ],
    ),
  );
  // What each fleet's panel will actually show: its own, plus the unclaimed
  // ones, which are shown in every fleet and in none.
  assert.deepEqual(texts(".start-fleet-sessions"), [`${t("fleet_sessions")}: 2`, `${t("fleet_sessions")}: 2`]);
});

test("a question waiting in a fleet is counted on that fleet alone", () => {
  start();
  push(
    snapshot(
      ["fleetdeck", "vpn"],
      [
        { short: "a1", fleets: ["fleetdeck"], needs: "which branch?" },
        { short: "a2", fleets: [], needs: "which branch?" },
      ],
    ),
  );
  // The unclaimed session's question is nobody's fleet's, so it is not counted
  // twice over: only the fleet that claims one is marked.
  assert.deepEqual(texts(".start-fleet-waiting"), [`${t("fleet_waiting")}: 1`]);
});

test("a stalled session is not a question: it is not counted as waiting", () => {
  start();
  push(snapshot(["fleetdeck"], [{ short: "a1", fleets: ["fleetdeck"], needs: "usage limit reached, resets at 14:00" }]));
  assert.deepEqual(texts(".start-fleet-waiting"), []);
});

test("the fleet worked in last time is marked, and it is the one the panel wrote down", () => {
  start({ lastFleet: () => "vpn" });
  push(snapshot(["fleetdeck", "vpn"]));
  const marked = root.querySelectorAll(".start-fleet-last");
  assert.equal(marked.length, 1);
  assert.equal(marked[0].closest(".start-fleet").dataset.fleet, "vpn");
});

test("a remembered fleet that is no longer configured marks nothing", () => {
  start({ lastFleet: () => "gone" });
  push(snapshot(["fleetdeck", "vpn"]));
  assert.deepEqual(texts(".start-fleet-last"), []);
});

test("choosing a fleet leaves for it", async () => {
  start();
  push(snapshot(["fleetdeck", "vpn"]));
  fireEvent(root.querySelectorAll(".start-fleet")[1], "click");
  await settle();
  assert.deepEqual(navigated, ["vpn"]);
});

test("the panel not answering is said, rather than shown as a fleetless machine", () => {
  start();
  push(null, false);
  assert.equal(root.querySelector(".start-offline").hidden, false);
  assert.deepEqual(texts(".start-fleet-name"), []);
});

test("the new-fleet form is out of the way until it is asked for", () => {
  start();
  push(snapshot(["fleetdeck"]));
  assert.equal(root.querySelector(".start-new-form").hidden, true);
  fireEvent(root.querySelector(".start-new"), "click");
  assert.equal(root.querySelector(".start-new-form").hidden, false);
});

// The header's menu leaves for the start page with a fragment meaning "make
// one". Landing on a closed form after pressing "start a fleet" would be a
// second button to find, where the menu had already promised the form.
test("the form is open from the start when the menu sent the person to make one", () => {
  start({ openNew: true });
  push(snapshot(["fleetdeck"]));
  assert.equal(root.querySelector(".start-new-form").hidden, false);
});

test("a fleet with no name is refused here, without asking the panel", async () => {
  start();
  push(snapshot(["fleetdeck"]));
  fireEvent(root.querySelector(".start-new"), "click");
  root.querySelector(".start-new-path").value = "~/vpn";
  fireEvent(root.querySelector(".start-new-create"), "click");
  await settle();
  assert.equal(root.querySelector(".setup-error").textContent, t("start_name_required"));
  assert.deepEqual(requests, []);
});

test("a fleet with no folder is refused here too", async () => {
  start();
  push(snapshot(["fleetdeck"]));
  fireEvent(root.querySelector(".start-new"), "click");
  root.querySelector(".start-new-name").value = "vpn";
  fireEvent(root.querySelector(".start-new-create"), "click");
  await settle();
  assert.equal(root.querySelector(".setup-error").textContent, t("setup_path_required"));
  assert.deepEqual(requests, []);
});

test("making a fleet sends the name and the folder, and reports every step", async () => {
  routes["POST /api/fleets"] = reply(200, {
    ok: true,
    steps: [
      { name: "board", note: "~/vpn/board (made)" },
      { name: "config", note: "~/.config/fleetdeck/config.yaml (fleet vpn added)" },
    ],
  });
  start();
  push(snapshot(["fleetdeck"]));
  fireEvent(root.querySelector(".start-new"), "click");
  root.querySelector(".start-new-name").value = "vpn";
  root.querySelector(".start-new-path").value = "~/vpn";
  fireEvent(root.querySelector(".start-new-create"), "click");
  await settle();
  assert.deepEqual(requests, [{ url: "/api/fleets", method: "POST", body: { name: "vpn", path: "~/vpn" } }]);
  assert.deepEqual(texts(".setup-step"), [
    "board: ~/vpn/board (made)",
    "config: ~/.config/fleetdeck/config.yaml (fleet vpn added)",
  ]);
});

test("a made fleet says the panel has to be restarted before it is served", async () => {
  routes["POST /api/fleets"] = reply(200, { ok: true, steps: [{ name: "config", note: "added" }] });
  start();
  push(snapshot(["fleetdeck"]));
  fireEvent(root.querySelector(".start-new"), "click");
  root.querySelector(".start-new-name").value = "vpn";
  root.querySelector(".start-new-path").value = "~/vpn";
  fireEvent(root.querySelector(".start-new-create"), "click");
  await settle();
  // The fleet is on disk and in the configuration, and this panel will not
  // serve it: saying so is the whole of the honesty here, and the list below
  // deliberately does not grow a fleet that cannot be opened.
  assert.equal(root.querySelector(".setup-status").textContent, t("start_made"));
  assert.deepEqual(texts(".start-fleet-name"), ["fleetdeck"]);
});

test("a refused step is shown with its reason and nothing is called done", async () => {
  routes["POST /api/fleets"] = reply(200, {
    ok: false,
    steps: [{ name: "board", error: "mkdir ~/vpn: permission denied" }],
  });
  start();
  push(snapshot(["fleetdeck"]));
  fireEvent(root.querySelector(".start-new"), "click");
  root.querySelector(".start-new-name").value = "vpn";
  root.querySelector(".start-new-path").value = "~/vpn";
  fireEvent(root.querySelector(".start-new-create"), "click");
  await settle();
  assert.deepEqual(texts(".setup-step"), [`board: ${t("setup_skipped")}: mkdir ~/vpn: permission denied`]);
  assert.equal(root.querySelector(".setup-status").textContent, t("start_failed"));
});

test("the panel's own refusal is shown as it was worded, never reworded here", async () => {
  routes["POST /api/fleets"] = reply(409, { error: 'fleet "vpn" is already there' });
  start();
  push(snapshot(["fleetdeck"]));
  fireEvent(root.querySelector(".start-new"), "click");
  root.querySelector(".start-new-name").value = "vpn";
  root.querySelector(".start-new-path").value = "~/vpn";
  fireEvent(root.querySelector(".start-new-create"), "click");
  await settle();
  assert.equal(root.querySelector(".setup-error").textContent, 'fleet "vpn" is already there');
});

test("a panel that makes no fleets says so instead of offering a button that cannot work", async () => {
  routes["POST /api/fleets"] = reply(503, { error: "this panel does not make fleets" });
  start();
  push(snapshot(["fleetdeck"]));
  fireEvent(root.querySelector(".start-new"), "click");
  root.querySelector(".start-new-name").value = "vpn";
  root.querySelector(".start-new-path").value = "~/vpn";
  fireEvent(root.querySelector(".start-new-create"), "click");
  await settle();
  assert.equal(root.querySelector(".start-new-unavailable").hidden, false);
  assert.equal(root.querySelector(".start-new-create").disabled, true);
});

test("the folder chooser fills the folder in, and is absent outside the window", () => {
  start();
  push(snapshot(["fleetdeck"]));
  fireEvent(root.querySelector(".start-new"), "click");
  assert.equal(root.querySelector(".start-choose"), null);

  root.replaceChildren();
  listeners = [];
  start({ choose: async () => "/Users/x/work/" });
  push(snapshot(["fleetdeck"]));
  fireEvent(root.querySelector(".start-new"), "click");
  assert.notEqual(root.querySelector(".start-choose"), null);
});

test("a chosen folder is put in as the fleet's own, under the name being made", async () => {
  start({ choose: async () => "/Users/x/work/" });
  push(snapshot(["fleetdeck"]));
  fireEvent(root.querySelector(".start-new"), "click");
  root.querySelector(".start-new-name").value = "vpn";
  fireEvent(root.querySelector(".start-choose"), "click");
  await settle();
  assert.equal(root.querySelector(".start-new-path").value, "/Users/x/work/vpn");
});

test("a second press while the panel is working sends nothing twice", async () => {
  let release;
  routes["POST /api/fleets"] = () => new Promise((resolve) => { release = () => resolve(reply(200, { ok: true, steps: [] })); });
  start();
  push(snapshot(["fleetdeck"]));
  fireEvent(root.querySelector(".start-new"), "click");
  root.querySelector(".start-new-name").value = "vpn";
  root.querySelector(".start-new-path").value = "~/vpn";
  fireEvent(root.querySelector(".start-new-create"), "click");
  await settle();
  fireEvent(root.querySelector(".start-new-create"), "click");
  await settle();
  assert.equal(requests.length, 1);
  release();
  await settle();
});
