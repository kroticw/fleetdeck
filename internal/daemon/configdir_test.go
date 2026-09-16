package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"testing"
)

// daemonDirFor repeats, in the test, the naming rule SocketPathFor implements, so a
// change to that rule fails here instead of being mirrored silently by a helper both
// sides share.
func daemonDirFor(t *testing.T, base, configDir string) string {
	t.Helper()
	current, err := user.Current()
	if err != nil {
		t.Fatalf("current user: %v", err)
	}
	sum := sha256.Sum256([]byte(configDir))
	return filepath.Join(base, "cc-daemon-"+current.Uid, hex.EncodeToString(sum[:])[:8])
}

// plantLiveSocket puts a listening control socket in dir and keeps it accepting for
// the rest of the test: resolveSocketCandidate probes liveness by dialing, so a socket
// file alone would be rejected as a crashed daemon's leftover.
func plantLiveSocket(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("create daemon dir: %v", err)
	}
	path := filepath.Join(dir, "control.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	return path
}

// writeControlKey plants a key file shaped the way ControlKeyIn demands: 0600 inside a
// 0700 daemon directory.
func writeControlKey(t *testing.T, configDir, key string) {
	t.Helper()
	dir := filepath.Join(configDir, "daemon")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("create daemon dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "control.key"), []byte(key+"\n"), 0o600); err != nil {
		t.Fatalf("write control key: %v", err)
	}
}

func TestControlKeyInReadsTheKeyOfTheDirectoryItIsGiven(t *testing.T) {
	configDir := t.TempDir()
	writeControlKey(t, configDir, "0123456789abcdef")

	got, err := ControlKeyIn(configDir)
	if err != nil {
		t.Fatalf("ControlKeyIn: %v", err)
	}
	if got != "0123456789abcdef" {
		t.Fatalf("key = %q, want %q", got, "0123456789abcdef")
	}
}

// Two config directories hold two different keys, and each daemon accepts only its
// own: reading the wrong one is the failure this whole parameter exists to stop.
func TestControlKeyInDoesNotFallBackToAnotherDirectory(t *testing.T) {
	mine := t.TempDir()
	theirs := t.TempDir()
	writeControlKey(t, theirs, "not-mine")

	_, err := ControlKeyIn(mine)
	if !errors.Is(err, ErrNoControlKey) {
		t.Fatalf("err = %v, want ErrNoControlKey", err)
	}
}

func TestControlKeyInRefusesAKeyFileOthersCanRead(t *testing.T) {
	configDir := t.TempDir()
	writeControlKey(t, configDir, "readable-by-everyone")
	if err := os.Chmod(filepath.Join(configDir, "daemon", "control.key"), 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	if _, err := ControlKeyIn(configDir); !errors.Is(err, ErrNoControlKey) {
		t.Fatalf("err = %v, want ErrNoControlKey", err)
	}
}

func TestSocketPathForNamesTheDaemonServingThatConfigDir(t *testing.T) {
	base := shortTempDir(t)
	orig := socketGlobBase
	socketGlobBase = base
	t.Cleanup(func() { socketGlobBase = orig })

	configDir := "/some/where/.claude"
	want := plantLiveSocket(t, daemonDirFor(t, base, configDir))

	got, err := SocketPathFor(configDir)
	if err != nil {
		t.Fatalf("SocketPathFor: %v", err)
	}
	if got != want {
		t.Fatalf("socket = %q, want %q", got, want)
	}
}

// The case the whole function exists for: two daemons are live at once — one per
// config directory — and the one belonging to another directory must never be
// answered with, however it sorts. Its key would be refused and every write would
// fail against a fleet that is not the one on screen.
func TestSocketPathForIgnoresADaemonOfAnotherConfigDir(t *testing.T) {
	base := shortTempDir(t)
	orig := socketGlobBase
	socketGlobBase = base
	t.Cleanup(func() { socketGlobBase = orig })

	plantLiveSocket(t, daemonDirFor(t, base, "/some/where/else/.claude"))

	_, err := SocketPathFor("/some/where/.claude")
	if !errors.Is(err, ErrDaemonUnavailable) {
		t.Fatalf("err = %v, want ErrDaemonUnavailable", err)
	}
}

// A client built for a config directory keeps resolving against that directory alone,
// so a daemon restart recovers to the same fleet rather than to whichever daemon
// happens to be live.
func TestDiscoverInResolvesOnlyItsOwnConfigDir(t *testing.T) {
	base := shortTempDir(t)
	orig := socketGlobBase
	socketGlobBase = base
	t.Cleanup(func() { socketGlobBase = orig })

	configDir := "/some/where/.claude"
	want := plantLiveSocket(t, daemonDirFor(t, base, configDir))
	plantLiveSocket(t, daemonDirFor(t, base, "/some/where/else/.claude"))

	c := DiscoverIn(configDir, nil)
	got, err := c.resolve()
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != want {
		t.Fatalf("resolved %q, want %q", got, want)
	}
}
