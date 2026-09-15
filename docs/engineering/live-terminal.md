# Live terminal, window and test stand: field notes

These are facts about the live terminal and the layers around it. Each one was established while the live terminal was being built (September 2026, Claude Code 2.1.263, xterm.js 6.0.0, webview_go). They are the facts that cost a day to find and that nothing else in the repository records. Each says how it was established: **measured** (observed on a live system), **read** (in a binary or in code), or **decided** (a choice, with its reason).

Read this before changing `web/js/liveterminal.js`, `internal/server/pty.go` or the window, and before building a test stand. The daemon's own wire protocol is in [`docs/protocol/daemon-control-socket.md`](../protocol/daemon-control-socket.md). This page is about what our side does around it.

## 1. The path a byte takes

1. The page's `createLiveTerminal` (`web/js/liveterminal.js`) first fetches `GET /api/terminal-token`, fresh for every socket. It then opens `GET /api/sessions/{short}/pty?cols=&rows=` as a WebSocket, sized by the terminal's own fit, and sends `{"type":"auth","token":…}` as the first message.
2. The bridge (`internal/server/pty.go`) checks the token, then attaches to the daemon (with a 10 s limit to open) and answers `{"type":"ready","writable":…}`. From then on:
   - screen bytes and keystrokes travel as binary frames;
   - the page sends `{"type":"resize","cols","rows"}` as a text frame.

   `writable` is false when the panel has no control key. The terminal then shows the session but cannot type.
3. The terminal socket uses the daemon's **short** id. The digest uses the **full** session id, because the transcript file is named after it. Mixing the two produced "transcript not found" on every session (measured).
4. How a stream ends is carried in the close code:

   | Code | Meaning |
   | --- | --- |
   | 4000 | the stream ended and the daemon no longer lists the session |
   | 4001 | kicked; the reason is the daemon's own words (Windows only, see section 3) |
   | 4002 | the stream ended while the session is still listed |
   | 4003 | the stream ended and the daemon could not be asked why |
   | 4401 | the daemon refused the control key |
   | 4403 | the socket did not present the panel's terminal token |
   | 4404 | no such session |
   | 4503 | no daemon to attach through |

5. **Decided:** the orchestrator column reconnects by itself after 1, 2, 5 and then every 10 s. 4000, 4001 and 4404 are final. 4403 is retried: the token lives exactly as long as the panel's process, so the one moment a page presents a stale token is a panel restart between reading the token and opening the socket, which is the very case reconnecting is for. The session panel does not reconnect on its own; opening the session again is its reconnect.

## 2. The session's size belongs to everyone

