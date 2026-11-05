package codexruntime

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestPTYChild(_ *testing.T) {
	if os.Getenv("FLEETDECK_RUNTIME_PTY_TEST") != "1" {
		return
	}
	if os.Getenv("FLEETDECK_RUNTIME_DESCENDANT") == "1" {
		signal.Ignore(syscall.SIGTERM, syscall.SIGHUP)
		ready := os.NewFile(3, "ready")
		_, _ = ready.Write([]byte("ready"))
		_ = ready.Close()
		for {
			time.Sleep(time.Hour)
		}
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		switch scanner.Text() {
		case "size":
			size, err := unix.IoctlGetWinsize(0, unix.TIOCGWINSZ)
			if err != nil {
				os.Exit(3)
			}
			_, _ = fmt.Fprintf(os.Stdout, "SIZE=%dx%d\n", size.Col, size.Row)
		case "flood":
			_, _ = fmt.Fprint(os.Stdout, strings.Repeat("x", 128*1024)+"FLOOD_DONE\n")
		case "env":
			_, _ = fmt.Fprintf(os.Stdout, "ENV=%s|%s|%s\n", os.Getenv("TERM"), os.Getenv("CODEX_THREAD_ID"), os.Getenv("CODEX_INTERNAL_ORIGINATOR_OVERRIDE"))
		case "tui":
			// A synchronized-output frame on start and after every resize,
			// the way a full-screen TUI clears and redraws on SIGWINCH.
			frame := func() {
				size, _ := unix.IoctlGetWinsize(0, unix.TIOCGWINSZ)
				_, _ = fmt.Fprintf(os.Stdout, "\x1b[?2026h\x1b[2J\x1b[HFRAME=%dx%d\x1b[?2026l", size.Col, size.Row)
			}
			winch := make(chan os.Signal, 1)
			signal.Notify(winch, syscall.SIGWINCH)
			go func() {
				for range winch {
					frame()
				}
			}()
			frame()
		case "quit":
			os.Exit(0)
		case "finish":
			_, _ = fmt.Fprintln(os.Stdout, "FINAL_OUTPUT")
			os.Exit(0)
		case "descendant":
			binary, _ := os.Executable()
			readReady, writeReady, err := os.Pipe()
			if err != nil {
				os.Exit(5)
			}
			child := exec.Command(binary, "-test.run=^TestPTYChild$")
			child.Env = append(os.Environ(), "FLEETDECK_RUNTIME_DESCENDANT=1")
			child.Stdin, child.Stdout, child.Stderr = os.Stdin, os.Stdout, os.Stderr
			child.ExtraFiles = []*os.File{writeReady}
			if err := child.Start(); err != nil {
				os.Exit(6)
			}
			_ = writeReady.Close()
			var ready [5]byte
			if _, err := io.ReadFull(readReady, ready[:]); err != nil {
				os.Exit(7)
			}
			_, _ = fmt.Fprintf(os.Stdout, "DESCENDANT_PID=%d\n", child.Process.Pid)
			os.Exit(0)
		}
	}
	os.Exit(0)
}

func TestPTYDrainsFinalOutputBeforeEOF(t *testing.T) {
	p := testPTY(t)
	v := p.attach(0)
	defer func() { _ = v.Close() }()
	if _, err := v.Write([]byte("finish\n")); err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(v)
	if err != nil || !strings.Contains(string(raw), "FINAL_OUTPUT") {
		t.Fatalf("final output lost: %q, err=%v", raw, err)
	}
}

func TestPTYClosesDescendantsAfterLeaderExits(t *testing.T) {
	p := testPTY(t)
	v := p.attach(0)
	defer func() { _ = v.Close() }()
	if _, err := v.Write([]byte("descendant\n")); err != nil {
		t.Fatal(err)
	}
	waitPTY(t, p, "DESCENDANT_PID=")
	raw, _, _, _ := p.output(0)
	_, rest, _ := strings.Cut(string(raw), "DESCENDANT_PID=")
	pid, err := strconv.Atoi(strings.TrimSpace(strings.SplitN(rest, "\n", 2)[0]))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if pid > 0 {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
	select {
	case <-p.done:
	case <-time.After(time.Second):
		t.Fatal("leader never exited")
	}
	p.Close()
	deadline := time.Now().Add(time.Second)
	for !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
		if time.Now().After(deadline) {
			t.Fatal("PTY descendant survived its owner")
		}
		time.Sleep(5 * time.Millisecond)
	}
	pid = -1
}

