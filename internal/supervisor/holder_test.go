package supervisor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// An update replaces whatever panel answers at the URL, not only one this
// window started: a panel left by an earlier window, or started from a
// terminal, holds the port just the same.

func needLsof(t *testing.T) {
	t.Helper()
	if _, err := lsofPath(); err != nil {
		t.Skipf("no lsof on this machine: %v", err)
	}
}

// startForeign starts a stand-in panel the way a terminal would -- not
// through StartPanel -- so nothing here knows its PID but the kernel.
func startForeign(t *testing.T, kind string) (addr, logPath string, cmd *exec.Cmd) {
	t.Helper()
	addr = freeAddr(t)
	logPath = filepath.Join(t.TempDir(), "foreign.log")
	log, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	cmd = exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), helperEnv+"="+kind+"@"+addr)
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { _ = cmd.Wait(); _ = log.Close() }()
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := WaitAnswer(ctx, "http://"+addr+"/"); err != nil {
		t.Fatalf("the stand-in never answered: %v", err)
	}
	return addr, logPath, cmd
}

func TestStopHolderStopsAPanelNobodyHereStarted(t *testing.T) {
	needLsof(t)
	addr, logPath, cmd := startForeign(t, "listen")

	if err := StopHolder(context.Background(), "http://"+addr+"/", 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if answersNow("http://" + addr + "/") {
		t.Fatal("the panel still answers after StopHolder")
	}
	if alive(cmd.Process.Pid) {
		t.Fatalf("pid %d still runs after StopHolder", cmd.Process.Pid)
	}
	// Asked first, as Panel.Stop asks: the stand-in's own words in its log.
	data, _ := os.ReadFile(logPath)
	if !strings.Contains(string(data), termNotice) {
		t.Fatalf("the panel was not sent SIGTERM first; its log holds:\n%s", data)
	}
}

func TestStopHolderKillsAPanelThatIgnoresTerm(t *testing.T) {
	needLsof(t)
	addr, _, cmd := startForeign(t, "ignore-term")
	if err := StopHolder(context.Background(), "http://"+addr+"/", 500*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for alive(cmd.Process.Pid) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if alive(cmd.Process.Pid) {
		t.Fatal("a panel that ignores SIGTERM was left running")
	}
}

// Whatever holds the port is stopped only when it answers as a fleetdeck
// panel. Here the holder is this test process itself: a StopHolder that did
// not ask first would send SIGTERM to the test.
func TestStopHolderRefusesToStopSomethingThatIsNotAPanel(t *testing.T) {
	needLsof(t)
	addr := freeAddr(t)
	serveElsewhere(t, addr)

	err := StopHolder(context.Background(), "http://"+addr+"/", time.Second)
	if err == nil || !strings.Contains(err.Error(), "not a fleetdeck panel") {
		t.Fatalf("StopHolder = %v, want a refusal saying the holder is not a panel", err)
	}
	if !answersNow("http://" + addr + "/") {
		t.Fatal("the holder stopped answering: it was stopped anyway")
	}
}

func TestStopHolderWithNothingAtTheURLHasNothingToDo(t *testing.T) {
	if err := StopHolder(context.Background(), "http://"+freeAddr(t)+"/", time.Second); err != nil {
		t.Fatalf("StopHolder on an empty port = %v, want nil", err)
	}
}
