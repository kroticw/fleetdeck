//go:build darwin

package main

import (
	"os/exec"
	"testing"
	"time"
)

// The window started by an update removes the bundle swapped out the moment
// the old window quits (supervisor.Takeover.OldWindowQuit). That moment is
// told by the system, not found by asking every so often.

func TestAProcessQuittingIsTold(t *testing.T) {
	cmd := exec.Command("/bin/sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()

	quit := exitOf(cmd.Process.Pid, func() bool { return true })
	select {
	case <-quit:
		t.Fatal("told of a quit while the process still runs")
	case <-time.After(100 * time.Millisecond):
	}

	_ = cmd.Process.Kill()
	<-exited
	select {
	case <-quit:
	case <-time.After(5 * time.Second):
		t.Fatal("the process quit and nothing was told within 5s")
	}
}

// The old window can be gone before anyone starts watching for it; that is
// told at once, not never.
func TestAProcessAlreadyGoneIsToldAtOnce(t *testing.T) {
	cmd := exec.Command("/usr/bin/true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}

	select {
	case <-exitOf(cmd.Process.Pid, func() bool { return true }):
	case <-time.After(5 * time.Second):
		t.Fatal("a process already gone was not told within 5s")
	}
}

// A PID can be given to another process once its own has gone. Watching
// starts only while it is still the process meant -- for the old window,
// while it is still this one's parent -- and otherwise it is told as gone.
func TestAPidThatIsNoLongerTheProcessMeantIsToldAsGone(t *testing.T) {
	cmd := exec.Command("/bin/sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	select {
	case <-exitOf(cmd.Process.Pid, func() bool { return false }):
	case <-time.After(5 * time.Second):
		t.Fatal("a PID that is no longer the process meant was watched as if it were")
	}
}
