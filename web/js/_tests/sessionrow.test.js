// web/js/_tests/sessionrow.test.js
//
// A session row at a glance: a dot in its state's colour, the name with the
// cost beside it, the state as a word the operator reads (not the daemon's
// English), how long it has been at it, and the context it has used.

import test from "node:test";
import assert from "node:assert/strict";
import { rowHtml } from "../sessions.js";
import { t } from "../i18n.js";

test("a working session is marked working, its state said as a word", () => {
  const html = rowHtml({ short: "aa11", name: "build", needs: "", state: "working", costUSD: 4.12 });
  assert.match(html, /class="srow" data-short="aa11" data-state="working"/);
  assert.match(html, new RegExp(`class="sstate">${t("dock_state_working")}<`));
});

test("a session waiting for an answer is marked so", () => {
  const html = rowHtml({ short: "bb22", name: "diploma", needs: "Which structure?", state: "idle" });
  assert.match(html, /data-state="waiting"/);
  assert.match(html, new RegExp(`class="sstate">${t("dock_state_waiting")}<`));
});

test("an idle session that waits for nobody is said to be idle", () => {
  const html = rowHtml({ short: "cc33", name: "x", needs: "", state: "idle" });
  assert.match(html, /data-state="idle"/);
  assert.match(html, new RegExp(`class="sstate">${t("session_state_idle")}<`));
});

test("a stalled session is marked stalled", () => {
  const html = rowHtml({ short: "dd44", name: "x", needs: "", state: "blocked" }, true);
  assert.match(html, /data-state="stalled"/);
  assert.match(html, new RegExp(`class="sstate">${t("dock_state_stalled")}<`));
});

test("the cost sits in the row's head, beside the name", () => {
  const html = rowHtml({ short: "ee55", name: "x", needs: "", state: "working", costUSD: 590.03 });
  const head = /<div class="srow-head">([\s\S]*?)<\/div>/.exec(html)[1];
  assert.match(head, /class="scost">\$590\.03</);
});
