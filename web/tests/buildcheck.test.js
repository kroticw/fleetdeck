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
  nextAction,
  hasUnsentText,
  takeAttempts,
  rememberAttempts,
  ATTEMPTS_KEY,
  MAX_WINDOW_ATTEMPTS,
  rememberOpenSession,
  takeOpenSession,
  pageStorage,
} from "../js/buildcheck.js";
import { t } from "../js/i18n.js";

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

// A person who downloaded the app reads the header to learn which version they
// have, and a commit hash tells them nothing. A release shows its tag; the
// commit stays in the title for whoever needs it.
test("a release build shows its version in the header, not the commit", () => {
  const html = brandHTML({ web: "a", version: "v0.2.0", revision: "24d0c7a657ebdf751972b6963b7039d5f4c10eb5" });
  assert.match(html, />v0\.2\.0</, "the tag is on screen");
  assert.doesNotMatch(html, />[^<]*24d0c7a[^<]*</, "the commit is not on screen");
  assert.match(html, /title="[^"]*v0\.2\.0/, "the version is in the title too");
  assert.match(html, /title="[^"]*24d0c7a657ebdf751972b6963b7039d5f4c10eb5/, "the full commit stays in the title");
});

// A build from a checkout reports "dev". The header must not dress that up as
// a version: it says dev, and the commit beside it is what tells two such
// builds apart.
test("a dev build says dev beside its commit and shows nothing that reads as a version", () => {
  const html = brandHTML({ web: "a", version: "dev", revision: "24d0c7a657eb", modified: true });
  assert.match(html, />dev 24d0c7a\*</);
  assert.doesNotMatch(html, />[^<]*v\d/, "nothing on screen looks like a release tag");
});