- **Measured:** an attach's `cols`/`rows` set the size of the session's real PTY, so every other attacher sees it too, including the operator's own `claude attach` in Terminal.app. The last attacher wins. When an attacher leaves, the daemon gives the session the size of the attacher that attached last among those still attached — not of the last resize (CLI 2.1.269). The current size is reported nowhere.
- **Measured: a session wider than the terminal breaks its screen.** When the size changes, the daemon repaints every attacher for the new size. It clears the screen and places each word at an absolute column (`ESC[2J`, then `ESC[<col>G` per word, `CR ESC[1B` between lines). In an xterm narrower than that, every column past the edge is clamped to the last one and the lines run into each other. On a disposable session (CLI 2.1.269), a 76-column terminal with a 120 × 40 attacher joining showed 39 rows of 40 wrong. After the terminal's own resize, the screen matched a fresh attach at its size exactly. Clearing or refreshing xterm cannot help, because the bytes are already laid out for the other width; only a repaint for this terminal's size can.
- **Decided:** the terminal takes the size back. When the stream places the cursor at an absolute column past the terminal's last column, or moves it down past its last row, the terminal sends a size after 150 ms, and then at most once a second while that keeps happening (`web/js/liveterminal.js`, `followSize` and `takeSizeBack`). The positions are watched by hooks in xterm's parser as it reads the stream, which also reads a sequence cut between two pieces as one. xterm reads a moment after the stream arrives, so a screen that arrived before the size was last sent, or before a reconnect, or after the stream closed, does not count: the repaint for that size is already behind it. Nothing else counts: a column equal to the last, an absolute row past the bottom, a move right, wide characters at the edge, or a column past 500 or a move down past 200 (an application finding its corner). The alternate screen is not tracked, so switching to it is no reason by itself. A terminal that cannot be measured takes nothing back and takes the size back once it can; a pane still moving is taken back from once it settles.
- **Decided: what a take-back sends.** Not the terminal's whole size. In the dimension the session outgrew, the terminal's own; in the other, the smaller of its own and the session's. Sending its whole size made the session bigger where another attacher is smaller, and the two took turns for ever: a 76 × 60 terminal beside a 120 × 40 one moved the session between 76 × 60 and 120 × 40 every round. The session's size in the other dimension is read from what it drew:
  - **Columns:** the widest drawn row on the screen.
  - **Rows:** the lowest row the stream placed the cursor on (CUP, HVP and VPA counted from 1, CUD and CUU, the lowest kept) since the screen was cleared or a size was sent. The screen's last drawn row is not used: a screen wider than the terminal wraps and reaches rows the session does not have. A row past 200, a move by more, and a row below the terminal's own last row are not read. The estimate rests on the commands the daemon moves the cursor with: in every stream measured, CUP, CUD and CR, and no line feed, cursor save and restore, CNL or CPL, reverse index or scroll region, which the estimate does not follow.
  - **Measured** by replaying recordings of a disposable session (CLI 2.1.269) through xterm, with a 76 × 40 terminal beside 120 × 40, 76 × 60, 60 × 60, 120 × 24 and 80 × 24 attachers, and during four minutes of streamed output. The widest row gave the session's exact columns on every complete screen, and the stream's rows its exact rows. The screen's last drawn row gave 40 and 29 for 24-row sessions. No stream contained a line feed.
