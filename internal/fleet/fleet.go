// Package fleet is what one panel knows about several fleets: which fleets
// there are, which one a browser tab asked for, and which fleets claim each
// session.
//
// A fleet owns a board, its documentation and an orchestrator session. It does
// not own a list of sessions: the daemon is one per user and its sessions
// belong to nobody in particular, so which fleet a session is in is derived
// from what already exists — the fleet's orchestrator pin and the session
// field of its board's cards — never stored. A stored list would be a fourth
// source of truth next to the daemon, the transcripts and the board, and an
// orchestrator that creates sessions through MCP, past the panel, would never
// update it.
//
// Like internal/state this package performs no I/O: configuration, boards and
// the daemon reach it as arguments.
package fleet

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/kroticw/fleetdeck/internal/board"
)

// Fleet is one fleet as configuration describes it.
type Fleet struct {
	Name      string
	BoardPath string
	DocsPaths []string
	// Orchestrator is the daemon short id pinned to this fleet's orchestrator
	// column, empty when nothing is pinned yet.
	Orchestrator string
}

// fallbackName names a fleet whose board path says nothing usable.
const fallbackName = "main"

// DefaultName is the name of a fleet configuration did not name: the folder
// above its board, which for a workspace is the workspace itself
// (~/fleetdeck/board is "fleetdeck"). A fleet with no board, or with a board
// directly under the filesystem root, is "main".
func DefaultName(boardPath string) string {
	if boardPath == "" {
		return fallbackName
	}
	parent := filepath.Base(filepath.Dir(filepath.Clean(boardPath)))
	if parent == string(filepath.Separator) || parent == "." {
		return fallbackName
	}
	return parent
}

// Error is a configuration mistake in one fleet. Index is the fleet's position
// in the list Validate was given, -1 when the mistake is the list itself; the
// caller knows where in its own file that position is written and says so.
type Error struct {
	Index int
	Msg   string
}

func (e *Error) Error() string {
	if e.Index < 0 {
		return e.Msg
	}
	return fmt.Sprintf("fleet %d: %s", e.Index, e.Msg)
}

// Validate refuses a list of fleets the panel could not tell apart or would
// mix up: two fleets with one name cannot both be addressed from a tab, one
// orchestrator in two fleets would read two boards, and two fleets on one
// board would claim the same sessions. Every fleet but the first needs a
// board; the first may have none, which is how a panel without a board has
// always worked.
func Validate(fleets []Fleet) error {
	if len(fleets) == 0 {
		return &Error{Index: -1, Msg: "no fleet is configured"}
	}
	names := map[string]int{}
	orchestrators := map[string]int{}
	boards := map[string]int{}
	for i, f := range fleets {
		if err := validateName(f.Name); err != "" {
			return &Error{Index: i, Msg: err}
		}
		if j, taken := names[f.Name]; taken {
			return &Error{Index: i, Msg: fmt.Sprintf("name %q is already the name of fleet %d", f.Name, j)}
		}
		names[f.Name] = i
		if f.Orchestrator != "" {
			if j, taken := orchestrators[f.Orchestrator]; taken {
				return &Error{Index: i, Msg: fmt.Sprintf("orchestrator %q is already the orchestrator of fleet %d (%q)", f.Orchestrator, j, fleets[j].Name)}
			}
			orchestrators[f.Orchestrator] = i
		}
		if f.BoardPath == "" {
			if i > 0 {
				return &Error{Index: i, Msg: "board path must be set"}
			}
			continue
		}
		clean := filepath.Clean(f.BoardPath)
		if j, taken := boards[clean]; taken {
			return &Error{Index: i, Msg: fmt.Sprintf("board %q is already the board of fleet %d (%q)", f.BoardPath, j, fleets[j].Name)}
		}
		boards[clean] = i
	}
	return nil
}

// validateName returns what is wrong with name, or "" when nothing is. A name
// travels in a tab's address and is compared exactly, so spaces around it
// would make two names that look the same and never match.
func validateName(name string) string {
	if strings.TrimSpace(name) == "" {
		return "name must not be empty"
	}
	if strings.TrimSpace(name) != name {
		return fmt.Sprintf("name %q has leading or trailing spaces", name)
	}
	if strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return fmt.Sprintf("name %q contains a control character", name)
	}
	return ""
}

// ErrUnknown is what Select answers for a name no fleet has.
var ErrUnknown = errors.New("no such fleet")

// Select returns the fleet a tab named. No name is the first fleet: that is
// the address every tab opened before there were several fleets still has.
// A name no fleet has is refused rather than answered with the first fleet: a
// tab left on a renamed fleet must not be shown another fleet's board under
// the old name in its address bar.
func Select(fleets []Fleet, name string) (Fleet, error) {
	if name == "" && len(fleets) > 0 {
		return fleets[0], nil
	}
	for _, f := range fleets {
		if f.Name == name {
			return f, nil
		}
	}
	quoted := make([]string, len(fleets))
	for i, f := range fleets {
		quoted[i] = fmt.Sprintf("%q", f.Name)
	}
	return Fleet{}, fmt.Errorf("%w %q; the fleets are %s", ErrUnknown, name, strings.Join(quoted, ", "))
}

// Claims maps each session short id to the names of the fleets claiming it,
// in configuration order. A fleet claims its orchestrator and every session a
// card on its board names. cards is keyed by fleet name; cards under a name
// no configured fleet has claim nothing. A session no fleet claims is absent
// from the result — it belongs to no fleet, which the panel shows in every
// fleet rather than in none.
func Claims(fleets []Fleet, cards map[string][]board.Card) map[string][]string {
	out := map[string][]string{}
	for _, f := range fleets {
		claimed := map[string]bool{}
		claim := func(short string) {
			if short == "" || claimed[short] {
				return
			}
			claimed[short] = true
			out[short] = append(out[short], f.Name)
		}
		claim(f.Orchestrator)
		for _, c := range cards[f.Name] {
			claim(c.Session)
		}
	}
	return out
}
