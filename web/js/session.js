// web/js/session.js
//
// The session panel: two tabs over one session, and the input that types into
// it. This is the first part of the interface that writes.
//
// The two tabs have two sources and are never mixed. The digest is readable
// text reconstructed from the session's transcript file — what the session
// said, after the fact, with the housekeeping stripped out. The screen is the
// terminal as the daemon holds it right now, escape sequences and all. They
// disagree by design: the transcript lags behind, and the screen has no memory
// of anything that scrolled away. One pane that showed sometimes one and
// sometimes the other would leave a person unable to tell which they were
// reading, so each tab keeps its own source and says which it is.
//
// Nothing here is built out of an HTML string. Every node is created and every
// piece of text is assigned as .textContent, which means a session's own words
// — and sessions we did not write can join this fleet (spec 3.1) — have no path
// into markup at all, and a translated string cannot break out of an attribute
// it was interpolated into. It also makes the panel drivable under node's test
// runner against the stand-in document in web/tests/fake-dom.js. Only the clock
// is reached through a parameter: the document, fetch and the terminal
// constructor are read from the global object, which is where the browser puts
// them and where a test can put its own.

import { fetchDigest, fetchScreen, sendKeys, sendText } from "./api.js";
import { get } from "./store.js";
import { t } from "./i18n.js";
import { syncSteps } from "./steps.js";
import { wireImagePaste } from "./pasteimage.js";

// How many transcript steps the digest asks for, and how often each tab
// refreshes. The screen is polled faster because it is what a person watches
// while a session works; the digest only changes when a session speaks.
//
// Polled, and not streamed over a socket — which looks like the simpler design
// until you read what attach costs. A session's attach stream is exclusive: the
// daemon evicts whoever held it when a new reader arrives, and writes
// "EKICKED: ..." into the evicted stream in place of PTY bytes
// (internal/daemon/client.go, ekickedPrefix). A panel holding that stream open
// would sit in the single slot, so an operator running `claude agents attach`
// against their own session would evict the panel and the panel's next read
// would evict the operator, back and forth, over the operator's own terminal.
// The same fact is why ReadScreen shortens any deadline it is given to two
// seconds (screenDeadline). Polling is not a shortcut here; it is what leaves
// the terminal to the person using it.
const DIGEST_LIMIT = 30;
const DIGEST_INTERVAL_MS = 3000;
const SCREEN_INTERVAL_MS = 1000;

// The key buttons send bytes, not names.
//
// internal/server hands the `keys` field straight to daemon.Client.SendKeys,
// which writes it into the session's PTY with conn.Write([]byte(keys)). There
// is no translation layer anywhere between this table and the terminal, so
// sending "up" would type the letters u and p into a live session instead of
// moving its selection.
export const KEYS = [
  { id: "escape", label: "Esc", bytes: "\u001b" },
  { id: "up", label: "\u2191", bytes: "\u001b[A" },
  { id: "down", label: "\u2193", bytes: "\u001b[B" },
  { id: "enter", label: "Enter", bytes: "\r" },
];

// The roles a transcript step may claim. The value reaches a class name, and it
// comes out of a file written by a session, so it is matched against this list
// rather than trusted into the DOM.
const KNOWN_ROLES = new Set(["user", "assistant"]);

// createPoller runs `pass` now and then every delayMs, with exactly one timer
// outstanding at any moment.
//
// That invariant is the whole point of this function. The obvious way to write
// this — arming a fresh timer at the end of the very function the timer calls,
// without clearing the one that fired it — doubles the number of live timers on
// every tick, so a tab left open for a minute is issuing thousands of requests
// a second. It is invisible until the machine is on fire, so it is a unit test
// (session.test.js) rather than a comment.
//
// The second rule: a pass that fails is still a pass. The next one is armed
// whether `pass` resolved or threw, so one dropped attach or a daemon restarted
// underneath the panel does not freeze a tab on an error until a person thinks
// to close and reopen it.
//
// A failing pass throws, and this is the only place that catches — deliberately.
// If each tab caught its own failures, each tab would decide for itself whether
// to keep polling, and the plan this replaces had exactly that: the digest tab
// retried and the screen tab froze on the first error. One catch, one rule, no
// way for the two tabs to disagree.
export function createPoller(pass, delayMs, { timers = globalThis, onError = () => {} } = {}) {
  let handle = null;
  let stopped = false;

  const arm = () => {
    if (stopped) return;
    // Clear before setting. Nothing should reach here with a timer already
    // armed, and if anything ever does — a second start(), a double click on a
    // tab — the old one is cancelled rather than left running unreferenced.
    if (handle !== null) timers.clearTimeout(handle);
    handle = timers.setTimeout(tick, delayMs);
  };

  const tick = () => {
    handle = null;
    void run();
  };

  const run = async () => {
    if (stopped) return;
    try {
      await pass();
    } catch (err) {
      onError(err);
    }
    arm();
  };

  return {
    start() {
      void run();
    },
    stop() {
      // Must leave nothing running: a stop that misses a timer leaves a panel
      // nobody is looking at still polling a session.
      stopped = true;
      if (handle !== null) timers.clearTimeout(handle);
      handle = null;
    },
  };
}

