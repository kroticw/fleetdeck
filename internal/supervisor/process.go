package supervisor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

// Panel is a panel process this supervisor started. The supervisor stops
// only what it started, by PID -- never by matching a process name, which is
// how the old rebuild script stopped panels and how it would stop the wrong
// one the day two installs coexist.
type Panel struct {
	PID  int
	done chan struct{}
	err  error // how the process ended; set before done is closed
}

// Exited is closed when the panel process has ended.
func (p *Panel) Exited() <-chan struct{} { return p.done }

// Err is how the panel process ended, as exec reports it: "exit status 3",
// "signal: killed". It waits for the process to end.
func (p *Panel) Err() error {
	<-p.done
	return p.err
}

// StartPanel starts the panel binary in a session of its own, as the starter's
// own direct child.
//
// Its own session keeps signals meant for the window -- a Ctrl+C in the
// terminal a window was started from -- off the panel: a window's panel goes
// when its window goes (the operator's rule, 2026-09-11), and it goes by its
// own graceful shutdown, having watched its parent (cmd/fleetdeck, owner.go).
// That is also why the panel is started directly, never through a shell: the
// panel watches its parent, and a process in between would be the one it
// watched. TestAStartedPanelIsTheStartersOwnChild holds this.
//
// Output goes to logPath, appended. The process is reaped in the background,
// so an exited panel does not linger as a zombie while the window runs on.
func StartPanel(bin string, args, env []string, logPath string) (*Panel, error) {
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return nil, err
	}
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(bin, args...)
	cmd.Env = env
	cmd.Stdout = log
	cmd.Stderr = log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		_ = log.Close()
		return nil, fmt.Errorf("start the panel: %w", err)
	}
	_ = log.Close()
	p := &Panel{PID: cmd.Process.Pid, done: make(chan struct{})}
	go func() {
		p.err = cmd.Wait()
		close(p.done)
	}()
	return p, nil
}

// Stop asks the panel to go with SIGTERM and, if it is still there after
// grace, kills it. A panel left holding the port is a new panel that can
// never start.
func (p *Panel) Stop(grace time.Duration) error {
	if err := syscall.Kill(p.PID, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("stop the panel: %w", err)
	}
	select {
	case <-p.done:
		return nil
	case <-time.After(grace):
	}
	if err := syscall.Kill(p.PID, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("kill the panel: %w", err)
	}
	select {
	case <-p.done:
		return nil
	case <-time.After(5 * time.Second):
		return fmt.Errorf("the panel (pid %d) did not exit even after SIGKILL", p.PID)
	}
}

// WaitAnswer waits until something answers HTTP at url, whatever the status:
// any answer means the panel is up and listening. It gives up when ctx does.
func WaitAnswer(ctx context.Context, url string) error {
	client := &http.Client{Timeout: 500 * time.Millisecond}
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		if resp, err := client.Do(req); err == nil {
			_ = resp.Body.Close()
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("nothing answered at %s: %w", url, ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// ErrBusy: another update of the same tree is already running -- from another
// window, a terminal, a second press of the button.
var ErrBusy = errors.New("an update of this tree is already running")

// Acquire takes the update lock at path and returns the function that
// releases it. The lock is an flock, so it is released by the kernel if the
// holder dies; a crashed update never leaves the next one locked out.
func Acquire(path string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrBusy
		}
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}

// LockPath is where the update lock for a tree lives: in the user's cache
// directory, named after the tree. Not inside the tree -- a file there would
// mark every build from it as built from a modified tree.
func LockPath(treeDir string) (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(treeDir)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(abs))
	return filepath.Join(cache, "fleetdeck", "update-"+hex.EncodeToString(sum[:6])+".lock"), nil
}
