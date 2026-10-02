package review

import "testing"

func stageFiles(t *testing.T) []DiffFile {
	t.Helper()
	files, err := ParseDiff(stageDiff, 0)
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func at(line int) Anchor {
	return Anchor{Commit: "v1", Path: "a.go", Side: SideNew, Start: line, End: line}
}

// The four comments of the design document's worked example, with the
// positions the stage repository showed.
func TestTheWorkedExampleLandsWhereTheDocumentSays(t *testing.T) {
	t.Parallel()
	files := stageFiles(t)
	for _, c := range []struct {
		name string
		line int
		want Position
	}{
		{"A untouched, three lines inserted above", 6, Position{Path: "a.go", Start: 9, End: 9, State: StateInPlace}},
		{"B rewritten in place", 8, Position{Path: "a.go", Start: 11, End: 11, State: StateChanged}},
		{"C deleted", 10, Position{Path: "a.go", Start: 12, End: 12, State: StateDeleted}},
		{"D below an insert, a rewrite and a deletion", 11, Position{Path: "a.go", Start: 13, End: 13, State: StateInPlace}},
	} {
		if got := Trace(at(c.line), files); got != c.want {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
	}
}

func TestAFileTheDiffDoesNotMentionIsUntouched(t *testing.T) {
	t.Parallel()
	a := Anchor{Commit: "v1", Path: "other.go", Side: SideNew, Start: 3, End: 4}
	if got := Trace(a, stageFiles(t)); got != (Position{Path: "other.go", Start: 3, End: 4, State: StateInPlace}) {
		t.Fatalf("got %+v", got)
	}
}

func TestARenameIsFollowed(t *testing.T) {
	t.Parallel()
	files := []DiffFile{{OldPath: "a.go", NewPath: "load.go", Hunks: []Hunk{{OldStart: 1, OldCount: 0, NewStart: 2, NewCount: 1}, {OldStart: 4, OldCount: 0, NewStart: 6, NewCount: 3}}}}
	if got := Trace(at(6), files); got != (Position{Path: "load.go", Start: 10, End: 10, State: StateInPlace}) {
		t.Fatalf("got %+v", got)
	}
}

func TestADeletedFileSaysSo(t *testing.T) {
	t.Parallel()
	files := []DiffFile{{OldPath: "a.go", NewPath: "", Hunks: []Hunk{{OldStart: 1, OldCount: 16, NewStart: 0, NewCount: 0}}}}
	if got := Trace(at(6), files); got.State != StateFileGone {
		t.Fatalf("got %+v", got)
	}
}

func TestAnInsertRightAfterTheAnchorLeavesItAlone(t *testing.T) {
	t.Parallel()
	files := []DiffFile{{OldPath: "a.go", NewPath: "a.go", Hunks: []Hunk{{OldStart: 6, OldCount: 0, NewStart: 7, NewCount: 2}}}}
	if got := Trace(at(6), files); got != (Position{Path: "a.go", Start: 6, End: 6, State: StateInPlace}) {
		t.Fatalf("got %+v", got)
	}
}

func TestAnInsertAtTheTopOfTheFileShiftsEverything(t *testing.T) {
	t.Parallel()
	files := []DiffFile{{OldPath: "a.go", NewPath: "a.go", Hunks: []Hunk{{OldStart: 0, OldCount: 0, NewStart: 1, NewCount: 2}}}}
	if got := Trace(at(1), files); got != (Position{Path: "a.go", Start: 3, End: 3, State: StateInPlace}) {
		t.Fatalf("got %+v", got)
	}
}

func TestAnInsertInsideARangeStretchesItAndMarksItChanged(t *testing.T) {
	t.Parallel()
	a := Anchor{Commit: "v1", Path: "a.go", Side: SideNew, Start: 5, End: 8}
	files := []DiffFile{{OldPath: "a.go", NewPath: "a.go", Hunks: []Hunk{{OldStart: 6, OldCount: 0, NewStart: 7, NewCount: 2}}}}
	if got := Trace(a, files); got != (Position{Path: "a.go", Start: 5, End: 10, State: StateChanged}) {
		t.Fatalf("got %+v", got)
	}
}

func TestARangeTouchedTwiceIsOneChangedRange(t *testing.T) {
	t.Parallel()
	a := Anchor{Commit: "v1", Path: "a.go", Side: SideNew, Start: 3, End: 12}
	files := []DiffFile{{OldPath: "a.go", NewPath: "a.go", Hunks: []Hunk{
		{OldStart: 4, OldCount: 1, NewStart: 4, NewCount: 2},
		{OldStart: 10, OldCount: 2, NewStart: 11, NewCount: 0},
	}}}
	if got := Trace(a, files); got != (Position{Path: "a.go", Start: 3, End: 11, State: StateChanged}) {
		t.Fatalf("got %+v", got)
	}
}

func TestARangeWhoseEndIsRewrittenEndsWhereTheRewriteEnds(t *testing.T) {
	t.Parallel()
	a := Anchor{Commit: "v1", Path: "a.go", Side: SideNew, Start: 3, End: 5}
	files := []DiffFile{{OldPath: "a.go", NewPath: "a.go", Hunks: []Hunk{{OldStart: 5, OldCount: 1, NewStart: 5, NewCount: 3}}}}
	if got := Trace(a, files); got != (Position{Path: "a.go", Start: 3, End: 7, State: StateChanged}) {
		t.Fatalf("got %+v", got)
	}
}

func TestARangeDeletedWholeIsDeleted(t *testing.T) {
	t.Parallel()
	a := Anchor{Commit: "v1", Path: "a.go", Side: SideNew, Start: 4, End: 6}
	files := []DiffFile{{OldPath: "a.go", NewPath: "a.go", Hunks: []Hunk{{OldStart: 3, OldCount: 5, NewStart: 2, NewCount: 0}}}}
	if got := Trace(a, files); got != (Position{Path: "a.go", Start: 2, End: 2, State: StateDeleted}) {
		t.Fatalf("got %+v", got)
	}
}

func TestARangePartlyDeletedIsChangedNotDeleted(t *testing.T) {
	t.Parallel()
	a := Anchor{Commit: "v1", Path: "a.go", Side: SideNew, Start: 4, End: 6}
	files := []DiffFile{{OldPath: "a.go", NewPath: "a.go", Hunks: []Hunk{{OldStart: 5, OldCount: 1, NewStart: 4, NewCount: 0}}}}
	if got := Trace(a, files); got != (Position{Path: "a.go", Start: 4, End: 5, State: StateChanged}) {
		t.Fatalf("got %+v", got)
	}
}
