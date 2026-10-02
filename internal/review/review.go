package review

import (
	"context"
	"fmt"
)

// MaxFileLines is the most diff lines a file is shown with; beyond it the
// file is folded with its paths kept (ParseDiff).
const MaxFileLines = 3000

const contextLines = 2

type Placed struct {
	Comment
	Position Position `json:"position"`
}

type View struct {
	Workdir     string     `json:"workdir"`
	Head        string     `json:"head"`
	Base        Base       `json:"base"`
	Files       []DiffFile `json:"files"`
	Uncommitted []string   `json:"uncommitted"`
	Comments    []Placed   `json:"comments"`
	Replies     []Reply    `json:"replies"`
	Rounds      []Round    `json:"rounds"`
	Rev         int        `json:"rev"`
}

// NewAnchor builds an anchor from what the operator pointed at, reading the
// text from the commit itself: the page says where, git says what is there.
func NewAnchor(ctx context.Context, g Git, commit, path string, side Side, start, end int) (Anchor, error) {
	if start < 1 || end < start {
		return Anchor{}, fmt.Errorf("line range %d-%d is not a range", start, end)
	}
	lines, err := g.Lines(ctx, commit, path)
	if err != nil {
		return Anchor{}, err
	}
	if end > len(lines) {
		return Anchor{}, fmt.Errorf("%s has %d lines at %.12s, not %d", path, len(lines), commit, end)
	}
	return Anchor{
		Commit: commit, Path: path, Side: side, Start: start, End: end,
		Text:   append([]string(nil), lines[start-1:end]...),
		Before: append([]string(nil), lines[max(0, start-1-contextLines):start-1]...),
		After:  append([]string(nil), lines[end:min(len(lines), end+contextLines)]...),
	}, nil
}

func Build(ctx context.Context, g Git, dir string) (View, error) {
	top, err := g.Toplevel(ctx)
	if err != nil {
		return View{}, fmt.Errorf("%s is not a git working tree: %w", g.Dir, err)
	}
	head, err := g.Head(ctx)
	if err != nil {
		return View{}, err
	}
	base, err := g.Base(ctx)
	if err != nil {
		return View{}, err
	}
	raw, err := g.RawDiff(ctx, base.Commit, head, false)
	if err != nil {
		return View{}, err
	}
	files, err := ParseDiff(raw, MaxFileLines)
	if err != nil {
		return View{}, err
	}
	uncommitted, err := g.Uncommitted(ctx)
	if err != nil {
		return View{}, err
	}
	comments, err := Load(dir)
	if err != nil {
		return View{}, err
	}
	replies, err := LoadReplies(dir)
	if err != nil {
		return View{}, err
	}
	positions := Place(ctx, g, base, head, comments.Comments)
	placed := make([]Placed, 0, len(comments.Comments))
	for _, c := range comments.Comments {
		placed = append(placed, Placed{Comment: c, Position: positions[c.ID]})
	}
	return View{
		Workdir: top, Head: head, Base: base,
		Files:       orEmpty(files),
		Uncommitted: orEmpty(uncommitted),
		Comments:    orEmpty(placed),
		Replies:     orEmpty(replies),
		Rounds:      orEmpty(comments.Rounds),
		Rev:         comments.Rev,
	}, nil
}

// orEmpty turns a nil slice into an empty one. A clean tree, an empty diff
// (the branch already merged into its base) and a review nobody has
// commented on are the ordinary case, and the frontend reads every one of
// these fields with a plain for-of: Go's nil slice marshals as JSON null,
// which for-of throws on, so none of these fields may ever reach the page
// that way.
func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// Place traces every comment onto today's code: new-side anchors onto head,
// old-side ones onto the current base. One trace diff per distinct source
// commit, not per comment.
func Place(ctx context.Context, g Git, base Base, head string, comments []Comment) map[string]Position {
	type diff struct {
		files []DiffFile
		err   error
	}
	cache := map[[2]string]diff{}
	out := make(map[string]Position, len(comments))
	for _, c := range comments {
		a := c.Anchor
		// An old-side anchor points at code the diff is about to remove, so it
		// is traced onto the base it was removed against; a new-side one is
		// traced onto head. Trace itself never looks at a.Side.
		target := head
		if a.Side == SideOld {
			target = base.Commit
		}
		key := [2]string{a.Commit, target}
		d, ok := cache[key]
		if !ok {
			raw, err := g.RawDiff(ctx, a.Commit, target, true)
			if err == nil {
				d.files, d.err = ParseDiff(raw, 0)
			} else {
				d.err = err
			}
			cache[key] = d
		}
		if d.err != nil {
			out[c.ID] = Position{Path: a.Path, Start: a.Start, End: a.End, State: StateUnavailable}
			continue
		}
		out[c.ID] = Trace(a, d.files)
	}
	return out
}
