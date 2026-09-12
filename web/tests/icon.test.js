// The application's icon, inline in the page.
//
// The icon exists twice in this repository: as the source the .icns is built
// from (cmd/fleetdeck-window/icon-source.svg and its light variant) and as the
// markup the page inlines (web/js/icon.js). The window's copy cannot be
// imported here — web/embed.go embeds web/ and nothing above it — so the two
// are kept identical by this test instead of by hope. Redraw the icon and this
// fails until the page's copy is redrawn with it.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

import { ICON_DARK, ICON_LIGHT, fleetIconHTML } from "../js/icon.js";

// The shapes of an SVG file, with the wrapper, comments and whitespace taken
// off: what has to match, rather than how it happens to be laid out.
function shapes(svg) {
  return svg
    .replace(/<!--[\s\S]*?-->/g, "")
    .replace(/^[\s\S]*?<svg[^>]*>/, "")
    .replace(/<\/svg>[\s\S]*$/, "")
    .replace(/\s+/g, " ")
    .trim();
}

function source(name) {
  return readFileSync(new URL(`../../cmd/fleetdeck-window/${name}`, import.meta.url), "utf8");
}

test("the page's dark icon is the same drawing as the window's source", () => {
  assert.equal(shapes(ICON_DARK), shapes(source("icon-source.svg")));
});

test("the page's light icon is the same drawing as the window's source", () => {
  assert.equal(shapes(ICON_LIGHT), shapes(source("icon-source-light.svg")));
});

// The sources carry width/height 1024, the size the .icns is built at. Left in
// place they would paint a 1024-pixel icon across the start page.
test("the inlined icon is sized by its caller, never by the source's 1024", () => {
  const html = fleetIconHTML(64);
  assert.equal(html.includes('width="1024"'), false);
  assert.equal(html.includes('height="1024"'), false);
  assert.equal((html.match(/width="64"/g) ?? []).length, 2);
  assert.equal((html.match(/height="64"/g) ?? []).length, 2);
});

// Both variants are in the markup and the stylesheet shows one of them, because
// the theme is a CSS state: "auto" follows the system and changes without the
// page hearing about it (web/js/theme.js applies data-theme only for an
// explicit choice). A variant picked in JS would be the wrong one for everyone
// who never touched the toggle and then changed their system theme.
test("both variants are inlined, each under the class the stylesheet shows", () => {
  const html = fleetIconHTML(20);
  assert.equal((html.match(/<svg/g) ?? []).length, 2);
  assert.match(html, /class="app-icon-dark"/);
  assert.match(html, /class="app-icon-light"/);
  assert.match(html, /viewBox="0 0 64 64"/);
});

// The icon is decoration beside a name that is already text. Announced, it
// would read as a second copy of the fleet's name in a screen reader.
test("the icon is hidden from assistive technology", () => {
  assert.match(fleetIconHTML(64), /aria-hidden="true"/);
});
