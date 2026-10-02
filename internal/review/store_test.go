package review

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func anchorAt(line int) Anchor {
	return Anchor{Commit: "c1", Path: "a.go", Side: SideNew, Start: line, End: line, Text: []string{"x"}}
}

func TestANewReviewStartsEmptyAndTheFirstWriteMakesItsDirectory(t *testing.T) {
	t.Parallel()
	dir := Dir(t.TempDir(), "T-057")
	c, err := Load(dir)
	if err != nil || c.Version != 1 || c.Rev != 0 || len(c.Comments) != 0 {
		t.Fatalf("load = %+v, %v", c, err)
	}
	got, err := Update(dir, "T-057", 0, func(c *Comments) error {
		c.AddDraft(anchorAt(8), "wrap it", "", "2026-09-28T10:00:00+03:00")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Rev != 1 || got.Card != "T-057" || got.Comments[0].ID != "c1" || got.Comments[0].Round != 0 {
		t.Fatalf("after the first write: %+v", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "comments.json")); err != nil {
		t.Fatal(err)
	}
}

func TestAWriteAgainstAnOldRevisionIsRefusedAndWritesNothing(t *testing.T) {
	t.Parallel()
	dir := Dir(t.TempDir(), "T-057")
	if _, err := Update(dir, "T-057", 0, func(c *Comments) error { c.AddDraft(anchorAt(1), "a", "", "t"); return nil }); err != nil {
		t.Fatal(err)
	}
	_, err := Update(dir, "T-057", 0, func(c *Comments) error { c.AddDraft(anchorAt(2), "b", "", "t"); return nil })
	if !errors.Is(err, ErrStale) {
		t.Fatalf("err = %v, want ErrStale", err)
	}
	c, _ := Load(dir)
	if len(c.Comments) != 1 {
		t.Fatalf("a refused write must leave the file alone: %+v", c.Comments)
	}
}

func TestConcurrentWritesAtTheSameRevisionLetExactlyOneThrough(t *testing.T) {
	t.Parallel()
	dir := Dir(t.TempDir(), "T-057")
	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		wg.Go(func() {
			_, errs[i] = Update(dir, "T-057", 0, func(c *Comments) error { c.AddDraft(anchorAt(i+1), "x", "", "t"); return nil })
		})
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
		}
	}
	if ok != 1 {
		t.Fatalf("%d writes went through at revision 0, want exactly one", ok)
	}
}

func TestFreezingARoundTakesEveryDraftAndOnlyDrafts(t *testing.T) {
	t.Parallel()
	c := Comments{Version: 1}
	c.AddDraft(anchorAt(1), "a", "", "t")
	n, err := c.Freeze("h1", "t1", map[string]Position{"c1": {Path: "a.go", Start: 1, End: 1, State: StateInPlace}})
	if err != nil || n != 1 || c.Comments[0].Round != 1 {
		t.Fatalf("round %d, %v, %+v", n, err, c.Comments)
	}
	c.AddDraft(anchorAt(2), "b", "c1", "t")
	n, err = c.Freeze("h2", "t2", nil)
	if err != nil || n != 2 || c.Comments[0].Round != 1 || c.Comments[1].Round != 2 {
		t.Fatalf("round %d, %v, %+v", n, err, c.Comments)
	}
	if _, err := c.Freeze("h3", "t3", nil); err == nil {
		t.Fatal("a round with no drafts must be refused")
	}
}

func TestASentCommentCannotBeEditedOrDeleted(t *testing.T) {
	t.Parallel()
	c := Comments{Version: 1}
	id := c.AddDraft(anchorAt(1), "a", "", "t")
	if _, err := c.Freeze("h", "t", nil); err != nil {
		t.Fatal(err)
	}
	if err := c.EditDraft(id, "b"); !errors.Is(err, ErrNotDraft) {
		t.Fatalf("edit: %v", err)
	}
	if err := c.DeleteDraft(id); !errors.Is(err, ErrNotDraft) {
		t.Fatalf("delete: %v", err)
	}
	if err := c.SetResolved(id, true); err != nil || !c.Comments[0].Resolved {
		t.Fatalf("resolve: %v %+v", err, c.Comments[0])
	}
}

func TestIdsAreNeverReusedAfterADraftIsDeleted(t *testing.T) {
	t.Parallel()
	c := Comments{Version: 1}
	c.AddDraft(anchorAt(1), "a", "", "t")
	second := c.AddDraft(anchorAt(2), "b", "", "t")
	if err := c.DeleteDraft(second); err != nil {
		t.Fatal(err)
	}
	if third := c.AddDraft(anchorAt(3), "c", "", "t"); third != "c3" {
		t.Fatalf("id = %s, want c3: an agent may already have read c2 somewhere", third)
	}
}
