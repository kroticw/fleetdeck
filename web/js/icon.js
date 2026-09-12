// web/js/icon.js
// The application's icon, inline in the page.
//
// A copy, deliberately. The drawing lives in cmd/fleetdeck-window as the source
// the .icns is built from (scripts/build-icon.sh, `make icon`), and web/embed.go
// embeds web/ and nothing above it, so the page cannot read that file. The two
// copies are held together by web/tests/icon.test.js, which compares the shapes
// and fails when one is redrawn without the other — the alternative was a page
// whose icon quietly stopped being the application's.
//
// Both variants are inlined and the stylesheet shows one, rather than the page
// picking one: the theme is a CSS state. "auto" is the default and follows the
// system, and web/js/theme.js writes data-theme only for an explicit choice, so
// a variant chosen in JS would be wrong for everyone who never touched the
// toggle and then changed their system theme — and would stay wrong until the
// page was reloaded.
//
// The light variant is used here and nowhere else in the repository: only the
// dark one goes into the .icns. Both are documented in
// docs/engineering/window-and-panel.md.

// The source files' shapes, verbatim. Only width/height are the caller's: the
// sources carry 1024, the size the .icns is built at.
export const ICON_DARK = `<rect x="2" y="2" width="60" height="60" rx="14" fill="#1b1e24" stroke="#2c313a" stroke-width="1.5"/>
  <path d="M17.5 31 C15 23.5, 15.5 17.2, 32 16.6 C48.5 17.2, 49 23.5, 46.5 31 Z" fill="#e7e9ec"/>
  <rect x="14.5" y="30.6" width="35" height="7" rx="1.2" fill="#1b2a30" stroke="#34a99a" stroke-width="1.2"/>
  <path d="M10 38.2 C14.5 48.2, 49.5 48.2, 54 38.2 L54 37.4 L10 37.4 Z" fill="#14202a" stroke="#34a99a" stroke-width="1.2"/>
  <path d="M12.6 37.6 C20 41.4, 44 41.4, 51.4 37.6" fill="none" stroke="#4fc4b5" stroke-width="1.7" stroke-linecap="round"/>
  <path d="M32 30.5 L32 36 M29.4 32.2 L34.6 32.2 M29.6 35 C30.6 36.6, 33.4 36.6, 34.4 35" fill="none" stroke="#4fc4b5" stroke-width="1.5" stroke-linecap="round"/>`;

export const ICON_LIGHT = `<rect x="2" y="2" width="60" height="60" rx="14" fill="#ffffff" stroke="#c2c8d1" stroke-width="1.5"/>
  <path d="M17.5 31 C15 23.5, 15.5 17.2, 32 16.6 C48.5 17.2, 49 23.5, 46.5 31 Z" fill="#ffffff" stroke="#c2c8d1" stroke-width="1"/>
  <rect x="14.5" y="30.6" width="35" height="7" rx="1.2" fill="#1a2b31"/>
  <path d="M10 38.2 C14.5 48.2, 49.5 48.2, 54 38.2 L54 37.4 L10 37.4 Z" fill="#12202a"/>
  <path d="M12.6 37.6 C20 41.4, 44 41.4, 51.4 37.6" fill="none" stroke="#2fa899" stroke-width="1.7" stroke-linecap="round"/>
  <path d="M32 30.5 L32 36 M29.4 32.2 L34.6 32.2 M29.6 35 C30.6 36.6, 33.4 36.6, 34.4 35" fill="none" stroke="#2fa899" stroke-width="1.5" stroke-linecap="round"/>`;

function svg(className, size, shapes) {
  return `<svg xmlns="http://www.w3.org/2000/svg" class="${className}" width="${size}" height="${size}" viewBox="0 0 64 64">${shapes}</svg>`;
}

// fleetIconHTML is the icon at a size, as markup: both variants, hidden from
// assistive technology because the name beside it is already text.
export function fleetIconHTML(size) {
  return `<span class="app-icon" aria-hidden="true">${svg("app-icon-dark", size, ICON_DARK)}${svg("app-icon-light", size, ICON_LIGHT)}</span>`;
}

export default fleetIconHTML;
