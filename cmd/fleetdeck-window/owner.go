//go:build darwin

package main

import (
	"encoding/json"
	"fmt"
	"html"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kroticw/fleetdeck/internal/supervisor"
)

// panelBinaryName is the panel the window starts: in the app bundle it sits
// beside the window in Contents/MacOS, and after a plain `make build` both
// are in bin/ -- beside each other either way.
const panelBinaryName = "fleetdeck"

func panelBinary(windowExecutable string) string {
	return filepath.Join(filepath.Dir(windowExecutable), panelBinaryName)
}

// panelArgs tells the panel which window it belongs to: the panel refuses to
// start unless that window is its parent, and goes when it is gone
// (cmd/fleetdeck, owner.go).
//
// On a stand it also hands on the stand's socket (standIsolation): a panel
// finds the fleet daemon by uid, not by HOME, and -stand-socket is the only
// thing that keeps it off the real one. The keeper starts every panel with
// these arguments -- the restart after an update's swap too -- so none is
// started without it.
//
// It hands on the port too, the port of the URL the window looks at
// (panelPort), so the panel listens where the window looks rather than on
// server.port; and a dev app's copy of the operator's config (devapp.go).
func panelArgs(window int, standSocket string, port int, configPath string) []string {
	args := []string{"--owner-pid", strconv.Itoa(window)}
	if port != 0 {
		args = append(args, "--port", strconv.Itoa(port))
	}
	if standSocket != "" {
		args = append(args, "--stand-socket", standSocket)
	}
	if configPath != "" {
		args = append(args, "--config", configPath)
	}
	return args
}

// standSocketEnv names a stand's daemon socket for a window run on a stand.
// An environment variable rather than a flag, because a window is also started
// by another window -- an update's new window, by the one it replaces, which
// may be a version that knows nothing of stands -- and the environment is what
// passes through every such start untouched.
const standSocketEnv = "FLEETDECK_STAND_SOCKET"

// standIsolation is the stand socket this window hands its panels, "" when it
// is not on a stand, and an error when it is on one that names no socket: a
// variable templated from nothing must not become a panel that finds the real
// daemon, as an empty -stand-socket is refused by the panel itself
// (cmd/fleetdeck, checkStandSocket).
func standIsolation(lookup func(string) (string, bool)) (string, error) {
	socket, set := lookup(standSocketEnv)
	if !set {
		return "", nil
	}
	if strings.TrimSpace(socket) == "" {
		return "", fmt.Errorf("%s is set but empty: this window is on a stand that names no daemon socket, and a panel started without one would find the real fleet daemon; refusing to start", standSocketEnv)
	}
	return socket, nil
}

// How long a panel the window started has to answer. The window shows its
// starting page for as long as it waits, and a panel that answers sooner is not
// kept waiting, so a long ceiling costs a fast start nothing; a short one fails
// a slow start that would have answered.
//
// Measured on 2026-09-11 on the operator's kind of machine, with the fleet
// running: ten separately built panels, each run for the first time, answered
// at the second poll, 107-110 ms. The deadline was that three times over,
// 330 ms. On 2026-09-14 on GitHub's macos-26 runner the window's panel did not
// answer within 330 ms three runs of three, each right after the window's
// tests, and logged nothing; how long it would have taken is not known, the
// keeper stopped it. Measured there since, the keeper logging the time to
// answer (runs 34852671968 and 34854756877): 104-110 ms before the tests, 254
// ms at the moment they ended, 103-106 ms after, and 102-107 ms while they ran;
// a panel started by a script with no window, on fresh runners, 32-104 ms.
// The load under which 330 ms was missed was not caught again, so the ceiling
// is not three times a worst case: it is a choice, 3 s, well past every start
// measured and still short enough that a panel which will not start is said
// so soon. Not measured: a start straight after login with nothing in the
// disk cache.
const panelStartTimeout = 3 * time.Second

