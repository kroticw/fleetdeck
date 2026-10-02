package review

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBuildNeverReturnsNullSlicesForACleanTreeWithAnEmptyDiff is fix round 1,
// item 2: Go encodes a nil slice as JSON null, and the frontend was never
// written to expect one — a clean tree (no uncommitted files) and an empty
// diff (the branch already merged into its base) are the ordinary case, not
// an edge case, and crashed the overlay before this test existed.
func TestBuildNeverReturnsNullSlicesForACleanTreeWithAnEmptyDiff(t *testing.T) {
	t.Parallel()
	dir, run := repo(t)
	run("update-ref", "refs/remotes/origin/master", run("rev-parse", "HEAD"))
	run("symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/master")
	g := Git{Dir: dir}
	reviewDir := Dir(t.TempDir(), "T-001")
	v, err := Build(t.Context(), g, reviewDir)
	if err != nil {
		t.Fatal(err)
	}
	if v.Base.Commit != v.Head {
		t.Fatalf("base %s != head %s: the stand does not describe a merged branch", v.Base.Commit, v.Head)
	}
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"files":[]`, `"uncommitted":[]`, `"comments":[]`, `"replies":[]`, `"rounds":[]`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("json = %s, want it to contain %s", raw, want)
		}
	}
}

const v1 = "package a\n\nimport \"fmt\"\n\nfunc Load(path string) error {\n\traw, err := read(path)\n\tif err != nil {\n\t\treturn err\n\t}\n\trecord(len(raw))\n\treturn nil\n}\n\nfunc read(path string) ([]byte, error) {\n\treturn nil, nil\n}\n"
const v2 = "package a\n\nimport \"fmt\"\n\n// Load reads path and reports its size.\n// It never retries.\n// Errors are wrapped.\nfunc Load(path string) error {\n\traw, err := read(path)\n\tif err != nil {\n\t\treturn fmt.Errorf(\"read %s: %w\", path, err)\n\t}\n\treturn nil\n}\n\nfunc read(path string) ([]byte, error) {\n\treturn nil, nil\n}\n"

// stage is the design document's stand: a branch off origin/master holding
// v1 of a.go, a comment on each of lines 6, 8, 10 and 11, then the agent's v2.
func stage(t *testing.T) (Git, string) {
	t.Helper()
	dir, run := repo(t)
	run("update-ref", "refs/remotes/origin/master", run("rev-parse", "HEAD"))
	run("symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/master")
	run("switch", "--quiet", "--create", "work")
	write(t, dir, "a.go", v1)
	run("commit", "--quiet", "--all", "--message", "v1")
	c1 := run("rev-parse", "HEAD")
	g := Git{Dir: dir}
	reviewDir := Dir(t.TempDir(), "T-001")
	_, err := Update(reviewDir, "T-001", 0, func(c *Comments) error {
		for _, line := range []int{6, 8, 10, 11} {
			a, err := NewAnchor(t.Context(), g, c1, "a.go", SideNew, line, line)
			if err != nil {
				return err
			}
			c.AddDraft(a, "note", "", "t")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	write(t, dir, "a.go", v2)
	run("commit", "--quiet", "--all", "--message", "v2")
	return g, reviewDir
}

func TestTheStandsCommentsAreTracedOntoTheNewHead(t *testing.T) {
	t.Parallel()
	g, reviewDir := stage(t)
	v, err := Build(t.Context(), g, reviewDir)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]Position{
		"c1": {Path: "a.go", Start: 9, End: 9, State: StateInPlace},
		"c2": {Path: "a.go", Start: 11, End: 11, State: StateChanged},
		"c3": {Path: "a.go", Start: 12, End: 12, State: StateDeleted},
		"c4": {Path: "a.go", Start: 13, End: 13, State: StateInPlace},
	}
	for _, p := range v.Comments {
		if p.Position != want[p.ID] {
			t.Errorf("%s: %+v, want %+v", p.ID, p.Position, want[p.ID])
		}
	}
	if len(v.Files) != 1 || v.Files[0].NewPath != "a.go" {
		t.Fatalf("files = %+v", v.Files)
	}
}

func TestAnAnchorTakesItsTextFromTheCommitNotFromThePage(t *testing.T) {
	t.Parallel()
	_, reviewDir := stage(t)
	c, err := Load(reviewDir)
	if err != nil {
		t.Fatal(err)
	}
	a := c.Comments[1].Anchor
	if len(a.Text) != 1 || a.Text[0] != "\t\treturn err" || len(a.Before) != 2 || len(a.After) != 2 {
		t.Fatalf("anchor = %+v", a)
	}
}

func TestACommitThatIsGoneLeavesItsCommentUnavailableNotTheWholeReview(t *testing.T) {
	t.Parallel()
	g, reviewDir := stage(t)
	if _, err := Update(reviewDir, "T-001", 1, func(c *Comments) error {
		c.Comments[0].Anchor.Commit = "1111111111111111111111111111111111111111"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	v, err := Build(t.Context(), g, reviewDir)
	if err != nil {
		t.Fatalf("one lost commit must not fail the review: %v", err)
	}
	if v.Comments[0].Position.State != StateUnavailable || v.Comments[1].Position.State != StateChanged {
		t.Fatalf("positions = %+v %+v", v.Comments[0].Position, v.Comments[1].Position)
	}
}

func TestUncommittedFilesAreListed(t *testing.T) {
	t.Parallel()
	g, reviewDir := stage(t)
	if err := os.WriteFile(filepath.Join(g.Dir, "wip.go"), []byte("package a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	v, err := Build(t.Context(), g, reviewDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Uncommitted) != 1 || v.Uncommitted[0] != "wip.go" {
		t.Fatalf("uncommitted = %v", v.Uncommitted)
	}
}
