package server

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/kroticw/fleetdeck/internal/board"
)

// chooseFolderScript asks the Finder for a folder. The prompt arrives as argv
// rather than inside the script's text, so nothing typed into it is AppleScript.
// activate brings the dialog in front of the window that asked for it: osascript
// run by the panel is not the frontmost application.
var chooseFolderScript = []string{
	"-e", "on run argv",
	"-e", "activate",
	"-e", "return POSIX path of (choose folder with prompt (item 1 of argv) default location (path to home folder))",
	"-e", "end run",
}

// ChooseFolder is Deps.PickDirectory on macOS: osascript, run through run, shows
// the Finder's folder dialog, and the folder comes back as a card's repo holds
// it, from home. A cancelled dialog is "" and no error.
func ChooseFolder(home string, run func(ctx context.Context, args ...string) ([]byte, error)) func(ctx context.Context, prompt string) (string, error) {
	return func(ctx context.Context, prompt string) (string, error) {
		out, err := run(ctx, append(append([]string{}, chooseFolderScript...), prompt)...)
		text := strings.TrimSpace(string(out))
		if err != nil {
			// -128 is AppleScript's "User canceled".
			if strings.Contains(text, "(-128)") {
				return "", nil
			}
			return "", fmt.Errorf("osascript: %w: %s", err, text)
		}
		rel, err := filepath.Rel(home, filepath.Clean(text))
		if err != nil || rel == "." || !filepath.IsLocal(rel) {
			return "", fmt.Errorf("%s is not a folder inside the home directory %s", text, home)
		}
		return board.NormalizeRepo(rel)
	}
}

// handlePickDirectory opens the folder dialog and answers the chosen repo, ""
// when the operator cancelled. The page cannot do this itself: a browser never
// tells a page where a folder it was given lives. It answers once the dialog
// is closed, which takes as long as the operator does.
func (d Deps) handlePickDirectory(w http.ResponseWriter, r *http.Request) {
	if d.PickDirectory == nil {
		unavailable(w, "a folder dialog: it is macOS only")
		return
	}
	var body struct {
		Prompt string `json:"prompt"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	repo, err := d.PickDirectory(r.Context(), body.Prompt)
	if err != nil {
		fail(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"repo": repo})
}
