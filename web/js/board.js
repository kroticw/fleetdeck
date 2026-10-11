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
  // which the drop asks about before writing over that agent's work. A card
  // still being written is not draggable: its stage on the server is not yet
  // the one it is drawn in, and a second drop would be refused against it.
  return `
    <article class="kcard ${zoneClass(c.zone)}${mark}${stateClass(c)}" data-path="${path}" data-stage="${escapeHTML(c.stage ?? "")}"${c.session ? ` data-session="${escapeHTML(c.session)}"` : ""} draggable="${c.pending ? "false" : "true"}">
      <div class="ktitle">${title}</div>
      <div class="kmeta">${cardNumberHTML(c.id)}${sessionHTML}</div>
      ${statusHTML(c)}
      <div class="kprog"><i data-progress="${clampProgress(c.progress)}"></i></div>
    </article>`;
}

// stateClass and statusHTML draw what the board knows about a card that the
// snapshot does not: a move it has asked for and not had answered (pending,
// slow), or one that was refused (failed). pendingView puts the flags on.
function stateClass(c) {
  if (c.failed) return " kcard-failed";
  if (!c.pending) return "";
  return c.slow ? " kcard-pending kcard-slow" : " kcard-pending";
}

function statusHTML(c) {
  if (c.failed) return `<div class="kstatus kstatus-failed" role="status">${escapeHTML(t("card_move_failed"))}</div>`;
  if (!c.pending) return "";
  const text = c.slow ? t("card_slow") : c.pending === "start" ? t("card_starting") : t("card_saving");
  return `<div class="kstatus" role="status">${escapeHTML(text)}</div>`;
}

// ghostHTML is a card the operator has just created and the board has not
// read yet: its title, marked as being created, in the column it will land in.
// No data-path, so nothing opens or drags it: there is no file behind it yet.
function ghostHTML(g) {
  const text = g.slow ? t("card_slow") : t("card_creating");
  return `
    <article class="kcard kcard-ghost kcard-pending${g.slow ? " kcard-slow" : ""} ${zoneClass(g.zone)}">
      <div class="ktitle">${escapeHTML(g.title)}</div>
      <div class="kstatus" role="status">${escapeHTML(text)}</div>
    </article>`;
}

// ADD_STAGE is the one column that carries the "new card" button, and it
// is one rather than every column because a card cannot be started anywhere
// else: internal/board.CreateCard writes stage new, and the board refuses every
// other stage while the card's session field is empty. A button over the review
// column would promise a card that lands in new anyway.
const ADD_STAGE = "new";

