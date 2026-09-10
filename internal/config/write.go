package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// SetField rewrites exactly one line of an existing configuration file — the
// one naming a dotted key path through the file's own YAML nesting (e.g.
// "orchestrator.session") — and leaves every other byte alone: comments,
// blank lines, key order, indentation. It exists for the same reason
// internal/board.SetField never rewrites a whole card to change one
// frontmatter field: Save marshals the whole document from a Config value,
// which is fine for a file this package just created, but cannot tell a
// comment an operator wrote on purpose from nothing at all. Every live
// caller that changes one setting on a file a person may have hand-edited
// goes through SetField instead.
//
// Only an existing scalar key can be set this way. A file missing the
// section that would hold key fails loudly rather than guessing where to
// insert it back — deciding indentation, ordering among siblings, and
// whether a whole new mapping needs to appear is a bigger problem than one
// field write should try to solve, and every config this runs against in
// practice was either written by Save (which always emits the full shape)
// or by a person who therefore already has that shape on disk to edit
// SetField's way from then on.
func SetField(path, key, value string) error {
	fileMu.Lock()
	defer fileMu.Unlock()

	if key == "" {
		return fmt.Errorf("key must not be empty")
	}
	// A value containing a newline would make yaml.v3 marshal it as a block
	// scalar spanning several lines, which breaks the one promise this
	// function makes: it touches exactly one line. Refused here, before
	// anything is read or written, rather than left to the parseability
	// guard below to catch after the fact for some indentations and not
	// others.
	if strings.ContainsAny(value, "\n\r") {
		return fmt.Errorf("value must not contain a newline: SetField only ever rewrites a single line")
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("re-read config before write: %w", err)
	}

	out, err := substituteNestedField(raw, strings.Split(key, "."), value)
	if err != nil {
		return fmt.Errorf("config %s: %w", path, err)
	}

	// The substitution touched only one line's bytes, but the result must
	// still be exactly the shape Load expects — a corrupting edit here would
	// otherwise sit silently on disk until the next startup finally tries to
	// parse it, which is the one moment nobody is around to fix it. This
	// mirrors Load's own KnownFields(true) decode rather than a looser
	// yaml.Unmarshal, so a mistake gets caught here, not there.
	dec := yaml.NewDecoder(bytes.NewReader(out))
	dec.KnownFields(true)
	var probe file
	if err := dec.Decode(&probe); err != nil {
		return fmt.Errorf("config %s: writing %s would leave unparseable YAML: %w", path, key, err)
	}

	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat config before write: %w", err)
	}
	return writeFileAtomically(path, out, info.Mode())
}

// nestedKeyLine matches a mapping key at the start of a line, capturing its
// leading whitespace and everything after the colon (the value, and/or a
// comment, and/or nothing at all when the key opens a nested block).
func nestedKeyLine(name string) *regexp.Regexp {
	return regexp.MustCompile(`^([ \t]*)` + regexp.QuoteMeta(name) + `:(.*)$`)
}

