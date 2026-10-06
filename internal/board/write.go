package board

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"
)

var (
	validStages   = map[string]bool{"new": true, "active": true, "review": true, "done": true, "blocked": true}
	validProgress = map[int]bool{0: true, 10: true, 20: true, 40: true, 60: true, 80: true, 100: true}
	startedStages = map[string]bool{"active": true, "review": true, "done": true, "blocked": true}
	// shortID is the board validator's own rule for a session field
	// (scripts/validate_cards.py). Emptying the field is not offered: it is
	// what ties a card to the agent writing it, and every stage but new
	// requires it.
	shortID = regexp.MustCompile(`^[0-9a-fA-F]{6,12}$`)
)

// SetField rewrites exactly one frontmatter line and leaves every other byte
// alone. The file is re-read here, immediately before the write, so the
// panel never writes from a copy of the card it rendered earlier. That does
// not make the write race-free: a line an agent appends to this same file
// between this re-read and the rename below is still lost, because the
// agents that append to a card do not coordinate with this process at all.
// Narrowing the window is all a re-read can do.
//
// expect, when it is not nil, is the value the caller believes the field
// holds; the write is refused with ErrStale when the card holds something
// else. It closes the other half of the same race, the half a re-read on its
// own makes worse rather than better: a hand dragging a card works from a
// snapshot up to a second old, and without the check the panel writes the
// stage it saw over the stage the card's own agent has since moved to.
func SetField(path, field, value string, expect *string) error {
	var normalized string
	switch field {
	case "stage":
		if !validStages[value] {
			return fmt.Errorf("unknown stage %q", value)
		}
		normalized = value
	case "progress":
		n, err := strconv.Atoi(value)
		if err != nil || !validProgress[n] {
			return fmt.Errorf("progress must be one of 0,10,20,40,60,80,100, got %q", value)
		}
		// Write the normalized number, never the caller's raw string: a
		// value like "0100" or "+100" passes strconv.Atoi but is not the
		// plain decimal literal YAML expects, and yaml.v3 resolves a
		// leading-zero scalar as octal on the next read.
		normalized = strconv.Itoa(n)
	case "session":
		if !shortID.MatchString(value) {
			return fmt.Errorf("session must be a short id of 6 to 12 hex digits, got %q", value)
		}
		normalized = value
	case "repo":
		repo, err := NormalizeRepo(value)
		if err != nil {
			return err
		}
		normalized = repo
		if repo == "" {
			// Quoted: a bare ~ or an empty value is YAML's null.
			normalized = `""`
		}
	default:
		return fmt.Errorf("%w: %s", ErrUnknownField, field)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("re-read card before write: %w", err)
	}
	m := frontmatterRe.FindSubmatch(raw)
	if m == nil {
		return fmt.Errorf("card %s has no frontmatter to write into", path)
	}
	var fm frontmatter
	if err := yaml.Unmarshal(m[1], &fm); err != nil {
		return fmt.Errorf("card %s has malformed frontmatter, refusing write: %w", path, err)
	}
	if expect != nil {
		if held := fieldValue(field, fm); held != normalize(field, *expect) {
			return fmt.Errorf("%w: %s is %q, not the %q this write was made against", ErrStale, field, held, *expect)
		}
	}
	if err := checkCrossFieldRules(field, normalized, fm); err != nil {
		return err
	}

	out, err := writeFrontmatterField(raw, field, normalized)
	if err != nil {
		return fmt.Errorf("card %s %w", path, err)
	}
	if out, err = doneProgress(out, field, normalized, fm); err != nil {
		return fmt.Errorf("card %s %w", path, err)
	}

	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat card before write: %w", err)
	}
	return atomicWrite(path, out, info.Mode())
}

// fieldValue is what the card holds for one of the three fields the panel
// writes, as a string, so a precondition can be compared whatever the field.
func fieldValue(field string, fm frontmatter) string {
	switch field {
	case "stage":
		return fm.Stage
	case "progress":
		return strconv.Itoa(fm.Progress)
	case "repo":
		return fm.Repo
	default:
		return fm.Session
	}
}

// homeSpellings are the ways a person writes the home directory itself, alone
// or in front of a path from it.
var homeSpellings = []string{"~", "$HOME", "${HOME}"}

