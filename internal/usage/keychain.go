// Package usage reads account rate-limit windows from the Anthropic OAuth usage
// endpoint. The token is read from the macOS Keychain, sent only to that
// endpoint, never logged and never stored.
package usage

import (
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"os/user"
	"strings"
)

// ErrNoToken means no OAuth token could be read from the Keychain.
var ErrNoToken = errors.New("no oauth token in keychain")

// KeychainToken reads the Claude Code OAuth token of the current user.
func KeychainToken() (string, error) {
	u, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("current user: %w", err)
	}
	out, err := exec.Command("security", "find-generic-password",
		"-s", "Claude Code-credentials", "-a", u.Username, "-w").Output()
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