// terminalTheme reads the panel's own colour tokens (app.css's :root custom
// properties, already resolved for whichever theme is current) and turns
// them into the object xterm.js's `theme` constructor option wants.
//
// Without this xterm falls back to its own default palette — a light grey on
// black regardless of what the rest of the page is doing — which is
// invisible as a defect for as long as the whole app is dark-only, and is
// exactly what a live run surfaced once light became a real, chosen theme:
// the terminal stayed a solid black rectangle inside an otherwise light
// panel. Read once, at the moment the terminal is built (the panel is torn
// down and rebuilt on every open, so this does not need to react to a theme
// switch mid-session — only a fresh open needs to start on the right one).
//
// getComputedStyle and document.documentElement are both real-browser-only:
// web/tests/fake-dom.js's FakeDocument has neither, on purpose — it is a
// wiring test double, not a layout engine. Returning undefined here rather
// than throwing lets those tests construct a terminal exactly as they did
// before this function existed; the real page always has both.
function terminalTheme() {
  if (typeof getComputedStyle !== "function" || !document.documentElement) return undefined;
  const style = getComputedStyle(document.documentElement);
  const token = (name) => style.getPropertyValue(name).trim();
  return {
    background: token("--surface"),
    foreground: token("--text"),
    cursor: token("--accent"),
    cursorAccent: token("--surface"),
    selectionBackground: token("--surface-hover"),
  };
}

// defaultTerminalFactory builds an xterm.js terminal in `host`.
//
// window.Terminal is what web/vendor/xterm.js assigns when index.html loads it
// with a plain <script> tag — the UMD bundle exports exactly one name. A script
// tag that 404s reports nothing to anyone, which is precisely the kind of
// silent failure this project keeps finding, so its absence is turned into a
// message on the screen instead of a tab that stays mysteriously blank.
function defaultTerminalFactory(host) {
  const Terminal = globalThis.Terminal;
  if (typeof Terminal !== "function") return null;
  const terminal = new Terminal({
    convertEol: true,
    fontSize: 12,
    scrollback: 2000,
    theme: terminalTheme(),
  });
  terminal.open(host);
  return terminal;
}

