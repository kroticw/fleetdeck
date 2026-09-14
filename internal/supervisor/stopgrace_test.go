package supervisor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Only an update's restart gives a panel handoverStopGrace. Every other stop
// gives it stopGrace, the panel's own graceful shutdown bound and a second: a
// panel that takes a moment to go -- a card write finishing, a request being
// answered -- goes by itself and is not killed.

func requireLeftByItself(t *testing.T, logPath string) {
	t.Helper()
	data, _ := os.ReadFile(logPath)
	if !strings.Contains(string(data), slowTermNotice) {
		t.Fatalf("the panel was killed before it could go by itself; its log holds:\n%s", data)
	}
}

func TestStoppingAPanelLetsItTakeASecondToGo(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "panel.log")
	p, _ := startHelperLogging(t, "slow-term", logPath)
	if err := p.Stop(stopGrace); err != nil {
		t.Fatal(err)
	}
	requireLeftByItself(t, logPath)
}

func TestStoppingThePanelOnThePortLetsItTakeASecondToGo(t *testing.T) {
	needLsof(t)
	logPath := filepath.Join(t.TempDir(), "panel.log")
	p, addr := startHelperLogging(t, "slow-term", logPath)
	if err := StopHolder(context.Background(), "http://"+addr+"/", stopGrace); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.Exited():
	case <-time.After(5 * time.Second):
		t.Fatal("the panel is still running after it was stopped")
	}
	requireLeftByItself(t, logPath)
}

// The panel a handover stops first is the installed one, stopped before the
// new window's own starts: that is not an update's restart either.
func TestAHandoverLetsTheInstalledPanelTakeASecondToGo(t *testing.T) {
	r := newUpdateRig(t)
	r.pauseOld()
	if err := StopHolder(context.Background(), r.url, time.Second); err != nil {
		t.Fatal(err)
	}
	r.oldKeeper.Env = helperEnvFor("listen-slow-term", r.addr)
	r.resumeOld()
	if !waitRevision(r.url, "old", 10*time.Second) {
		t.Fatal("the old panel never answered again")
	}

	if err := r.update("old").Run(context.Background()); err != nil {
		t.Fatalf("update: %v (steps %v)", err, r.steps())
	}
	requireLeftByItself(t, r.oldKeeper.LogPath)
}

// A panel the keeper started that never answers is stopped too, and with the
// ordinary grace: that is not an update's restart.
func TestAKeeperGivingUpOnAPanelThatNeverAnswersLetsItTakeASecondToGo(t *testing.T) {
	addr := freeAddr(t)
	k := newKeeper(t, "silent-slow-term", addr)
	k.StartTimeout = 500 * time.Millisecond
	r := run(t, k)
	r.expect(t, Starting, 5*time.Second)
	r.expect(t, Failed, 15*time.Second)
	requireLeftByItself(t, k.LogPath)
}