// substituteNestedField walks parts one nesting level at a time, narrowing
// the search to the lines belonging to the block just entered, until the
// last part names the leaf line to rewrite. It does not parse YAML into a
// tree — like internal/board.substituteField, it treats the file as lines of
// text and only ever touches the one line it was asked for, which is what
// lets every other byte survive untouched.
func substituteNestedField(raw []byte, parts []string, value string) ([]byte, error) {
	lines := strings.Split(string(raw), "\n")
	lo, hi := 0, len(lines)
	parentIndent := -1 // -1: the next part must sit at column 0

	for i, part := range parts {
		re := nestedKeyLine(part)
		found := -1
		var indent string
		for ln := lo; ln < hi; ln++ {
			m := re.FindStringSubmatch(lines[ln])
			if m == nil {
				continue
			}
			// Only the first part needs an explicit depth check: nothing has
			// narrowed [lo, hi) yet, so a same-named key nested somewhere
			// else in the file could otherwise be mistaken for the top-level
			// one. Every later part's [lo, hi) is already the previous
			// part's own block, narrowed below so that every non-blank,
			// non-comment line inside it is deeper than that block's own
			// indent by construction — re-checking depth here would be a
			// second, redundant place enforcing the same invariant, with no
			// guarantee the two never drift apart.
			if parentIndent == -1 && len(m[1]) != 0 {
				continue // a top-level key must start at column 0
			}
			found, indent = ln, m[1]
			break
		}
		if found == -1 {
			return nil, fmt.Errorf("has no %s key", strings.Join(parts[:i+1], "."))
		}

		if i == len(parts)-1 {
			newLine, err := replaceLeafLine(lines[found], indent, part, value)
			if err != nil {
				return nil, err
			}
			lines[found] = newLine
			return []byte(strings.Join(lines, "\n")), nil
		}

		// Descend: the next part is searched only among this block's direct
		// children, from the line after the one just matched up to the first
		// line (blank and comment lines aside) indented no deeper than it —
		// that line belongs to the parent block, or a sibling of it, and
		// ends this one.
		blockIndent := len(indent)
		end := hi
		for ln := found + 1; ln < hi; ln++ {
			trimmed := strings.TrimSpace(lines[ln])
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			lineIndent := len(lines[ln]) - len(strings.TrimLeft(lines[ln], " \t"))
			if lineIndent <= blockIndent {
				end = ln
				break
			}
		}
		lo, hi, parentIndent = found+1, end, blockIndent
	}
	return nil, fmt.Errorf("empty key")
}

// replaceLeafLine rebuilds one "key: value" line with a new value, keeping
// its indentation and any trailing comment exactly as they were. value is
// run through yaml.Marshal rather than written verbatim so a string that
// would need quoting (empty, or one that looks like another YAML type)
// round-trips the same way Save's own marshalling would produce it.
func replaceLeafLine(line, indent, key, value string) (string, error) {
	m := nestedKeyLine(key).FindStringSubmatch(line)
	if m == nil {
		return "", fmt.Errorf("internal error: %q does not match its own key %q", line, key)
	}
	comment := trailingComment(m[2])

	scalar, err := yaml.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode %s: %w", key, err)
	}
	scalarText := strings.TrimRight(string(scalar), "\n")

	newLine := indent + key + ": " + scalarText
	if comment != "" {
		newLine += "  " + comment
	}
	return newLine, nil
}

// trailingComment returns the "# ..." suffix of rest (everything after a
// mapping key's colon), or "" when there is none. It only ever reads the old
// value being discarded, so getting this wrong cannot corrupt the new one —
// but it still follows YAML's own rule for where a comment may start (at the
// very beginning of the value, or after whitespace) rather than treating
// every "#" as one: an unquoted value like `a#b` is the single scalar "a#b",
// not "a" followed by a comment, and must not be split as if it were. It is
// quote-aware only insofar as it must not mistake a "#" inside a quoted
// scalar for a comment marker — the values this ever runs against are plain
// identifiers that yaml.v3 does not quote, so a full YAML scalar grammar is
// not needed, only enough of one not to corrupt a hand-written quoted value
// it did not choose to write itself.
func trailingComment(rest string) string {
	i := 0
	n := len(rest)
	for i < n && (rest[i] == ' ' || rest[i] == '\t') {
		i++
	}
	valueStart := i
	if i < n && (rest[i] == '"' || rest[i] == '\'') {
		quote := rest[i]
		i++
		for i < n {
			if rest[i] == quote {
				if quote == '\'' && i+1 < n && rest[i+1] == '\'' {
					i += 2
					continue
				}
				i++
				break
			}
			i++
		}
		valueStart = i
	}
	for i < n {
		if rest[i] == '#' && (i == valueStart || rest[i-1] == ' ' || rest[i-1] == '\t') {
			return rest[i:]
		}
		i++
	}
	return ""
}

