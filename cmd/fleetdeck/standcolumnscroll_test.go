package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// scripts/ci-window-stand.sh holds a scrolled column to its place across the
// panel's snapshots, as the board reports it (web/js/standreport.js,
// probeColumnScroll). The board reports once it has drawn two snapshots after
// scrolling, which is two to five seconds after the page loads; the stand took
// its screenshot and closed the window on timers of its own, and run
// 34952423754 closed it 5.1 s after the page loaded, before the report (T-074).
// The stand waits for the report, and says when it never came apart from a
// column that did not keep its place (scripts/stand-column-scroll.sh).

const columnScrollLine = `2026/09/15 09:28:07.000000 fleetdeck-window: the board reports its column scroll: {"report":"columnScroll","stage":"done","asked":120,"renders":2,"scrollTop":%s}`

// standColumnScroll runs script after sourcing scripts/stand-column-scroll.sh
// the way the stand does, with args, and returns its output and exit status.
func standColumnScroll(t *testing.T, script string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command("/bin/sh", append([]string{"-c", "set -eu\n. scripts/stand-column-scroll.sh\n" + script, "sh"}, args...)...)
	cmd.Dir = filepath.Join("..", "..")
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	out, err := cmd.CombinedOutput()
	if exit, ok := err.(*exec.ExitError); ok {
		return string(out), exit.ExitCode()
	}
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	return string(out), 0
}

func TestTheStandWaitsForTheColumnScrollReportThatComesLate(t *testing.T) {
	log := filepath.Join(t.TempDir(), "window.log")
	if err := os.WriteFile(log, []byte("fleetdeck-window: the panel's page says \"panel\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The report lands 1.5 s after the stand starts waiting, as a board that
	// draws its snapshots late reports.
	go func() {
		time.Sleep(1500 * time.Millisecond)
		f, err := os.OpenFile(log, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			return
		}
		defer f.Close()
		_, _ = f.WriteString(strings.Replace(columnScrollLine, "%s", "120", 1) + "\n")
	}()
	start := time.Now()
	out, status := standColumnScroll(t, `wait_column_scroll "$1" 20 $$`, log)
	if status != 0 {
		t.Fatalf("the stand did not hear the report that came after 1.5 s (status %d):\n%s", status, out)
	}
	if waited := time.Since(start); waited > 10*time.Second {
		t.Fatalf("the stand waited %v for a report that came after 1.5 s: it waits out its bound, not for the report", waited)
	}
}

func TestTheStandStopsWaitingForTheColumnScrollReportAtItsBound(t *testing.T) {
	log := filepath.Join(t.TempDir(), "window.log")
	if err := os.WriteFile(log, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if out, status := standColumnScroll(t, `wait_column_scroll "$1" 1 $$`, log); status == 0 {
		t.Fatalf("the stand heard a report that never came:\n%s", out)
	}
	if waited := time.Since(start); waited > 5*time.Second {
		t.Fatalf("the stand waited %v past a bound of 1 s", waited)
	}
}

// A window that is gone reports nothing more: the stand stops waiting at once.
func TestTheStandStopsWaitingForTheColumnScrollReportOfAWindowThatIsGone(t *testing.T) {
	log := filepath.Join(t.TempDir(), "window.log")
	if err := os.WriteFile(log, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	gone := exec.Command("/usr/bin/true")
	if err := gone.Run(); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if _, status := standColumnScroll(t, `wait_column_scroll "$1" 20 "$2"`, log, strconv.Itoa(gone.Process.Pid)); status == 0 {
		t.Fatal("the stand heard a report from a window that is gone")
	}
	if waited := time.Since(start); waited > 5*time.Second {
		t.Fatalf("the stand waited %v on a window that is gone", waited)
	}
}

func TestTheColumnScrollVerdictTellsNoReportFromAColumnThatMoved(t *testing.T) {
	for _, c := range []struct {
		name   string
		log    string
		status int
		words  string
	}{
		{"kept", strings.Replace(columnScrollLine, "%s", "120", 1), 0, "the done column scrolled to 120 px: after 2 snapshots drawn it is at 120 px"},
		{"drawn back at its top", strings.Replace(columnScrollLine, "%s", "0", 1), 1, "after 2 snapshots drawn it is at 0 px"},
		{"never reported", "fleetdeck-window: the panel's page says \"panel\"", 1, "the board never reported its scrolled column within 30 s"},
	} {
		log := filepath.Join(t.TempDir(), "window.log")
		if err := os.WriteFile(log, []byte(c.log+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		out, status := standColumnScroll(t, `column_scroll_verdict "$1" 30`, log)
		if status != c.status || !strings.Contains(out, c.words) {
			t.Errorf("%s: status %d, output %q; want status %d naming %q", c.name, status, out, c.status, c.words)
		}
	}
}
