import { subscribe } from "./store.js";
import { t } from "./i18n.js";
import { markScrollable, watchSelf } from "./scrollable.js";
// The card's number, drawn the same way here and in the session list.
import { cardNumberHTML } from "./cardnumber.js";

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
  // data-stage is what a drop writes against: the stage this card was drawn
  // in, so the write can be refused if the card has moved since (the expect
  // of PATCH /api/cards). data-session says whether an agent is holding it,
  // which the drop asks about before writing over that agent's work.
  return `
    <article class="kcard ${zoneClass(c.zone)}${mark}" data-path="${path}" data-stage="${escapeHTML(c.stage ?? "")}"${c.session ? ` data-session="${escapeHTML(c.session)}"` : ""} draggable="true">
      <div class="ktitle">${title}</div>
      <div class="kmeta">${cardNumberHTML(c.id)}${sessionHTML}</div>
      <div class="kprog"><i data-progress="${clampProgress(c.progress)}"></i></div>
    </article>`;
}

// ADD_STAGE is the one column that carries the "new card" button, and it
// is one rather than every column because a card cannot be started anywhere
// else: internal/board.CreateCard writes stage new, and the board refuses every
// other stage while the card's session field is empty. A button over the review
// column would promise a card that lands in new anyway.
const ADD_STAGE = "new";

