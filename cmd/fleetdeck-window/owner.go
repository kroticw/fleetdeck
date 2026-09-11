//go:build darwin

package main

import (
	"fmt"
	"html"
	"path/filepath"
	"strconv"
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
func panelArgs(window int) []string {
	return []string{"--owner-pid", strconv.Itoa(window)}
}

// How long a panel the window started has to answer: the worst of ten
// measured starts, three times over.
//
// Measured on 2026-09-11 on the operator's kind of machine, with the fleet
// running: ten separately built panels -- ten different binaries, each run
// for the first time, as after every update -- started against a stand
// configuration and polled the way supervisor.WaitAnswer polls (at once, then
// every 100 ms). Every one answered at the second poll, 107-110 ms: the panel
// itself is up in well under 100 ms. Not measured: a start straight after
// login, with nothing of the binary in the disk cache. A panel slower than
// this is not waited on in silence -- the window says it did not start and
// offers to start it again.
const (
	measuredWorstStart = 110 * time.Millisecond
	startMargin        = 3
	panelStartTimeout  = measuredWorstStart * startMargin
)

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
type screen struct {
	url     string
	logPath string
	// showingPanel: the web view has the panel's own page, as opposed to one
	// of the window's pages below.
	showingPanel bool
	// takingOver: this window was started by an update to take the panel over
	// from the previous version, rather than because nothing answered.
	takingOver bool
}

// on returns what to do for e: navigate to the panel, or show html, or --
// both zero -- nothing.
func (s *screen) on(e supervisor.Event) (navigate bool, page string) {
	switch e.State {
	case supervisor.Answering:
		if s.showingPanel {
			return false, ""
		}
		s.showingPanel = true
		return true, ""
	case supervisor.Starting:
		if s.showingPanel {
			// A restart after a crash: the panel's own page already says it is
			// offline and reconnects by itself, keeping what the person had
			// open. Covering it with a page of the window's would lose that.
			return false, ""
		}
		return false, startingPage(s.url, s.takingOver)
	case supervisor.Replacing:
		// Covers the panel's page too: that page belongs to the panel being
		// stopped, and it would only go offline under the person.
		s.showingPanel = false
		return false, replacingPage(e.Detail)
	case supervisor.Failed:
		s.showingPanel = false
		return false, failedPage(s.url, e, s.logPath)
	}
	return false, ""
}

// blankPage is the window's ground before the keeper has said anything: the
// panel's own background, so a panel that answers at once opens without a
// white flash in between.
const blankPage = `<!doctype html><html><head><meta charset="utf-8"><title>fleetdeck</title></head><body style="margin:0;background:#14161a"></body></html>`

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

// replacingPage is shown while a panel an earlier window left behind is being
// stopped, for the window to start its own. whose says why it is replaced.
func replacingPage(whose string) string {
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
  <p>На месте панели отвечала панель, оставленная прежним окном: %s. Окно останавливает её и запускает свою.</p>
</main>
</body>
</html>`, pageStyle, html.EscapeString(whose))
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
