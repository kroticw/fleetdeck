// Package codexruntime owns Codex processes independently of HTTP panels.
package codexruntime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

var (
	ErrReplayLost = errors.New("terminal replay overflow; a fresh screen is required")
	ErrNoLease    = errors.New("terminal view does not own the input lease")
)

// terminal drains its PTY regardless of attached views. Its bounded tail is a
// byte stream, never a screen snapshot: an outpaced view fails explicitly.
type terminal struct {
	master         *os.File
	cmd            *exec.Cmd
	mu             sync.Mutex
	tail           []byte
	total          uint64
	limit          int
	changed        chan struct{}
	leaseMu        sync.Mutex
	views          map[uint64]*terminalView
	next, writer   uint64
	closing        bool
	done, readDone chan struct{}
	closeOnce      sync.Once
}

func startTerminal(ctx context.Context, argv []string, cwd string, cols, rows, limit int) (*terminal, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(argv) == 0 || !filepath.IsAbs(argv[0]) || !filepath.IsAbs(cwd) || !validGeometry(cols, rows) || limit < 1 || limit > 4<<20 {
		return nil, errors.New("invalid terminal executable, directory, geometry or buffer limit")
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir, cmd.Env = cwd, terminalEnv(os.Environ())
	master, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		return nil, fmt.Errorf("start Codex terminal: %w", err)
	}
	// Rewrap a nonblocking, close-on-exec descriptor so Go's poller can
	// unblock a quiet Read on Close. Plain Dup would leak it to later children.
	fd, err := unix.FcntlInt(master.Fd(), unix.F_DUPFD_CLOEXEC, 0)
	if err == nil {
		err = unix.SetNonblock(fd, true)
	}
	if err != nil {
		if fd >= 0 {
			_ = unix.Close(fd)
		}
		_ = master.Close()
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = cmd.Wait()
		return nil, fmt.Errorf("prepare terminal polling: %w", err)
	}
	_ = master.Close()
	master = os.NewFile(uintptr(fd), "codex-pty")
	t := &terminal{master: master, cmd: cmd, limit: limit, changed: make(chan struct{}), views: make(map[uint64]*terminalView), done: make(chan struct{}), readDone: make(chan struct{})}
	go t.read()
	go func() {
		_ = cmd.Wait()
		// The leader's exit does not prove its children have exited. Reap
		// the owned group now, while its identity is still tied to this run,
		// rather than signalling a potentially reused PID on a later Close.
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		close(t.done)
	}()
	go func() {
		select {
		case <-ctx.Done():
			t.Close()
		case <-t.done:
		}
	}()
	return t, nil
}

func (t *terminal) read() {
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
			close(t.changed)
			t.changed = make(chan struct{})
			t.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

func (t *terminal) output(since uint64) ([]byte, uint64, bool, <-chan struct{}) {
	t.mu.Lock()
	defer t.mu.Unlock()
	oldest := t.total - uint64(len(t.tail))
	lost := since < oldest
	if since < oldest {
		since = oldest
	}
	if since > t.total {
		since = t.total
	}
	return append([]byte(nil), t.tail[since-oldest:]...), t.total, lost, t.changed
}

func (t *terminal) attach(offset uint64) *terminalView {
	t.leaseMu.Lock()
	defer t.leaseMu.Unlock()
	t.next++
	v := &terminalView{terminal: t, id: t.next, offset: offset, done: make(chan struct{})}
	t.views[v.id] = v
	if t.writer == 0 {
		t.writer = v.id
	}
	return v
}

func (t *terminal) Close() {
	t.closeOnce.Do(func() {
		t.leaseMu.Lock()
		t.closing = true
		t.leaseMu.Unlock()
		// Only this TUI's process group is ours. The shared native daemon is
		// deliberately outside it and must never be terminated here.
		select {
		case <-t.done:
		default:
			_ = syscall.Kill(-t.cmd.Process.Pid, syscall.SIGTERM)
			select {
			case <-t.done:
			case <-time.After(time.Second):
				_ = syscall.Kill(-t.cmd.Process.Pid, syscall.SIGKILL)
			}
		}
		_ = t.master.Close()
		<-t.done
		<-t.readDone
	})
}

type terminalView struct {
	terminal  *terminal
	id        uint64
	readMu    sync.Mutex
	offset    uint64
	done      chan struct{}
	closeOnce sync.Once
}

func (v *terminalView) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	v.readMu.Lock()
	defer v.readMu.Unlock()
	ended := false
	for {
		select {
		case <-v.done:
			return 0, io.ErrClosedPipe
		default:
		}
		data, _, lost, changed := v.terminal.output(v.offset)
		if lost {
			return 0, ErrReplayLost
		}
		if len(data) > 0 {
			n := copy(p, data)
			v.offset += uint64(n)
			return n, nil
		}
		if ended {
			return 0, io.EOF
		}
		select {
		case <-v.done:
			return 0, io.ErrClosedPipe
		case <-v.terminal.readDone:
			// Re-read after the pump has finished: its final append and this
			// signal can become ready together.
			ended = true
		case <-changed:
		}
	}
}

func (v *terminalView) ownsLease() bool {
	if v.terminal.closing || v.terminal.writer != v.id {
		return false
	}
	select {
	case <-v.done:
		return false
	case <-v.terminal.done:
		return false
	default:
		return true
	}
}

func (v *terminalView) Writable() bool {
	v.terminal.leaseMu.Lock()
	defer v.terminal.leaseMu.Unlock()
	return v.ownsLease()
}

func (v *terminalView) Write(p []byte) (int, error) {
	t := v.terminal
	t.leaseMu.Lock()
	defer t.leaseMu.Unlock()
	if !v.ownsLease() {
		return 0, ErrNoLease
	}
	if len(p) > 64<<10 {
		return 0, errors.New("terminal input exceeds 64 KiB")
	}
	if err := t.master.SetWriteDeadline(time.Now().Add(time.Second)); err != nil {
		return 0, err
	}
	return t.master.Write(p)
}

func (v *terminalView) Resize(ctx context.Context, cols, rows int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validGeometry(cols, rows) {
		return errors.New("invalid terminal geometry")
	}
	t := v.terminal
	t.leaseMu.Lock()
	defer t.leaseMu.Unlock()
	if !v.ownsLease() {
		return ErrNoLease
	}
	return pty.Setsize(t.master, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
}

func (v *terminalView) Close() error {
	v.closeOnce.Do(func() {
		t := v.terminal
		t.leaseMu.Lock()
		defer t.leaseMu.Unlock()
		close(v.done)
		delete(t.views, v.id)
		if t.writer == v.id {
			t.writer = 0
			for id := range t.views {
				if t.writer == 0 || id < t.writer {
					t.writer = id
				}
			}
		}
	})
	return nil
}

func validGeometry(cols, rows int) bool { return cols >= 1 && cols <= 500 && rows >= 1 && rows <= 200 }

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
