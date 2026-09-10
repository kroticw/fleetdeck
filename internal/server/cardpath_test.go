package server

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func patchCard(d Deps, path string) *http.Response {
	rec := do(d, http.MethodPatch, "/api/cards", `{"path":`+quote(path)+`,"field":"progress","value":"40"}`)
	return rec.Result()
}

func quote(s string) string {
	return `"` + strings.ReplaceAll(s, `\`, `\\`) + `"`
}

// board.SetField cannot create a file, but it will rewrite a stage: or progress:
// line in any file that already has frontmatter — another vault, a cloned
// repository, a spec checked out next to the board. The path comes from the
// browser, so this is the boundary that has to refuse it.
func TestPatchCardRefusesAPathOutsideTheBoard(t *testing.T) {
	d, calls, _ := cardDeps(t)
	outside := filepath.Join(t.TempDir(), "victim.md")
	if err := os.WriteFile(outside, []byte("---\nstage: new\n---\n"), 0o600); err != nil {
		t.Fatalf("write victim: %v", err)
	}

	cases := map[string]string{
		"traversal":                        "../../etc/passwd",
		"traversal to a real file outside": filepath.Join("..", filepath.Base(filepath.Dir(outside)), "victim.md"),
		"absolute path outside":            outside,
		"absolute system path":             "/etc/passwd",
		"the board directory itself":       ".",
		"the board's parent":               "..",
	}
	for name, path := range cases {
		t.Run(name, func(t *testing.T) {
			resp := patchCard(d, path)
			if resp.StatusCode != http.StatusForbidden {
				t.Fatalf("a write to %q must be refused with 403, got %d", path, resp.StatusCode)
			}
			if len(*calls) != 0 {
				t.Fatalf("nothing must reach the board, got %v", *calls)
			}
		})
	}
}

// The whole reason both sides are resolved before they are compared: a symlink
// inside the board passes every string-prefix test there is while the write
// lands wherever it points.
func TestPatchCardRefusesASymlinkOutOfTheBoard(t *testing.T) {
	d, calls, card := cardDeps(t)
	outside := filepath.Join(t.TempDir(), "victim.md")
	if err := os.WriteFile(outside, []byte("---\nstage: new\n---\n"), 0o600); err != nil {
		t.Fatalf("write victim: %v", err)
	}
	link := filepath.Join(filepath.Dir(card), "escape.md")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	if resp := patchCard(d, "escape.md"); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a symlink out of the board must be refused with 403, got %d", resp.StatusCode)
	}
	if len(*calls) != 0 {
		t.Fatalf("nothing must reach the board, got %v", *calls)
	}
}

// A directory symlink one level up is the same escape wearing a different hat:
// the card's own name is innocent and the directory it sits in is not.
func TestPatchCardRefusesACardUnderASymlinkedDirectory(t *testing.T) {
	d, calls, card := cardDeps(t)
	outsideDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(outsideDir, "victim.md"), []byte("---\nstage: new\n---\n"), 0o600); err != nil {
		t.Fatalf("write victim: %v", err)
	}
	if err := os.Symlink(outsideDir, filepath.Join(filepath.Dir(card), "elsewhere")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	if resp := patchCard(d, "elsewhere/victim.md"); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a card under a symlinked directory must be refused with 403, got %d", resp.StatusCode)
	}
	if len(*calls) != 0 {
		t.Fatalf("nothing must reach the board, got %v", *calls)
	}
}

// The board directory is resolved too, not only the requested path. On macOS
// /tmp is a symlink to /private/tmp, and comparing a resolved path against an
// unresolved board would refuse every card the panel has — the same mistake that
// broke the daemon client earlier in this project, in the other direction.
func TestPatchCardAcceptsACardWhenTheBoardItselfIsReachedThroughASymlink(t *testing.T) {
	d, calls, card := cardDeps(t)
	link := filepath.Join(t.TempDir(), "board-link")
	if err := os.Symlink(filepath.Dir(card), link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	d.BoardDir = link

	if resp := patchCard(d, "c.md"); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("a real card must still be written, got %d", resp.StatusCode)
	}
	if len(*calls) != 1 || (*calls)[0] != "card:"+card+":progress:40" {
		t.Fatalf("the board must receive the resolved card path, got %v", *calls)
	}
}

// An absolute path is not refused for being absolute — the panel's own snapshot
// gives the browser absolute card paths, and those are what it sends back.
func TestPatchCardAcceptsAnAbsolutePathInsideTheBoard(t *testing.T) {
	d, calls, card := cardDeps(t)
	if resp := patchCard(d, card); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("an absolute path inside the board must be written, got %d", resp.StatusCode)
	}
	if len(*calls) != 1 || (*calls)[0] != "card:"+card+":progress:40" {
		t.Fatalf("unexpected calls: %v", *calls)
	}
}

// A card that is not there is still inside the board, and saying so is the
// board's job: confinement must not turn a 404 into a 403.
func TestPatchCardLetsAMissingCardInsideTheBoardReachTheBoard(t *testing.T) {
	d, calls, card := cardDeps(t)
	if resp := patchCard(d, "gone.md"); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("a missing card must be the board's answer, not a refusal, got %d", resp.StatusCode)
	}
	want := "card:" + filepath.Join(filepath.Dir(card), "gone.md") + ":progress:40"
	if len(*calls) != 1 || (*calls)[0] != want {
		t.Fatalf("unexpected calls: %v", *calls)
	}
}

// A panel that was never told where its board is cannot confine a write. It must
// say so, not fall back to writing wherever the request points.
func TestPatchCardWithoutABoardDirectoryIsUnavailable(t *testing.T) {
	d, calls, card := cardDeps(t)
	d.BoardDir = ""
	if resp := patchCard(d, card); resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("a panel with no board directory must answer 503, got %d", resp.StatusCode)
	}
	if len(*calls) != 0 {
		t.Fatalf("nothing must reach the board, got %v", *calls)
	}
}

func TestPatchCardWithAnUnreadableBoardDirectoryIsUnavailable(t *testing.T) {
	d, calls, card := cardDeps(t)
	d.BoardDir = filepath.Join(t.TempDir(), "there-is-no-board-here")
	if resp := patchCard(d, card); resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("a board directory that does not exist must answer 503, got %d", resp.StatusCode)
	}
	if len(*calls) != 0 {
		t.Fatalf("nothing must reach the board, got %v", *calls)
	}
}
