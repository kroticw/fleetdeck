// web/js/_tests/sessions.test.js
//
// Lives under _tests/ for the same reason header.test.js does: web/embed.go's
// plain (non "all:") directory pattern already excludes any directory whose
// name starts with "_", so this subtree never reaches the binary or the HTTP
// surface.
//
// Run with: node --test web/js/_tests/sessions.test.js

import test from "node:test";
import assert from "node:assert/strict";
import { rowHtml } from "../sessions.js";

// sessions.js reaches for navigator.language at import time through i18n.js.
globalThis.navigator ??= { language: "en" };

test("a waiting session's reason keeps its full text in the title", () => {
  const long = "answer: which branch should this go to? " + "y".repeat(500);
  const html = rowHtml({ short: "aa11", name: "n", needs: long });
  const title = html.match(/class="sreason" title="([^"]*)"/)?.[1];
  assert.equal(title, long, "the clipped row must still carry the whole reason");
});

test("a stalled session with no needs carries detail, in full, in the title", () => {
  const detail = "waiting on my own subagents\nsecond line\nthird line";
  const html = rowHtml({ short: "bb22", name: "n", needs: "", state: "blocked", detail });
  const title = html.match(/class="sreason" title="([^"]*)"/)?.[1];
  assert.equal(title, detail);
});

test("a quote or a tag in the reason cannot break out of the title attribute", () => {
  const nasty = 'he said "go" <img src=x onerror=alert(1)>';
  const html = rowHtml({ short: "cc33", name: "n", needs: "", state: "blocked", detail: nasty });
  assert.ok(!html.includes("<img"), "the reason must never reach the DOM as markup");
  assert.ok(!html.includes('="go"'), "an unescaped quote would end the attribute early");
  assert.ok(html.includes("&quot;go&quot;"));
});

test("a session that is neither waiting nor stalled has no reason row at all", () => {
  const html = rowHtml({ short: "dd44", name: "n", state: "working", detail: "some detail" });
  assert.equal(html.includes("sreason"), false, "an absent reason is absent, not an empty box");
});
