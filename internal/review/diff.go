package review

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

type Line struct {
	Kind string `json:"kind"`
	Old  int    `json:"old"`
	New  int    `json:"new"`
	Text string `json:"text"`
}

type Hunk struct {
	OldStart int    `json:"oldStart"`
	OldCount int    `json:"oldCount"`
	NewStart int    `json:"newStart"`
	NewCount int    `json:"newCount"`
	Lines    []Line `json:"lines"`
}

type DiffFile struct {
	OldPath   string `json:"oldPath"`
	NewPath   string `json:"newPath"`
	Binary    bool   `json:"binary"`
	Truncated bool   `json:"truncated"`
	Hunks     []Hunk `json:"hunks"`
}

var hunkHeader = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

// ParseDiff reads git's unified diff. Paths are taken from the ---/+++ and
// rename lines rather than from "diff --git a/x b/y", which cannot be split
// when a path holds a space. A file with more than maxLines diff lines keeps
// its paths and loses its hunks, marked Truncated, so the page can say how
// much it is not showing instead of hanging on it.
func ParseDiff(raw string, maxLines int) ([]DiffFile, error) {
	var files []DiffFile
	var cur *DiffFile
	var hunk *Hunk
	oldLine, newLine, count := 0, 0, 0
	flushHunk := func() {
		if cur != nil && hunk != nil {
			cur.Hunks = append(cur.Hunks, *hunk)
		}
		hunk = nil
	}
	flushFile := func() {
		flushHunk()
		if cur != nil {
			if maxLines > 0 && count > maxLines {
				cur.Hunks, cur.Truncated = nil, true
			}
			files = append(files, *cur)
		}
		cur, count = nil, 0
	}
	for _, line := range strings.Split(strings.TrimSuffix(raw, "\n"), "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			flushFile()
			cur = &DiffFile{}
			// Only a fallback for a file with no ---/+++ lines (a pure rename,
			// a mode change): git writes identical halves then, so splitting
			// at the midpoint is exact even with spaces in the path.
			if rest := strings.TrimPrefix(line, "diff --git "); len(rest)%2 == 1 {
				half := rest[:len(rest)/2]
				if p, ok := strings.CutPrefix(half, "a/"); ok {
					cur.OldPath, cur.NewPath = p, p
				}
			}
		case cur == nil:
			continue
		case hunk == nil && strings.HasPrefix(line, "rename from "):
			cur.OldPath = strings.TrimPrefix(line, "rename from ")
		case hunk == nil && strings.HasPrefix(line, "rename to "):
			cur.NewPath = strings.TrimPrefix(line, "rename to ")
		case hunk == nil && strings.HasPrefix(line, "new file mode "):
			cur.OldPath = ""
		case hunk == nil && strings.HasPrefix(line, "deleted file mode "):
			cur.NewPath = ""
		case hunk == nil && strings.HasPrefix(line, "--- "):
			cur.OldPath = sidePath(strings.TrimPrefix(line, "--- "), "a/")
		case hunk == nil && strings.HasPrefix(line, "+++ "):
			cur.NewPath = sidePath(strings.TrimPrefix(line, "+++ "), "b/")
		case hunk == nil && strings.HasPrefix(line, "Binary files "):
			cur.Binary = true
		case strings.HasPrefix(line, "@@ "):
			flushHunk()
			m := hunkHeader.FindStringSubmatch(line)
			if m == nil {
				return nil, fmt.Errorf("unreadable hunk header %q", line)
			}
			h := Hunk{OldStart: atoi(m[1]), OldCount: countOf(m[2]), NewStart: atoi(m[3]), NewCount: countOf(m[4])}
			hunk = &h
			oldLine, newLine = h.OldStart, h.NewStart
		case hunk != nil && line != "" && line[0] == '\\':
			// "\ No newline at end of file" belongs to the line before it.
		case hunk != nil:
			// An empty line is a blank context line printed without its
			// space (diff.suppressBlankEmpty); skipping it would shift every
			// later line number by one.
			l := Line{Kind: " "}
			if line != "" {
				l = Line{Kind: line[:1], Text: line[1:]}
			}
			switch l.Kind {
			case "-":
				l.Old = oldLine
				oldLine++
			case "+":
				l.New = newLine
				newLine++
			default:
				l.Kind, l.Old, l.New = " ", oldLine, newLine
				oldLine++
				newLine++
			}
			hunk.Lines = append(hunk.Lines, l)
			count++
		}
	}
	flushFile()
	return files, nil
}

func sidePath(p, prefix string) string {
	// git ends a name holding a space with a tab, so the line can be split
	// from a timestamp by tools that expect one.
	p = strings.TrimSuffix(p, "\t")
	if p == "/dev/null" {
		return ""
	}
	return strings.TrimPrefix(p, prefix)
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func countOf(s string) int {
	if s == "" {
		return 1
	}
	return atoi(s)
}
