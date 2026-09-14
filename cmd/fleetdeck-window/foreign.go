//go:build darwin

package main

// A panel the window did not start is used as it is (internal/supervisor's
// Keeper): one from a terminal is somebody's on purpose, one of another
// window still open is that window's. Until 2026-09-14 it was also used in
// silence. That day the operator installed v0.9.0 and the window opened on a
// panel built from source three days earlier, which a launch agent written
// by an earlier `fleetdeck init` kept on 127.0.0.1:7777. The header -- drawn
// by the panel's page, from the panel's build -- showed that panel's commit
// and its old Update button, nothing said the panel was not the app's build,
// and the operator went looking for a defect in a release that had none.
//
// So the window now compares the build a panel it did not start reports with
// its own, and when they differ says so over the panel's page: which build
// the panel is, which the app is, where the panel's binary is, and what keeps
// it there. The panel is still shown, and still used: a developer who started
// one from a terminal on purpose loses nothing but a strip of screen.
//
// The notice is drawn by the window, not by the page. The page is the other
// build's -- the operator's was from before any of this existed -- so nothing
// in it can be relied on to say anything. The window puts a script of its own
// into every page it loads (webview's Init, a user script the page's
// Content-Security-Policy does not apply to), and the script asks the window
// what to show.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"

	"github.com/kroticw/fleetdeck/internal/supervisor"
	"github.com/kroticw/fleetdeck/internal/version"
)

// launchAgentLabel is the label of the launch agent `fleetdeck init` wrote
// before 2026-09-11 (launchAgentLabel in cmd/fleetdeck, which still recognises
// such a file and prints how to remove it).
const launchAgentLabel = "dev.fleetdeck.panel"

// The names the notice script and the window share: a contract across two
// languages, checked by TestTheNoticeScriptCallsTheBindingsTheWindowBinds.
const (
	// noticeBindingName answers the page with the notice's markup, "" for none.
	noticeBindingName = "fleetdeckPanelNotice"
	// noticeShownBindingName is how the page tells the window what it painted:
	// the notice's text, or "" once it has taken a notice down. The window
	// writes it to its log -- the one record, short of a person looking, that
	// the notice reached the screen.
	noticeShownBindingName = "fleetdeckPanelNoticeShown"
	// replaceBindingName is what the notice's button calls.
	replaceBindingName = "fleetdeckReplacePanel"
	// noticeRepaintFunction is what the window calls when the notice changes
	// under a page already loaded.
	noticeRepaintFunction = "fleetdeckRepaintPanelNotice"
	noticeElementID       = "fleetdeck-window-notice"
)

// build is what a window knows of its own build.
type build struct {
	Version  string
	Revision string
	Modified bool
}

func ownBuild() build {
	info, _ := debug.ReadBuildInfo()
	return buildFrom(version.String(), info)
}

func buildFrom(version string, info *debug.BuildInfo) build {
	b := build{Version: version}
	if info == nil {
		return b
	}
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			b.Revision = s.Value
		case "vcs.modified":
			b.Modified = s.Value == "true"
		}
	}
	return b
}

// sameBuild says whether panel is the window's own build, as far as either
// can tell. The commit decides: a release and a build from a checkout of the
// same commit are the same code, whatever version each was stamped with. A
// window that knows no commit of its own -- built without version control
// information -- can compare release versions and nothing else, and says
// nothing about what it cannot tell.
func sameBuild(own build, panel supervisor.PanelBuild) bool {
	if own.Revision != "" {
		return own.Revision == panel.Revision && own.Modified == panel.Modified
	}
	if released(own.Version) && released(panel.Version) {
		return own.Version == panel.Version
	}
	return true
}

func released(version string) bool {
	return version != "" && version != "dev"
}

// buildLabel names a build the way the panel's header does
// (web/js/buildcheck.js, brandHTML): a release's version; "dev" and the short
// commit for a build from a checkout; the short commit alone for a panel from
// before the version was reported; a star for uncommitted edits.
func buildLabel(version, revision string, modified bool) string {
	short := revision
	if len(short) > 7 {
		short = short[:7]
	}
	label := short
	switch {
	case version == "dev" && short != "":
		label = "dev " + short
	case version == "dev":
		label = "dev"
	case version != "":
		label = version
	}
	if modified && label != "" {
		label += "*"
	}
	return label
}

// launchAgent is the launch agent an earlier `fleetdeck init` wrote.
type launchAgent struct {
	Path string
	// Program is the binary the agent starts, "" when the file could not be
	// read as a property list.
	Program string
	// Holds: Program is the panel answering. launchd starts it at login, and
	// again whenever it stops (KeepAlive).
	Holds bool
}