// NormalizeRepo is a card's repo field as it is written: a path from the home
// directory, with a "~/" or "$HOME/" in front read as the same path and
// dropped. The home directory itself is "": a card with no repo is worked in
// home (T-134), and "~", "~/" and "$HOME" are that same card. A path that is
// absolute, climbs out of home, names another user's home or spans lines is
// refused.
func NormalizeRepo(value string) (string, error) {
	repo := strings.TrimSpace(value)
	for _, home := range homeSpellings {
		if repo == home {
			return "", nil
		}
		if rest, ok := strings.CutPrefix(repo, home+"/"); ok {
			repo = rest
			break
		}
	}
	switch {
	case repo == "":
		return "", nil
	case strings.HasPrefix(repo, "~") || strings.HasPrefix(repo, "$") ||
		!filepath.IsLocal(repo) || strings.IndexFunc(repo, unicode.IsControl) >= 0:
		return "", &RuleRefusal{codeRepoOutsideHome, fmt.Sprintf("repo %q must be a path inside the home directory, e.g. src/fleetdeck", value)}
	}
	return filepath.Clean(repo), nil
}

// RepoDir is the directory a card's repo names: NormalizeRepo's path under
// home, and home itself for a card with no repo. Anything but a directory
// there is refused — a repo is the folder a worker starts in, not free text.
func RepoDir(home, value string) (string, error) {
	rel, err := NormalizeRepo(value)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, rel)
	info, err := os.Stat(dir)
	switch {
	case err != nil:
		return "", &RuleRefusal{codeRepoNotADirectory, fmt.Sprintf("repo %s: %v", dir, err)}
	case !info.IsDir():
		return "", &RuleRefusal{codeRepoNotADirectory, fmt.Sprintf("repo %s is not a directory: %s", rel, dir)}
	}
	return dir, nil
}

// normalize is the caller's expected value in the form fieldValue reports:
// progress arrives from a snapshot as a number and from a select as a string,
// and "080" and "80" are the same progress. A value that is no progress at all
// is left as it came and simply fails to match.
func normalize(field, value string) string {
	if field != "progress" {
		return value
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return value
	}
	return strconv.Itoa(n)
}

// writeFrontmatterField puts value in the card's frontmatter: over the field's
// own line when there is one, and on a new line when there is not.
//
// Only session and repo are ever added. A card with no stage or progress line
// is a card the board's validator already refuses, and inventing the field
// here would hide that; a card with no session line is one the panel itself
// wrote before the field was in its template, and a card with no repo line is
// the ordinary card — the field is optional. Both are on the board and legal.
func writeFrontmatterField(raw []byte, field, value string) ([]byte, error) {
	out, err := substituteField(raw, field, value)
	if err == nil || (field != "session" && field != "repo") || !errors.Is(err, ErrNoSuchField) {
		return out, err
	}
	return insertField(raw, field, value)
}

// insertAfter is the line each field the panel may add goes after, in the
// order scripts/new_card.py writes a card: session after progress, repo after
// session. A card without that line takes the field after progress, and a card
// without either at the end of the block.
var insertAfter = map[string][]string{
	"session": {"progress"},
	"repo":    {"session", "progress"},
}

// insertField adds "field: value" to the frontmatter, after the line
// insertAfter names for it, and at the end of the block when the card has none
// of them.
func insertField(raw []byte, field, value string) ([]byte, error) {
	m := frontmatterRe.FindSubmatch(raw)
	if m == nil {
		return nil, fmt.Errorf("has no frontmatter block")
	}
	head := raw[:len(m[0])]
	line := []byte(field + ": " + value + "\n")
	at := -1
	for _, anchor := range insertAfter[field] {
		if loc := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(anchor) + `:.*\n`).FindIndex(head); loc != nil {
			at = loc[1]
			break
		}
	}
	if at < 0 {
		// The closing "---" of the block, which frontmatterRe's own match ends
		// with: the new line goes before the line that closes it.
		closing := bytes.LastIndex(head, []byte("---"))
		if closing < 0 {
			return nil, fmt.Errorf("has no frontmatter block")
		}
		at = closing
	}
	out := make([]byte, 0, len(raw)+len(line))
	out = append(out, raw[:at]...)
	out = append(out, line...)
	out = append(out, raw[at:]...)
	return out, nil
}

