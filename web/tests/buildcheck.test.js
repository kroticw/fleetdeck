// Whether the page a window is showing is the page the panel serves now.
//
// The panel is rebuilt and replaced under a window that stays open for days,
// and the page in it reconnects to the new panel while still running the old
// code. These cases pin what the page says about that, and -- just as much --
// when it says nothing: a banner that shows on every rebuild of the same
// interface becomes noise nobody reads by the next day.

import { test } from "node:test";
import assert from "node:assert/strict";

import {
  buildState,
  takeReloadFrom,
  rememberReloadFrom,
  reloadNow,
  bannerHTML,
  brandHTML,
  readOwnBuild,
  RELOAD_FROM_KEY,
} from "../js/buildcheck.js";

function fakeStorage(initial = {}) {
  const map = new Map(Object.entries(initial));
  return {
    getItem: (k) => (map.has(k) ? map.get(k) : null),
    setItem: (k, v) => map.set(k, String(v)),
    removeItem: (k) => map.delete(k),
    map,
  };
}

// Storage that refuses every call, which is what a WebView with site data
// blocked -- or a thumbnail renderer -- hands a page.
const brokenStorage = {
  getItem() { throw new Error("denied"); },
  setItem() { throw new Error("denied"); },
  removeItem() { throw new Error("denied"); },
};

// --- the decision -----------------------------------------------------------

test("the same interface is current, whatever else about the panel changed", () => {
  assert.equal(buildState("aaa", "aaa", ""), "current");
});

test("a different interface under the page is stale", () => {
  assert.equal(buildState("aaa", "bbb", ""), "stale");
});

// A page served by a panel that predates fingerprints, or a snapshot from one,
// has nothing to compare. Guessing either way would be a banner on no evidence.
test("a page with no fingerprint of its own says nothing", () => {
  assert.equal(buildState("", "bbb", ""), "unknown");
});

test("a snapshot with no fingerprint says nothing", () => {
  assert.equal(buildState("aaa", undefined, ""), "unknown");
  assert.equal(buildState("aaa", "", ""), "unknown");
});

// The reload was asked for from "aaa" and the document that came back is
// "aaa" again: the window was handed the old page. Offering the same button
// once more is the banner nobody believes -- this state exists so the page can
// say something else instead.
test("a reload that brought back the same document has failed", () => {
  assert.equal(buildState("aaa", "bbb", "aaa"), "reloadFailed");
});

test("a reload that brought a new document has worked", () => {
  assert.equal(buildState("bbb", "bbb", "aaa"), "current");
});

// The panel moved on again between the click and the load: the page is newer
// than the one it left, just not the newest. That is an ordinary stale page,
// not a failed reload -- the reload did its job.
test("a reload that landed on an intermediate build is stale, not failed", () => {
  assert.equal(buildState("bbb", "ccc", "aaa"), "stale");
});

// --- remembering across the reload ----------------------------------------

test("the build a reload left from is read back once and then forgotten", () => {
  const storage = fakeStorage({ [RELOAD_FROM_KEY]: "aaa" });
  assert.equal(takeReloadFrom(storage), "aaa");
  assert.equal(takeReloadFrom(storage), "", "a second load must not think it is still the first after a reload");
});

test("remembering a reload stores the build being left", () => {
  const storage = fakeStorage();
  rememberReloadFrom(storage, "aaa");
  assert.equal(storage.map.get(RELOAD_FROM_KEY), "aaa");
});

// Without storage the page cannot tell a failed reload from a first load. It
// then behaves exactly as a page without this feature: the banner still works,
// and nothing throws on the way.
test("storage that refuses everything costs only the failed-reload message", () => {
  assert.equal(takeReloadFrom(brokenStorage), "");
  assert.doesNotThrow(() => rememberReloadFrom(brokenStorage, "aaa"));
  assert.equal(takeReloadFrom(undefined), "");
  assert.doesNotThrow(() => rememberReloadFrom(undefined, "aaa"));
});

test("the button remembers where it is leaving from, then reloads", () => {
  const storage = fakeStorage();
  const calls = [];
  reloadNow(storage, "aaa", () => calls.push(storage.map.get(RELOAD_FROM_KEY)));
  assert.deepEqual(calls, ["aaa"], "the build must be stored before the reload, not after it");
});

// --- what is drawn ---------------------------------------------------------

test("no banner while the page is current or there is nothing to compare", () => {
  assert.equal(bannerHTML("current"), "");
  assert.equal(bannerHTML("unknown"), "");
});

test("a stale page offers the reload", () => {
  const html = bannerHTML("stale");
  assert.match(html, /class="build-reload"/);
});

// The whole point of the failed state: the action that just did not work is
// not offered again. The message names the step that will work instead.
test("a failed reload does not offer the same button again", () => {
  const html = bannerHTML("reloadFailed");
  assert.ok(html.length > 0, "a failed reload must say so");
  assert.doesNotMatch(html, /build-reload/);
  assert.match(html, /Cmd\+Q/);
});

test("the page reads its own build from the document it arrived in", () => {
  const doc = { querySelector: (s) => (s === 'meta[name="fleetdeck-build"]' ? { content: "aaa" } : null) };
  assert.equal(readOwnBuild(doc), "aaa");
  assert.equal(readOwnBuild({ querySelector: () => null }), "");
});

// --- which build is answering ----------------------------------------------

test("the brand names the commit and keeps the full picture in its title", () => {
  const html = brandHTML({
    web: "f".repeat(64),
    revision: "24d0c7a657ebdf751972b6963b7039d5f4c10eb5",
    commitTime: "2026-09-10T19:00:15Z",
    builtAt: "2026-09-11T10:00:00Z",
    executable: "/Users/x/claude/fleetdeck-dev/bin/fleetdeck",
  });
  assert.match(html, />24d0c7a</, "the short commit is on screen");
  assert.match(html, /24d0c7a657ebdf751972b6963b7039d5f4c10eb5/, "the full commit is in the title");
  assert.match(html, /fleetdeck-dev\/bin\/fleetdeck/, "the binary's path is in the title");
  assert.match(html, /2026-09-11/, "when it was built is in the title");
  assert.match(html, /2026-09-10/, "when its commit was made is in the title");
});

// "Modified" says the commit does not describe the contents. It stays visible,
// as a mark beside the commit, and it never raises a banner on its own.
test("a build from a modified tree is marked beside its commit", () => {
  const clean = brandHTML({ web: "a", revision: "24d0c7a657eb", modified: false });
  const dirty = brandHTML({ web: "a", revision: "24d0c7a657eb", modified: true });
  assert.notEqual(clean, dirty);
  assert.match(dirty, /24d0c7a\*/);
  assert.equal(buildState("a", "a", ""), "current", "a modified tree alone is not a reason to reload");
});

test("the brand survives a panel with no fingerprint at all", () => {
  assert.match(brandHTML(undefined), /fleetdeck/);
  assert.match(brandHTML({ web: "a" }), /fleetdeck/);
});

// The path comes from the machine, not from us. It goes into an attribute,
// and an attribute is where an unescaped quote ends the attribute early.
test("the title escapes what it quotes", () => {
  const html = brandHTML({ web: "a", revision: "24d0c7a", executable: '/tmp/"><img src=x>' });
  assert.doesNotMatch(html, /"><img/);
});
