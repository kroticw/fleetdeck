// web/js/standcontrols.js
//
// What a surface says of its capsules in the fleetdeck window's log on a CI
// stand, and nowhere else (window.fleetdeckHost.stand): how tall and how round
// each control is, how much of its fill is see-through, whether it blurs what is
// under it, and how its text reads against the worst ground it can lie on.
// scripts/standcheck holds them to the material the window drew (T-070). A
// screenshot of glass shows a capsule and cannot prove its text reads over a
// light board and a dark one alike.

const tenth = (n) => Math.round(n * 10) / 10;
const hundredth = (n) => Math.round(n * 100) / 100;

// The controls each surface draws as capsules, by name; floating ones are
// frosted panels over content.
const CONTROLS = {
  orchestrator: [
    { name: "editPencil", selector: ".o-name-edit" },
    { name: "picker", selector: ".o-pick-select" },
    { name: "fontSmaller", selector: ".term-font-smaller" },
    { name: "fontSize", selector: ".term-font-reset" },
    { name: "fontBigger", selector: ".term-font-bigger" },
    { name: "fold", selector: ".col-size-fold" },
    { name: "fleetButton", selector: ".fleet-menu-button" },
    { name: "fleetList", selector: ".fleet-menu-list" },
  ],
  board: [
    { name: "newCard", selector: ".newcard" },
    { name: "newCardTitle", selector: ".newcard-title" },
    { name: "newCardZone", selector: ".newcard-zone" },
    { name: "newCardCreate", selector: ".newcard-create" },
    { name: "newCardCancel", selector: ".newcard-cancel" },
  ],
};

// The grounds a theme lays under glass: the board's and the islands'. What the
// glass shows under a see-through capsule lies between the lightest and the
// darkest of them.
const GROUNDS = ["--bg", "--surface", "--surface-raised", "--surface-hover"];

