// Package executor defines the identities shared by agent integrations.
// Provider-specific protocols and process management belong in their adapters.
package executor

import (
	"errors"
	"strings"
)

// ID selects the coding agent that executes a card.
type ID string

const (
	// Claude selects the existing Claude Code integration.
	Claude ID = "claude"
	// Codex selects the Codex integration.
	Codex ID = "codex"
)

var (
	// ErrUnknownExecutor means a selection names an unsupported agent.
	ErrUnknownExecutor = errors.New("unknown executor")
	// ErrInvalidKey means a session reference is neither a legacy Claude ID nor a Codex key.
	ErrInvalidKey = errors.New("invalid session key")
)

// Key identifies a session across all executors. Claude short IDs retain their
// existing spelling; Codex sessions use codex:<Fleetdeck runtime UUID>.
type Key string

// ParseID preserves the legacy default only when no executor was specified.
func ParseID(raw string) (ID, error) {
	switch ID(raw) {
	case "", Claude:
		return Claude, nil
	case Codex:
		return Codex, nil
	default:
		return "", ErrUnknownExecutor
	}
}

// ParseKey resolves a reference without guessing its executor from a model or role.
func ParseKey(raw string) (ID, Key, error) {
	if suffix, found := strings.CutPrefix(raw, "codex:"); found {
		if !validUUID(suffix) {
			return "", "", ErrInvalidKey
		}
		return Codex, Key("codex:" + strings.ToLower(suffix)), nil
	}
	if len(raw) >= 6 && len(raw) <= 12 && hexadecimal(raw) {
		return Claude, Key(raw), nil
	}
	return "", "", ErrInvalidKey
}

func hexadecimal(raw string) bool {
	for i := 0; i < len(raw); i++ {
		b := raw[i]
		switch {
		case b >= '0' && b <= '9', b >= 'a' && b <= 'f', b >= 'A' && b <= 'F':
		default:
			return false
		}
	}
	return true
}
func validUUID(raw string) bool {
	if len(raw) != 36 {
		return false
	}
	for i := 0; i < len(raw); i++ {
		switch i {
		case 8, 13, 18, 23:
			if raw[i] != '-' {
				return false
			}
		default:
			if !hexadecimal(raw[i : i+1]) {
				return false
			}
		}
	}
	return true
}
