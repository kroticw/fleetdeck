package supervisor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// standInSource is a Source that answers what a test tells it to, and records
// what it was asked. What it stands in for is a git tree or a releases page;
// what it does not stand in for is anything below it -- the lock, the staging
// directory, the handover and the swap are the real ones in these tests.
type standInSource struct {
	offer     string
	checkErr  error
	stageErr  error
	staged    []string // versions Stage was asked for
	stagedDir string
	// bundle, when set, is copied into the staging directory as the new app.
	bundle func(dir string) (string, error)
}

func (s *standInSource) Check(context.Context) (string, error) {
	return s.offer, s.checkErr
}

func (s *standInSource) Stage(_ context.Context, dir, version string, say func(Progress)) (string, error) {
	s.staged = append(s.staged, version)
	s.stagedDir = dir
	if say != nil {
		say(Progress{Step: "build", Detail: version})
	}
	if s.stageErr != nil {
		return "", s.stageErr
	}
	return s.bundle(dir)
}

// bundleWithPanel makes the least thing Update will accept as a staged app:
// a bundle with a panel where the window looks for one.
func bundleWithPanel(dir string) (string, error) {
	app := filepath.Join(dir, BundleName)
	if err := os.MkdirAll(filepath.Dir(PanelIn(app)), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(PanelIn(app), []byte("#!/bin/sh\n"), 0o755); err != nil {
		return "", err
	}
	return app, nil
}

// updateWith builds an Update around a source, with a Launch that plays a new
// window reporting whatever steps the test wants.
func updateWith(t *testing.T, src Source, report func(Handover)) (*Update, *[]Progress) {
	t.Helper()
	dir := t.TempDir()
	canonical := filepath.Join(dir, BundleName)
	if err := os.MkdirAll(canonical, 0o755); err != nil {
		t.Fatal(err)
	}
	var seen []Progress
	u := &Update{
		Source:          src,
		Canonical:       canonical,
		LockPath:        filepath.Join(t.TempDir(), "update.lock"),
		HandoverTimeout: 10 * time.Second,
		Launch: func(_, _, handover string) (func(), error) {
			go report(Handover{Path: handover})
			return func() {}, nil
		},
		Pause:    func() {},
		Resume:   func() {},
		Progress: func(p Progress) { seen = append(seen, p) },
	}
	return u, &seen
}

func steps(ps []Progress) string {
	var s []string
	for _, p := range ps {
		s = append(s, p.Step)
	}
	return strings.Join(s, ",")
}

// Update does not know or care whether the new app came from a git tree or
// from a download: it asks a Source what there is, asks it to put that beside
// the installed app, and hands the rest to the new window.
func TestAnUpdateStagesWhateverItsSourceOffers(t *testing.T) {
	src := &standInSource{offer: "v0.4.0", bundle: bundleWithPanel}
	u, seen := updateWith(t, src, func(h Handover) {
		_ = h.Report(StepAlive, "")
		_ = h.Report(StepDone, "")
	})

	if err := u.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(src.staged) != 1 || src.staged[0] != "v0.4.0" {
		t.Fatalf("staged %v, want one staging of v0.4.0", src.staged)
	}
	if src.stagedDir != StagingDir(u.Canonical) {
		t.Errorf("staged into %s, want %s -- it has to be a sibling of the installed app for the swap", src.stagedDir, StagingDir(u.Canonical))
	}
	if got, want := steps(*seen), "check,build,handover,handover:alive,handover:done,done"; got != want {
		t.Fatalf("steps %s, want %s", got, want)
	}
}

// Nothing newer means nothing is downloaded, nothing is built and nothing is
// moved -- and the person is told, rather than left looking at a button that
// did nothing.
func TestAnUpdateWithNothingNewStagesNothing(t *testing.T) {
	src := &standInSource{offer: "", bundle: bundleWithPanel}
	u, seen := updateWith(t, src, func(Handover) {})

	if err := u.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(src.staged) != 0 {
		t.Fatalf("staged %v with nothing newer to stage", src.staged)
	}
	if got, want := steps(*seen), "check,current"; got != want {
		t.Fatalf("steps %s, want %s", got, want)
	}
}

// A source that cannot say what is newest -- no network, a tree on the wrong
// branch -- stops the update there, with its own error kept whole so the
// window can tell one refusal from another.
func TestAnUpdateCarriesTheSourcesRefusalOut(t *testing.T) {
	refusal := &ReleasesUnreachableError{URL: "https://example.invalid", Err: errors.New("no route to host")}
	src := &standInSource{checkErr: refusal}
	u, _ := updateWith(t, src, func(Handover) {})

	err := u.Run(context.Background())

	var unreachable *ReleasesUnreachableError
	if !errors.As(err, &unreachable) {
		t.Fatalf("got %v, want the source's own error", err)
	}
}

// Staging that fails leaves the installed app alone: no new window is started,
// and the keeper of the running one is never paused.
func TestAnUpdateThatCannotStageStartsNoNewWindow(t *testing.T) {
	src := &standInSource{offer: "v0.4.0", stageErr: errors.New("the archive is not signed")}
	launched := false
	u, _ := updateWith(t, src, func(Handover) {})
	u.Launch = func(_, _, _ string) (func(), error) {
		launched = true
		return func() {}, nil
	}

	if err := u.Run(context.Background()); err == nil {
		t.Fatal("an update that could not stage anything reported success")
	}
	if launched {
		t.Fatal("a new window was started from a staging that failed")
	}
}

// The staging directory is cleared before a source is asked to fill it: what
// an earlier update left there is the bundle it swapped out, an old version of
// this very app, and starting that would be an update backwards.
func TestAnUpdateClearsWhatAnEarlierOneLeftStaged(t *testing.T) {
	src := &standInSource{offer: "v0.4.0", bundle: bundleWithPanel}
	u, _ := updateWith(t, src, func(h Handover) {
		_ = h.Report(StepDone, "")
	})
	leftover := filepath.Join(StagingDir(u.Canonical), "left-from-last-time")
	if err := os.MkdirAll(leftover, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := u.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(leftover); err == nil {
		t.Fatal("what an earlier update left staged is still there")
	}
}
