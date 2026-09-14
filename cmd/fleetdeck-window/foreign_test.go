//go:build darwin

package main

import (
	"html"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/kroticw/fleetdeck/internal/supervisor"
)

// On 2026-09-14 the operator installed v0.9.0, and the window opened on a
// panel built from source three days earlier, kept on 127.0.0.1:7777 by a
// launch agent an earlier `fleetdeck init` had written. The header showed the
// panel's commit and the panel's old Update button, and nothing said the
// panel was not the window's build. The window still uses a panel it did not
// start as it is; these tests pin that it now says so when that panel is
// another build.

const (
	releaseRev = "1e76fb3aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	oldRev     = "de3aa16bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	// terminalPID stands for the PID the keeper found on the port.
	terminalPID = 31337
)

var release = build{Version: "v0.9.0", Revision: releaseRev}

func answering(holder *supervisor.PanelBuild) supervisor.Event {
	return supervisor.Event{State: supervisor.Answering, Holder: holder}
}

func TestTheWindowReadsItsBuildFromItsOwnBuildRecord(t *testing.T) {
	info := &debug.BuildInfo{Settings: []debug.BuildSetting{
		{Key: "vcs.revision", Value: releaseRev},
		{Key: "vcs.modified", Value: "true"},
	}}
	got := buildFrom("v0.9.0", info)
	if want := (build{Version: "v0.9.0", Revision: releaseRev, Modified: true}); got != want {
		t.Fatalf("buildFrom = %+v, want %+v", got, want)
	}
	if got := buildFrom("dev", nil); got != (build{Version: "dev"}) {
		t.Fatalf("buildFrom with no build record = %+v", got)
	}
}

func TestWhichBuildsAreTheSame(t *testing.T) {
	for _, c := range []struct {
		name  string
		own   build
		panel supervisor.PanelBuild
		same  bool
	}{
		{"one commit, one version", release, supervisor.PanelBuild{Version: "v0.9.0", Revision: releaseRev}, true},
		{"one commit, built from a checkout and released", release, supervisor.PanelBuild{Version: "dev", Revision: releaseRev}, true},
		{"another commit", release, supervisor.PanelBuild{Revision: oldRev}, false},
		{"one commit, one with uncommitted edits", release, supervisor.PanelBuild{Version: "v0.9.0", Revision: releaseRev, Modified: true}, false},
		{"a panel that names no commit", release, supervisor.PanelBuild{Version: "v0.9.0"}, false},
		{"a window that knows no commit, the same release", build{Version: "v0.9.0"}, supervisor.PanelBuild{Version: "v0.9.0", Revision: oldRev}, true},
		{"a window that knows no commit, another release", build{Version: "v0.9.0"}, supervisor.PanelBuild{Version: "v0.8.0", Revision: oldRev}, false},
		{"a window that knows nothing of itself", build{Version: "dev"}, supervisor.PanelBuild{Revision: oldRev}, true},
	} {
		if got := sameBuild(c.own, c.panel); got != c.same {
			t.Errorf("%s: sameBuild = %v, want %v", c.name, got, c.same)
		}
	}
}

// The label is what the header of each build shows (web/js/buildcheck.js,
// brandHTML), so the notice names a build the way a person already sees it.
func TestABuildIsNamedTheWayTheHeaderNamesIt(t *testing.T) {
	for _, c := range []struct {
		version, revision string
		modified          bool
		want              string
	}{
		{"v0.9.0", releaseRev, false, "v0.9.0"},
		{"dev", oldRev, false, "dev de3aa16"},
		{"", oldRev, false, "de3aa16"},
		{"", oldRev, true, "de3aa16*"},
		{"dev", "", false, "dev"},
		{"", "", false, ""},
	} {
		if got := buildLabel(c.version, c.revision, c.modified); got != c.want {
			t.Errorf("buildLabel(%q, %q, %v) = %q, want %q", c.version, c.revision, c.modified, got, c.want)
		}
	}
}