// substituteField replaces the value of the first line matching "field:..."
// inside raw's frontmatter block (never inside the body, where an agent's
// own text might coincidentally match the same pattern) and refuses if that
// would change the number of lines in the file — a real guard against a
// substitution eating or adding a line, not a formality.
func substituteField(raw []byte, field, value string) ([]byte, error) {
	m := frontmatterRe.FindSubmatch(raw)
	if m == nil {
		return nil, fmt.Errorf("has no frontmatter block")
	}
	head := raw[:len(m[0])]
	line := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(field) + `:.*$`)
	loc := line.FindIndex(head)
	if loc == nil {
		return nil, fmt.Errorf("%w %s", ErrNoSuchField, field)
	}
	replacement := field + ": " + value
	out := make([]byte, 0, len(raw)+len(replacement))
	out = append(out, head[:loc[0]]...)
	out = append(out, replacement...)
	out = append(out, head[loc[1]:]...)
	out = append(out, raw[len(head):]...)
	if strings.Count(string(out), "\n") != strings.Count(string(raw), "\n") {
		return nil, fmt.Errorf("refusing to write: line count would change")
	}
	return out, nil
}

// RuleRefusal is a write refused by one of the board's rules: a cross-field
// rule, or a repo that is no folder under home.
// Code names the rule, one code per rule whatever the stage or value it was
// tripped by, so the interface can say the rule in its own language and
// point at the way out; the words are for logs and for a page with no
// sentence for the code.
type RuleRefusal struct {
	Code string
	msg  string
}

func (e *RuleRefusal) Error() string { return e.msg }

// The codes a RuleRefusal can carry. Each becomes a dictionary key in
// web/js/i18n.js (card_refused_<code>).
const (
	codeSessionRequired   = "session_required"
	codeDoneHoldsProgress = "done_holds_progress_100"
	codeRepoOutsideHome   = "repo_outside_home"
	codeRepoNotADirectory = "repo_not_a_directory"
)

var ruleCodes = []string{codeSessionRequired, codeDoneHoldsProgress, codeRepoOutsideHome, codeRepoNotADirectory}

// checkCrossFieldRules keeps a card in a state the board's own validator
// accepts. The rule is one-directional: progress 100 with a stage other than
// done is legal, which is what lets doneProgress below write the progress
// first and the stage second without either order tripping this.
func checkCrossFieldRules(field, value string, fm frontmatter) error {
	switch field {
	case "stage":
		if startedStages[value] && fm.Session == "" {
			return &RuleRefusal{codeSessionRequired, fmt.Sprintf("cannot set stage to %s while session is empty: the board requires a session at stage %s", value, value)}
		}
	case "progress":
		if fm.Stage == "done" && value != "100" {
			return &RuleRefusal{codeDoneHoldsProgress, fmt.Sprintf("cannot set progress to %s while stage is done: the board requires progress 100 at stage done", value)}
		}
	}
	return nil
}

// doneProgress is the second field a move into done writes: the board's schema
// binds the two (scripts/validate_cards.py, "with stage done, progress must be
// 100"), so refusing the stage until a separate edit set the progress made
// every accepted card cost two writes — and the drag, which can only write
// one, always failed.
//
// Only a hand ever arrives here with done: the dispatcher writes active
// (internal/orchestrator), and an agent edits its card with its own editor
// without going through this package at all. So this is the operator's own
// gesture completed, not a value invented behind a writer's back.
//
// It goes into the same bytes as the stage rather than a second write, which
// is what keeps the card off disk in the half state and keeps the window an
// agent appending to the same file can be lost in exactly as wide as one
// ordinary write's.
func doneProgress(out []byte, field, value string, fm frontmatter) ([]byte, error) {
	if field != "stage" || value != "done" || fm.Progress == 100 {
		return out, nil
	}
	return writeFrontmatterField(out, "progress", "100")
}

// atomicWrite writes data to path without ever leaving a truncated or empty
// file on disk: it writes to a temp file in the same directory, syncs it,
// preserves the original file's mode, then renames it over the original.
// The temp file is removed on any error before the rename.
func atomicWrite(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".board-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file for atomic write: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Chmod(tmpPath, mode); err != nil {
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename temp file over card: %w", err)
	}
	cleanup = false
	return nil
}