export function columnHTML(label, stage, cards, orphanPaths, stoppedPaths = new Set(), ghosts = []) {
  // An empty column is a real drop target, not dead space -- it must never
  // become unreachable -- but it has nothing that needs reading, so it has
  // no reason to claim the same width as a column carrying thirty cards.
  // .kcol-empty (app.css) is what actually shrinks it; this only says which
  // columns qualify.
  const empty = cards.length === 0 && ghosts.length === 0;
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
      ${ghosts.map(ghostHTML).join("")}
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

  const ghosts = snap?.creating ?? [];
  const columns = STAGES.map((stage, i) =>
    columnHTML(stage, stage, known[i], orphanPaths, stoppedPaths, stage === ADD_STAGE ? ghosts : []),
  ).join("");
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

// How long a write may go unanswered before the card says the panel is slow,
// how long a move the server has answered is drawn without the snapshot
// agreeing, and how long a refused move says so on its card. A write is about
// 0.4 s on a signed board (T-159); three seconds is past any normal one.
export const SLOW_MS = 3000;
export const SETTLE_MS = 10000;
export const FAILED_MS = 5000;

// pendingView is the snapshot as the board should draw it while moves it has
// asked for have not come back yet.
//
// A card dropped into another column is drawn there from the moment the move
// begins — once every question about it is answered — rather than when the
// write answers: the write is a signed commit, and the card used to sit in the
// column it was dropped out of for as long as it took. pending maps a card's
// path to {stage, kind, since, answeredAt}: kind is "move" or "start", and
// answeredAt is set once the server has said yes.
//
// Until then the card is marked as being written (pending, slow), whatever
// the snapshot says: the file is written before its commit, so the snapshot
// can carry the new stage while the write may still be refused. Once answered,
// the entry is kept until the snapshot agrees, as it was before, or until
// SETTLE_MS has passed without it agreeing: a card is not left for ever in a
// column the board never wrote. An unanswered write is never let go by the
// clock — the server bounds its own waits.
//
// failed maps a path to {until, to}: when its refusal stops being drawn on the
// card, and the stage the refused move asked for. A card the snapshot shows in
// that stage did move — a start that got part of the way answers a refusal
// with the card already active — and is not called unmoved. Entries the
// snapshot no longer has a card for, and ones out of time, are pruned here
// rather than by timers.
export function pendingView(snap, pending, now = Date.now(), failed = new Map()) {
  if (!snap || (pending.size === 0 && failed.size === 0)) return snap;
  const seen = new Set();
  const cards = (snap.cards ?? []).map((c) => {
    seen.add(c.path);
    const refusal = failed.get(c.path);
    if (refusal !== undefined && (now >= refusal.until || c.stage === refusal.to)) failed.delete(c.path);
    else if (refusal !== undefined) return { ...c, failed: true };
    const want = pending.get(c.path);
    if (want === undefined) return c;
    if (want.answeredAt !== undefined) {
      if (c.stage === want.stage || now - want.answeredAt > SETTLE_MS) {
        pending.delete(c.path);
        return c;
      }
      return { ...c, stage: want.stage };
    }
    return { ...c, stage: want.stage, pending: want.kind, slow: now - want.since > SLOW_MS };
  });
  for (const path of [...pending.keys()]) {
    if (!seen.has(path)) pending.delete(path);
  }
  for (const path of [...failed.keys()]) {
    if (!seen.has(path)) failed.delete(path);
  }
  return { ...snap, cards };
}

// The stages the board refuses a card with no session (internal/board,
// checkCrossFieldRules). Active is not among them: a drop there offers to
// start one (boardmove.js).
const NEED_SESSION = new Set(["review", "blocked", "done"]);

// dropVerdict is what a drop of the card being dragged into the column of
// stage would come to, as far as the page can tell before asking: "source" for
// its own column, "refused" where the board is known to say no, "open" where
// the move goes ahead or is asked about. A refused drop is still taken — the
// board's refusal is what says why.
export function dropVerdict(move, stage) {
  if (stage === move.from) return "source";
  if (!STAGES.includes(stage)) return "refused";
  if (!move.session && NEED_SESSION.has(stage)) return "refused";
  return "open";
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
  // Refused moves still said on their cards, and cards created and not yet
  // read: what pendingView and render draw on top of the snapshot.
  const failed = new Map();
  const ghosts = [];
  // The card being dragged, while one is. render reads the same state off
  // root.dataset and skips the frame; this holds what the drop needs to know.
  let dragging = null;
  const hold = (card) => {
    dragging = card;
    if (card) root.dataset.dragging = "1";
    else delete root.dataset.dragging;
  };

  const draw = () => {
    const now = Date.now();
    const view = pendingView(latest, pending, now, failed);
    for (const g of [...ghosts]) {
      // Gone once the board carries its card, or once that has failed to
      // happen for SETTLE_MS after the server said it was written.
      const landed = g.path && (latest?.cards ?? []).some((c) => c.path === g.path);
      if (landed || (g.createdAt !== undefined && now - g.createdAt > SETTLE_MS)) ghosts.splice(ghosts.indexOf(g), 1);
      else g.slow = g.createdAt === undefined && now - g.since > SLOW_MS;
    }
    render(root, ghosts.length ? { ...view, creating: ghosts } : view);
  };
  // Drawn again when a mark runs out, for a board no snapshot reaches in the
  // meantime: a dropped socket must not leave "slow" unsaid or a refusal up
  // for ever. unref, where there is one, keeps a timer from holding a test
  // process open.
  const later = (ms) => setTimeout(draw, ms + 50)?.unref?.();

  // The columns are lit by hand rather than drawn lit: render skips every
  // frame while a card is in the air, and a drop can leave the board undrawn
  // for as long as the move's dialog is up.
  const columns = () => root.querySelectorAll(":scope > .kcol");
  const light = (move) => {
    for (const column of columns()) {
      const verdict = dropVerdict(move, column.dataset.stage);
      column.classList.toggle("kcol-drop-open", verdict === "open");
      column.classList.toggle("kcol-drop-refused", verdict === "refused");
    }
  };
  const aim = (target) => {
    for (const column of columns()) column.classList.toggle("kcol-drop-over", column === target);
  };
  const unlight = () => {
    for (const column of columns()) {
      for (const name of ["kcol-drop-open", "kcol-drop-refused", "kcol-drop-over"]) column.classList.remove(name);
    }
  };

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
    if (!card || !root.contains(card) || !card.dataset.path) return;
    onOpenCard(card.dataset.path);
  });

  root.addEventListener("dragstart", (ev) => {
    const card = ev.target.closest(".kcard");
    if (!card || !onMove || !card.dataset.path) return;
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
    light(dragging);
  });

  // A drop target is a column that does not preventDefault on dragover: the
  // browser refuses the drop otherwise, and it refuses it silently.
  root.addEventListener("dragover", (ev) => {
    if (!dragging) return;
    const column = ev.target.closest(".kcol");
    aim(column?.dataset.stage === dragging.from ? null : column);
    if (!column) return;
    ev.preventDefault();
    if (ev.dataTransfer) ev.dataTransfer.dropEffect = "move";
  });

  // dragleave fires on every child the pointer crosses, so only a pointer
  // that has left the column for somewhere outside it takes the target off.
  root.addEventListener("dragleave", (ev) => {
    const column = ev.target.closest(".kcol");
    if (!dragging || !column || column.contains(ev.relatedTarget)) return;
    column.classList.remove("kcol-drop-over");
  });

  root.addEventListener("drop", (ev) => {
    const column = ev.target.closest(".kcol");
    const move = dragging;
    hold(null);
    unlight();
    if (!move || !column) return;
    ev.preventDefault();
    const to = column.dataset.stage;
    if (to && to !== move.from) {
      // begin is onMove saying nothing is left to ask: the card is drawn in
      // its new column from here, marked as being written. A move that was
      // begun and did not happen goes back with a word on the card; one the
      // operator declined in a dialog was never begun and says nothing.
      let begun = false;
      const begin = (kind) => {
        begun = true;
        // A drop again while an earlier refusal is still said: this move is
        // the one to draw.
        failed.delete(move.path);
        pending.set(move.path, { stage: to, kind, since: Date.now() });
        later(SLOW_MS);
        draw();
      };
      const refused = () => {
        if (!begun) return;
        pending.delete(move.path);
        failed.set(move.path, { until: Date.now() + FAILED_MS, to });
        later(FAILED_MS);
      };
      Promise.resolve(onMove({ ...move, to }, { begin }))
        .then((drawn) => {
          if (!drawn) return refused();
          const asked = pending.get(move.path) ?? { kind: "move", since: Date.now() };
          pending.set(move.path, { ...asked, stage: drawn, answeredAt: Date.now() });
          later(SETTLE_MS);
        })
        // A move that threw rather than answering leaves the card where it
        // was, and the board still has to be drawn: without this the gesture
        // ends with the board frozen on the frame the drag started from, which
        // reads as the panel having stopped.
        .catch((err) => {
          console.error("the card was not moved", err);
          refused();
        })
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
    unlight();
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

  // creating is the new card form saying a card is on its way: it is drawn
  // in the new column at once, and the handle says how the write went —
  // created with the card's path, which the snapshot is then watched for, or
  // failed, which takes it off (the form reopens with what was typed).
  return {
    creating(title, zone) {
      const ghost = { title, zone, since: Date.now() };
      ghosts.push(ghost);
      later(SLOW_MS);
      draw();
      return {
        created(path) {
          ghost.path = path;
          ghost.createdAt = Date.now();
          later(SETTLE_MS);
          draw();
        },
        failed() {
          const at = ghosts.indexOf(ghost);
          if (at >= 0) ghosts.splice(at, 1);
          draw();
        },
      };
    },
  };
}
