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

export function columnHTML(label, stage, cards, orphanPaths) {
  // An empty column is a real drop target, not dead space -- it must never
  // become unreachable -- but it has nothing that needs reading, so it has
  // no reason to claim the same width as a column carrying thirty cards.
  // .kcol-empty (app.css) is what actually shrinks it; this only says which
  // columns qualify.
  const empty = cards.length === 0;
  return `
    <div class="kcol${empty ? " kcol-empty" : ""}" data-stage="${escapeHTML(stage)}">
      <h5>${escapeHTML(label)} <span class="kcount">${cards.length}</span></h5>
      ${cards.map((c) => cardHTML(c, orphanPaths)).join("")}
    </div>`;
}

// render draws every column from one snapshot. snap may be null (before the
// first frame arrives, or after the socket drops) — that renders the same
// empty board a snapshot with no cards would, never an error and never a
// blank root. A snapshot with boardError set (state.Snapshot.BoardError,
// e.g. a misconfigured board path or zero cards found) is a distinct case
// from that: cards is nil either way, but the board failed to load rather
// than loading and finding nothing, so it must not look identical to a
// healthy empty board (spec section 7's "degrade in parts, never silently").
// snap?.boardError rather than snap.boardError so this still falls through
// to the normal empty-columns render when snap itself is null.
function render(root, snap) {
  if (snap?.boardError) {
    root.innerHTML = `<div class="kerror">${escapeHTML(snap.boardError)}</div>`;
    return;
  }

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

  markScrollable(root);
}

// The board's own scrollbar is the platform's, and on macOS that is an
// overlay that stays invisible until the pointer is over it -- exactly the
// discoverability gap #38 fixed once for columns that had nowhere left to
// shrink to. Six columns wide enough to carry a readable title no longer
// always fit without scrolling (see app.css's own note on #board), so that
// gap is back unless something renders regardless of hover state. This adds
// .board-scrollable, a plain class app.css turns into a right-edge shadow,
// whenever there is genuinely more board to the right of what is currently
// visible -- not "the board happens to be wider than its box" (true for the
// whole session, however far scrolled) but "scrolling right now would show
// something new". Real measurements, re-taken after every render, on
// resize, and on scroll itself: the same content can cross the
// fits/doesn't boundary on a resize with no new snapshot, and scrolling to
// the far column must turn the shadow off rather than fade content that has
// nothing left past it to promise.
const SCROLL_END_SLACK_PX = 1; // sub-pixel layout rounding, not a real gap
function markScrollable(root) {
  const moreToTheRight = root.scrollLeft + root.clientWidth < root.scrollWidth - SCROLL_END_SLACK_PX;
  root.classList.toggle("board-scrollable", moreToTheRight);
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

  // A resize alone can cross the fits/doesn't boundary with no new snapshot
  // to trigger a re-render -- the columns already on screen are unchanged,
  // only how much of them the window can show. Re-measuring is enough here;
  // the markup itself does not need rebuilding for that.
  window.addEventListener("resize", () => markScrollable(root));

  // And scrolling the board itself is exactly what turns the shadow off --
  // reaching the last column is the one thing markScrollable exists to
  // notice, so it has to run on the event that actually gets there.
  root.addEventListener("scroll", () => markScrollable(root));

  subscribe((snap) => render(root, snap));
}