// sessionIDPattern is the shape SetSessionLabel accepts for the map key it
// writes: a plain UUID, the same shape internal/transcript's own (unexported)
// sessionUUIDPattern validates a session id against before ever using it in a
// filesystem lookup. Duplicated here rather than imported — importing
// transcript into config for one two-line regex would be an odd dependency —
// but the reason is the same: this id arrives from an HTTP request body
// (internal/server's session-label route) and is about to become a YAML
// mapping key, and writing an unvalidated string as a key is exactly the
// injection this package's surgical writers otherwise refuse to allow.
var sessionIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// sessionLabelsHeader matches the top-level "session_labels:" line.
var sessionLabelsHeader = regexp.MustCompile(`^session_labels:(.*)$`)

// sessionLabelsHeaderFlowEmpty matches session_labels:'s own line only when
// it closes itself as an empty flow mapping on the same line — "{}", what
// Save itself writes for a nil or empty map (verified against Save's actual
// output, not assumed) — optionally followed by a trailing comment, which
// group 1 captures without its leading whitespace. A bare "session_labels:"
// line looks identical whether zero entries or many follow it below, so —
// unlike the flow form — bareness can only ever be read off the header
// line's own text; whether the section actually has children is decided
// separately, by scanning the lines below it (see substituteSessionLabel).
// Only the flow form needs rewriting before an insert: a block child cannot
// be appended under a line that already closed itself as an empty flow
// mapping, while a bare header with a first child appended below it needs
// no rewrite at all. The comment group exists so that rewrite can carry a
// hand-written comment on the "{}" line forward rather than discarding it.
var sessionLabelsHeaderFlowEmpty = regexp.MustCompile(`^session_labels:\s*\{\s*\}(?:[ \t]+(#.*))?$`)

// defaultSessionLabelIndent is yaml.v3's own indent (verified against Save's
// actual output, not assumed), used only when session_labels has no existing
// entry to copy an indent from.
const defaultSessionLabelIndent = "    "

// SetSessionLabel adds, updates, or removes one entry in the configuration
// file's session_labels map — the operator's own name for a session, keyed
// by its transcript UUID (see Config.SessionLabels' own comment for why the
// UUID and not the short id). It is surgical the way SetField is: every
// other byte of the file, comments included, survives. It cannot be SetField
// itself, because — unlike every key SetField knows about — a session's
// entry is not guaranteed to already be on disk: an operator can label any
// session, so this is the one surgical writer in this package allowed to
// insert a new line rather than refuse when the specific key is missing.
//
// Unlike SetField's own keys, the session_labels: mapping itself is also not
// guaranteed to exist, and this function does not assume it does the way
// SetField's own doc comment assumes every dotted key it knows about is
// already on disk. That assumption holds for every field SetField actually
// serves (orchestrator.session included) because every one of them has been
// in the schema since the config file's very first released shape — Save
// has always emitted them, so any file ever written by any shipped
// `fleetdeck init` already has them. session_labels is the first top-level
// key ever added to the schema after that first release, so the ordinary
// case for it — not a hand-edited oddity — is a config written yesterday,
// before this feature existed, that simply predates the key. Refusing that
// as an error would make the feature unusable for every operator who set up
// fleetdeck before today, which is exactly what a real run against a real
// config caught. So when session_labels: is missing entirely, a fresh
// top-level section is appended instead of refused: a new top-level mapping
// key is valid YAML regardless of what precedes it, so this still never
// rewrites an existing line, only ever adds new ones after everything that
// was already there.
//
// An empty label means "remove this session's entry" — the caller
// (internal/server's session-label route) documents the same rule where an
// operator can see it: a config that only ever gained entries and never lost
// any would eventually carry hand-written labels for sessions nobody
// remembers. Removing an entry that is not there is a no-op, not an error —
// the desired state (no label recorded for this session) already holds.
func SetSessionLabel(path, sessionID, label string) error {
	fileMu.Lock()
	defer fileMu.Unlock()

	if !sessionIDPattern.MatchString(sessionID) {
		return fmt.Errorf("sessionID must be a session UUID, got %q", sessionID)
	}
	// Same reasoning as SetField's own newline guard: a label with an
	// embedded newline would make yaml.v3 marshal it as a multi-line block
	// scalar, splicing extra lines into the file instead of touching only
	// the one line this function promises to touch.
	if strings.ContainsAny(label, "\n\r") {
		return fmt.Errorf("label must not contain a newline: SetSessionLabel only ever rewrites a single line")
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("re-read config before write: %w", err)
	}

	out, err := substituteSessionLabel(raw, sessionID, label)
	if err != nil {
		return fmt.Errorf("config %s: %w", path, err)
	}
	if out == nil {
		// Removing a label that was never set: nothing in the file changes,
		// so there is nothing to validate or write either — the file on
		// disk is already known-good, because it is untouched.
		return nil
	}

	// Same parse-before-write guard as SetField: catch a corrupting edit
	// here, not at the next startup when nobody is around to fix it.
	dec := yaml.NewDecoder(bytes.NewReader(out))
	dec.KnownFields(true)
	var probe file
	if err := dec.Decode(&probe); err != nil {
		return fmt.Errorf("config %s: writing a session label would leave unparseable YAML: %w", path, err)
	}

	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat config before write: %w", err)
	}
	return writeFileAtomically(path, out, info.Mode())
}