export function columnHTML(label, stage, cards, orphanPaths, stoppedPaths = new Set()) {
  // An empty column is a real drop target, not dead space -- it must never
  // become unreachable -- but it has nothing that needs reading, so it has
  // no reason to claim the same width as a column carrying thirty cards.
  // .kcol-empty (app.css) is what actually shrinks it; this only says which
  // columns qualify.
  const empty = cards.length === 0;
  // A row of its own under the head, as wide as the column: a square beside
  // the count was missed by eye and by pointer.
  const add =
    stage === ADD_STAGE
      ? `<button type="button" class="btn btn-sm kcol-add" aria-label="${escapeHTML(t("new_card"))}">${escapeHTML(t("new_card"))}</button>`
      : "";
  return `
    <div class="kcol${empty ? " kcol-empty" : ""}" data-stage="${escapeHTML(stage)}">
      <h5>${escapeHTML(label)} <span class="kcount">${cards.length}</span></h5>
      ${add}
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
export function render(root, snap) {
  // A card being dragged is held by the browser as the element the gesture
  // started on. Replacing #board's children mid-gesture therefore ends the
  // drag, and the card is left in the column it came from with no sign of
  // why. The frame is dropped instead; the store keeps the newest snapshot,
  // and renderBoard draws again the moment the gesture is over. The flag is
  // on the element rather than in a variable so that every way in here obeys
  // it, not only the subscription.
  if (root.dataset?.dragging) return;

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

  // In the fleetdeck window a column scrolls on its own (web/app.css), and the
  // panel's snapshot comes every second: columns drawn anew are at their top,
  // so each one scrolled is put back where it was, by its stage. Columns after
  // a board error start at their top: the error left none to remember.
  const scrolled = new Map();
  for (const column of root.querySelectorAll(":scope > .kcol")) {
    if (column.scrollTop > 0) scrolled.set(column.dataset.stage, column.scrollTop);
  }

  root.innerHTML = columns + otherColumn;

  for (const column of root.querySelectorAll(":scope > .kcol")) {
    const top = scrolled.get(column.dataset.stage);
    if (top !== undefined) column.scrollTop = top;
  }

  // CSP forbids an inline style="..." attribute (index.html: style-src
  // 'self'), so the bar's width can never be baked into the HTML string
  // above — it is set here as a CSSOM property assignment instead, which the
  // policy does not touch.
  for (const bar of root.querySelectorAll(".kprog i[data-progress]")) {
    bar.style.width = bar.dataset.progress + "%";
  }

  markScrollable(root);
}

// pendingView is the snapshot as the board should draw it while a move it has
// asked for has not come back yet.
//
// A card dropped into another column is written by a PATCH, and the board only
// learns the result from the next snapshot — up to a second later. Drawn from
// the snapshot alone the card would jump back to the column it came from and
// then forward again, which reads as the drop having failed. So the asked-for
// stage is drawn instead, until the snapshot agrees.
//
// pending is pruned here rather than by a timer: an entry the snapshot has
// caught up with, and one whose card is no longer on the board at all, are
// both over.
export function pendingView(snap, pending) {
  if (!snap || pending.size === 0) return snap;
  const seen = new Set();
  const cards = (snap.cards ?? []).map((c) => {
    seen.add(c.path);
    const want = pending.get(c.path);
    if (want === undefined) return c;
    if (c.stage === want) {
      pending.delete(c.path);
      return c;
    }
    return { ...c, stage: want };
  });
  for (const path of [...pending.keys()]) {
    if (!seen.has(path)) pending.delete(path);
  }
  return { ...snap, cards };
}

// The board scrolls itself rather than holding boxes that scroll, so it marks
// itself. The mechanism and the reasoning behind it live in scrollable.js,
// which is also where the panel's other scrolling boxes get it from.
//
// onAddCard opens the new card form over the new column, and onMove is a card
// dropped into another column: `{path, from, to}`, answered with the stage the
// board should draw until the next snapshot, or nothing when the move did not
// happen. A board wired without them opens cards and nothing else.
export function renderBoard(root, onOpenCard, { onAddCard, onMove } = {}) {
  // The snapshot the board would be drawn from if it were being drawn right
  // now, and the moves it has asked for and not seen come back.
  let latest = null;
  const pending = new Map();
  // The card being dragged, while one is. render reads the same state off
  // root.dataset and skips the frame; this holds what the drop needs to know.
  let dragging = null;
  const hold = (card) => {
    dragging = card;
    if (card) root.dataset.dragging = "1";
    else delete root.dataset.dragging;
  };

  const draw = () => render(root, pendingView(latest, pending));

  // One delegated listener rather than one per card: root.innerHTML is
  // replaced whole on every snapshot, so per-card listeners would need to be
  // re-attached every time anyway, and delegation reads the path straight
  // back from the browser's own attribute decoding — no re-escaping needed.
  root.addEventListener("click", (ev) => {
    if (ev.target.closest(".kcol-add")) {
      onAddCard?.();
      return;
    }
    const card = ev.target.closest(".kcard");
    if (!card || !root.contains(card)) return;
    onOpenCard(card.dataset.path);
  });

  root.addEventListener("dragstart", (ev) => {
    const card = ev.target.closest(".kcard");
    if (!card || !onMove) return;
    hold({
      path: card.dataset.path,
      from: card.dataset.stage,
      session: card.dataset.session ?? "",
      // Alive means the card is being kept by an agent right now, which is
      // what the drop asks about before writing over that agent's work. It is
      // read off the marks the card was drawn with rather than off the
      // snapshot: a card whose session is dead or stopped names a session and
      // is nobody's to lose, and those two are exactly what the marks say.
      live: Boolean(card.dataset.session) && !card.classList.contains("kcard-orphan") && !card.classList.contains("kcard-stopped"),
    });
    // text/plain rather than a type of our own: a drop outside the board then
    // carries the card's path as text rather than nothing at all, and the
    // browser has a label to draw the drag with.
    ev.dataTransfer?.setData?.("text/plain", card.dataset.path);
    if (ev.dataTransfer) ev.dataTransfer.effectAllowed = "move";
    card.classList?.add?.("kcard-dragging");
  });

  // A drop target is a column that does not preventDefault on dragover: the
  // browser refuses the drop otherwise, and it refuses it silently.
  root.addEventListener("dragover", (ev) => {
    if (!dragging || !ev.target.closest(".kcol")) return;
    ev.preventDefault();
    if (ev.dataTransfer) ev.dataTransfer.dropEffect = "move";
  });

  root.addEventListener("drop", (ev) => {
    const column = ev.target.closest(".kcol");
    const move = dragging;
    hold(null);
    if (!move || !column) return;
    ev.preventDefault();
    const to = column.dataset.stage;
    if (to && to !== move.from) {
      Promise.resolve(onMove({ ...move, to }))
        .then((drawn) => {
          if (drawn) pending.set(move.path, drawn);
        })
        // A move that threw rather than answering leaves the card where it
        // was, and the board still has to be drawn: without this the gesture
        // ends with the board frozen on the frame the drag started from, which
        // reads as the panel having stopped.
        .catch((err) => console.error("the card was not moved", err))
        .then(draw);
      return;
    }
    draw();
  });

  // dragend fires whatever ended the gesture — a drop, Escape, a release over
  // nothing — so the board is drawn again from here rather than from drop
  // alone, which a cancelled drag never reaches.
  root.addEventListener("dragend", () => {
    hold(null);
    draw();
  });

  // Resize and scroll both change the answer with no new snapshot behind them:
  // a narrower window can hide a column that fitted, and reaching the last
  // column is the one thing the mark exists to stop claiming.
  watchSelf(root);

  subscribe((snap) => {
    latest = snap;
    draw();
  });
}
