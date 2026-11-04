package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

// terminalProbe owns its child PTY independently of output consumers.
// Its bounded byte tail is for measurements, not a screen snapshot.
type terminalProbe struct {
	master    *os.File
	cmd       *exec.Cmd
	mu        sync.Mutex
	tail      []byte
	total     uint64
	limit     int
	changed   chan struct{}
	done      chan struct{}
	readDone  chan struct{}
	closeOnce sync.Once
}

func startTerminal(ctx context.Context, argv []string, cwd string, cols, rows, limit int) (*terminalProbe, error) {
	if len(argv) == 0 || !filepath.IsAbs(argv[0]) {
		return nil, errors.New("terminal requires an absolute executable")
	}
	if !filepath.IsAbs(cwd) || cols < 1 || cols > 500 || rows < 1 || rows > 200 || limit < 1 || limit > 4<<20 {
		return nil, errors.New("invalid terminal directory, geometry or buffer limit")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = cwd
	cmd.Env = terminalEnv(os.Environ())
	master, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		return nil, fmt.Errorf("start terminal: %w", err)
	}
	// os.NewFile must observe O_NONBLOCK when it initializes its poll descriptor.
	// Merely changing the flag on the existing os.File makes a quiet Read fail
	// with EAGAIN instead of waiting through Go's poller.
	fd, duplicateErr := unix.FcntlInt(master.Fd(), unix.F_DUPFD_CLOEXEC, 0)
	if duplicateErr == nil {
		duplicateErr = unix.SetNonblock(fd, true)
	}
	if duplicateErr != nil {
		if fd >= 0 {
			_ = unix.Close(fd)
		}
		_ = master.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, fmt.Errorf("prepare terminal polling: %w", duplicateErr)
	}
	_ = master.Close()
	master = os.NewFile(uintptr(fd), "probe-pty")
	t := &terminalProbe{master: master, cmd: cmd, limit: limit, changed: make(chan struct{}, 1), done: make(chan struct{}), readDone: make(chan struct{})}
	go t.read()
	go func() { _ = cmd.Wait(); close(t.done) }()
	return t, nil
}

func (t *terminalProbe) read() {
	defer close(t.readDone)
	buf := make([]byte, 32*1024)
	for {
		n, err := t.master.Read(buf)
		if n > 0 {
			t.mu.Lock()
			t.total += uint64(n)
			t.tail = append(t.tail, buf[:n]...)
			if len(t.tail) > t.limit {
				copy(t.tail, t.tail[len(t.tail)-t.limit:])
				t.tail = t.tail[:t.limit]
			}
			t.mu.Unlock()
			select {
			case t.changed <- struct{}{}:
			default:
			}
		}
		if err != nil {
			return
		}
	}
}

// Output reports overflow explicitly; callers must not treat a byte tail as a screen.
func (t *terminalProbe) Output(since uint64) ([]byte, uint64, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	oldest := t.total - uint64(len(t.tail))
	dropped := since < oldest
	if since < oldest {
		since = oldest
	}
	if since > t.total {
		since = t.total
	}
	return append([]byte(nil), t.tail[since-oldest:]...), t.total, dropped
}
func (t *terminalProbe) Resize(cols, rows int) error {
	if cols < 1 || cols > 500 || rows < 1 || rows > 200 {
		return errors.New("invalid terminal geometry")
	}
	return pty.Setsize(t.master, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
}
func (t *terminalProbe) Close() {
	t.closeOnce.Do(func() {
		// This closes only this probe's process group, never the shared Codex daemon.
		select {
		case <-t.done:
		default:
			_ = syscall.Kill(-t.cmd.Process.Pid, syscall.SIGKILL)
		}
		_ = t.master.Close()
		<-t.done
		<-t.readDone
	})
}

// A browser-backed xterm must not inherit a noninteractive host terminal or
// the parent coding agent's task identity. Other user configuration is retained.
func terminalEnv(base []string) []string {
	env := make([]string, 0, len(base)+1)
	for _, entry := range base {
		key, _, _ := strings.Cut(entry, "=")
		switch key {
		case "TERM", "CODEX_THREAD_ID", "CODEX_INTERNAL_ORIGINATOR_OVERRIDE":
			continue
		}
		env = append(env, entry)
	}
	return append(env, "TERM=xterm-256color")
}
