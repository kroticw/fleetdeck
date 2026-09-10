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
