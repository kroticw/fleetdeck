// What a Codex session waits on, answered by an explicit press next to its
// terminal: never automatically, never a session-wide grant.

import { test, beforeEach, afterEach } from "node:test";
import assert from "node:assert/strict";

import { installDOM, fireEvent, settle } from "./fake-dom.js";

let dom;
let host;
let createRequests;
let pending;
let answers;
let refuse;

beforeEach(async () => {
  dom = installDOM();
  host = dom.element("div");
  dom.document.body.appendChild(host);
  pending = [];
  answers = [];
  refuse = null;
  ({ createRequests } = await import("../js/requests.js"));
});

afterEach(() => dom.restore());

const make = () =>
  createRequests(host, "codex:x", {
    list: async () => pending,
    respond: async (id, answer) => {
      answers.push({ id, ...answer });
      if (refuse) throw refuse;
      pending = [];
    },
  });

test("requests: nothing pending shows nothing", async () => {
  const r = make();
  await r.refresh();
  assert.equal(r.node.hidden, true);
});

test("requests: a command is shown as it will run and answered once by an explicit press", async () => {
  pending = [{ token: "t1", kind: "command", command: "rm -rf build", cwd: "/repo", reason: "clean" }];
  const r = make();
  await r.refresh();
  assert.equal(r.node.hidden, false);
  assert.match(r.node.textContent, /rm -rf build/);
  assert.match(r.node.textContent, /\/repo/);
  const buttons = [...r.node.querySelectorAll("button")].map((b) => b.dataset.decision);
  assert.deepEqual(buttons, ["accept", "decline", "cancel"]);
  assert.equal(answers.length, 0);
  fireEvent(r.node.querySelector('button[data-decision="decline"]'), "click");
  await settle();
  assert.deepEqual(answers, [{ id: "codex:x", token: "t1", decision: "decline" }]);
  assert.equal(r.node.hidden, true);
});

test("requests: a question is answered with the chosen option", async () => {
  pending = [{ token: "q1", kind: "userInput", questions: [{ id: "color", header: "Color", question: "Which?", options: ["red", "blue"] }] }];
  const r = make();
  await r.refresh();
  fireEvent(r.node.querySelector('button[data-option="blue"]'), "click");
  await settle();
  assert.deepEqual(answers, [{ id: "codex:x", token: "q1", answers: { color: ["blue"] } }]);
});

test("requests: a stale answer says so and shows the current requests", async () => {
  pending = [{ token: "t1", kind: "fileChange", reason: "edit" }];
  const r = make();
  await r.refresh();
  refuse = Object.assign(new Error("this request was already answered or replaced; reload the requests"), { status: 409 });
  pending = [{ token: "t2", kind: "fileChange", reason: "edit again" }];
  fireEvent(r.node.querySelector('button[data-decision="accept"]'), "click");
  await settle();
  assert.match(r.node.textContent, /already answered/);
  assert.match(r.node.textContent, /edit again/);
});

test("requests: a kind the panel cannot answer points to the terminal and offers no buttons", async () => {
  pending = [{ token: "o1", kind: "other", method: "item/permissions/requestApproval" }];
  const r = make();
  await r.refresh();
  assert.equal(r.node.querySelectorAll("button").length, 0);
  assert.match(r.node.textContent, /permissions/);
});