// launchdThrottle is what launchd's ThrottleInterval defaults to: the launch
// agent the window replaces did not start the panel again sooner than this
// after it last started, and the keeper does not either -- a panel that dies
// within it is reported, not restarted.
const launchdThrottle = 10 * time.Second

// takenPanelPoll is how often a panel the window did not start -- one from a
// launch agent, a terminal, another window still running -- is checked for
// being gone.
const takenPanelPoll = 2 * time.Second

// waitShownAfter: any wait longer than this is shown to the person, with the
// time it has taken so far.
const waitShownAfter = 2 * time.Second

// startBindingName is the function the failure page calls to have the window
// start the panel again.
const startBindingName = "fleetdeckStartPanel"

// screen is what the window shows for each thing the keeper reports. It is
// driven from the UI thread only.
//
// A navigation is a request, not a page. Until 2026-09-14 the screen took the
// panel's first answer, and the navigation it asked for, as the panel's page
// being shown, and let every later answer go by. An update restarts its panel
// a moment after that first answer, so the page was cut off while it loaded:
// one window was left white -- the document had come, its styles and scripts
// had not -- and the next stayed on its "Принимаю пульт" page, the navigation
// refused before anything arrived. webview_go sets no navigation delegate, so
// nothing said so. Now the page says it has loaded (pageLoadScript), and until
// it has, the window asks for it again: when a panel answers again, when WebKit
// says the navigation failed, and when its wait goes by (navscreen.go).
type screen struct {
	url     string
	logPath string
	// The board's page asked for and shown, by WebKit's word and the page's
	// (navscreen.go).
	pageWatch
	// answering: a panel answers, as the keeper last reported.
	answering bool
	// startingUp: the starting page is on screen.
	startingUp bool
	// takingOver: this window was started by an update to take the panel over
	// from the previous version, rather than because nothing answered. Until
	// handed, the handover is still restarting the panel, and its page is not
	// asked for.
	takingOver bool
	handed     bool
	now        func() time.Time
}

// What the panel's page says of itself, through pageLoadedBindingName.
const (
	// pagePanel: it loaded, and its styles and its scripts came with it.
	pagePanel = "panel"
	// pageBroken: it loaded without its styles or without its scripts -- the
	// white window.
	pageBroken = "broken"
	// pageLeaving: it is going away, to be replaced by another load.
	pageLeaving = "leaving"
	// pageLoading: its document has begun, and its styles and scripts are on
	// their way.
	pageLoading = "loading"
)

// How long a document WebKit has committed has, from the commit, to say it is
// the panel before it is asked for again: the slowest seen, three times over.
//
// It was 840 ms: one load of 280 ms on an update stand, timed from the ask
// before the window could tell the ask from the commit. On GitHub's macos-15
// runner, in PR #166's window-on-oldest-macos run of 2026-09-14, the board's
// first document said it was the panel 952 ms after its commit, and the wait
// cut it off. Every window log the runner kept that day -- PR #166, T-058's
// three runs, T-059's two diagnostic runs -- timed from commit to panel: 28
// documents nothing cut off took 34-740 ms, and the one that was cut off 952 ms.
// A committed document that fails, or loses its web content process, is not
// waited on (navscreen.go); this bound is for one that goes quiet.
const (
	measuredWorstCommitToPanel = 952 * time.Millisecond
	pageLoadingWait            = measuredWorstCommitToPanel * pageLoadMargin
)

const (
	pageLoadedBindingName = "fleetdeckPageLoaded"
	// pageRunningMarker is the data attribute web/js/main.js sets on the
	// document once it and every module it imports have arrived. The name is
	// a contract across two languages; pageload_test.go holds both sides.
	pageRunningMarker = "fleetdeckPage"
)