- **Why it settles.** A take-back never makes the session bigger in either dimension and makes it smaller in the one it outgrew. That holds as long as what the stream draws stays within the session's size, which the daemon's own repaints do. So take-backs shrink the session, which cannot go on for ever: between sizes set by anything else they stop, and they stop exactly when the session fits every terminal that does this. Two terminals of any shapes settle on the smaller of each dimension, in one take-back when they differ in one dimension or both.
- **Limitation:** both estimates are only as good as what the screen draws. Claude Code draws rules across its whole width and its status on the last row, so they are exact. A screen that draws neither gives less than the session's size, and the session stays smaller than it needs to be until a size is set again: a pane of the terminal that changes size, another attacher attaching, resizing or leaving, or a reconnect. A take-back never makes it bigger again. With no estimate at all, the terminal's own size stands in.
- **Decided: pauses.** Three take-backs within ten seconds pause the take-backs for 10 seconds, the next time for 30, and from then on for 60. A session made bigger during a pause is taken back once when the pause is over. A pane that changes size, comes back into view, or a stream that reconnects starts over at the first pause, and so do two minutes without a take-back. Whatever makes the session bigger again that often is not something taking it back fixes, so this bounds any loop nobody foresaw (21 resizes in five minutes against something that makes the session bigger every second). A stop for good was tried first and was wrong: beside a passive viewer, two short visits of a tool used up the three take-backs, and the orchestrator's column, which seldom changes size, stayed broken. An attacher that stays longer than the wait is taken back from while it is attached, and again when it leaves if the daemon then gives the session a bigger attacher's size.
- **Decided: only whole screens.** A take-back that falls due while xterm is still reading what has arrived waits for it: half a screen gives half the session's rows. Columns are read from the screen the session drew (from `baseY`), not from what the viewport shows, which scrolled back is history and can be wider than the session is now. A full clear forgets whether the session was bigger, and the new screen says it again.
- **Measured: a smaller attacher that leaves leaves the session smaller.** The daemon records the size each attacher last asked for, and a take-back asks for the smaller size. When the smaller attacher leaves, the daemon gives the session the terminal's recorded size, which is that smaller size. On the disposable session a 44 × 39 page stayed at 44 × 24 after a 120 × 24 attacher left, and a 44 × 68 page stayed at 44 × 56 after a 59 × 35 page closed. The screen is drawn correctly, in fewer rows or columns than the pane has, until the pane changes size or the terminal reconnects. Not handled.
- **Measured: taller at the same width breaks the bottom.** The daemon reaches each next line with a cursor down (`ESC[1B`), and xterm stops the cursor at the last row, so every row past it is drawn over the last one. On the same disposable session, a 76 × 60 attacher beside a 76 × 40 terminal piled 20 rows onto the last one and hid the input line; the terminal's own resize repaired it. **Decided:** a cursor moved down past the last row counts too. Past the last row means from the row the stream placed the cursor on, not from xterm's own cursor: a screen wider than the terminal wraps, and its wrapping has already moved xterm's cursor down rows the session does not have. Counted from xterm's cursor, a 120 × 24 session looked taller than a 76 × 40 terminal. A move of more than 200 rows is an application finding the bottom and does not count, and neither does an absolute row past the bottom. Four minutes of streamed output at the terminal's own size moved the cursor past no edge.
- **The trade-off, named:** while this terminal is open, a viewer of the session bigger in some dimension gets this terminal's size there 150 ms after it attaches or resizes. That includes the operator's own `claude attach` in Terminal.app. A smaller screen draws correctly in a bigger terminal, so that viewer sees a smaller screen, not a broken one. `web/tests/terminalwidth.test.js` replays the daemon's way of drawing through xterm's own core and compares the result with a fresh attach.
- **Measured: which attach resizes.** An attach at a fixed 120 × 40 from a tool that types into sessions (send keys or text, cancel, a command, an answer to a dialog) reshapes the session while it is attached, and when it leaves the size goes to whoever attached last. A message delivered as a reply does not attach and resizes nothing.
- **Measured: how to see the real size.** Read it from the session's tty, not from the page. The frames are rendered separately for each attacher, so what one page draws proves nothing about the PTY behind it. Find the `claude` process whose working directory is the session's (`lsof -a -p <pid> -d cwd`), take its tty from `ps -o tty= -p <pid>`, and run `stty -f /dev/<tty> size`.
- **Decided:** the page tells the session its size at attach, after its pane has been still for 150 ms, and after a font step. It sends one resize per settle, never one per pointer move or per key.
- **The trap:** anything that changes the terminal's pane resizes the session for every viewer. That includes a status row that appears above the terminal, a header that wraps onto a second line, and a scrollbar that appears in the element the terminal sits in.
  - **Measured:** the column's reconnect row makes the pane shorter while reconnecting. The terminal therefore attaches with fewer rows and resizes again after `ready` (39 then 42 rows). This is known and not fixed.

## 3. Kicks happen only on Windows

- **Measured on macOS:** two attaches to one session held at the same time both stay open, and both receive bytes (12907 and 9137 bytes over the same window). Nobody is evicted.
  - The control: the same probe, run against a fake daemon that does kick, does catch the eviction, with `EKICKED: Session opened in another window` followed by a close. So "not evicted" is a result, not a probe that cannot see.
- **Read:** in CLI 2.1.263 the only call to `kick()` is `if(P()==="windows")for(let D of r.attachers.values())D.kick();`.
- **Decided:** kick detection stays in the code. A Windows daemon kicks, and the rule is the daemon's to change. A kick is detected only when the marker is the last thing sent before a close (protocol section 8).
- **Read:** a reader that falls more than 1 MiB behind is closed with no marker at all. To the client this looks exactly like the session exiting.

## 4. What Claude Code does inside a terminal (2.1.263, measured)

- It turns on mouse tracking (`?1000h ?1002h ?1003h ?1006h`), focus reports (`?1004h`) and bracketed paste. Because of the focus reports, the page sends `\x1b[I` and `\x1b[O` whenever the terminal gains or loses the focus. They show up in every capture of the key frames, and they are not keys a person typed. Right after an attach the page also sends `\x1b[?1;2c`: that is xterm's answer to the session's device-attributes query.
- Its history is **not** in xterm's scrollback. The buffer holds about 70 lines for a 58-row screen. Scrolling back means sending wheel reports to Claude Code, which moves its own view one line per report and speeds up dense bursts itself.
- It hard-wraps to the terminal's width, so every change of size is a full redraw.
- Escape interrupts a turn and prints `⎿ Interrupted · What should Claude do instead?`. On an idle session with an empty input it does nothing visible.
- Its search is Ctrl+O, then `/`, the query, Enter, then `n`/`N`, and Escape to leave. The handler is not in the public key-binding table. `/` outside Ctrl+O opens the slash-command menu instead. It is fragile, so **decided:** do not intercept Cmd+F for it.

