package supervisor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The new window removes the bundle swapped out, beside the installed app --
// on a person's machine, inside /Applications. What it removes is checked by
// the removal itself, not trusted to how Staged and Canonical were worked out:
// a wrong path there would be a deleted app.

type retireRig struct {
	t         *testing.T
	canonical string
	staged    string
	logged    []string
}

func newRetireRig(t *testing.T) *retireRig {
	t.Helper()
	apps := t.TempDir()
	r := &retireRig{t: t, canonical: filepath.Join(apps, BundleName), staged: filepath.Join(StagingDir(filepath.Join(apps, BundleName)), BundleName)}
	for _, b := range []string{r.canonical, r.staged} {
		if err := os.MkdirAll(filepath.Join(b, "Contents", "MacOS"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(PanelIn(b), []byte(b), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

func (r *retireRig) retire(staged, canonical string) {
	tk := &Takeover{
		Staged:    staged,
		Canonical: canonical,
		LockPath:  filepath.Join(r.t.TempDir(), "update.lock"),
		Logf: func(format string, args ...any) {
			r.logged = append(r.logged, fmt.Sprintf(format, args...))
		},
	}
	tk.retire(context.Background())
}

func (r *retireRig) mustExist(path string) {
	r.t.Helper()
	if _, err := os.Lstat(path); err != nil {
		r.t.Fatalf("%s is gone: %v (log: %q)", path, err, r.logged)
	}
}

func (r *retireRig) mustHaveSaid(word string) {
	r.t.Helper()
	for _, l := range r.logged {
		if strings.Contains(l, word) {
			return
		}
	}
	r.t.Fatalf("the refusal was not logged with %q: %q", word, r.logged)
}

func TestTheBundleSwappedOutIsRemovedFromBesideTheInstalledApp(t *testing.T) {
	r := newRetireRig(t)
	r.retire(r.staged, r.canonical)
	if _, err := os.Stat(r.staged); !os.IsNotExist(err) {
		t.Fatalf("the bundle swapped out is still there: %v (log: %q)", err, r.logged)
	}
	r.mustExist(r.canonical)
}

func TestNothingIsRemovedFromAnywhereButTheStagingDirectoryOfTheInstalledApp(t *testing.T) {
	r := newRetireRig(t)
	elsewhere := filepath.Join(t.TempDir(), BundleName)
	if err := os.MkdirAll(elsewhere, 0o755); err != nil {
		t.Fatal(err)
	}
	r.retire(elsewhere, r.canonical)
	r.mustExist(elsewhere)
	r.mustExist(r.canonical)
	r.mustHaveSaid("staging directory")
}

func TestTheInstalledAppIsNeverRemovedAsTheBundleSwappedOut(t *testing.T) {
	r := newRetireRig(t)
	r.retire(r.canonical, r.canonical)
	r.mustExist(r.canonical)
	r.mustExist(r.staged)
	r.mustHaveSaid("installed")
}

func TestNoBundleNamedRemovesNothing(t *testing.T) {
	r := newRetireRig(t)
	r.retire("", r.canonical)
	r.mustExist(r.canonical)
	r.mustExist(r.staged)
	r.mustHaveSaid("no bundle")
}

// A symlink at the staged path would have RemoveAll take the link, and a check
// that resolved it first would compare its target -- the installed app, say.
func TestABundleSwappedOutThatIsASymlinkIsNotRemoved(t *testing.T) {
	r := newRetireRig(t)
	if err := os.RemoveAll(r.staged); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(r.canonical, r.staged); err != nil {
		t.Fatal(err)
	}
	r.retire(r.staged, r.canonical)
	r.mustExist(r.staged)
	r.mustExist(PanelIn(r.canonical))
	r.mustHaveSaid("symlink")
}

// The old window gone is not the lock free: a second window or a terminal may
// have started an update of the same app, into the same staging directory. The
// bundle stays until the lock is free.
func TestTheBundleSwappedOutWaitsForTheLockEvenOnceTheOldWindowHasQuit(t *testing.T) {
	r := newRetireRig(t)
	lock := filepath.Join(t.TempDir(), "update.lock")
	release, err := Acquire(lock)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	said := make(chan string, 16)
	tk := &Takeover{
		Staged:        r.staged,
		Canonical:     r.canonical,
		LockPath:      lock,
		OldWindowGone: func() bool { return true },
		RetireWait:    10 * time.Second,
		Logf: func(format string, args ...any) {
			select {
			case said <- fmt.Sprintf(format, args...):
			default:
			}
		},
	}
	done := make(chan struct{})
	go func() { tk.retire(context.Background()); close(done) }()

	for waiting := false; !waiting; {
		select {
		case line := <-said:
			waiting = strings.Contains(line, "waiting")
		case <-done:
			t.Fatal("retire returned while another holder had the update lock")
		case <-time.After(10 * time.Second):
			t.Fatal("retire neither waited for the lock nor returned within 10s")
		}
	}
	r.mustExist(r.staged)

	release()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("retire did not end within 10s of the lock being let go")
	}
	if _, err := os.Stat(r.staged); !os.IsNotExist(err) {
		t.Fatalf("the bundle swapped out is still there once the lock was free: %v", err)
	}
	r.mustExist(r.canonical)
}

// An installed app that is a symlink into its own staging directory resolves
// to the very bundle in that directory, so the check that Staged is the bundle
// in the installed app's staging directory passes. Only the check that Staged
// is not the installed app refuses -- and without it the removal takes what
// the installed app points at.
func TestAnInstalledAppThatIsASymlinkIntoItsStagingDirectoryIsNotRemoved(t *testing.T) {
	apps := t.TempDir()
	canonical := filepath.Join(apps, BundleName)
	staged := filepath.Join(StagingDir(canonical), BundleName)
	if err := os.MkdirAll(filepath.Dir(PanelIn(staged)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(PanelIn(staged), []byte("the installed app, through its symlink"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(staged, canonical); err != nil {
		t.Fatal(err)
	}
	r := &retireRig{t: t, canonical: canonical, staged: staged}
	r.retire(staged, canonical)
	r.mustExist(PanelIn(canonical))
	r.mustHaveSaid("installed")
}