// How many times in a row the panel's page is asked for before the window
// says it did not load, the margin every measured wait is given, and how often
// the window looks at a page asked for. How long each wait is follows from
// what WebKit and the page have said (navscreen.go, waitFor).
//
// There was once a short wait here, pageLoadWait: the worst of twenty page
// loads measured on a stand, 177 ms, three times over. It asked again every
// 531 ms while the page said nothing, and on the operator's update of
// 2026-09-14 that was every ask until the web content process, paused for
// 1.38 s, answered the third; see navscreen.go.
const (
	pageLoadMargin = 3
	pageLoadTries  = 3
	// pageLoadTick is how often the window looks at a page asked for: often
	// enough that no wait is stretched by much.
	pageLoadTick = 100 * time.Millisecond
)

// on returns what to do for e: navigate to the panel, or show html, or --
// both zero -- nothing.
func (s *screen) on(e supervisor.Event) (navigate bool, page string) {
	switch e.State {
	case supervisor.Answering:
		s.answering = true
		if s.showingPanel || (s.takingOver && !s.handed) {
			return false, ""
		}
		s.tries = 0
		return s.ask(), ""
	case supervisor.Starting:
		s.answering = false
		if s.showingPanel {
			// A restart after a crash: the panel's own page already says it is
			// offline and reconnects by itself, keeping what the person had
			// open. Covering it with a page of the window's would lose that.
			return false, ""
		}
		if s.startingUp {
			// Already up, counting: the handover's second start, or a panel
			// that died before it answered.
			return false, ""
		}
		s.asked, s.startingUp = false, true
		return false, startingPage(s.url, s.takingOver)
	case supervisor.Replacing:
		// Covers the panel's page too: that page belongs to the panel being
		// stopped, and it would only go offline under the person.
		s.answering = false
		s.cover()
		return false, replacingPage(e.Detail, e.Asked)
	case supervisor.Failed:
		s.answering = false
		s.cover()
		return false, failedPage(s.url, e, s.logPath)
	}
	return false, ""
}

// ask is a navigation to the panel, counted. It has not begun until the page
// says so.
func (s *screen) ask() bool {
	s.startingUp = false
	s.pageWatch.ask(s.time())
	return true
}

// time is now, by the screen's clock.
func (s *screen) time() time.Time {
	if s.now == nil {
		return time.Now()
	}
	return s.now()
}

// cover is one of the window's own pages going up.
func (s *screen) cover() {
	s.startingUp = false
	s.pageWatch.cover()
}

// reopen is a navigation to the panel asked for by a person or by the page
// itself, rather than by the keeper: counted like the keeper's.
func (s *screen) reopen() {
	s.tries = 0
	s.ask()
}

// reload is a person's reload of the board -- Cmd+R, Reload in the menu. It
// says whether to navigate: the panel's page is asked for again, as a first
// try, only where the screen would ask for it itself -- a panel answering and
// no handover still restarting it. Over the window's own pages it does
// nothing: the starting page is replaced when the panel answers, the handover
// page when the handover is done, and the keeper's failure page has its own
// button to start the panel, which a reload should not become a second way to
// press. The page saying the panel's page did not load is shown with a panel
// answering, and a reload asks again there, as its button does.
func (s *screen) reload() bool {
	if !s.answering || (s.takingOver && !s.handed) {
		return false
	}
	s.reopen()
	return true
}

// pageSays takes the panel's page's word about itself, and the address it
// said it from.
func (s *screen) pageSays(state, href string) {
	s.pageWatch.pageSays(state, href, s.time())
}

// target is the address the window asks for: where the panel's page last
// said it was, and the window's own URL until it has said.
func (s *screen) target() string {
	if s.href != "" {
		return s.href
	}
	return s.url
}

// handedOver is the handover this window was started for being done. It says
// whether to navigate: the panel's page is asked for now if a panel answers,
// and otherwise when one does.
func (s *screen) handedOver() bool {
	s.handed = true
	if !s.answering || s.showingPanel {
		return false
	}
	s.tries = 0
	return s.ask()
}

