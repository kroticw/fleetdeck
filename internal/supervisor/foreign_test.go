package supervisor

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// A panel the keeper did not start is used as it is, and the window shows it.
// On 2026-09-14 the operator's v0.9.0 window showed a panel built from source
// three days earlier, kept on the port by a launch agent, and nothing on
// screen said the two were different builds. So the keeper now reports what
// such a panel says of its own build, for the window to compare with its own;
// and it replaces one when -- and only when -- a person asks, and only the
// panel the person was shown.

// builtStandIn is a copy of the stand-in binary with a revision file beside
// it, which the stand-in reports as its build's revision.
func builtStandIn(t *testing.T, revision string) string {
	t.Helper()
	dir := t.TempDir()
	exe := filepath.Join(dir, "fleetdeck")
	self, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, self, 0o755); err != nil {
		t.Fatal(err)
	}
	setRevision(t, exe, revision)
	return exe
}

// setRevision changes the revision the stand-in at exe reports, while it
// runs: the stand-in reads the file at every snapshot, so the build on the
// port changes without the port ever going quiet.
func setRevision(t *testing.T, exe, revision string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(filepath.Dir(exe), "revision"), []byte(revision+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func sameFile(t *testing.T, a, b string) bool {
	t.Helper()
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}

// noReplacement reads the keeper's events for span and fails on any that
// means a panel was stopped or given up on. It returns the Answering events
// it saw.
func noReplacement(t *testing.T, r *keeperRun, span time.Duration) []Event {
	t.Helper()
	var seen []Event
	deadline := time.After(span)
	for {
		select {
		case e := <-r.events:
			if e.State != Answering {
				t.Fatalf("event %s (%+v): a press that must stop nothing did something", e.State, e)
			}
			seen = append(seen, e)
		case <-deadline:
			return seen
		}
	}
}

func TestAPanelTheKeeperDidNotStartIsReportedWithWhatItSaysOfItsBuild(t *testing.T) {
	addr := freeAddr(t)
	exe := builtStandIn(t, "de3aa16c0ffee")
	foreignPanel(t, addr, exe, 0)
	r := run(t, windowKeeper(t, addr))

	up := r.expect(t, Answering, 5*time.Second)
	if up.Ours {
		t.Fatalf("Answering %+v, want the panel already there", up)
	}
	if up.Holder == nil {
		t.Fatal("Answering carries no build for a fleetdeck panel the keeper did not start")
	}
	if up.Holder.Revision != "de3aa16c0ffee" {
		t.Errorf("Holder.Revision = %q, want the revision the panel reports", up.Holder.Revision)
	}
	if !sameFile(t, up.Holder.Executable, exe) {
		t.Errorf("Holder.Executable = %q, want %q", up.Holder.Executable, exe)
	}
}

// The PID is the kernel's answer to who listens on the port, taken when the
// panel is reported: it is what a press is checked against before anything is
// stopped.
func TestTheBuildOfAPanelTheKeeperDidNotStartCarriesThePIDOnItsPort(t *testing.T) {
	needLsof(t)
	addr := freeAddr(t)
	terminal := foreignPanel(t, addr, builtStandIn(t, "abc"), 0)
	r := run(t, windowKeeper(t, addr))

	up := r.expect(t, Answering, 5*time.Second)
	if up.Holder == nil || up.Holder.PID != terminal.Process.Pid {
		t.Fatalf("Answering %+v, want the holder's PID %d", up.Holder, terminal.Process.Pid)
	}
}

func TestAKeeperWithNoWindowReportsTheBuildToo(t *testing.T) {
	addr := freeAddr(t)
	foreignPanel(t, addr, builtStandIn(t, "abc"), 0)
	r := run(t, newKeeper(t, "listen", addr))

	if up := r.expect(t, Answering, 5*time.Second); up.Holder == nil || up.Holder.Revision != "abc" {
		t.Fatalf("Answering %+v, want the panel's build", up)
	}
}

func TestSomethingThatIsNotAPanelIsReportedWithNoBuild(t *testing.T) {
	addr := freeAddr(t)
	serveElsewhere(t, addr)
	r := run(t, windowKeeper(t, addr))

	if up := r.expect(t, Answering, 5*time.Second); up.Ours || up.Holder != nil {
		t.Fatalf("Answering %+v, want no build: what answers is not a fleetdeck panel", up)
	}
}

// The window's notice is about the panel that was on the port when it was
// drawn. Another build taking the port without it going quiet -- a second
// window's panel started within the two looks it takes to see a panel gone --
// is reported again, so the notice is not left describing a panel that is not
// there.
func TestTheKeeperSaysSoWhenThePanelOnThePortBecomesAnotherBuild(t *testing.T) {
	addr := freeAddr(t)
	exe := builtStandIn(t, "aaa")
	foreignPanel(t, addr, exe, 0)
	r := run(t, windowKeeper(t, addr))

	if up := r.expect(t, Answering, 5*time.Second); up.Holder == nil || up.Holder.Revision != "aaa" {
		t.Fatalf("Answering %+v, want build aaa", up)
	}
	setRevision(t, exe, "bbb")
	up := r.expect(t, Answering, 5*time.Second)
	if up.Holder == nil || up.Holder.Revision != "bbb" {
		t.Fatalf("Answering %+v, want the build now on the port, bbb", up)
	}
}

func TestReplaceStopsAPanelFromATerminalAndStartsTheKeepersOwn(t *testing.T) {
	needLsof(t) // replacing stops the holder, found by lsof
	addr := freeAddr(t)
	terminal := foreignPanel(t, addr, os.Args[0], 0)
	k := windowKeeper(t, addr)
	r := run(t, k)

	up := r.expect(t, Answering, 5*time.Second)
	if up.Ours || up.Holder == nil {
		t.Fatalf("Answering %+v, want the terminal's panel", up)
	}
	k.Replace(*up.Holder)
	replacing := r.expect(t, Replacing, 5*time.Second)
	if !replacing.Asked {
		t.Errorf("Replacing %+v, want it marked as asked for", replacing)
	}
	r.expect(t, Starting, 10*time.Second)
	if up := r.expect(t, Answering, 10*time.Second); !up.Ours {
		t.Fatalf("Answering %+v, want the keeper's own panel", up)
	}
	if alive(terminal.Process.Pid) {
		t.Fatal("the terminal's panel is still running after it was replaced")
	}
}

// A press that came before the panel it was about -- a page of a panel
// already gone, a second press -- must not stop whatever answers next.
func TestAReplaceAskedBeforeThePanelAnsweredStopsNothing(t *testing.T) {
	addr := freeAddr(t)
	terminal := foreignPanel(t, addr, os.Args[0], 0)
	k := windowKeeper(t, addr)
	k.Replace(PanelBuild{})
	r := run(t, k)

	if up := r.expect(t, Answering, 5*time.Second); up.Ours {
		t.Fatalf("Answering %+v, want the terminal's panel", up)
	}
	noReplacement(t, r, time.Second)
	if !alive(terminal.Process.Pid) {
		t.Fatal("a Replace asked before the panel answered stopped it")
	}
}

// The build on the port changed between the notice and the press: nothing is
// stopped, and the keeper says what is on the port now.
func TestAReplaceForABuildNoLongerOnThePortStopsNothingAndSaysWhatIsThere(t *testing.T) {
	needLsof(t)
	addr := freeAddr(t)
	exe := builtStandIn(t, "aaa")
	terminal := foreignPanel(t, addr, exe, 0)
	k := windowKeeper(t, addr)
	r := run(t, k)

	up := r.expect(t, Answering, 5*time.Second)
	if up.Holder == nil {
		t.Fatalf("Answering %+v, want the terminal's panel", up)
	}
	shown := *up.Holder
	setRevision(t, exe, "bbb")
	k.Replace(shown)

	saidNow := false
	for _, e := range noReplacement(t, r, 3*time.Second) {
		if e.Holder != nil && e.Holder.Revision == "bbb" {
			saidNow = true
		}
	}
	if !saidNow {
		t.Error("the keeper did not report the build now on the port")
	}
	if !alive(terminal.Process.Pid) {
		t.Fatal("a press about another build stopped the panel on the port")
	}
}

// Another process on the port with the very same build: the PID tells them
// apart.
func TestAReplaceForAnotherProcessOnThePortStopsNothing(t *testing.T) {
	needLsof(t)
	addr := freeAddr(t)
	terminal := foreignPanel(t, addr, os.Args[0], 0)
	k := windowKeeper(t, addr)
	r := run(t, k)

	up := r.expect(t, Answering, 5*time.Second)
	if up.Holder == nil {
		t.Fatalf("Answering %+v, want the terminal's panel", up)
	}
	shown := *up.Holder
	shown.PID = terminal.Process.Pid + 100000
	k.Replace(shown)

	noReplacement(t, r, 2*time.Second)
	if !alive(terminal.Process.Pid) {
		t.Fatal("a press about another process stopped the panel on the port")
	}
}

// What answers is not a fleetdeck panel: a press -- from a page, which can
// call the binding itself -- is ignored, and the keeper does not give up.
func TestAReplaceWhenWhatAnswersIsNotAPanelIsIgnored(t *testing.T) {
	addr := freeAddr(t)
	serveElsewhere(t, addr)
	k := windowKeeper(t, addr)
	r := run(t, k)

	r.expect(t, Answering, 5*time.Second)
	k.Replace(PanelBuild{PID: os.Getpid()})
	noReplacement(t, r, 2*time.Second)
	if !answersNow("http://" + addr + "/") {
		t.Fatal("what answered is gone")
	}
}

// The panel of a window still open is that window's, whoever asks.
func TestAReplaceOfThePanelOfAWindowStillOpenStopsNothing(t *testing.T) {
	needLsof(t)
	addr := freeAddr(t)
	window := exec.Command("sleep", "60")
	if err := window.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = window.Process.Kill(); _ = window.Wait() })
	theirs := foreignPanel(t, addr, os.Args[0], window.Process.Pid)
	k := windowKeeper(t, addr)
	r := run(t, k)

	up := r.expect(t, Answering, 5*time.Second)
	if up.Holder == nil {
		t.Fatalf("Answering %+v, want the other window's panel", up)
	}
	k.Replace(*up.Holder)
	noReplacement(t, r, 2*time.Second)
	if !alive(theirs.Process.Pid) {
		t.Fatal("a press stopped the panel of a window that is still open")
	}
}

// The window has the last word on what may be replaced -- a panel a launch
// agent keeps may not -- and the keeper asks it again at the press.
func TestAReplaceTheWindowRefusesStopsNothing(t *testing.T) {
	needLsof(t)
	addr := freeAddr(t)
	terminal := foreignPanel(t, addr, os.Args[0], 0)
	k := windowKeeper(t, addr)
	k.MayReplace = func(PanelBuild) bool { return false }
	r := run(t, k)

	up := r.expect(t, Answering, 5*time.Second)
	if up.Holder == nil {
		t.Fatalf("Answering %+v, want the terminal's panel", up)
	}
	k.Replace(*up.Holder)
	noReplacement(t, r, 2*time.Second)
	if !alive(terminal.Process.Pid) {
		t.Fatal("a press the window refuses stopped the panel")
	}
}
