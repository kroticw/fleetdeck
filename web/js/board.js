import { subscribe } from "./store.js";
import { t } from "./i18n.js";

const STAGES = ["new", "active", "review", "blocked", "done"];

// The board vocabulary of zones the design spec names (section 4). Anything
// else — empty, a typo, a value the vocabulary doesn't have yet — falls back
// to zone-none rather than being written unchecked into a class attribute.
const ZONES = new Set(["urgent", "unplanned", "planned", "niceToHave"]);

const ESCAPE_MAP = { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" };

// Cards are markdown files written by agents, not by the browser user, but
// they are still untrusted input to this HTML-string-building renderer:
// nothing here may assume a title, path or session id can't contain "<" or
// '"'.
function escapeHTML(value) {
  return String(value).replace(/[&<>"']/g, (ch) => ESCAPE_MAP[ch]);
}

function basename(path) {
  return String(path).split("/").pop();
}

function zoneClass(zone) {
  return "zone-" + (ZONES.has(zone) ? zone : "none");
}

// clampProgress guards the progress bar's width against a card whose progress
// field holds something outside 0-100 (bad frontmatter is not this module's
// job to reject, only to render without breaking layout).
function clampProgress(progress) {
  return Math.max(0, Math.min(100, progress));
}

function cardHTML(c, orphanPaths) {
  const path = escapeHTML(c.path);
  if (c.parseError) {
    return `
      <article class="kcard kcard-broken" data-path="${path}">
        <div class="ktitle">${escapeHTML(basename(c.path))}</div>
        <div class="kbroken">${escapeHTML(t("card_broken"))}: ${escapeHTML(c.parseError)}</div>
      </article>`;
  }

  const title = escapeHTML(c.title || basename(c.path));
  const dead = c.session && orphanPaths.has(c.path);

  let sessionHTML = "";
  if (c.session) {
    const sessionId = escapeHTML(c.session);
    sessionHTML = dead
      ? `<div class="ksession ksession-dead">${sessionId} — ${escapeHTML(t("session_dead"))}</div>`
      : `<div class="ksession">${sessionId}</div>`;
  }

  return `
    <article class="kcard ${zoneClass(c.zone)}${dead ? " kcard-orphan" : ""}" data-path="${path}">
      <div class="ktitle">${title}</div>
      ${sessionHTML}
      <div class="kprog"><i data-progress="${clampProgress(c.progress)}"></i></div>
    </article>`;
}

function columnHTML(label, stage, cards, orphanPaths) {
  return `
    <div class="kcol" data-stage="${escapeHTML(stage)}">
      <h5>${escapeHTML(label)} <span class="kcount">${cards.length}</span></h5>
      ${cards.map((c) => cardHTML(c, orphanPaths)).join("")}
    </div>`;
}

// render draws every column from one snapshot. snap may be null (before the
// first frame arrives, or after the socket drops) — that renders the same
// empty board a snapshot with no cards would, never an error and never a
// blank root.
function render(root, snap) {
  const cards = snap?.cards ?? [];
  const orphanPaths = new Set(snap?.orphanCards ?? []);

  const known = STAGES.map((stage) => cards.filter((c) => c.stage === stage));
  const other = cards.filter((c) => !STAGES.includes(c.stage));

  const columns = STAGES.map((stage, i) => columnHTML(stage, stage, known[i], orphanPaths)).join("");
  const otherColumn = columnHTML("other", "other", other, orphanPaths);

  root.innerHTML = columns + otherColumn;

  // CSP forbids an inline style="..." attribute (index.html: style-src
  // 'self'), so the bar's width can never be baked into the HTML string
  // above — it is set here as a CSSOM property assignment instead, which the
  // policy does not touch.
  for (const bar of root.querySelectorAll(".kprog i[data-progress]")) {
    bar.style.width = bar.dataset.progress + "%";
  }
}

export function renderBoard(root, onOpenCard) {
  // One delegated listener rather than one per card: root.innerHTML is
  // replaced whole on every snapshot, so per-card listeners would need to be
  // re-attached every time anyway, and delegation reads the path straight
  // back from the browser's own attribute decoding — no re-escaping needed.
  root.addEventListener("click", (ev) => {
    const card = ev.target.closest(".kcard");
    if (!card || !root.contains(card)) return;
    onOpenCard(card.dataset.path);
  });

  subscribe((snap) => render(root, snap));
}
