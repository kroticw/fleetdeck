package config

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/kroticw/fleetdeck/internal/fleet"
)

// The fleets list is the one place in the file where a value sits inside a
// list entry, which SetField's dotted key paths cannot reach, and where a
// whole new entry is ever added. Both writers below are surgical the way
// SetField and SetSessionLabel are: they read the file as lines, add or
// rewrite only the lines of the one entry they were asked about, and leave
// every other byte — comments, blank lines, key order, indentation — as the
// file had it. Neither falls back to Save: re-marshalling the whole document
// is what once erased an operator's comments.

// fleetsHeader matches the top-level "fleets:" line.
var fleetsHeader = regexp.MustCompile(`^fleets:(.*)$`)

// fleetsHeaderFlowEmpty matches "fleets: []", optionally followed by a
// comment, which group 1 captures.
var fleetsHeaderFlowEmpty = regexp.MustCompile(`^fleets:\s*\[\s*\](?:[ \t]+(#.*))?$`)

// marshalIndent is the indentation yaml.v3 gives a list entry under a
// top-level key, which Save uses and every entry marshalled here starts with.
const marshalIndent = "    "

// AddFleet appends fl to the configuration file's fleets list, opening the
// list at the end of the file when there is none. The file must exist: a
// fleet is added to a configuration, never used to create one. Nothing is
// written when the result would not load — a name another fleet has, an
// orchestrator another fleet has, a missing board.
func AddFleet(path string, fl fleet.Fleet) error {
	fileMu.Lock()
	defer fileMu.Unlock()

	for _, v := range append([]string{fl.Name, fl.BoardPath, fl.Orchestrator}, fl.DocsPaths...) {
		if strings.ContainsAny(v, "\n\r") {
			return fmt.Errorf("fleet %q: a value must not contain a newline", fl.Name)
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("re-read config before write: %w", err)
	}
	out, err := appendFleet(raw, fl)
	if err != nil {
		return fmt.Errorf("config %s: adding fleet %q: %w", path, fl.Name, err)
	}
	return writeCheckedConfig(path, out, fmt.Sprintf("adding fleet %q", fl.Name))
}

// SetFleetOrchestrator pins short as the orchestrator of the listed fleet
// named name; an empty short unpins it. The top-level fleet is not in the
// list and is pinned through SetField's orchestrator.session instead. The
// entry's session line is rewritten in place, keeping its comment; an entry
// with no orchestrator key gets one added as its last lines.
func SetFleetOrchestrator(path, name, short string) error {
	fileMu.Lock()
	defer fileMu.Unlock()

	if strings.ContainsAny(short, "\n\r") {
		return fmt.Errorf("orchestrator must not contain a newline: SetFleetOrchestrator only ever rewrites a single line")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("re-read config before write: %w", err)
	}
	out, err := substituteFleetOrchestrator(raw, name, short)
	if err != nil {
		return fmt.Errorf("config %s: %w", path, err)
	}
	if out == nil {
		return nil
	}
	return writeCheckedConfig(path, out, fmt.Sprintf("pinning the orchestrator of fleet %q", name))
}

// writeCheckedConfig writes out over path only when out loads the way Load
// would load it, validation included, so a writer here can never leave a
// file the next start refuses.
func writeCheckedConfig(path string, out []byte, what string) error {
	f := configToFile(Default())
	dec := yaml.NewDecoder(bytes.NewReader(out))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil {
		return fmt.Errorf("config %s: %s would leave unparseable YAML: %w", path, what, describeYAMLError(err))
	}
	if err := validate(fileToConfig(f)); err != nil {
		return fmt.Errorf("config %s: %s: %w", path, what, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat config before write: %w", err)
	}
	return writeFileAtomically(path, out, info.Mode())
}

// splitLines splits raw into lines without the phantom empty line a final
// newline would add (see substituteSessionLabel for what that line broke),
// and returns the function that joins them back the way raw ended.
func splitLines(raw []byte) ([]string, func([]string) []byte) {
	trailingNewline := strings.HasSuffix(string(raw), "\n")
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	if len(raw) == 0 {
		lines = nil
	}
	return lines, func(lines []string) []byte {
		out := strings.Join(lines, "\n")
		if trailingNewline {
			out += "\n"
		}
		return []byte(out)
	}
}

func isBlankOrComment(line string) bool {
	trimmed := strings.TrimSpace(line)
	return trimmed == "" || strings.HasPrefix(trimmed, "#")
}

func indentOf(line string) int {
	return len(line) - len(strings.TrimLeft(line, " \t"))
}

// fleetsBlock finds the fleets header and the line its block ends before. A
// list may be written with its dashes at column 0 under the key, so a line at
// column 0 ends the block only when it is not a list entry.
func fleetsBlock(lines []string) (header, end int) {
	header = -1
	for i, l := range lines {
		if fleetsHeader.MatchString(l) {
			header = i
			break
		}
	}
	if header == -1 {
		return -1, -1
	}
	end = len(lines)
	for ln := header + 1; ln < len(lines); ln++ {
		if isBlankOrComment(lines[ln]) {
			continue
		}
		if indentOf(lines[ln]) == 0 && !strings.HasPrefix(lines[ln], "-") {
			end = ln
			break
		}
	}
	return header, end
}

// lastContent is the last line in (from, to) that is neither blank nor a
// comment, or from when there is none: what follows it, blank lines and
// comments included, belongs to whatever comes next in the file.
func lastContent(lines []string, from, to int) int {
	last := from
	for ln := from + 1; ln < to; ln++ {
		if !isBlankOrComment(lines[ln]) {
			last = ln
		}
	}
	return last
}

func marshalFleetEntry(fl fleet.Fleet) ([]string, error) {
	var ff fleetFile
	ff.Name = fl.Name
	ff.Board.Path = fl.BoardPath
	ff.Docs.Paths = fl.DocsPaths
	ff.Orchestrator.Session = fl.Orchestrator
	out, err := yaml.Marshal(struct {
		Fleets []fleetFile `yaml:"fleets"`
	}{[]fleetFile{ff}})
	if err != nil {
		return nil, fmt.Errorf("encode fleet: %w", err)
	}
	lines := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	return lines[1:], nil
}

func appendFleet(raw []byte, fl fleet.Fleet) ([]byte, error) {
	entry, err := marshalFleetEntry(fl)
	if err != nil {
		return nil, err
	}
	lines, _ := splitLines(raw)
	// The entry may become the file's last line, and a line this function
	// wrote ends in a newline, the way appendNewSessionLabelsSection's do.
	join := func(lines []string) []byte { return []byte(strings.Join(lines, "\n") + "\n") }
	header, end := fleetsBlock(lines)
	if header == -1 {
		return join(append(append(lines, "fleets:"), entry...)), nil
	}

	if m := fleetsHeaderFlowEmpty.FindStringSubmatch(lines[header]); m != nil {
		lines[header] = "fleets:"
		if m[1] != "" {
			lines[header] += "  " + m[1]
		}
	} else if rest := strings.TrimSpace(fleetsHeader.FindStringSubmatch(lines[header])[1]); rest != "" && !strings.HasPrefix(rest, "#") {
		return nil, fmt.Errorf("fleets is not written as a block list; add the fleet by hand")
	}

	// Follow the indentation of the entries already there.
	for ln := header + 1; ln < end; ln++ {
		if isBlankOrComment(lines[ln]) {
			continue
		}
		dash := lines[ln][:indentOf(lines[ln])]
		for i, l := range entry {
			if !strings.HasPrefix(l, marshalIndent) {
				return nil, fmt.Errorf("internal error: marshalled entry line %q is not indented by %q", l, marshalIndent)
			}
			entry[i] = dash + strings.TrimPrefix(l, marshalIndent)
		}
		break
	}

	insertAt := lastContent(lines, header, end) + 1
	out := make([]string, 0, len(lines)+len(entry))
	out = append(out, lines[:insertAt]...)
	out = append(out, entry...)
	out = append(out, lines[insertAt:]...)
	return join(out), nil
}

// fleetEntry is one entry of the fleets list: the lines [start, end), and
// the column its keys start at — the one after its dash.
type fleetEntry struct {
	start, end, keyCol int
}

// keyOn reports whether line holds key at the entry's key column, on the
// dash line or on a line of its own, and returns what follows the colon.
func (e fleetEntry) keyOn(line string, isDashLine bool, key string) (string, bool) {
	if len(line) < e.keyCol {
		return "", false
	}
	if !isDashLine && indentOf(line) != e.keyCol {
		return "", false
	}
	rest, ok := strings.CutPrefix(line[e.keyCol:], key+":")
	return rest, ok
}

func fleetEntries(lines []string, header, end int) []fleetEntry {
	var entries []fleetEntry
	dashCol := -1
	for ln := header + 1; ln < end; ln++ {
		if isBlankOrComment(lines[ln]) {
			continue
		}
		col := indentOf(lines[ln])
		if dashCol == -1 {
			dashCol = col
		}
		if col != dashCol || !strings.HasPrefix(lines[ln][col:], "-") {
			continue
		}
		afterDash := lines[ln][col+1:]
		keyCol := col + 1 + indentOf(afterDash)
		if len(entries) > 0 {
			entries[len(entries)-1].end = ln
		}
		entries = append(entries, fleetEntry{start: ln, end: end, keyCol: keyCol})
	}
	return entries
}

func substituteFleetOrchestrator(raw []byte, name, short string) ([]byte, error) {
	lines, join := splitLines(raw)
	header, end := fleetsBlock(lines)
	var target *fleetEntry
	if header != -1 {
		for _, e := range fleetEntries(lines, header, end) {
			for ln := e.start; ln < e.end; ln++ {
				rest, ok := e.keyOn(lines[ln], ln == e.start, "name")
				if !ok {
					continue
				}
				var got string
				if err := yaml.Unmarshal([]byte(rest), &got); err == nil && got == name {
					e := e
					target = &e
				}
				break
			}
			if target != nil {
				break
			}
		}
	}
	if target == nil {
		return nil, fmt.Errorf("fleet %q is not in the fleets list", name)
	}

	scalar, err := yaml.Marshal(short)
	if err != nil {
		return nil, fmt.Errorf("encode orchestrator: %w", err)
	}
	scalarText := strings.TrimRight(string(scalar), "\n")

	for ln := target.start; ln < target.end; ln++ {
		rest, ok := target.keyOn(lines[ln], ln == target.start, "orchestrator")
		if !ok {
			continue
		}
		if value := strings.TrimSpace(rest); value != "" && !strings.HasPrefix(value, "#") {
			return nil, fmt.Errorf("fleet %q: orchestrator is not written as a block; pin it by hand", name)
		}
		for child := ln + 1; child < target.end; child++ {
			if isBlankOrComment(lines[child]) {
				continue
			}
			if indentOf(lines[child]) <= target.keyCol {
				break
			}
			indent := lines[child][:indentOf(lines[child])]
			if !strings.HasPrefix(lines[child][len(indent):], "session:") {
				continue
			}
			newLine, err := replaceLeafLine(lines[child], indent, "session", short)
			if err != nil {
				return nil, err
			}
			lines[child] = newLine
			return join(lines), nil
		}
		if short == "" {
			return nil, nil
		}
		sessionLine := strings.Repeat(" ", target.keyCol+2) + "session: " + scalarText
		out := append(append(append([]string{}, lines[:ln+1]...), sessionLine), lines[ln+1:]...)
		return join(out), nil
	}

	if short == "" {
		return nil, nil
	}
	keyIndent := strings.Repeat(" ", target.keyCol)
	insertAt := lastContent(lines, target.start, target.end) + 1
	added := []string{keyIndent + "orchestrator:", keyIndent + "  session: " + scalarText}
	out := make([]string, 0, len(lines)+len(added))
	out = append(out, lines[:insertAt]...)
	out = append(out, added...)
	out = append(out, lines[insertAt:]...)
	return join(out), nil
}