// A panel from before the version was in the fingerprint answers without one:
// the header shows the commit exactly as it did then.
test("a panel that reports no version keeps the commit on screen", () => {
  const html = brandHTML({ web: "a", revision: "24d0c7a657eb" });
  assert.match(html, />24d0c7a</);
  assert.doesNotMatch(html, /dev/);
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

// --- inside the fleetdeck window ---------------------------------------------
//
// In the window the page can be reloaded without a person: the window binds a
// function that navigates it afresh. So there the page does not ask, it acts --
// with one exception, and one limit. The exception: text typed and not yet
// sent would be lost, so while a field holds any, the page waits and shows the
// banner instead. The limit: a reload that brings back the same page is tried
// once more, then the page says so; it never reloads itself in a loop.

test("in the window a stale page reloads itself", () => {
  assert.equal(nextAction({ state: "stale", host: true, unsent: false, attempts: 0 }), "reload");
});

test("in a browser a stale page still only offers the reload", () => {
  assert.equal(nextAction({ state: "stale", host: false, unsent: false, attempts: 0 }), "banner");
});

// The operator is halfway through a message. Reloading now throws it away.
test("text typed and not sent holds the reload back", () => {
  assert.equal(nextAction({ state: "stale", host: true, unsent: true, attempts: 0 }), "banner");
});

test("a current page, or one with nothing to compare, is left alone", () => {
  for (const host of [true, false]) {
    assert.equal(nextAction({ state: "current", host, unsent: false, attempts: 0 }), "none");
    assert.equal(nextAction({ state: "unknown", host, unsent: false, attempts: 0 }), "none");
  }
});

test("a failed reload in the window is tried once more, then reported", () => {
  assert.equal(nextAction({ state: "reloadFailed", host: true, unsent: false, attempts: 0 }), "reload");
  assert.equal(
    nextAction({ state: "reloadFailed", host: true, unsent: false, attempts: MAX_WINDOW_ATTEMPTS }),
    "banner",
    "past the limit the page must stop and say so, not reload forever",
  );
});

test("a failed reload in a browser is reported at once", () => {
  assert.equal(nextAction({ state: "reloadFailed", host: false, unsent: false, attempts: 0 }), "banner");
});

test("the retry count survives the reload and is read back once", () => {
  const storage = fakeStorage();
  rememberAttempts(storage, 1);
  assert.equal(storage.map.get(ATTEMPTS_KEY), "1");
  assert.equal(takeAttempts(storage), 1);
  assert.equal(takeAttempts(storage), 0, "a later load must start from zero");
  assert.equal(takeAttempts(brokenStorage), 0);
  assert.doesNotThrow(() => rememberAttempts(brokenStorage, 1));
});

// --- what counts as unsent ---------------------------------------------------

function field(value, { terminal = false } = {}) {
  return { value, closest: (sel) => (terminal && sel === ".xterm" ? {} : null) };
}
function docWith(...fields) {
  return { querySelectorAll: () => fields };
}

test("an empty page has nothing unsent", () => {
  assert.equal(hasUnsentText(docWith()), false);
  assert.equal(hasUnsentText(docWith(field(""), field("   "))), false, "whitespace alone is not a message");
});

test("a field with text in it is unsent", () => {
  assert.equal(hasUnsentText(docWith(field(""), field("почти дописала"))), true);
});

// xterm.js keeps a hidden textarea of its own for keyboard input. Whatever is
// in it has already gone to the session byte by byte, so a terminal never
// holds anything back.
test("the terminal's own hidden field does not count", () => {
  assert.equal(hasUnsentText(docWith(field("x", { terminal: true }))), false);
});

// --- the session that was open ---------------------------------------------
//
// A reload closes whatever session panel was open. When a person pressed
// reload they expect that; when the window reloads by itself it would close
// the panel under them for no reason they can see. The open session is written
// down and opened again after the load.

test("the open session is remembered across a reload and read back once", () => {
  const storage = fakeStorage();
  rememberOpenSession(storage, "52d3591d");
  assert.equal(takeOpenSession(storage), "52d3591d");
  assert.equal(takeOpenSession(storage), "", "a later load must not reopen it again");
});

test("closing the session forgets it", () => {
  const storage = fakeStorage();
  rememberOpenSession(storage, "52d3591d");
  rememberOpenSession(storage, "");
  assert.equal(takeOpenSession(storage), "");
});

test("storage that refuses everything costs only the reopening", () => {
  assert.doesNotThrow(() => rememberOpenSession(brokenStorage, "52d3591d"));
  assert.equal(takeOpenSession(brokenStorage), "");
});

// In the window, with a message half typed, the page does not reload -- and
// says that it will, by itself, once the field is empty. Without that sentence
// the operator sees an ordinary "reload?" banner and has no way to know the
// window would have taken care of it.
//
// The sentence itself is checked, not merely that the banner differs: the
// button's label changes too, and a first version of this case that only
// compared the two banners stayed green with the sentence removed.
test("a stale page waiting on unsent text says it will reload by itself", () => {
  const waiting = bannerHTML("stale", { waiting: true });
  assert.ok(waiting.includes(t("build_stale_waiting")), "the banner must say the reload will come by itself");
  assert.ok(!bannerHTML("stale").includes(t("build_stale_waiting")), "and only while waiting");
  assert.match(waiting, /class="build-reload"/, "reloading now stays possible, at the person's own choice");
});

// Reading the sessionStorage property throws where site data is blocked. A
// default parameter that throws takes the banner's whole wiring down, so the
// page reaches storage only through this.
test("reaching storage never throws, even when the property itself does", () => {
  const had = Object.getOwnPropertyDescriptor(globalThis, "sessionStorage");
  Object.defineProperty(globalThis, "sessionStorage", { configurable: true, get() { throw new Error("SecurityError"); } });
  try {
    assert.equal(pageStorage(), undefined);
  } finally {
    if (had) Object.defineProperty(globalThis, "sessionStorage", had);
    else delete globalThis.sessionStorage;
  }
});
