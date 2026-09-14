// Package usage reads account rate-limit windows from the Anthropic OAuth usage
// endpoint. The token is read from the macOS Keychain, sent only to that
// endpoint, never logged and never stored.
package usage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"os/user"
	"strings"
	"time"
)

// ErrNoToken means no OAuth token could be read from the Keychain.
var ErrNoToken = errors.New("no oauth token in keychain")

// KeychainToken reads the Claude Code OAuth token of the current user.
//
// The lookup runs `security`, which takes as long as the keychain makes it, and
// ends when ctx does. A panel told to stop waits for the collect cycle it is
// in, and an update gives the panel it replaces no time to wait out a keychain
// (T-060): on a stand, a SIGTERM during a lookup that took 1 s waited for it.
func KeychainToken(ctx context.Context) (string, error) {
	u, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("current user: %w", err)
	}
	cmd := exec.CommandContext(ctx, "security", "find-generic-password",
		"-s", "Claude Code-credentials", "-a", u.Username, "-w")
	// Output waits for the output pipe to close as well as for the process: a
	// lookup that leaves a child holding the pipe would keep that wait going
	// after ctx has ended the lookup.
	cmd.WaitDelay = 100 * time.Millisecond
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%w: keychain lookup failed", ErrNoToken)
	}
	var creds struct {
		ClaudeAIOAuth struct {
			AccessToken string `json:"accessToken"`
		} `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(out))), &creds); err != nil {
		return "", fmt.Errorf("%w: credentials are not JSON", ErrNoToken)
	}
	if creds.ClaudeAIOAuth.AccessToken == "" {
		return "", fmt.Errorf("%w: credentials carry no access token", ErrNoToken)
	}
	return creds.ClaudeAIOAuth.AccessToken, nil
}