// appendNewSessionLabelsSection is reached only when the file has no
// session_labels key at all — see SetSessionLabel's own comment for why
// that is the ordinary case for this specific key, not a hand-edited
// oddity. Falling back to a full config.Save here would destroy exactly
// the comments this whole package exists to protect, so the section is
// appended surgically instead: a fresh top-level "session_labels:" mapping
// is valid YAML regardless of what precedes it, so appending it after
// everything that is already in the file — with a newline first if the
// file did not already end in one — never touches an existing line.
func appendNewSessionLabelsSection(raw []byte, sessionID, label string) ([]byte, error) {
	scalar, err := yaml.Marshal(label)
	if err != nil {
		return nil, fmt.Errorf("encode label: %w", err)
	}
	scalarText := strings.TrimRight(string(scalar), "\n")

	content := string(raw)
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += "session_labels:\n" + defaultSessionLabelIndent + sessionID + ": " + scalarText + "\n"
	return []byte(content), nil
}

// substituteSessionLabel returns the new file content, or (nil, nil) when
// label is empty and sessionID had no entry to remove — the caller reads a
// nil, nil-error result as "nothing to do" rather than as an empty file.
func substituteSessionLabel(raw []byte, sessionID, label string) ([]byte, error) {
	lines := strings.Split(string(raw), "\n")

	header := -1
	for i, l := range lines {
		if sessionLabelsHeader.MatchString(l) {
			header = i
			break
		}
	}
	if header == -1 {
		if label == "" {
			// Removing an entry from a section that is not even there:
			// the desired state (no label recorded) already holds.
			return nil, nil
		}
		return appendNewSessionLabelsSection(raw, sessionID, label)
	}
	flowEmpty := sessionLabelsHeaderFlowEmpty.MatchString(lines[header])

	// The block's children run from the line after the header to the first
	// line (blank and comment lines aside) indented back to column 0 — that
	// line belongs to the section after session_labels, or EOF. This holds
	// regardless of whether the section turns out to have any children at
	// all: a flow-style "{}" header is never followed by an indented child,
	// so the scan below still lands on end == header+1 for it.
	end := len(lines)
	for ln := header + 1; ln < len(lines); ln++ {
		trimmed := strings.TrimSpace(lines[ln])
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if len(lines[ln])-len(strings.TrimLeft(lines[ln], " \t")) == 0 {
			end = ln
			break
		}
	}

	entryRe := regexp.MustCompile(`^([ \t]+)` + regexp.QuoteMeta(sessionID) + `:(.*)$`)
	entryLine, entryIndent := -1, ""
	hasAnyChild := false
	for ln := header + 1; ln < end; ln++ {
		trimmed := strings.TrimSpace(lines[ln])
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		hasAnyChild = true
		if m := entryRe.FindStringSubmatch(lines[ln]); m != nil {
			entryLine, entryIndent = ln, m[1]
			break
		}
	}
	// "empty" governs only how a first entry gets inserted (see below): no
	// existing child to copy an indent from, and — only for the flow form —
	// the header line itself needs rewriting first.
	empty := flowEmpty || !hasAnyChild

	if label == "" {
		if entryLine == -1 {
			return nil, nil
		}
		out := make([]string, 0, len(lines)-1)
		out = append(out, lines[:entryLine]...)
		out = append(out, lines[entryLine+1:]...)
		return []byte(strings.Join(out, "\n")), nil
	}

	scalar, err := yaml.Marshal(label)
	if err != nil {
		return nil, fmt.Errorf("encode label: %w", err)
	}
	scalarText := strings.TrimRight(string(scalar), "\n")

	if entryLine != -1 {
		m := entryRe.FindStringSubmatch(lines[entryLine])
		comment := trailingComment(m[2])
		newLine := entryIndent + sessionID + ": " + scalarText
		if comment != "" {
			newLine += "  " + comment
		}
		lines[entryLine] = newLine
		return []byte(strings.Join(lines, "\n")), nil
	}

	// Inserting the section's first entry. An empty flow-style header
	// ("session_labels: {}") is rewritten to the bare block-style form
	// first — a block child cannot be appended under a mapping that already
	// closed itself on the same line — and the new entry is indented by
	// yaml.v3's own default, since there is no sibling to copy an indent
	// from. A non-empty section instead copies an existing sibling's
	// indentation and is appended at the end of the block.
	//
	// The rewrite is gated on flowEmpty specifically, not on empty: a bare
	// "session_labels:" header with no children yet needs no rewrite at
	// all, and rewriting it unconditionally (as an earlier version of this
	// function did) silently dropped any trailing comment the operator had
	// put on that very line — the header line is not a child of the
	// section, so it is not one of the lines the child-comment-preserving
	// logic elsewhere in this function ever looks at.
	indent := defaultSessionLabelIndent
	insertAt := end
	if empty {
		if flowEmpty {
			m := sessionLabelsHeaderFlowEmpty.FindStringSubmatch(lines[header])
			lines[header] = "session_labels:"
			if m[1] != "" {
				lines[header] += "  " + m[1]
			}
		}
		insertAt = header + 1
	} else {
		for ln := header + 1; ln < end; ln++ {
			trimmed := strings.TrimSpace(lines[ln])
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			indent = lines[ln][:len(lines[ln])-len(strings.TrimLeft(lines[ln], " \t"))]
			break
		}
	}
	newLine := indent + sessionID + ": " + scalarText
	out := make([]string, 0, len(lines)+1)
	out = append(out, lines[:insertAt]...)
	out = append(out, newLine)
	out = append(out, lines[insertAt:]...)
	return []byte(strings.Join(out, "\n")), nil
}

// writeFileAtomically writes data to path without ever leaving a truncated
// file behind: a temp file in the same directory, synced, chmod'd to mode,
// then renamed over the original. Shared by Save (which always writes 0600,
// since it may be creating the file for the first time) and SetField (which
// preserves whatever mode the existing file already had).
func writeFileAtomically(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp config file: %w", err)
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
		return fmt.Errorf("write temp config file: %w", err)
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("set temp config file permissions: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temp config file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp config file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace config file: %w", err)
	}
	cleanup = false

	if dirHandle, err := os.Open(dir); err == nil {
		_ = dirHandle.Sync()
		_ = dirHandle.Close()
	}
	return nil
}