// findLaunchAgent is the agent in home, or nil when there is none, or when the
// file at its path is not one init wrote.
func findLaunchAgent(home, executable string) *launchAgent {
	path := filepath.Join(home, "Library", "LaunchAgents", launchAgentLabel+".plist")
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	a := &launchAgent{Path: path}
	var p struct {
		Label            string
		Program          string
		ProgramArguments []string
	}
	// plutil by absolute path: an app started from the Dock has only the
	// system's directories as PATH. It reads every plist format launchd does.
	out, err := exec.Command("/usr/bin/plutil", "-convert", "json", "-o", "-", path).Output()
	switch {
	case err == nil && json.Unmarshal(out, &p) == nil:
		if p.Label != launchAgentLabel {
			return nil
		}
		a.Program = p.Program
		if a.Program == "" && len(p.ProgramArguments) > 0 {
			a.Program = p.ProgramArguments[0]
		}
	case !bytes.Contains(raw, []byte(launchAgentLabel)):
		return nil
	}
	a.Holds = a.Program != "" && samePath(a.Program, executable)
	return a
}

func samePath(a, b string) bool {
	if a == b {
		return true
	}
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}

// notice is what the window says about the panel it shows.
type notice struct {
	URL    string
	Panel  string // the panel's build, as buildLabel names it
	Window string // this window's build
	// Executable is where the panel's binary is.
	Executable string
	// NotAPanel: what answers is not a fleetdeck panel at all.
	NotAPanel bool
	// OtherWindow is the PID of the window, still open, whose panel it is.
	OtherWindow int
	Agent       *launchAgent
	// CanReplace: the notice offers to replace the panel with the window's
	// own. Not for another window's panel, and not for one a launch agent
	// would start again at once.
	CanReplace bool
}

// noticeFor is what the window says about e, or nil for nothing: e is not a
// panel the window did not start, or it is one of the window's own build.
func noticeFor(own build, url string, e supervisor.Event, home string) *notice {
	if e.State != supervisor.Answering || e.Ours {
		return nil
	}
	n := &notice{URL: url, Window: buildLabel(own.Version, own.Revision, own.Modified)}
	if e.Holder == nil {
		n.NotAPanel = true
		return n
	}
	h := e.Holder
	if sameBuild(own, *h) {
		return nil
	}
	n.Panel = buildLabel(h.Version, h.Revision, h.Modified)
	n.Executable = h.Executable
	n.OtherWindow = h.Owner
	n.Agent = findLaunchAgent(home, h.Executable)
	n.CanReplace = n.OtherWindow == 0 && (n.Agent == nil || !n.Agent.Holds)
	return n
}