func testPTY(t *testing.T) *terminal {
	t.Helper()
	t.Setenv("FLEETDECK_RUNTIME_PTY_TEST", "1")
	// Race-instrumented helper processes otherwise delay os.Exit by a second.
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	p, err := startTerminal(ctx, []string{binary, "-test.run=^TestPTYChild$"}, os.Environ(), t.TempDir(), 80, 24, 4096)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

func waitPTY(t *testing.T, p *terminal, marker string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for {
		data, _, _, changed := p.output(0)
		if strings.Contains(string(data), marker) {
			return
		}
		select {
		case <-changed:
		case <-p.readDone:
			data, _, _, _ = p.output(0)
			if strings.Contains(string(data), marker) {
				return
			}
			t.Fatalf("PTY ended before %q", marker)
		case <-ctx.Done():
			t.Fatalf("PTY never printed %q", marker)
		}
	}
}

func TestPTYContinuesWithoutAttachedViews(t *testing.T) {
	p := testPTY(t)
	v := p.attach(0)
	if _, err := v.Write([]byte("flood\n")); err != nil {
		t.Fatal(err)
	}
	if err := v.Close(); err != nil {
		t.Fatal(err)
	}
	waitPTY(t, p, "FLOOD_DONE")
	data, total, lost, _ := p.output(0)
	if !lost || len(data) > 4096 || total < 128*1024 {
		t.Fatalf("len=%d total=%d lost=%v", len(data), total, lost)
	}
	// Overflow must be explicit. An arbitrary ANSI tail is not a screen snapshot.
	v = p.attach(0)
	defer func() { _ = v.Close() }()
	if _, err := v.Read(make([]byte, 32)); !errors.Is(err, ErrReplayLost) {
		t.Fatalf("lost replay: %v", err)
	}
	select {
	case <-p.done:
		t.Fatal("detaching the view stopped its process")
	default:
	}
}

func TestPTYLeaseControlsInputAndResize(t *testing.T) {
	p := testPTY(t)
	a, b := p.attach(0), p.attach(0)
	defer func() { _ = b.Close() }()
	if !a.Writable() || b.Writable() {
		t.Fatal("expected one writer")
	}
	if _, err := b.Write([]byte("quit\n")); !errors.Is(err, ErrNoLease) {
		t.Fatalf("observer wrote into process: %v", err)
	}
	if err := b.Resize(context.Background(), 100, 30); !errors.Is(err, ErrNoLease) {
		t.Fatalf("observer resized process: %v", err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if !b.Writable() {
		t.Fatal("lease was not transferred")
	}
	if err := a.Resize(context.Background(), 75, 20); !errors.Is(err, ErrNoLease) {
		t.Fatalf("disconnected view resized process: %v", err)
	}
	if err := b.Resize(context.Background(), 101, 33); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Write([]byte("size\n")); err != nil {
		t.Fatal(err)
	}
	waitPTY(t, p, "SIZE=101x33")
}

func TestPTYReadIsUnblockedByDetach(t *testing.T) {
	p := testPTY(t)
	v := p.attach(0)
	ended := make(chan error, 1)
	go func() { _, err := v.Read(make([]byte, 8)); ended <- err }()
	if err := v.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-ended:
		if err == nil {
			t.Fatal("closed view read succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("quiet view read did not unblock")
	}
}

func TestPTYReaderStartsAtItsOwnByteOffset(t *testing.T) {
	p := testPTY(t)
	a := p.attach(0)
	defer func() { _ = a.Close() }()
	if _, err := a.Write([]byte("size\n")); err != nil {
		t.Fatal(err)
	}
	waitPTY(t, p, "SIZE=80x24")
	_, offset, _, _ := p.output(0)
	b := p.attach(offset)
	defer func() { _ = b.Close() }()
	if _, err := a.Write([]byte("env\n")); err != nil {
		t.Fatal(err)
	}
	var got strings.Builder
	for !strings.Contains(got.String(), "ENV=xterm-256color||") {
		buf := make([]byte, 128)
		n, err := b.Read(buf)
		if err != nil {
			t.Fatal(err)
		}
		got.Write(buf[:n])
	}
	if strings.Contains(got.String(), "SIZE=") {
		t.Fatalf("reader replayed output preceding its offset: %q", got.String())
	}
}

func TestPTYStartsWithIndependentTerminalIdentity(t *testing.T) {
	t.Setenv("TERM", "dumb")
	t.Setenv("CODEX_THREAD_ID", "parent-task")
	t.Setenv("CODEX_INTERNAL_ORIGINATOR_OVERRIDE", "parent-origin")
	p := testPTY(t)
	v := p.attach(0)
	defer func() { _ = v.Close() }()
	if _, err := v.Write([]byte("env\n")); err != nil {
		t.Fatal(err)
	}
	waitPTY(t, p, "ENV=xterm-256color||")
}

func TestPTYRepaintAfterOverflowStartsAtACompleteFrame(t *testing.T) {
	p := testPTY(t)
	v := p.attach(0)
	defer func() { _ = v.Close() }()
	if _, err := v.Write([]byte("tui\n")); err != nil {
		t.Fatal(err)
	}
	waitPTY(t, p, "FRAME=80x24")
	if _, err := v.Write([]byte("flood\n")); err != nil {
		t.Fatal(err)
	}
	waitPTY(t, p, "FLOOD_DONE")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	from, err := p.repaint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	all, total, _, _ := p.output(0)
	oldest := total - uint64(len(all))
	if from < oldest+uint64(len(frameEnd)) || string(all[from-oldest-uint64(len(frameEnd)):from-oldest]) != frameEnd {
		t.Fatal("replay does not start right after a complete frame")
	}
	// The redraw at the restored geometry arrives after the boundary, so a
	// blank screen replayed from there ends with the current full frame.
	deadline := time.After(3 * time.Second)
	for {
		data, _, lost, changed := p.output(from)
		if lost {
			t.Fatal("repaint boundary fell out of the buffer")
		}
		if strings.HasSuffix(string(data), "FRAME=80x24"+frameEnd) && strings.Contains(string(data), "\x1b[2J") {
			break
		}
		select {
		case <-changed:
		case <-deadline:
			t.Fatalf("no full redraw after the boundary: %q", data)
		}
	}
	if _, err := v.Write([]byte("size\n")); err != nil {
		t.Fatal(err)
	}
	waitPTY(t, p, "SIZE=80x24")
}