// parseColor reads a computed colour or a token as the sheet writes one: #rgb,
// #rrggbb, rgb()/rgba() with commas or spaces and a slash, or transparent.
// Anything else is null.
export function parseColor(text) {
  const s = String(text ?? "").trim().toLowerCase();
  if (s === "transparent") return { r: 0, g: 0, b: 0, a: 0 };
  let m = s.match(/^#([0-9a-f]{3}|[0-9a-f]{6})$/);
  if (m) {
    const hex = m[1].length === 3 ? [...m[1]].map((c) => c + c).join("") : m[1];
    return { r: parseInt(hex.slice(0, 2), 16), g: parseInt(hex.slice(2, 4), 16), b: parseInt(hex.slice(4, 6), 16), a: 1 };
  }
  m = s.match(/^rgba?\(\s*([\d.]+)[\s,]+([\d.]+)[\s,]+([\d.]+)\s*(?:[,/]\s*([\d.]+)(%?)\s*)?\)$/);
  if (!m) return null;
  const alpha = m[4] === undefined ? 1 : m[5] === "%" ? Number(m[4]) / 100 : Number(m[4]);
  return { r: Number(m[1]), g: Number(m[2]), b: Number(m[3]), a: alpha };
}

// over is top laid over an opaque bottom.
function over(top, bottom) {
  const mix = (t, b) => t * top.a + b * (1 - top.a);
  return { r: mix(top.r, bottom.r), g: mix(top.g, bottom.g), b: mix(top.b, bottom.b), a: 1 };
}

function luminance({ r, g, b }) {
  const channel = (v) => {
    const c = v / 255;
    return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
  };
  return 0.2126 * channel(r) + 0.7152 * channel(g) + 0.0722 * channel(b);
}

// contrastRatio is WCAG's ratio between two opaque colours, 1 to 21.
export function contrastRatio(a, b) {
  const [light, dark] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (light + 0.05) / (dark + 0.05);
}

const backdropOf = (style) => {
  const value = style.backdropFilter || style.webkitBackdropFilter || "none";
  return value === "" ? "none" : value;
};

// The fills from el up to a frosted panel, which blurs whatever is under it:
// top first. A solid one hides every fill and ground under it when they are
// laid together, so the walk need not stop there.
function fillsUnder(win, el) {
  const fills = [];
  for (let at = el; at; at = at.parentElement) {
    const style = win.getComputedStyle(at);
    const fill = parseColor(style.backgroundColor);
    if (fill && fill.a > 0) fills.push(fill);
    if (at !== el && backdropOf(style) !== "none") break;
  }
  return fills;
}

// How opaque el is drawn: its opacity times every ancestor's.
function opacityOf(win, el) {
  let product = 1;
  for (let at = el; at; at = at.parentElement) {
    const value = parseFloat(win.getComputedStyle(at).opacity);
    if (Number.isFinite(value)) product *= value;
  }
  return product;
}

// The worst contrast colour has on el: against el's fills laid over the lightest
// and over the darkest ground the theme has. el and everything drawn in it are
// laid with its opacity over what lies under it.
function worstContrast(win, el, colour) {
  const text = parseColor(colour) ?? { r: 0, g: 0, b: 0, a: 1 };
  const fills = fillsUnder(win, el);
  const hasOwn = (parseColor(win.getComputedStyle(el).backgroundColor)?.a ?? 0) > 0;
  const own = hasOwn ? fills[0] : null;
  const under = hasOwn ? fills.slice(1) : fills;
  const opacity = opacityOf(win, el);
  const rootStyle = win.getComputedStyle(win.document.documentElement);
  const grounds = GROUNDS.map((name) => parseColor(rootStyle.getPropertyValue(name))).filter((c) => c && c.a >= 1);
  grounds.sort((x, y) => luminance(x) - luminance(y));
  const bases = grounds.length ? [grounds[0], grounds[grounds.length - 1]] : [{ r: 255, g: 255, b: 255, a: 1 }];
  let worst = Infinity;
  for (const base of bases) {
    const ground = under.reduceRight((below, fill) => over(fill, below), base);
    const face = own ? over(own, ground) : ground;
    const ink = text.a >= 1 ? text : over(text, face);
    const seen = (c) => over({ ...c, a: opacity }, ground);
    worst = Math.min(worst, contrastRatio(seen(ink), seen(face)));
  }
  return hundredth(worst);
}

// An empty field shows its placeholder, not its text: how that reads, or null
// for a control with none showing.
function placeholderContrast(win, el) {
  if (!el.placeholder || el.value) return null;
  return worstContrast(win, el, win.getComputedStyle(el, "::placeholder").color);
}

// controlsReport is what surface's capsules in win say of themselves. A control
// not on screen is left out.
export function controlsReport(win, surface) {
  const controls = [];
  for (const { name, selector } of CONTROLS[surface] ?? []) {
    const el = win.document.querySelector(selector);
    if (!el) continue;
    const box = el.getBoundingClientRect();
    if (box.width <= 0 || box.height <= 0) continue;
    const style = win.getComputedStyle(el);
    const backdrop = backdropOf(style);
    const opacity = opacityOf(win, el);
    controls.push({
      name,
      height: tenth(box.height),
      radius: tenth(parseFloat(style.borderTopLeftRadius) || 0),
      opacity: hundredth(opacity),
      fillAlpha: hundredth((parseColor(style.backgroundColor)?.a ?? 0) * opacity),
      backdrop,
      floating: backdrop !== "none",
      contrast: worstContrast(win, el, style.color),
      placeholderContrast: placeholderContrast(win, el),
      disabled: el.disabled === true,
    });
  }
  return { surface, report: "controls", glass: win.document.documentElement.dataset.glass ?? "", controls };
}

// watchControls reports surface's controls once laid out, on a resize, whenever
// the page's markup or its root's attributes change -- a theme, a glass, a menu
// opened -- and when the returned function is called; at most once a frame, and
// only when the report changed.
export function watchControls(win, surface, report) {
  let last = "";
  let queued = false;
  const measure = () => {
    queued = false;
    const now = controlsReport(win, surface);
    const text = JSON.stringify(now);
    if (text === last) return;
    last = text;
    report(now);
  };
  const later = () => {
    if (queued) return;
    queued = true;
    win.requestAnimationFrame(measure);
  };
  win.addEventListener("resize", later);
  if (typeof win.MutationObserver === "function") {
    new win.MutationObserver(later).observe(win.document.body, { childList: true, subtree: true, attributes: true });
    new win.MutationObserver(later).observe(win.document.documentElement, { attributes: true });
  }
  later();
  return later;
}