func TestAPanelOfTheWindowsOwnBuildGetsNoNotice(t *testing.T) {
	home := t.TempDir()
	holder := &supervisor.PanelBuild{Version: "v0.9.0", Revision: releaseRev, Executable: "/Users/dev/fleetdeck/bin/fleetdeck", PID: terminalPID}
	if n := noticeFor(release, testURL, answering(holder), home); n != nil {
		t.Fatalf("noticeFor(own build) = %+v, want none", n)
	}
	// Not even beside a launch agent: the agent is init's to report, and a
	// panel of this build shows exactly what the window would.
	writeAgent(t, home, launchAgentLabel, holder.Executable)
	if n := noticeFor(release, testURL, answering(holder), home); n != nil {
		t.Fatalf("noticeFor(own build, agent) = %+v, want none", n)
	}
}

func TestOnlyAPanelTheWindowDidNotStartIsLookedAt(t *testing.T) {
	home := t.TempDir()
	for _, e := range []supervisor.Event{
		{State: supervisor.Answering, Ours: true, PID: 7},
		{State: supervisor.Starting, PID: 7},
		{State: supervisor.Replacing, Detail: "gone"},
		{State: supervisor.Failed},
	} {
		if n := noticeFor(release, testURL, e, home); n != nil {
			t.Errorf("noticeFor(%s) = %+v, want none", e.State, n)
		}
	}
}

func TestAPanelOfAnotherBuildIsNamedWithBothBuildsAndItsBinary(t *testing.T) {
	holder := &supervisor.PanelBuild{Revision: oldRev, Executable: "/Users/op/.local/bin/fleetdeck", PID: terminalPID}
	n := noticeFor(release, testURL, answering(holder), t.TempDir())
	if n == nil {
		t.Fatal("no notice for a panel of another build")
	}
	page := noticeHTML(n)
	for _, want := range []string{testURL, "de3aa16", "v0.9.0", "/Users/op/.local/bin/fleetdeck"} {
		if !strings.Contains(page, want) {
			t.Errorf("the notice lacks %q:\n%s", want, page)
		}
	}
	if !n.CanReplace || !strings.Contains(page, replaceBindingName) {
		t.Errorf("a panel from a terminal is not offered to be replaced: %+v\n%s", n, page)
	}
	// The button names the panel it replaces: the PID on the port when the
	// notice was drawn, which the press hands back.
	if !strings.Contains(page, `data-fleetdeck-arg="31337"`) {
		t.Errorf("the button does not name the panel it replaces:\n%s", page)
	}
	// The header is the panel's, and says the panel's build; the notice marks
	// it as the panel's, not the app's.
	if !strings.Contains(page, "#header .build-rev::before") {
		t.Errorf("the notice does not mark the header's build as the panel's:\n%s", page)
	}
}

// Without the PID on the port a press could not be checked against what is
// there when it comes, so nothing is offered.
func TestAPanelWhoseProcessWasNotFoundIsNotOfferedToBeReplaced(t *testing.T) {
	holder := &supervisor.PanelBuild{Revision: oldRev, Executable: "/Users/op/.local/bin/fleetdeck"}
	n := noticeFor(release, testURL, answering(holder), t.TempDir())
	if n == nil || n.CanReplace || strings.Contains(noticeHTML(n), replaceBindingName) {
		t.Fatalf("noticeFor = %+v, want a notice with no button", n)
	}
}