## 5. xterm.js 6.0.0

- **Wheel (read and measured).** While the application tracks the mouse, xterm sends at most **one** report per wheel event, however far the event asks to move. It also damps pixel steps under 50 px to 0.3 of their size. A wheel notch moved one line where a page moves seven. `followWheel` counts the event's distance in lines, keeps the remainder for the next event, and replays one single-line `WheelEvent` per whole line. Measured after the fix: one 100 px step sends 7 reports at 12 px type (14 px cells) and 6 at 14 px type (16 px cells).
- **Selection:** with mouse tracking on, a plain drag is reported to the session. `macOptionClickForcesSelection` makes Option+drag select text.
- **Link providers:** `provideLinks(y)` counts `y` from 1, the line is `buffer.active.getLine(y - 1)`, and link ranges are 1-based and inclusive. A wide character takes a cell plus a zero-width cell, so skip width-0 cells when mapping text to columns.
- **Font size:** setting `options.fontSize` re-measures the cell at once. A fit immediately after it proposes the new column count, in WebKit and in Chrome alike (measured). Until the refit, the terminal is drawn at the old column count with the bigger cells, so it is larger than its element (see section 6).
- **Fit addon:** it measures the terminal's parent element. A padding on that element counts as room the terminal does not have, which is why `.o-term` has none. `proposeDimensions()` returns nothing when the element has no layout, for example in a folded column, and `fit()` then does nothing silently.
- **Keys:** xterm sends nothing to the session for Cmd combinations. A custom key handler that returns `false` keeps a key from becoming input; `preventDefault` is still needed to keep the page from acting on it. xterm's own keydown handler runs before any bubble-phase document listener, so to see a key before xterm does, listen in the capture phase. `card.js` does this for Escape.
- **Content Security Policy:** xterm writes `<style>` elements at run time, so `style-src` needs `'unsafe-inline'` (see the README).

## 6. Layout above and around a terminal

Every rule here exists because the session's size is shared (section 2). Two mechanisms explain most of them.

**Why a row next to the terminal resizes the session, and a note over it does not.**

1. A `ResizeObserver` watches the element the terminal is drawn into.
2. A row that appears or disappears in the same flex column (an error line, a notice, a header that grows) takes height from that element or gives it back.
3. The observer fires, the terminal refits after 150 ms of quiet, and the new row count goes to the daemon as a `resize`.
4. The daemon applies it to the session's real PTY, for every attacher.
5. A row that shows for a moment and then goes away therefore costs two resizes of someone else's terminal, and Claude Code redraws its whole screen after each one.

An element with `position: absolute` is out of the flow. It covers the terminal without taking any of the element's room, so the observer never fires. That is why the font-size note is drawn over the terminal and not as a row. A row that is there for good — the column's strip of controls, the panel's header — is harmless if it exists *before* the terminal is fitted and never changes height afterwards.

**Why the terminal's element must have `overflow: hidden`.**

1. Right after a change of font (or of anything else that changes the cell size), and until the refit 150 ms later, xterm is drawn at the old column and row counts with the new cells. It is larger than its element.
2. If the element lets that spill out, it spills into the nearest ancestor that scrolls. The screen tab's body scrolled then, because the digest tab, since removed, needed it to.
3. That ancestor grows scrollbars, and the scrollbars take room from the terminal's element.
4. The refit measures the element with the scrollbars in it: fewer columns and rows than the real room.
5. The terminal shrinks to that size, the overflow is gone, the scrollbars go away, the element grows back, the observer fires, and the terminal refits a second time.