// tick is time going by: a page asked for and not loaded is asked for again,
// pageLoadTries times in all, and then said not to have loaded. How long it is
// given follows from what WebKit and the page have said of it (waitFor).
func (s *screen) tick() (navigate bool, page string) {
	return s.follow(s.due(s.time()))
}

// pageLoadScript is put into every page the window loads. On the panel's own
// page, and only there, it says whether the page loaded with its styles and
// its scripts, and when it goes away. The window's own pages carry it too,
// from another origin, and say nothing.
func pageLoadScript(panelURL string) string {
	origin := panelURL
	if u, err := url.Parse(panelURL); err == nil {
		origin = u.Scheme + "://" + u.Host
	}
	quoted, _ := json.Marshal(origin)
	return `(() => {
  if (location.origin !== ` + string(quoted) + `) return;
  // With the address it says it from: a page the person went to is asked for
  // again there, not at the window's own URL.
  const say = (state) => {
    if (typeof window.` + pageLoadedBindingName + ` === "function") window.` + pageLoadedBindingName + `(state, location.href);
  };
  // This script runs as the document starts: the navigation has reached it.
  say("` + pageLoading + `");
  window.addEventListener("load", () => {
    const links = Array.from(document.querySelectorAll('link[rel="stylesheet"]'));
    const styled = links.length > 0 && links.every((link) => {
      try {
        return link.sheet !== null && link.sheet.cssRules.length > 0;
      } catch {
        return false;
      }
    });
    const ran = document.documentElement.dataset.` + pageRunningMarker + ` === "running";
    say(styled && ran ? "` + pagePanel + `" : "` + pageBroken + `");
  });
  window.addEventListener("pagehide", () => say("` + pageLeaving + `"));
})();`
}

// blankPage is the window's ground before the keeper has said anything: the
// panel's own background, so a panel that answers at once opens without a
// white flash in between.
const blankPage = `<!doctype html><html><head><meta charset="utf-8"><title>fleetdeck</title></head><body style="margin:0;background:#14161a"></body></html>`

// pageHeading is what a page of the window's says first, as it is written in
// the page, for the log; empty for a page with no heading.
func pageHeading(page string) string {
	_, rest, ok := strings.Cut(page, "<h1>")
	if !ok {
		return ""
	}
	heading, _, _ := strings.Cut(rest, "</h1>")
	return heading
}

const pageStyle = `<style>
  html, body { height: 100%; margin: 0; }
  body {
    display: flex; align-items: center; justify-content: center;
    font-family: system-ui, -apple-system, sans-serif;
    background: #14161a; color: #e7e9ec;
  }
  main { max-width: 40rem; padding: 2rem; }
  h1 { font-size: 1.1rem; font-weight: 600; margin: 0 0 0.75rem; }
  p { color: #9aa1ac; font-size: 0.9rem; line-height: 1.5; margin: 0 0 0.75rem; }
  code { background: #21252c; padding: 0.1em 0.4em; border-radius: 4px; }
  pre {
    background: #21252c; color: #c9ced6; padding: 0.75rem; border-radius: 6px;
    font-size: 0.8rem; line-height: 1.4; overflow-x: auto; white-space: pre-wrap;
    max-height: 16rem; margin: 0 0 0.75rem;
  }
  button {
    font: inherit; font-size: 0.9rem; padding: 0.4rem 1rem; border-radius: 6px;
    border: 1px solid #3a404a; background: #2a2f37; color: #e7e9ec; cursor: pointer;
  }
  button:disabled { opacity: 0.6; cursor: default; }
</style>`

