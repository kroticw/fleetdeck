// The page a panel with no configuration shows: choose the workspace, make it,
// then hand over to the panel. These pin what it sends, that every step's
// outcome is shown (a refused one included), and that it moves on to the panel
// only once the panel actually answers.

import { test, beforeEach, afterEach } from "node:test";
import assert from "node:assert/strict";

import { installDOM, fireEvent, settle } from "./fake-dom.js";
import { t, langCode } from "../js/i18n.js";

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

// every is stood in for by default: a real interval would keep node running
// after the last test, polling a fake panel nobody is looking at.
async function render(extra = {}) {
  renderSetup(root, { fetch: fakeFetch, reload: () => (reloaded += 1), wait: async () => {}, every: () => () => {}, ...extra });
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
  const asked = [];
  await render({
    choose: async (...args) => {
      asked.push(args);
      return "/Users/me/Documents/";
    },
  });
  fireEvent(root.querySelector("button.setup-choose"), "click");
  await settle();
  assert.equal(root.querySelector("input.setup-path").value, "/Users/me/Documents/fleetdeck");
  // The window has no dictionary of its own: the chooser's words come from here.
  assert.equal(asked.length, 1);
  const [message, prompt] = asked[0];
  assert.ok(message && !message.startsWith("setup_"), `the chooser's message is a sentence, not a key: ${message}`);
  assert.ok(prompt && !prompt.startsWith("setup_"), `the chooser's button is a word, not a key: ${prompt}`);
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

// The panel is not opened straight after setup any more: the orchestrator step
// comes first. The property carried over is the one this test was about — the
// page moves on only once the panel actually answers.
test("setup: once made, the page waits for the panel to answer, then goes on to the orchestrator", async () => {
  let asked = 0;
  routes["GET /api/snapshot"] = () => {
    asked += 1;
    return asked < 3 ? reply(503, { error: "this panel is not set up yet" }) : reply(200, {});
  };
  await render();
  await create();
  await settle();
  assert.ok(asked >= 3, `the page moved on after ${asked} asks; the panel answered on the third`);
  assert.equal(root.querySelector("h1.setup-title").textContent, t("wizard_title"));
  assert.equal(root.querySelector("input.setup-path"), null, "the folder step is gone");
  assert.equal(reloaded, 0, "the panel is opened from the orchestrator step, not before it");
  // What setup did stays readable: the next step carries it.
  const carried = root.querySelector("div.wizard-made").querySelectorAll("li.setup-step");
  assert.equal(carried.length, 4);
  assert.match(carried[2].textContent, /no fleetdeck-status found/);
});

test("setup: a panel that never takes over is said so, and the orchestrator step is not shown", async () => {
  routes["GET /api/snapshot"] = reply(503, { error: "this panel is not set up yet" });
  await render();
  await create();
  await settle();
  assert.equal(root.querySelector("section.wizard-new"), null);
  assert.equal(root.querySelector("p.setup-status").textContent, t("setup_no_handover"));
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

// --- the orchestrator step ---------------------------------------------------
//
// The same page, second step: a panel that is set up answers /api/setup with
// 404 (it has no such route), and the page opens straight at this step — which
// is how the wizard is run again from the orchestrator column.

const PREVIEW = {
  path: "/h/fleetdeck/docs/orchestrator.md",
  message: "fleetdeck: this session has been appointed the fleet's orchestrator. Read the whole of `/h/fleetdeck/docs/orchestrator.md` and work by it from now on.",
  brief: "<!-- fleetdeck -->\n# Orchestrating a fleet\n",
  workspace: "/h/fleetdeck",
  name: "orchestrator",
  canStart: true,
};

const FLEET = {
  orchestratorSession: "",
  sessions: [
    {
      short: "22222222",
      name: "parser work",
      cwd: "/h/src/parser",
      state: "working",
      detail: "refactoring the tokenizer",
      context: { tokens: 120000, window: 200000 },
    },
    { short: "33333333", label: "docs pass", name: "d", cwd: "/h/src/docs", state: "blocked", needs: "answer: Which colour should the probe use?" },
    { short: "44444444", name: "going away", state: "idle", dying: true },
  ],
};

async function wizard(snapshot = FLEET, preview = PREVIEW, extra = {}) {
  routes["GET /api/setup"] = reply(404, { error: "not found" });
  routes["GET /api/snapshot"] = reply(200, snapshot);
  routes[`GET /api/orchestrator?lang=${langCode}`] = reply(200, preview);
  await render({ every: () => () => {}, ...extra });
  await settle();
}

const sessionButton = (short) => root.querySelector(`button.wizard-session[data-short=${short}]`);
const posts = () => requests.filter((r) => r.method === "POST");

test("wizard: a panel that is set up opens straight at the orchestrator step", async () => {
  await wizard();
  assert.equal(root.querySelector("h1.setup-title").textContent, t("wizard_title"));
  assert.ok(root.querySelector("section.wizard-new"));
  assert.ok(root.querySelector("section.wizard-existing"));
});

test("wizard: before anything is sent, it shows the one line the session gets and where the rules are", async () => {
  await wizard();
  assert.equal(root.querySelector("code.wizard-line").textContent, PREVIEW.message);
  assert.match(root.textContent, /\/h\/fleetdeck\/docs\/orchestrator\.md/);
  assert.equal(root.querySelector("pre.wizard-brief").textContent, PREVIEW.brief);
  assert.equal(posts().length, 0);
});

test("wizard: a new session is said to start in the workspace under its name", async () => {
  await wizard();
  const text = root.querySelector("section.wizard-new").textContent;
  assert.match(text, /\/h\/fleetdeck/);
  assert.match(text, /orchestrator/);
});

test("wizard: every running session is listed with what it is busy with", async () => {
  await wizard();
  const parser = sessionButton("22222222").textContent;
  assert.match(parser, /parser work/);
  assert.match(parser, /\/h\/src\/parser/);
  assert.match(parser, /working/);
  assert.match(parser, /refactoring the tokenizer/);
  assert.match(parser, /60%/);
  const docs = sessionButton("33333333").textContent;
  assert.match(docs, /docs pass/, "the operator's own label wins over the daemon's name");
  assert.match(docs, /Which colour should the probe use\?/);
  assert.ok(docs.includes(t("wizard_waiting_for_you")));
  assert.equal(sessionButton("44444444"), null, "a dying session cannot be appointed");
});

test("wizard: no warning and no button for an existing session until one is chosen", async () => {
  await wizard();
  assert.equal(root.querySelector("div.wizard-warning").hidden, true);
  assert.equal(root.querySelector("button.wizard-appoint").hidden, true);
});

// The card's point: appointing an existing session is adding to a conversation
// that is someone else's, and the person has to know that before pressing.
test("wizard: choosing a session says, before the button, what appointing it does to its work", async () => {
  await wizard();
  fireEvent(sessionButton("22222222"), "click");
  const warning = root.querySelector("div.wizard-warning");
  assert.equal(warning.hidden, false);
  const text = warning.textContent;
  assert.match(text, /parser work/, "names the session");
  assert.match(text, /refactoring the tokenizer/, "names what it is doing");
  assert.match(text, /60%/, "says how full its context is");
  for (const key of ["wizard_warn_title", "wizard_warn_kept", "wizard_warn_context", "wizard_warn_busy", "wizard_warn_final"]) {
    assert.ok(text.includes(t(key)), `the warning leaves out ${key}`);
  }
  const button = root.querySelector("button.wizard-appoint");
  assert.equal(button.hidden, false);
  assert.match(button.textContent, /parser work/, "the button names the session it acts on");
  assert.equal(posts().length, 0, "choosing sends nothing");
});

test("wizard: a session whose work the daemon does not describe is said so, not left blank", async () => {
  await wizard({ sessions: [{ short: "55555555", name: "quiet", state: "idle" }] });
  fireEvent(sessionButton("55555555"), "click");
  assert.ok(root.querySelector("div.wizard-warning").textContent.includes(t("wizard_doing_unknown")));
});

test("wizard: appointing an existing session sends its short id and the page's language", async () => {
  routes["POST /api/orchestrator"] = reply(200, {
    ok: true,
    session: "22222222",
    steps: [
      { name: "brief", note: "wrote /h/fleetdeck/docs/orchestrator.md" },
      { name: "message", note: "delivered to 22222222" },
      { name: "pin", note: "orchestrator.session -> 22222222" },
    ],
  });
  await wizard();
  fireEvent(sessionButton("22222222"), "click");
  fireEvent(root.querySelector("button.wizard-appoint"), "click");
  await settle();
  assert.deepEqual(
    posts().map((r) => [r.url, r.body]),
    [["/api/orchestrator", { session: "22222222", lang: langCode }]],
  );
  assert.equal(root.querySelectorAll("li.setup-step").length, 3);
  assert.equal(root.querySelector("p.setup-status").textContent, t("wizard_done"));
  assert.equal(reloaded, 0, "the outcome stays on screen until the person opens the panel");
  fireEvent(root.querySelector("button.wizard-open"), "click");
  assert.equal(reloaded, 1);
});

test("wizard: a new session is asked for with the page's language", async () => {
  routes["POST /api/orchestrator"] = reply(200, { ok: true, session: "0a1b2c3d", steps: [] });
  await wizard();
  fireEvent(root.querySelector("button.wizard-create"), "click");
  await settle();
  assert.deepEqual(posts().map((r) => r.body), [{ new: true, lang: langCode }]);
});

test("wizard: a panel that cannot start sessions says why and offers only the existing ones", async () => {
  await wizard(FLEET, { ...PREVIEW, canStart: false });
  const create = root.querySelector("button.wizard-create");
  assert.equal(create.disabled, true);
  assert.ok(root.querySelector("section.wizard-new").textContent.includes(t("wizard_new_unavailable")));
  fireEvent(create, "click");
  await settle();
  assert.equal(posts().length, 0);
});

test("wizard: a failed step is shown with its reason, and the person can try again", async () => {
  routes["POST /api/orchestrator"] = reply(200, {
    ok: false,
    session: "33333333",
    steps: [
      { name: "brief", note: "wrote /h/fleetdeck/docs/orchestrator.md" },
      { name: "message", error: "33333333 did not take the message within 5s: it is most likely asking something on its own screen" },
    ],
  });
  await wizard();
  fireEvent(sessionButton("33333333"), "click");
  fireEvent(root.querySelector("button.wizard-appoint"), "click");
  await settle();
  assert.match(root.textContent, /its own screen/);
  assert.equal(root.querySelector("p.setup-status").textContent, t("wizard_failed"));
  assert.equal(root.querySelector("button.wizard-appoint").disabled, false);
  assert.equal(root.querySelector("button.wizard-create").disabled, false);
});

test("wizard: a refused request shows the panel's own words", async () => {
  routes["POST /api/orchestrator"] = reply(409, { error: "an orchestrator is being appointed already" });
  await wizard();
  fireEvent(root.querySelector("button.wizard-create"), "click");
  await settle();
  assert.match(root.querySelector("div.wizard-error").textContent, /being appointed already/);
});

test("wizard: while one appointment runs, neither button sends a second", async () => {
  let answer;
  routes["POST /api/orchestrator"] = () => new Promise((resolve) => (answer = resolve));
  await wizard();
  fireEvent(root.querySelector("button.wizard-create"), "click");
  await settle();
  fireEvent(root.querySelector("button.wizard-create"), "click");
  fireEvent(sessionButton("22222222"), "click");
  fireEvent(root.querySelector("button.wizard-appoint"), "click");
  await settle();
  assert.equal(posts().length, 1);
  answer(reply(200, { ok: true, session: "0a1b2c3d", steps: [] }));
  await settle();
});

test("wizard: skipping opens the panel and appoints no one", async () => {
  await wizard();
  fireEvent(root.querySelector("button.wizard-skip"), "click");
  assert.equal(reloaded, 1);
  assert.equal(posts().length, 0);
});

test("wizard: run again, it marks the current orchestrator and says what happens to it", async () => {
  await wizard({ ...FLEET, orchestratorSession: "22222222" });
  assert.ok(sessionButton("22222222").textContent.includes(t("wizard_current")));
  fireEvent(sessionButton("33333333"), "click");
  const replaced = root.querySelector("div.wizard-warning").textContent;
  assert.ok(replaced.includes(t("wizard_warn_replaces").replaceAll("{old}", "parser work")), "names the orchestrator being replaced and what it keeps");
  fireEvent(sessionButton("22222222"), "click");
  const again = root.querySelector("div.wizard-warning").textContent;
  assert.ok(again.includes(t("wizard_warn_again")));
  assert.ok(!again.includes(t("wizard_warn_replaces").replaceAll("{old}", "parser work")), "the session is not replacing itself");
});

test("wizard: the list follows the fleet, and a chosen session that leaves takes its button with it", async () => {
  let tick;
  await wizard(FLEET, PREVIEW, { every: (fn) => ((tick = fn), () => {}) });
  fireEvent(sessionButton("22222222"), "click");
  routes["GET /api/snapshot"] = reply(200, { sessions: [FLEET.sessions[1]] });
  tick();
  await settle();
  assert.equal(sessionButton("22222222"), null);
  assert.equal(root.querySelector("button.wizard-appoint").hidden, true);
  assert.equal(root.querySelector("div.wizard-warning").hidden, true);
});

test("wizard: a chosen session that stays stays chosen when the list is refreshed", async () => {
  let tick;
  await wizard(FLEET, PREVIEW, { every: (fn) => ((tick = fn), () => {}) });
  fireEvent(sessionButton("33333333"), "click");
  tick();
  await settle();
  assert.ok(sessionButton("33333333").className.includes("wizard-session-chosen"));
  assert.equal(root.querySelector("button.wizard-appoint").hidden, false);
});

test("wizard: sessions the daemon cannot list are said so, with its words", async () => {
  await wizard({ sessions: [], daemonError: "daemon unavailable" });
  assert.match(root.querySelector("section.wizard-existing").textContent, /daemon unavailable/);
});