func (n *notice) String() string {
	if n == nil {
		return ""
	}
	if n.NotAPanel {
		return fmt.Sprintf("what answers at %s is not a fleetdeck panel; it is shown as it is", n.URL)
	}
	s := fmt.Sprintf("the panel at %s is not this app's build: panel %s (%s), app %s", n.URL, orUnknown(n.Panel), n.Executable, orUnknown(n.Window))
	switch {
	case n.OtherWindow != 0:
		s += fmt.Sprintf("; it is the panel of another window, pid %d, still open", n.OtherWindow)
	case n.Agent != nil && n.Agent.Holds:
		s += fmt.Sprintf("; launch agent %s (%s) keeps it running", launchAgentLabel, n.Agent.Path)
	}
	if n.Agent != nil && !n.Agent.Holds {
		s += fmt.Sprintf("; launch agent %s (%s) starts %s at login", launchAgentLabel, n.Agent.Path, orUnknown(n.Agent.Program))
	}
	return s
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

const (
	noticeBoxStyle  = "position:fixed;left:0;right:0;bottom:0;z-index:2147483647;max-height:45vh;overflow:auto;box-sizing:border-box;padding:10px 16px;background:#3b2f14;color:#f5ead0;border-top:1px solid #a07a2c;box-shadow:0 -4px 16px rgba(0,0,0,.35);font:13px/1.5 system-ui,-apple-system,sans-serif;text-align:left"
	noticeTextStyle = "margin:4px 0"
	noticeCodeStyle = "font-family:ui-monospace,Menlo,monospace;font-size:12px;background:rgba(0,0,0,.3);padding:0 .3em;border-radius:3px;user-select:text"
	noticePreStyle  = "margin:4px 0;padding:6px 8px;font-family:ui-monospace,Menlo,monospace;font-size:12px;background:rgba(0,0,0,.3);border-radius:4px;white-space:pre-wrap;user-select:text"
	noticeBtnStyle  = "font:inherit;margin-top:4px;padding:3px 12px;border-radius:5px;border:1px solid #a07a2c;background:#5a4518;color:#f5ead0;cursor:pointer"
	// The header is the panel's page, and names the panel's build. The page
	// may be of any age, so it is marked from outside it: by the markup every
	// header has carried since the build was first shown there.
	noticeHeaderStyle = `<style>#header .build-rev::before{content:"панель ";opacity:.75}</style>`
)

// noticeHTML is the notice as the page shows it, "" for none. Everything a
// panel says of itself is escaped: it is another program's word.
func noticeHTML(n *notice) string {
	if n == nil {
		return ""
	}
	code := func(s string) string {
		return `<code style="` + noticeCodeStyle + `">` + html.EscapeString(s) + `</code>`
	}
	para := func(s string) string { return `<p style="` + noticeTextStyle + `">` + s + `</p>` }
	name := func(s string) string {
		if s == "" {
			return "неизвестна"
		}
		return "<b>" + html.EscapeString(s) + "</b>"
	}
	removeAgent := func(a *launchAgent) string {
		return `<pre style="` + noticePreStyle + `">launchctl bootout gui/$(id -u)/` + launchAgentLabel + "\nrm '" + html.EscapeString(a.Path) + `'</pre>`
	}

	var b strings.Builder
	b.WriteString(`<div id="` + noticeElementID + `" role="alert" style="` + noticeBoxStyle + `">`)
	if n.NotAPanel {
		b.WriteString(`<strong>На ` + code(n.URL) + ` отвечает не панель fleetdeck.</strong>`)
		b.WriteString(para("Окно показывает то, что там отвечает, как есть: чужие программы оно не останавливает. Освободите порт, и окно запустит свою панель."))
		b.WriteString(`</div>`)
		return b.String()
	}
	b.WriteString(noticeHeaderStyle)
	b.WriteString(`<strong>На ` + code(n.URL) + ` отвечает панель другой сборки.</strong>`)
	panel := "Панель: " + name(n.Panel)
	if n.Executable != "" {
		panel += ", запущена из " + code(n.Executable)
	}
	b.WriteString(para(panel + ". Приложение: " + name(n.Window) + ". Сборка в шапке — сборка панели, а не приложения."))
	switch {
	case n.OtherWindow != 0:
		b.WriteString(para(fmt.Sprintf("Это панель другого окна fleetdeck (pid %d), которое ещё открыто. Закройте то окно, и это запустит свою панель.", n.OtherWindow)))
	case n.Agent != nil && n.Agent.Holds:
		b.WriteString(para("Панель держит launch agent " + code(launchAgentLabel) + " — его записал прежний " + code("fleetdeck init") + ". Он запускает её при входе в систему и снова, как только она остановится, поэтому окно её не заменяет. Снимите агента в Терминале:"))
		b.WriteString(removeAgent(n.Agent))
		b.WriteString(para("Панель остановится, и окно запустит свою."))
	default:
		b.WriteString(para("Её запустило не окно — например, Терминал. Остановите её, и окно запустит свою, или замените её сейчас:"))
	}
	if n.Agent != nil && !n.Agent.Holds {
		b.WriteString(para("Кроме того, есть launch agent " + code(launchAgentLabel) + " от прежнего " + code("fleetdeck init") + ": при каждом входе в систему он запускает " + code(orUnknown(n.Agent.Program)) + " раньше приложения. Снять его:"))
		b.WriteString(removeAgent(n.Agent))
	}
	if n.CanReplace {
		b.WriteString(`<button type="button" style="` + noticeBtnStyle + `" data-fleetdeck-call="` + replaceBindingName + `">Заменить панелью приложения</button>`)
	}
	b.WriteString(`</div>`)
	return b.String()
}

// noticeScript is put into every page the window loads. It asks the window
// for the notice and paints it, takes it down when there is none, and wires
// the notice's button: an inline handler would be refused by the panel's
// Content-Security-Policy, a listener added from here is not.
const noticeScript = `(() => {
  const repaint = () => {
    if (typeof window.` + noticeBindingName + ` !== "function" || !document.body) return;
    window.` + noticeBindingName + `().then((markup) => {
      const shown = document.getElementById("` + noticeElementID + `");
      if (!markup) {
        if (shown) {
          shown.remove();
          window.` + noticeShownBindingName + `("");
        }
        return;
      }
      const box = document.createElement("div");
      box.innerHTML = markup;
      const next = box.firstElementChild;
      if (shown) shown.replaceWith(next);
      else document.body.appendChild(next);
      next.querySelectorAll("[data-fleetdeck-call]").forEach((button) => {
        button.addEventListener("click", () => {
          button.disabled = true;
          const call = window[button.dataset.fleetdeckCall];
          if (typeof call === "function") call();
        });
      });
      window.` + noticeShownBindingName + `(next.innerText);
    });
  };
  window.` + noticeRepaintFunction + ` = repaint;
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", repaint);
  else repaint();
})();`

// panelNotice is the notice the window shows now, shared between the keeper's
// events and the page's questions.
type panelNotice struct {
	mu     sync.Mutex
	markup string
}

// set makes n the notice, and says whether that changed what is shown.
func (p *panelNotice) set(n *notice) bool {
	markup := noticeHTML(n)
	p.mu.Lock()
	defer p.mu.Unlock()
	changed := markup != p.markup
	p.markup = markup
	return changed
}

func (p *panelNotice) page() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.markup
}
