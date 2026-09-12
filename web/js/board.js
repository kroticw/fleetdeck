import { subscribe } from "./store.js";
import { t } from "./i18n.js";
import { markScrollable, watchSelf } from "./scrollable.js";

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

// The card's number (frontmatter `id`, "T-NNN"), which is the one identifier a
// person reads off the board and says out loud — and then hands to
// card_path.py to reach the file again.
//
// It is drawn beside the session's short id and has to stay distinguishable
// from it: the two are different identifiers with different lifetimes (the
// card's is permanent and spoken, the session's changes with every run), and an
// operator naming the wrong one gets nowhere. So the number is not merely
// another line of small grey text — it carries its own class, its own shape in
// app.css, and a title saying which of the two it is.
//
// A card with no number gets a word saying so rather than an empty space: a
// gap reads as a panel that lost the number, and inventing one is worse than
// either. A card that does not parse is handled before this is reached, and
// says nothing about its number at all — nothing is known about it.
function numberHTML(c) {
  if (!c.id) {
    return `<span class="knum knum-none" title="${escapeHTML(t("card_no_number_hint"))}">${escapeHTML(t("card_no_number"))}</span>`;
  }
  return `<span class="knum" title="${escapeHTML(t("card_number_hint"))}">${escapeHTML(c.id)}</span>`;
}

// orphanPaths and stoppedPaths are the two things that can be wrong with a
// card's session, and they are two rather than one for the reason the whole
// change exists: a card whose agent was stopped used to be marked orphaned,
// which says the card lost its session — about a session sitting in the job
// store with its whole history, one resume away. A stopped card is marked as
// paused; only a session that cannot come back, or one that is in no list at
// all, orphans the card that names it.
function cardHTML(c, orphanPaths, stoppedPaths) {
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
  const stopped = c.session && !dead && stoppedPaths.has(c.path);

  let sessionHTML = "";
  if (c.session) {
    const sessionId = escapeHTML(c.session);
    if (dead) {
      sessionHTML = `<div class="ksession ksession-dead">${sessionId} — ${escapeHTML(t("session_dead"))}</div>`;
    } else if (stopped) {
      sessionHTML = `<div class="ksession ksession-stopped">${sessionId} — ${escapeHTML(t("session_stopped"))}</div>`;
    } else {
      sessionHTML = `<div class="ksession">${sessionId}</div>`;
    }
  }

  const mark = dead ? " kcard-orphan" : stopped ? " kcard-stopped" : "";
  return `
    <article class="kcard ${zoneClass(c.zone)}${mark}" data-path="${path}">
      <div class="ktitle">${title}</div>
      <div class="kmeta">${numberHTML(c)}${sessionHTML}</div>
      <div class="kprog"><i data-progress="${clampProgress(c.progress)}"></i></div>
    </article>`;
}

export function columnHTML(label, stage, cards, orphanPaths, stoppedPaths = new Set()) {
  // An empty column is a real drop target, not dead space -- it must never
  // become unreachable -- but it has nothing that needs reading, so it has
  // no reason to claim the same width as a column carrying thirty cards.
  // .kcol-empty (app.css) is what actually shrinks it; this only says which
  // columns qualify.
  const empty = cards.length === 0;
  return `
    <div class="kcol${empty ? " kcol-empty" : ""}" data-stage="${escapeHTML(stage)}">
      <h5>${escapeHTML(label)} <span class="kcount">${cards.length}</span></h5>
      ${cards.map((c) => cardHTML(c, orphanPaths, stoppedPaths)).join("")}
    </div>`;
}

// render draws every column from one snapshot. snap may be null (before the
// first frame arrives, or after the socket drops) — that renders the same
// empty board a snapshot with no cards would, never an error and never a
// blank root. A snapshot with boardError set (state.Snapshot.BoardError,
// e.g. a board path with no cards/ directory under it) is a distinct case
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
  const stoppedPaths = new Set(snap?.stoppedCards ?? []);

  const known = STAGES.map((stage) => cards.filter((c) => c.stage === stage));
  const other = cards.filter((c) => !STAGES.includes(c.stage));

  const columns = STAGES.map((stage, i) => columnHTML(stage, stage, known[i], orphanPaths, stoppedPaths)).join("");
  const otherColumn = columnHTML("other", "other", other, orphanPaths, stoppedPaths);

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

// The board scrolls itself rather than holding boxes that scroll, so it marks
// itself. The mechanism and the reasoning behind it live in scrollable.js,
// which is also where the panel's other scrolling boxes get it from.
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

  // Resize and scroll both change the answer with no new snapshot behind them:
  // a narrower window can hide a column that fitted, and reaching the last
  // column is the one thing the mark exists to stop claiming.
  watchSelf(root);

  subscribe((snap) => render(root, snap));
}
