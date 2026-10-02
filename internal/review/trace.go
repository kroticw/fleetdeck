package review

type Side string

const (
	SideNew Side = "new"
	SideOld Side = "old"
)

// Anchor is what a comment is attached to: a version of the code that never
// changes. Where the comment stands in today's code is never stored; it is
// traced from here every time it is shown.
type Anchor struct {
	Commit string   `json:"commit"`
	Path   string   `json:"path"`
	Side   Side     `json:"side"`
	Start  int      `json:"start"`
	End    int      `json:"end"`
	Text   []string `json:"text"`
	Before []string `json:"before"`
	After  []string `json:"after"`
}

type State string

const (
	StateInPlace     State = "in_place"
	StateChanged     State = "changed"
	StateDeleted     State = "deleted"
	StateFileGone    State = "file_gone"
	StateUnavailable State = "unavailable"
)

// Position is where an anchor stands in the target version. For StateDeleted
// Start == End and it means "after line Start", 0 being the top of the file.
type Position struct {
	Path  string `json:"path"`
	Start int    `json:"start"`
	End   int    `json:"end"`
	State State  `json:"state"`
}

// Trace moves a onto the version files diffs to. files must be a trace diff
// (RawDiff with trace set) from a.Commit. Every outcome but a plain shift is
// marked: a comment never lands on another line without saying so.
func Trace(a Anchor, files []DiffFile) Position {
	f, ok := fileFrom(files, a.Path)
	if !ok {
		return Position{Path: a.Path, Start: a.Start, End: a.End, State: StateInPlace}
	}
	if f.NewPath == "" {
		return Position{Path: a.Path, Start: a.Start, End: a.End, State: StateFileGone}
	}
	start, end, state := traceLines(a.Start, a.End, f.Hunks)
	return Position{Path: f.NewPath, Start: start, End: end, State: state}
}

func fileFrom(files []DiffFile, path string) (DiffFile, bool) {
	for _, f := range files {
		if f.OldPath == path {
			return f, true
		}
	}
	return DiffFile{}, false
}

// traceLines follows the old range [start, end] through hunks, which git lists
// in order of their old start.
func traceLines(start, end int, hunks []Hunk) (int, int, State) {
	var touched []Hunk
	covered := 0
	for _, h := range hunks {
		if h.OldCount == 0 {
			// An insert after old line OldStart lands inside the range only
			// strictly before its last line: after the last line, git says
			// every line of the range is unchanged.
			if h.OldStart >= start && h.OldStart < end {
				touched = append(touched, h)
			}
			continue
		}
		lo, hi := h.OldStart, h.OldStart+h.OldCount-1
		if hi >= start && lo <= end {
			touched = append(touched, h)
			covered += min(hi, end) - max(lo, start) + 1
		}
	}
	if len(touched) == 0 {
		return start + shiftBefore(start, hunks), end + shiftBefore(end, hunks), StateInPlace
	}
	allGone := covered == end-start+1
	for _, h := range touched {
		if h.NewCount > 0 {
			allGone = false
		}
	}
	first, last := touched[0], touched[len(touched)-1]
	if allGone {
		return first.NewStart, first.NewStart, StateDeleted
	}
	newStart := start + shiftBefore(start, hunks)
	if covers(first, start) {
		newStart = first.NewStart
	}
	newEnd := end + shiftBefore(end, hunks)
	if covers(last, end) {
		newEnd = last.NewStart + last.NewCount - 1
		if last.NewCount == 0 {
			newEnd = last.NewStart
		}
	}
	return newStart, max(newEnd, newStart), StateChanged
}

// covers reports whether h replaces old line n itself; an insert covers none.
func covers(h Hunk, n int) bool {
	return h.OldCount > 0 && n >= h.OldStart && n <= h.OldStart+h.OldCount-1
}

// shiftBefore is how far old line n moves because of the hunks wholly above it.
func shiftBefore(n int, hunks []Hunk) int {
	delta := 0
	for _, h := range hunks {
		switch {
		case h.OldCount == 0 && h.OldStart < n:
			delta += h.NewCount
		case h.OldCount > 0 && h.OldStart+h.OldCount-1 < n:
			delta += h.NewCount - h.OldCount
		}
	}
	return delta
}
