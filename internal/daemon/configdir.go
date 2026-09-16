package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
)

// A Claude Code installation is identified by its configuration directory, and more
// than one can be installed on a machine at once: Claude Code reads CLAUDE_CONFIG_DIR
// and falls back to ~/.claude, so a wrapper command that exports a different value is
// a second, fully separate installation with its own daemon, its own job store, its
// own transcripts and its own control key.
//
// Everything this package needs from an installation follows from that one directory,
// which is why it is the parameter carried around rather than four separate paths.

// DefaultConfigDir is ~/.claude, the installation Claude Code uses when nothing says
// otherwise.
//
// CLAUDE_CONFIG_DIR is deliberately not read here, even though that is what Claude
// Code itself reads. A panel launched from the Dock inherits no environment at all, so
// honouring the variable would mean the same machine shows one fleet when the panel is
// started from a terminal that exports it and another when it is started from the Dock
// — a silent switch between two fleets, decided by how the panel happened to be
// launched. An installation other than this one is named in the configuration file,
// which reads the same from anywhere.
func DefaultConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".claude"
	}
	return filepath.Join(home, ".claude")
}

// daemonDirName is the runtime directory Claude Code's daemon creates for the
// installation rooted at configDir: the first eight hex digits of the SHA-256 of that
// directory's path.
//
// This is a foreign format, the same kind of dependency internal/jobs carries and for
// the same reason — nothing in the daemon's protocol answers "which socket belongs to
// this installation", and the alternative is answering with whichever socket sorts
// first, which on a machine with two installations is a coin flip. Verified on
// 2026-09-15 against two installations live at once, each matching the directory
// Claude Code had actually created. If Claude Code changes the rule, SocketPathFor
// stops finding a socket and says the daemon is unavailable; it never silently
// answers with another installation's.
func daemonDirName(configDir string) string {
	sum := sha256.Sum256([]byte(configDir))
	return hex.EncodeToString(sum[:])[:8]
}

// SocketPathFor returns the control socket of the daemon serving configDir, checked
// for safe ownership and liveness exactly as SocketPath checks the candidates it
// globs. A live daemon of another installation is not an answer here: its key file is
// a different one, so every write against it would be refused, and every read would
// describe a fleet other than the one on screen.
func SocketPathFor(configDir string) (string, error) {
	current, err := user.Current()
	if err != nil {
		return "", err
	}
	candidate := filepath.Join(socketGlobBase, fmt.Sprintf("cc-daemon-%s", current.Uid), daemonDirName(configDir), "control.sock")
	return resolveSocketCandidate([]string{candidate})
}

// ControlKeyIn reads the control key of the installation rooted at configDir. It is
// ControlKey's implementation; see ControlKey for what the returned error does and
// does not say.
func ControlKeyIn(configDir string) (string, error) {
	keyPath := filepath.Join(configDir, "daemon", "control.key")

	if err := checkKeyFileSecurity(keyPath, os.Getuid()); err != nil {
		return "", wrapNoControlKey(err)
	}

	data, err := os.ReadFile(keyPath)
	if err != nil {
		return "", wrapNoControlKey(err)
	}

	key := strings.TrimSpace(string(data))
	if key == "" {
		return "", wrapNoControlKey(errors.New("control key file is empty"))
	}

	return key, nil
}

// DiscoverIn is Discover for one named installation: the client re-resolves against
// configDir's own daemon alone, so a daemon restart recovers to the same fleet instead
// of to whichever daemon happens to be live at that moment.
func DiscoverIn(configDir string, key func() (string, error)) *Client {
	c := New("", key)
	c.discoverable = true
	c.resolve = func() (string, error) { return SocketPathFor(configDir) }
	if path, err := c.resolve(); err == nil {
		c.socketPath = path
	}
	return c
}