**Measured** on the screen tab before the fix: one font step sent `81 × 44` and then `83 × 45`, and the size note showed the first, wrong size. With `overflow: hidden` the spill stays inside the element, nothing scrolls, and there is one resize (`83 × 45`). The orchestrator column's `.o-term` always had it, which is why the column never showed the problem.

The remaining rules:

- Do not let a row above a terminal wrap. A flex line breaks on the full width of its items, including an item that is allowed to shrink. The session's name arrives after the terminal has attached, so a header that wraps because of it grows after the fit. To handle a narrow header, give it `container-type: inline-size` and drop what does not fit with a container query: its width then changes only when the pane's width changes, and that resizes the session anyway.
- A label whose text changes width moves the buttons beside it out from under the pointer. Give it a content-box `min-width` and tabular numbers.
- **Measured:** a browser does not deliver `mousedown` to a disabled button. A `mousedown` that nobody cancels moves the focus to the page, out of the terminal. To keep the focus where it is, cancel `mousedown` on the group around the buttons, and give disabled buttons `pointer-events: none` so the press reaches that group.
- The card panel closes on a click outside it, so a click into a terminal closes an open card. A card opened from a wiki link *in* the terminal leaves the focus in the terminal, so the next Escape would have interrupted the session. Escape now closes an open card first.

## 7. The window (WKWebView through webview_go)

- **Read:** webview_go's UI delegate implements only the file-open panel. `window.open` does nothing, so links must open inside the page.
- The window's menu (`cmd/fleetdeck-window/menu_darwin.c`) is the only route by which Cmd+X/C/V/A/Z/Q/R reach the web view. Without a menu item for a key, AppKit has nothing to route that key equivalent through.
- **Measured:** WKWebView has no page zoom of its own, and the menu has no item on `=`, `-`, `0` or `+`. Cmd+=, Cmd+Shift+=, Cmd+- and Cmd+0 therefore reach the page as keydown events with `metaKey`, and `preventDefault` holds. `pageZoom` stayed 1 in every case.
- **Read, not measured:** webview_go builds its configuration with `[WKWebViewConfiguration new]`, whose website data store is the persistent default one, so `localStorage` (the remembered column width, fold and font sizes) should survive a relaunch of the app. Nobody has relaunched the app to check.
- **Measured:** a script injected at document start runs before `<body>` exists. A window script that passed in Chrome on a page that had already loaded failed in the web view for that reason. A stand must reproduce the moment the script actually runs in, not just the page it runs against.
- **How to probe WebKit without showing anything on the operator's screen.** Write a small Objective-C program:
  - `NSApplicationActivationPolicyProhibited`, so there is no Dock icon, no menu bar, and the app can never become active;
  - an `NSWindow` subclass that is never ordered front and overrides `noResponderFor:`, to record the event instead of beeping;
  - `menu_darwin.c` compiled in unchanged, and a `WKWebView` created with webview_go's preferences;
  - key events routed the way AppKit routes key equivalents: `[window performKeyEquivalent:]`, then `[[NSApp mainMenu] performKeyEquivalent:]`, then `[window sendEvent:]`;
  - results read back with `evaluateJavaScript`;
  - a control: a plain `a` must arrive in xterm as input.

  One thing such a probe cannot see: when the page declines a key, WebKit re-sends it to `NSApp`, and a probe whose window is never key sees nothing of that path.
- **Decided: on macOS 26 the window holds three web views** (`docs/engineering/window-and-panel.md`, "The glass frame"): the board from webview_go, and the orchestrator and sessions surfaces the window makes itself. The surfaces share the board's process pool and data store, so the font sizes and the theme in `localStorage` are one. They are given the preferences webview_go gives the board (`javaScriptCanAccessClipboard`, `DOMPasteAllowed`, `fullScreenEnabled`); without them paste into the orchestrator's terminal fails.
- **Decided: each web view has its own terminal socket.** The orchestrator's terminal lives in its surface; a session opened from the sessions surface opens as a sheet in the board, with the board's socket. The pinned orchestrator session is never opened as a sheet as well: opening it focuses its panel. Two attaches to one session would fight over its size (section 2).
- **Read, not measured:** a key the page declines in a surface goes the same way as in the board, through that surface's web view to `NSApp` and the menu. Nobody has pressed a key in a surface yet.

