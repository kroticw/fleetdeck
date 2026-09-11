package supervisor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// lsofPath finds lsof by absolute path first: an app started from the Dock
// has only the system's four directories as PATH, and on macOS lsof is in
// /usr/sbin.
func lsofPath() (string, error) {
	for _, p := range []string{"/usr/sbin/lsof", "/usr/bin/lsof"} {
		if FileExists(p) {
			return p, nil
		}
	}
	return exec.LookPath("lsof")
}

// holderGone bounds the wait for a panel that has been killed to let go of
// its port. SIGKILL is not refused; this only covers the kernel closing the
// socket.
const holderGone = 5 * time.Second

// StopHolder stops the fleetdeck panel that answers at panelURL, whoever
// started it -- this window, an earlier one, a terminal. An update replaces
// the panel that holds the port, not only one it knows the PID of.
//
// The PID is the kernel's answer to "who listens on this port" (lsof), not
// anything the holder says about itself. And only a holder that answers as a
// fleetdeck panel is stopped: anything else on the port is left alone and
// reported, never signalled. Stopping means SIGTERM first, as Panel.Stop
// does, and SIGKILL only after grace; done means nothing listens on the port
// any more -- which is what the next panel needs, and which a finished process
// its parent has not yet reaped satisfies too.
//
// Nothing answering at panelURL is nothing to stop, and not an error.
func StopHolder(ctx context.Context, panelURL string, grace time.Duration) error {
	if !answers(ctx, panelURL) {
		return nil
	}
	if !isPanel(ctx, panelURL) {
		return fmt.Errorf("what answers at %s is not a fleetdeck panel; it has been left alone", panelURL)
	}
	port, err := portOf(panelURL)
	if err != nil {
		return err
	}
	pid, err := listenerPID(ctx, port)
	if err != nil {
		return err
	}
	if pid == 0 {
		return nil
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("stop the panel (pid %d): %w", pid, err)
	}
	if portFreed(ctx, port, grace) {
		return nil
	}
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("kill the panel (pid %d): %w", pid, err)
	}
	if portFreed(ctx, port, holderGone) {
		return nil
	}
	return fmt.Errorf("the panel (pid %d) still holds port %d after SIGKILL", pid, port)
}

// isPanel reports whether panelURL answers as a fleetdeck panel: a snapshot
// that carries a build fingerprint.
func isPanel(ctx context.Context, panelURL string) bool {
	u, err := url.Parse(panelURL)
	if err != nil {
		return false
	}
	u.Path = "/api/snapshot"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return false
	}
	resp, err := (&http.Client{Timeout: answerTimeout}).Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	var snap struct {
		Build *json.RawMessage `json:"build"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&snap) != nil {
		return false
	}
	return snap.Build != nil
}

func portOf(panelURL string) (int, error) {
	u, err := url.Parse(panelURL)
	if err != nil {
		return 0, err
	}
	_, p, err := net.SplitHostPort(u.Host)
	if err != nil {
		return 0, fmt.Errorf("no port in %s: %w", panelURL, err)
	}
	return strconv.Atoi(p)
}

// listenerPID is the process listening on the TCP port, or 0 when none is.
func listenerPID(ctx context.Context, port int) (int, error) {
	lsof, err := lsofPath()
	if err != nil {
		return 0, fmt.Errorf("find who holds port %d: %w", port, err)
	}
	var out, errOut bytes.Buffer
	cmd := exec.CommandContext(ctx, lsof, "-nP", "-t", fmt.Sprintf("-iTCP:%d", port), "-sTCP:LISTEN")
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		// lsof exits 1 when it finds nothing, and says nothing.
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 && strings.TrimSpace(errOut.String()) == "" {
			return 0, nil
		}
		return 0, fmt.Errorf("lsof on port %d: %w %s", port, err, strings.TrimSpace(errOut.String()))
	}
	pids := map[int]bool{}
	for _, f := range strings.Fields(out.String()) {
		if pid, err := strconv.Atoi(f); err == nil {
			pids[pid] = true
		}
	}
	switch len(pids) {
	case 0:
		return 0, nil
	case 1:
		for pid := range pids {
			return pid, nil
		}
	}
	return 0, fmt.Errorf("%d processes listen on port %d; not choosing one to stop", len(pids), port)
}

// portFreed waits up to d for nothing to listen on port.
func portFreed(ctx context.Context, port int, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for {
		if pid, err := listenerPID(ctx, port); err == nil && pid == 0 {
			return true
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
}