// renderSession draws the panel for one session into `root` and starts polling.
// It returns a stop function; calling it, or the panel's own close button,
// leaves no timer and no terminal behind.
//
// The panel is opened with a short id, because that is the identity the session
// list hands over — and the two tabs are keyed differently, which is the one
// thing about this panel that cannot be guessed from the routes' names. The
// daemon knows a session by its short id and answers EUNKNOWN to the full one,
// so the screen, the keys and the text go out under `short`. The transcript is
// a file named after the full session id and the digest route resolves nothing
// shorter, so the digest needs that instead. Sending either to the other route
// fails at runtime and in no other way: the panel said "transcript not found"
// on every session until the two were told apart.
//
// The full id is looked up in the snapshot on every digest pass rather than
// captured when the panel opens. Captured once, a panel opened before the first
// snapshot arrives holds an empty id for as long as it stays open, and says the
// session is not listed while the session sits in the list beside it — seen in a
// screenshot, not deduced.
//
// timers and lookup exist for the tests, which cannot wait three real seconds
// for a second poll or drive a live WebSocket. Both default to the real thing,
// so nothing in the shipped path is a stand-in.
export function renderSession(
  root,
  short,
  onClose,
  { timers = globalThis, lookup = (id) => (get()?.sessions ?? []).find((s) => s.short === id) } = {},
) {

  let tab = "digest";
  let poller = null;
  let terminal = null;
  let body = null;
  let errorLine = null;
  let noticeLine = null;
  let disposePaste = null;
  let input = null;
  let nameLine = null;

  const el = (tag, className, text) => {
    const node = document.createElement(tag);
    if (className) node.className = className;
    if (text !== undefined) node.textContent = text;
    return node;
  };

  // The name of the session this panel is pointing at, and the short id when the
  // snapshot does not name it. The short id is never nothing: it is the identity
  // the session list shows for an unnamed session and the one an operator can
  // match against that list, whereas a blank header says only that the panel
  // does not know where it points — under a key row that promises to press keys
  // in "the live session".
  const currentName = () => lookup(short)?.name || short;

  // Resolved on every poll pass rather than captured when the panel opens, for
  // the same reason the digest's full session id is (see the note above
  // renderSession): a panel opened before the first snapshot lands would
  // otherwise hold whatever was known then — nothing — for as long as it stays
  // open, while the session sits named in the list beside it.
  //
  // Written only when it actually changed. A header rewritten once a second
  // drops any selection inside it and costs the work for no visible difference,
  // which is invisible in the resulting tree and therefore a counted assertion
  // in session.test.js rather than a comment here alone.
  const refreshName = () => {
    if (!nameLine) return;
    const name = currentName();
    if (nameLine.textContent !== name) nameLine.textContent = name;
  };

  // What the two message lines are saying, held here rather than only in the
  // nodes. drawShell builds fresh lines on every tab switch, so a message that
  // lived only in a node was silently lost by switching to the screen tab to
  // look at what the session was asking — which is precisely when there is a
  // message worth keeping.
  //
  // The error is two states, not one. A background poll used to clear the same
  // variable an operator's own failure was written to, and the digest polls
  // every few seconds: a refused paste or a failed send vanished within one
  // tick, leaving a person who had just pasted a file with no idea why nothing
  // happened. Found by comparing this pane against the orchestrator column,
  // which carries two independent slots for exactly this reason and says so.
  //
  // One line rather than two, because the two are never equally urgent — what
  // the operator just did wins, and a failing background refresh waits behind
  // it. What matters is that neither can erase the other.
  let actionError = "";
  let pollError = "";
  let noticeText = "";

  // paintError and paintNotice write into a line of their own above the input,
  // rather than replacing what the tab is showing. Replacing it would throw away
  // the terminal or the last digest that did arrive, and a transient failure
  // would cost a person the content they were reading.
  const paintError = () => {
    if (!errorLine) return;
    const message = actionError || pollError;
    errorLine.textContent = message;
    errorLine.hidden = !message;
  };

  const paintNotice = () => {
    if (!noticeLine) return;
    noticeLine.textContent = noticeText;
    noticeLine.hidden = !noticeText;
  };

  // What the operator's own action reported — a send, a key, a pasted image.
  // Cleared only by the next such action.
  const showError = (message) => {
    actionError = message ?? "";
    paintError();
  };

  // What the background poll reported. Cleared only by that poll succeeding, so
  // it can neither erase nor be erased by the line above.
  const showPollError = (message) => {
    pollError = message ?? "";
    paintError();
  };

  // showNotice is the same idea for something that is not a failure. It has a
  // line of its own rather than sharing the error line: "the session may ask you
  // for permission" is an expected step, and showing it where failures appear
  // would teach the operator to read the error line as noise.
  const showNotice = (message) => {
    noticeText = message ?? "";
    paintNotice();
  };

  // formatBytes is only ever given this module's own ceiling, so it needs no
  // more than whole mebibytes and no rounding rules worth arguing about.
  const formatBytes = (bytes) => `${Math.round(bytes / (1024 * 1024))} MiB`;

  const disposeTerminal = () => {
    // xterm holds a renderer, listeners and a resize observer. Dropping the
    // reference without disposing leaks all three for the life of the page.
    if (terminal && typeof terminal.dispose === "function") terminal.dispose();
    terminal = null;
  };

  const stopPolling = () => {
    if (poller) poller.stop();
    poller = null;
  };

  const stop = () => {
    stopPolling();
    disposeTerminal();
    // The paste handler goes with the panel. Left attached, it would keep
    // uploading into a session nobody is looking at any more.
    if (disposePaste) {
      disposePaste();
      disposePaste = null;
    }
  };

  // How a step's row is classed here. The shared renderer owns everything
  // inside a step; this pane owns what its rows are called.
  const stepClass = (role) => `s-step s-step-${KNOWN_ROLES.has(role) ? role : "other"}`;

  const renderSteps = (steps) => {
    if (steps.length === 0) {
      // The server errors on a transcript it cannot read, so an empty list is
      // a transcript that exists and holds nothing readable. Still says so:
      // an empty pane is indistinguishable from a pane that failed to load.
      body.replaceChildren(el("div", "s-empty", t("no_steps")));
      return;
    }
    // Drawn by web/js/steps.js, the same renderer the orchestrator column uses.
    // Before this, these two panes drew a step with two different pieces of
    // code, and only one of them had learned markdown, envelope unwrapping and
    // leaving an unchanged step alone — which is a defect no test on either
    // side could see.
    //
    // A pane that was showing the "no steps" message has that message as its
    // only child, and it is not a step; clearing it here means syncSteps always
    // starts from rows it wrote itself.
    if (body.firstChild && !body.firstChild.dataset?.stepKey) body.replaceChildren();
    syncSteps(body, steps, stepClass);
  };

  // Neither pass catches. A session with no transcript, a route that is not
  // there, a daemon that went away: all of them are failures a person must see,
  // and all of them must be tried again. Both happen in createPoller, once, for
  // both tabs. Whatever was last drawn stays under the message, so one failed
  // poll does not blank a pane that was full a second ago.
  const digestPass = async () => {
    const sessionId = lookup(short)?.sessionId ?? "";
    if (!sessionId) {
      // No snapshot yet, or a session that has left the fleet. Saying so beats
      // asking the server for /api/sessions//digest and reporting whatever that
      // returns — and because this runs on every pass, the tab starts working
      // by itself once the session is in a snapshot.
      throw new Error(t("session_not_listed"));
    }
    renderSteps(await fetchDigest(sessionId, DIGEST_LIMIT));
    showPollError("");
  };

  const ensureTerminal = () => {
    if (terminal) return terminal;
    const host = el("div", "s-term");
    body.replaceChildren(host);
    const made = defaultTerminalFactory(host);
    if (!made) {
      showPollError(t("terminal_missing"));
      return null;
    }
    terminal = made;
    return terminal;
  };

  const screenPass = async () => {
    const term = ensureTerminal();
    if (!term) return; // the library is missing; ensureTerminal already said so
    // A read the daemon refused is a result, not an exception: it still carries
    // what arrived before the attach broke. Draw that and show the message
    // beside it rather than discarding both. Anything worse than that — the
    // request never completing at all — throws, and createPoller reports it.
    const { screen, error } = await fetchScreen(short);
    if (screen !== "") {
      term.reset();
      term.write(screen);
    }
    showPollError(error);
  };

  const startPolling = () => {
    const isDigest = tab === "digest";
    const pass = isDigest ? digestPass : screenPass;
    poller = createPoller(
      async () => {
        // Before the pass, not after it: a pass that throws — a session with no
        // transcript, a daemon that went away — must still leave the header
        // naming the session, and the digest pass throws precisely when the
        // snapshot does not hold the session yet, which is the case the header
        // has to recover from.
        refreshName();
        await pass();
      },
      isDigest ? DIGEST_INTERVAL_MS : SCREEN_INTERVAL_MS,
      { timers, onError: (err) => showPollError(err.message) },
    );
    poller.start();
  };

  const selectTab = (next) => {
    if (next === tab) return;
    tab = next;
    stop();
    drawShell();
    startPolling();
  };

  const pressKey = async (key) => {
    try {
      await sendKeys(short, key.bytes);
      showError("");
    } catch (err) {
      showError(err.message);
    }
  };

  const submitTyped = async () => {
    // The raw value, not the trimmed one: if the send fails this is what goes
    // back into the box, and it must be what the person typed.
    const typed = input.value;
    if (typed.trim() === "") return;
    input.value = "";
    try {
      await sendText(short, typed.trim());
      showError("");
      // Only now, and only here. Whatever the last paste had to say, it said it
      // about a path that has just left the box — but if the send had failed the
      // path would be back in the box below, and the sentence explaining that
      // the session is about to ask permission would still be true.
      showNotice("");
    } catch (err) {
      // The one failure this panel must not have. Losing what somebody typed
      // is worse than any error message, so the text goes back exactly as it
      // was and the message goes beside it.
      input.value = typed;
      showError(err.message);
    }
  };

  function drawShell() {
    root.hidden = false;

    // What is in the box outlives the redraw. drawShell builds a new textarea on
    // every tab switch, and this panel already holds the rule that a person's
    // unsent words are the one thing it must not lose — text that failed to send
    // comes back into the box. A tab switch is not even a failure, which makes
    // dropping the words worse rather than better: nothing went wrong and they
    // are gone anyway. And switching to the screen tab to see what a session is
    // actually asking is exactly when a half-written answer exists.
    //
    // The caret is not carried with it: the redraw does not preserve focus
    // either, so there is nothing to put a caret back into.
    const typed = input ? input.value : "";

    const head = el("div", "s-head");

    const tabs = el("div", "s-tabs");
    for (const [id, key] of [
      ["digest", "tab_digest"],
      ["screen", "tab_screen"],
    ]) {
      const button = el("button", id === tab ? "s-tab s-tab-on" : "s-tab", t(key));
      button.type = "button";
      button.dataset.tab = id;
      button.addEventListener("click", () => selectTab(id));
      tabs.appendChild(button);
    }

    // Beside the tabs, because the header is where a person looks to find out
    // what they are looking at — and because the keys at the foot of the panel
    // now say they are pressed in a live session, which is only half an answer
    // until the panel says which one.
    nameLine = el("div", "s-who", currentName());
    // The name is the session list's own, and two sessions may carry the same
    // one; the short id under the pointer tells them apart. A property, never
    // interpolated into markup.
    nameLine.title = short;

    const close = el("button", "s-close", "✕");
    close.type = "button";
    close.title = t("close_session");
    close.addEventListener("click", () => {
      stop();
      onClose();
    });

    // The header holds the two controls that act on this panel and nothing
    // else: the tabs, and the button that closes it. Both are undone by
    // reopening the panel.
    head.appendChild(tabs);
    head.appendChild(nameLine);
    head.appendChild(close);

    // The keys are not among them, and this is the change the operator's first
    // look at this panel bought. Drawn where they used to be — a bare
    // `Esc ↑ ↓ Enter ✕` row in the top-right corner, opposite the tabs — they
    // are in the exact place every window on the operator's machine puts
    // controls that act on the window, and he read them as that and asked what
    // they were for. They are not that: each one presses a key inside a Claude
    // Code session running somewhere else, in work that is somebody's, and
    // nothing takes it back. A button whose purpose is unclear is either never
    // pressed or pressed to find out, and `↓` pressed to find out moves a menu
    // selection in that session.
    //
    // So they sit down here instead, against the box that writes into the same
    // session, under a label that names the destination. Grouped by where the
    // press lands, not by which corner had room.
    //
    // Only on the screen tab. The digest is transcript text already spoken, and
    // it is the tab the panel opens on: the first meeting with these buttons was
    // on the one tab where pressing them answers nothing on the screen in front
    // of you. A control that does nothing meaningful where it is shown teaches a
    // person that controls in this panel need not be understood.
    let keys = null;
    if (tab === "screen") {
      keys = el("div", "s-keys");
      keys.appendChild(el("span", "s-keys-label", t("keys_to_session")));
      for (const key of KEYS) {
        const button = el("button", "s-key", key.label);
        button.type = "button";
        button.dataset.key = key.id;
        button.addEventListener("click", () => {
          void pressKey(key);
        });
        keys.appendChild(button);
      }
    }

    body = el("div", "s-body");

    errorLine = el("div", "s-error");
    errorLine.hidden = true;

    noticeLine = el("div", "s-notice");
    noticeLine.hidden = true;

    const form = el("form", "s-form");
    // A form left to its default behaviour navigates the page away on Enter,
    // taking the whole panel with it.
    form.addEventListener("submit", (event) => event.preventDefault());
    input = el("textarea", "s-input");
    input.rows = 2;
    // Set as a property, never interpolated into markup: a translation holding
    // a quote would otherwise break out of the attribute it was written into.
    input.placeholder = t("write_to_session");
    input.value = typed;
    input.addEventListener("keydown", (event) => {
      // Enter sends, Shift+Enter is a newline — the same bargain every chat
      // input makes.
      if (event.key !== "Enter" || event.shiftKey) return;
      event.preventDefault();
      return submitTyped();
    });

    // Pasting an image goes to the same session this panel is pointing at. The
    // textarea is rebuilt on every tab switch, so the handler is attached here
    // rather than once at open — and disposed with the node it sat on, which is
    // what keeps a tab switch from leaving a second one behind and uploading a
    // pasted image twice.
    if (disposePaste) disposePaste();
    disposePaste = wireImagePaste(input, () => short, {
      onError: showError,
      onNotice: showNotice,
    });

    form.appendChild(input);

    // keys is absent on the digest tab, and filtered out rather than replaced by
    // an empty node: an empty container still takes the row's gap and leaves the
    // writing area sitting at a different height on each tab.
    root.replaceChildren(...[head, body, errorLine, noticeLine, keys, form].filter(Boolean));

    // The lines above are brand new and empty; what they were saying is held in
    // state, so it is written back. Without this, switching to the screen tab to
    // see what a session is actually asking threw away the message that said
    // why — the same rule this panel already holds for the half-written text in
    // the box, applied to the two lines beside it.
    paintError();
    paintNotice();
  }

  drawShell();
  startPolling();
  return stop;
}

export default renderSession;