## 8. The panel under the window

- The app starts the panel from inside its bundle, and the panel exits when the app's process ends, within 5 s. A panel that cannot take its port names what holds it: another fleetdeck panel (by commit and path), something else, or something that never answered.
- The header shows the commit the running panel was built from, with `*` for a tree that had uncommitted changes. When the interface served under a page changes, the page reloads itself.
- **Measured:** a launch agent written by an older `init` (`dev.fleetdeck.panel.plist`, `KeepAlive` true) can remain in `~/Library/LaunchAgents` after an upgrade, not loaded but not disabled either. At the next login it starts a second panel on the same port, and if the window's panel took the port first, the agent restarts every ten seconds against it. `init` prints the two commands that remove it. Any script of your own that bootstraps that agent brings it back.
- `make install` puts `fleetdeck` and `fleetdeck-status` into `~/.local/bin`. Terminal and window run the same build only if both were rebuilt.

## 9. Measuring: the stand

- A stand is this tree's panel on its own port and config, plus a headless Chrome with its own profile, driven over the DevTools Protocol. Headless Chrome has no browser zoom at all.
  - `Input.dispatchKeyEvent` with `modifiers` as a bitmask (Alt 1, Ctrl 2, Meta 4, Shift 8).
  - `Input.dispatchMouseEvent` for wheel, press, move and release, with modifiers for Option-drag.
  - `Page.addScriptToEvaluateOnNewDocument` to wrap `WebSocket.prototype.send` and record every resize and key frame the page sends.
- **Measured (2026-09-12, Chrome 153.0.8010.37):** the Chrome extension reaches loopback. A throwaway server on `127.0.0.1:7791` was navigated, screenshotted and read through the extension, and that server's own log holds the browser's `GET /` and its favicon request — it really fetched the page. This section said the opposite until now, which is a reason to reach for headless Chrome that does not exist. The reasons that do exist: a profile of its own, no browser zoom, and `--lang`, the only way to fix the language the page reads from `navigator.language` (`web/js/i18n.js`).
- **A stand is not isolated from the real daemon.** It lists the operator's sessions, and opening any of them resizes that session for the operator (#107 adds a way for a stand to refuse the real daemon). When a real session is needed, create a disposable one, pin it in the stand's config, open nothing else, and delete it afterwards.
- Prove that the condition happened before reading the result: for example, that the focus really is in the terminal before pressing Escape. And give every measurement a control that shows the instrument can see what it is looking for.
- Anything visible on the operator's screen (a window, a screenshot of their screen) needs their go-ahead first.

## 10. Tests and mutations in this repository

- Web tests are plain `node --test` with top-level `test()` calls only. `scripts/run-web-tests.sh` compares the test names each file declares with the names node reports as run, so **a test name must be unique across every web test file**.
- The fake DOM (`web/tests/fake-dom.js`):
  - supports single-class, tag and attribute selectors, and no descendant selectors;
  - bubbles events and honours `stopPropagation`;
  - fires `click` on disabled buttons, unlike a browser;
  - has no layout at all. Anything about sizes is answered by a stand, not by these tests.
- `web/tests/terminal-fakes.js` has the terminal, fit addon, observer, socket and clock doubles. `installFit(pane)` takes `"own"`, `null`, fixed dimensions, or a function of the terminal, which is how a test makes columns depend on the font.
- AppKit refuses menu and window work off the main thread, so `cmd/fleetdeck-window` tests do all their native calls from `TestMain`.
- For mutation testing, apply one edit at a time, run the whole web suite, and restore the file. Report two numbers: mutants killed out of mutants run, and new tests that killed none. Tell a mutant that stops a file from loading apart from one that survived: a whole-file failure hides every test in the file.
- A pull request in conflict gets **no** CI checks at all ("no checks reported"). Merge master into the branch; force-pushing is not allowed here.

## 11. Where the documentation used to say otherwise

- The `attach` section of the protocol document said a held attach closes "when another attacher takes over (a kick)", and described the kick as the daemon's general behaviour. On macOS that is false (section 3). It is corrected in the same change that added this page.