// writeAgent writes a launch agent the way `fleetdeck init` wrote one before
// 2026-09-11 (cmd/fleetdeck/init.go at 141bc1c).
func writeAgent(t *testing.T, home, label, program string) string {
	t.Helper()
	dir := filepath.Join(home, "Library", "LaunchAgents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, launchAgentLabel+".plist")
	body := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>` + label + `</string>
  <key>ProgramArguments</key><array><string>` + html.EscapeString(program) + `</string></array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>/tmp/fleetdeck.log</string>
  <key>StandardErrorPath</key><string>/tmp/fleetdeck.log</string>
</dict>
</plist>
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAPanelTheLaunchAgentKeepsIsNamedWithTheAgentAndHowToRemoveIt(t *testing.T) {
	home := t.TempDir()
	exe := filepath.Join(home, ".local", "bin", "fleetdeck")
	agent := writeAgent(t, home, launchAgentLabel, exe)
	holder := &supervisor.PanelBuild{Revision: oldRev, Executable: exe, PID: terminalPID}

	n := noticeFor(release, testURL, answering(holder), home)
	if n == nil || n.Agent == nil || !n.Agent.Holds {
		t.Fatalf("noticeFor = %+v, want the agent named as what keeps the panel", n)
	}
	page := noticeHTML(n)
	for _, want := range []string{launchAgentLabel, agent, "launchctl bootout gui/$(id -u)/" + launchAgentLabel, "rm "} {
		if !strings.Contains(page, want) {
			t.Errorf("the notice lacks %q:\n%s", want, page)
		}
	}
	// launchd would start the panel again the moment it was stopped: removing
	// the agent is the person's, and a button here would only fight launchd.
	if n.CanReplace || strings.Contains(page, replaceBindingName) {
		t.Errorf("a panel the agent keeps is offered to be replaced:\n%s", page)
	}
}

// The command is copied into a terminal: it must survive a quote in the path,
// as a shell reads it, once the page's own escaping is undone.
func TestTheCommandToRemoveTheAgentSurvivesAQuoteInItsPath(t *testing.T) {
	home := filepath.Join(t.TempDir(), "o'brien")
	exe := filepath.Join(home, ".local", "bin", "fleetdeck")
	agent := writeAgent(t, home, launchAgentLabel, exe)
	n := noticeFor(release, testURL, answering(&supervisor.PanelBuild{Revision: oldRev, Executable: exe, PID: terminalPID}), home)
	if n == nil || n.Agent == nil {
		t.Fatalf("noticeFor = %+v, want the agent named", n)
	}
	want := "rm '" + strings.ReplaceAll(agent, "'", `'\''`) + "'"
	if page := html.UnescapeString(noticeHTML(n)); !strings.Contains(page, want) {
		t.Errorf("the notice does not carry %s:\n%s", want, page)
	}
	if got := shellQuote(`a'b`); got != `'a'\''b'` {
		t.Errorf(`shellQuote("a'b") = %s`, got)
	}
}

func TestAnAgentStartingAnotherBinaryIsNamedButNotAsThePanelsKeeper(t *testing.T) {
	home := t.TempDir()
	writeAgent(t, home, launchAgentLabel, "/somewhere/else/fleetdeck")
	holder := &supervisor.PanelBuild{Revision: oldRev, Executable: "/Users/op/claude/fleetdeck/bin/fleetdeck", PID: terminalPID}

	n := noticeFor(release, testURL, answering(holder), home)
	if n == nil || n.Agent == nil || n.Agent.Holds {
		t.Fatalf("noticeFor = %+v, want the agent named, not as the panel's keeper", n)
	}
	if !n.CanReplace {
		t.Error("a panel the agent does not keep is not offered to be replaced")
	}
	if page := noticeHTML(n); !strings.Contains(page, launchAgentLabel) {
		t.Errorf("the notice does not name the agent:\n%s", page)
	}
}

// An agent file init did not write is somebody's own, as init itself holds.
func TestAFileAtTheAgentsPathThatInitDidNotWriteIsNotNamed(t *testing.T) {
	home := t.TempDir()
	exe := "/Users/op/.local/bin/fleetdeck"
	writeAgent(t, home, "org.example.mine", exe)
	n := noticeFor(release, testURL, answering(&supervisor.PanelBuild{Revision: oldRev, Executable: exe}), home)
	if n == nil || n.Agent != nil {
		t.Fatalf("noticeFor = %+v, want a notice naming no agent", n)
	}
}

func TestAPanelOfAnotherWindowStillOpenSaysSoAndOffersNoReplace(t *testing.T) {
	holder := &supervisor.PanelBuild{Revision: oldRev, Executable: "/Applications/fleetdeck.app/Contents/MacOS/fleetdeck", Owner: 4242, PID: terminalPID}
	n := noticeFor(release, testURL, answering(holder), t.TempDir())
	if n == nil {
		t.Fatal("no notice for another window's panel of another build")
	}
	page := noticeHTML(n)
	if !strings.Contains(page, "4242") {
		t.Errorf("the notice does not name the other window:\n%s", page)
	}
	if n.CanReplace || strings.Contains(page, replaceBindingName) {
		t.Errorf("the panel of a window still open is offered to be replaced:\n%s", page)
	}
}

func TestSomethingThatIsNotAPanelIsSaidToBeNone(t *testing.T) {
	n := noticeFor(release, testURL, answering(nil), t.TempDir())
	if n == nil || !n.NotAPanel {
		t.Fatalf("noticeFor(not a panel) = %+v, want a notice saying so", n)
	}
	page := noticeHTML(n)
	if !strings.Contains(page, "не панель fleetdeck") {
		t.Errorf("the notice does not say it is not a fleetdeck panel:\n%s", page)
	}
	if n.CanReplace || strings.Contains(page, replaceBindingName) {
		t.Errorf("something that is not a panel is offered to be replaced:\n%s", page)
	}
}

func TestANoticeOfNothingIsNoMarkup(t *testing.T) {
	if got := noticeHTML(nil); got != "" {
		t.Fatalf("noticeHTML(nil) = %q, want empty", got)
	}
}

func TestTheNoticeEscapesWhatThePanelSaysOfItself(t *testing.T) {
	const evil = `"></style><script>alert(1)</script>`
	holder := &supervisor.PanelBuild{Version: evil, Revision: evil, Executable: evil, PID: terminalPID}
	page := noticeHTML(noticeFor(build{Version: evil, Revision: releaseRev}, "http://x/"+evil, answering(holder), t.TempDir()))
	if strings.Contains(page, "<script>alert(1)</script>") || strings.Count(page, "</style>") != 1 {
		t.Fatalf("the notice carries what the panel said unescaped:\n%s", page)
	}
}

// The page half of the notice is a script the window puts into every page it
// loads; the Go half is the bindings it calls. Two languages, one agreement.
func TestTheNoticeScriptCallsTheBindingsTheWindowBinds(t *testing.T) {
	for _, name := range []string{noticeBindingName, noticeShownBindingName, noticeRepaintFunction, noticeElementID, "fleetdeckArg"} {
		if !strings.Contains(noticeScript, name) {
			t.Errorf("the notice script does not use %q", name)
		}
	}
	n := noticeFor(release, testURL, answering(&supervisor.PanelBuild{Revision: oldRev, PID: terminalPID}), t.TempDir())
	if page := noticeHTML(n); !strings.Contains(page, `id="`+noticeElementID+`"`) {
		t.Errorf("the notice is not the element the script looks for:\n%s", page)
	}
}

// A press is taken only for the notice shown now, and only for a panel it
// offers to replace. The page belongs to the panel and can call the binding
// itself, with any number; a page left from an earlier notice can too.
func TestAPressIsTakenOnlyForTheNoticeShownNow(t *testing.T) {
	holder := &supervisor.PanelBuild{Revision: oldRev, Executable: "/Users/op/.local/bin/fleetdeck", PID: terminalPID}
	n := noticeFor(release, testURL, answering(holder), t.TempDir())

	want, ok := replaceRequest(n, terminalPID)
	if !ok || want != *holder {
		t.Fatalf("replaceRequest(notice, its PID) = %+v, %v; want the panel the notice is about", want, ok)
	}
	if _, ok := replaceRequest(n, terminalPID+1); ok {
		t.Error("a press naming another process was taken")
	}
	if _, ok := replaceRequest(nil, terminalPID); ok {
		t.Error("a press with no notice shown was taken")
	}
	other := noticeFor(release, testURL, answering(&supervisor.PanelBuild{Revision: oldRev, Owner: 4242, PID: terminalPID}), t.TempDir())
	if _, ok := replaceRequest(other, terminalPID); ok {
		t.Error("a press about the panel of a window still open was taken")
	}
	if _, ok := replaceRequest(noticeFor(release, testURL, answering(nil), t.TempDir()), 0); ok {
		t.Error("a press about something that is not a panel was taken")
	}
}

// What the keeper asks at a press, about the panel on the port by then: the
// same question the notice's button answers.
func TestTheWindowLetsTheKeeperReplaceOnlyWhatItOffersToReplace(t *testing.T) {
	home := t.TempDir()
	may := mayReplace(release, testURL, home)
	terminal := supervisor.PanelBuild{Revision: oldRev, Executable: "/Users/op/.local/bin/fleetdeck", PID: terminalPID}
	if !may(terminal) {
		t.Error("a panel of another build from a terminal may not be replaced")
	}
	if may(supervisor.PanelBuild{Revision: releaseRev, Executable: "/x", PID: terminalPID}) {
		t.Error("a panel of the window's own build may be replaced")
	}
	if may(supervisor.PanelBuild{Revision: oldRev, Owner: 4242, PID: terminalPID}) {
		t.Error("the panel of a window still open may be replaced")
	}
	writeAgent(t, home, launchAgentLabel, terminal.Executable)
	if may(terminal) {
		t.Error("a panel the launch agent keeps may be replaced")
	}
}

// The log line is what a person reading the window's log after the fact has:
// the same facts as the notice, in the log's language.
func TestTheLogLineNamesThePanelItsBinaryAndTheAgent(t *testing.T) {
	home := t.TempDir()
	exe := "/Users/op/.local/bin/fleetdeck"
	agent := writeAgent(t, home, launchAgentLabel, exe)
	line := noticeFor(release, testURL, answering(&supervisor.PanelBuild{Revision: oldRev, Executable: exe}), home).String()
	for _, want := range []string{testURL, "de3aa16", "v0.9.0", exe, launchAgentLabel, agent} {
		if !strings.Contains(line, want) {
			t.Errorf("the log line lacks %q: %s", want, line)
		}
	}
	if line := noticeFor(release, testURL, answering(nil), home).String(); !strings.Contains(line, "not a fleetdeck panel") {
		t.Errorf("the log line for something that is not a panel: %s", line)
	}
}

// What a panel says of itself is another program's word, and a line break in
// it would forge lines of the window's log.
func TestTheLogLineQuotesWhatThePanelSaysOfItself(t *testing.T) {
	const forged = "/x\n2026/09/14 10:00:00 fleetdeck-window: forged"
	line := noticeFor(release, testURL, answering(&supervisor.PanelBuild{Revision: oldRev, Executable: forged}), t.TempDir()).String()
	if strings.Contains(line, "\n") {
		t.Fatalf("the log line carries a raw line break: %s", line)
	}
}

// Every look of the keeper can bring the same notice again; the page is
// repainted, and the log written, only when what it says changes.
func TestTheNoticeCountsAsChangedOnlyWhenItSaysSomethingElse(t *testing.T) {
	var p panelNotice
	other := func() *notice {
		return noticeFor(release, testURL, answering(&supervisor.PanelBuild{Revision: oldRev, Executable: "/x", PID: terminalPID}), t.TempDir())
	}
	first := other()
	if !p.set(first) {
		t.Error("the first notice did not count as a change")
	}
	if p.page() != noticeHTML(other()) {
		t.Errorf("page() = %q, want the notice's markup", p.page())
	}
	if p.current() != first {
		t.Errorf("current() = %+v, want the notice set", p.current())
	}
	if p.set(other()) {
		t.Error("the same notice again counted as a change")
	}
	if !p.set(nil) || p.page() != "" || p.current() != nil {
		t.Errorf("clearing the notice: page() = %q, current() = %+v", p.page(), p.current())
	}
	if p.set(nil) {
		t.Error("no notice after no notice counted as a change")
	}
}

func TestTheScreenSaysAPanelIsReplacedBecauseThePersonAsked(t *testing.T) {
	s := &screen{url: testURL, logPath: "/log"}
	s.on(supervisor.Event{State: supervisor.Answering})
	_, html := s.on(supervisor.Event{State: supervisor.Replacing, Asked: true, Detail: "the panel at " + testURL})
	if !strings.Contains(html, "Заменяю панель") || strings.Contains(html, "оставленная прежним окном") {
		t.Fatalf("on(Replacing, asked) = %q; want the replacing page, not the one about a left-behind panel", html)
	}
}