// startingPage is shown while a panel the window started has not answered
// yet. Usually for a fraction of a second; past waitShownAfter it shows how
// long it has been.
func startingPage(url string, takingOver bool) string {
	heading, text := "Запускаю панель", "На <code>"+html.EscapeString(url)+"</code> никто не отвечал, и окно запустило панель само. Обычно это доли секунды."
	if takingOver {
		heading, text = "Принимаю пульт", "Это новая версия приложения: она останавливает пульт прежней и запускает свой на <code>"+html.EscapeString(url)+"</code>."
	}
	return fmt.Sprintf(`<!doctype html>
<html>
<head>
<meta charset="utf-8">
<title>fleetdeck</title>
%s
</head>
<body>
<main>
  <h1>%s</h1>
  <p>%s</p>
  <p id="elapsed" hidden>Прошло <span id="seconds"></span> с.</p>
</main>
<script>
  const t0 = Date.now();
  const line = document.getElementById("elapsed");
  const seconds = document.getElementById("seconds");
  setInterval(() => {
    const ms = Date.now() - t0;
    if (ms >= %d) {
      seconds.textContent = Math.floor(ms / 1000);
      line.hidden = false;
    }
  }, 250);
</script>
</body>
</html>`, pageStyle, heading, text, waitShownAfter.Milliseconds())
}

// pageFailedPage says the panel answers but its page at pageURL never loaded,
// after the last wait, and offers to ask for it again.
func pageFailedPage(pageURL string, wait time.Duration) string {
	return fmt.Sprintf(`<!doctype html>
<html>
<head>
<meta charset="utf-8">
<title>fleetdeck</title>
%s
</head>
<body>
<main>
  <h1>Страница панели не загрузилась</h1>
  <p>Панель отвечает, но её страница <code>%s</code> так и не загрузилась целиком: окно ждало её %s после последней попытки.</p>
  <button id="again">Открыть снова</button>
</main>
<script>
  const again = document.getElementById("again");
  again.addEventListener("click", () => {
    again.disabled = true;
    again.textContent = "Открываю…";
    window.%s();
  });
</script>
</body>
</html>`, pageStyle, html.EscapeString(pageURL), wait, reloadBindingName)
}

// replacingPage is shown while a panel is being stopped for the window to
// start its own: one an earlier window left behind, whose says why; or, asked,
// one a person chose to replace from the notice about a panel of another
// build (foreign.go).
func replacingPage(whose string, asked bool) string {
	text := "На месте панели отвечала панель, оставленная прежним окном: " + html.EscapeString(whose) + ". Окно останавливает её и запускает свою."
	if asked {
		text = "Окно останавливает панель другой сборки, которую запустило не оно, и запускает свою."
	}
	return fmt.Sprintf(`<!doctype html>
<html>
<head>
<meta charset="utf-8">
<title>fleetdeck</title>
%s
</head>
<body>
<main>
  <h1>Заменяю панель</h1>
  <p>%s</p>
</main>
</body>
</html>`, pageStyle, text)
}

// failedPage says why the panel is not there, with what the panel itself last
// wrote, and offers to start it again.
func failedPage(url string, e supervisor.Event, logPath string) string {
	why := "неизвестно"
	if e.Err != nil {
		why = e.Err.Error()
	}
	tail := "(лог пуст)"
	if e.LogTail != "" {
		tail = e.LogTail
	}
	return fmt.Sprintf(`<!doctype html>
<html>
<head>
<meta charset="utf-8">
<title>fleetdeck</title>
%s
</head>
<body>
<main>
  <h1>Панель не запустилась</h1>
  <p>Окно пыталось запустить панель на <code>%s</code>: %s</p>
  <p>Последнее, что панель написала в лог:</p>
  <pre>%s</pre>
  <p>Весь лог: <code>%s</code></p>
  <button id="again">Запустить снова</button>
</main>
<script>
  const again = document.getElementById("again");
  again.addEventListener("click", () => {
    again.disabled = true;
    again.textContent = "Запускаю…";
    window.%s();
  });
</script>
</body>
</html>`, pageStyle, html.EscapeString(url), html.EscapeString(why), html.EscapeString(tail), html.EscapeString(logPath), startBindingName)
}
