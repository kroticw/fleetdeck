package server

import (
	"context"
	"errors"
	"net/http"
	"os/exec"
	"strings"
	"testing"
)

// fakeOsascript answers what osascript would, and records the arguments it got.
func fakeOsascript(out string, err error, got *[]string) func(context.Context, ...string) ([]byte, error) {
	return func(_ context.Context, args ...string) ([]byte, error) {
		*got = args
		return []byte(out), err
	}
}

// The browser cannot say where a chosen folder is, so the panel asks the Finder
// and hands back the folder the way a card's repo holds it: from the home
// directory, without the trailing slash AppleScript puts on a folder.
func TestChooseFolderAnswersTheRepoFromHome(t *testing.T) {
	t.Parallel()
	var got []string
	pick := ChooseFolder("/Users/me", fakeOsascript("/Users/me/src/fleetdeck/\n", nil, &got))
	repo, err := pick(t.Context(), "Выберите репозиторий")
	if err != nil {
		t.Fatal(err)
	}
	if repo != "src/fleetdeck" {
		t.Fatalf("repo = %q, want src/fleetdeck", repo)
	}
	// The prompt is an argument of the script, never part of its text: a quote
	// in it must not be able to change what the script does.
	if len(got) == 0 || got[len(got)-1] != "Выберите репозиторий" {
		t.Fatalf("the prompt must be the script's last argument: %q", got)
	}
	if strings.Contains(strings.Join(got[:len(got)-1], " "), "Выберите") {
		t.Fatalf("the prompt leaked into the script: %q", got)
	}
}

func TestChooseFolderTakesACancelForNoChoice(t *testing.T) {
	t.Parallel()
	var got []string
	pick := ChooseFolder("/Users/me", fakeOsascript("0:1: execution error: User canceled. (-128)\n", &exec.ExitError{}, &got))
	repo, err := pick(t.Context(), "p")
	if err != nil || repo != "" {
		t.Fatalf("a cancel is no choice and no error, got %q, %v", repo, err)
	}
}

// A card's repo is a path inside the home directory; a folder outside it is
// refused here rather than written into a card the dispatch then refuses.
func TestChooseFolderRefusesAFolderOutsideHome(t *testing.T) {
	t.Parallel()
	var got []string
	for _, out := range []string{"/Volumes/disk/proj/", "/Users/me/"} {
		pick := ChooseFolder("/Users/me", fakeOsascript(out, nil, &got))
		if repo, err := pick(t.Context(), "p"); err == nil {
			t.Fatalf("%s: want a refusal, got %q", out, repo)
		}
	}
}

func TestChooseFolderReportsAFailedScript(t *testing.T) {
	t.Parallel()
	var got []string
	pick := ChooseFolder("/Users/me", fakeOsascript("boom", errors.New("exit status 1"), &got))
	if _, err := pick(t.Context(), "p"); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("the script's own words must reach the operator: %v", err)
	}
}

func TestPickDirectoryRouteAnswersTheRepo(t *testing.T) {
	t.Parallel()
	d, _ := testDeps()
	var prompt string
	d.PickDirectory = func(_ context.Context, p string) (string, error) {
		prompt = p
		return "src/fleetdeck", nil
	}
	rec := do(d, http.MethodPost, "/api/pick-directory", `{"prompt":"Выберите"}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"repo":"src/fleetdeck"`) {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	if prompt != "Выберите" {
		t.Fatalf("prompt = %q", prompt)
	}
}

// Off macOS there is no Finder to ask, and the panel says so rather than
// pretending the route is missing.
func TestPickDirectoryRouteWithoutAPickerIsUnavailable(t *testing.T) {
	t.Parallel()
	d, _ := testDeps()
	if rec := do(d, http.MethodPost, "/api/pick-directory", `{}`); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestPickDirectoryRouteReportsARefusal(t *testing.T) {
	t.Parallel()
	d, _ := testDeps()
	d.PickDirectory = func(context.Context, string) (string, error) { return "", errors.New("outside home") }
	rec := do(d, http.MethodPost, "/api/pick-directory", `{}`)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "outside home") {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
}
