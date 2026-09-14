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

// Removal of the bundle swapped out does not give up (T-056, v0.10.1). While
// the bundle is on disk under /Applications, LaunchServices holds its path
// under the app's own identifier again however often it is told to forget it,
// so removing the bundle is the one thing that holds.

// sayTo hands every logged line to said, dropping none while there is room.
func sayTo(said chan<- string) func(format string, args ...any) {
	return func(format string, args ...any) {
		select {
		case said <- fmt.Sprintf(format, args...):
		default:
		}
	}
}

// gone says whether path is no longer on disk.
func gone(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Lstat(path)
	return os.IsNotExist(err)
}

// toldAfterRemoval says whether LaunchServices was told to forget staged once it
// was gone, and to take canonical after that.
func toldAfterRemoval(calls []registryCall, staged, canonical string) bool {
	for i, c := range calls {
		if c.op == "forget" && c.bundle == staged && c.gone {
			for _, later := range calls[i+1:] {
				if later.op == "register" && later.bundle == canonical {
					return true
				}
			}
		}
	}
	return false
}

// logged collects what a single call logged.
type logged struct{ lines []string }

func (l *logged) logf(format string, args ...any) {
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *logged) said(word string) bool {
	for _, line := range l.lines {
		if strings.Contains(line, word) {
			return true
		}
	}
	return false
}

// withStagingLogs puts the handover file and the new window's log beside the
// bundle swapped out, as an update leaves them.
func withStagingLogs(t *testing.T, r *retireRig) []string {
	t.Helper()
	var paths []string
	for _, name := range []string{"handover", NewWindowLog} {
		path := filepath.Join(StagingDir(r.canonical), name)
		if err := os.WriteFile(path, []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	return paths
}

// An update lock held past the close watch is waited out: removal is tried
// again, and the bundle goes once the lock is let go.
func TestTheBundleSwappedOutIsRemovedOnceTheLockIsLetGoPastTheWait(t *testing.T) {
	r := newRetireRig(t)
	lock := filepath.Join(t.TempDir(), "update.lock")
	release, err := Acquire(lock)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	said := make(chan string, 64)
	registry := &fakeRegistry{handover: filepath.Join(t.TempDir(), "handover")}
	tk := &Takeover{
		Staged:        r.staged,
		Canonical:     r.canonical,
		LockPath:      lock,
		OldWindowGone: func() bool { return true },
		RetireWait:    100 * time.Millisecond,
		RetireEvery:   100 * time.Millisecond,
		Registry:      registry,
		Logf:          sayTo(said),
	}
	done := make(chan struct{})
	go func() { tk.retire(context.Background()); close(done) }()

	waitSaid(t, said, "try 2")
	r.mustExist(r.staged)

	release()
	waitClosed(t, done, "removal once the update lock was let go")
	if !gone(t, r.staged) {
		t.Fatal("the bundle swapped out is still there once the lock was let go")
	}
	r.mustExist(r.canonical)
	if !toldAfterRemoval(registry.told(), r.staged, r.canonical) {
		t.Fatalf("LaunchServices was not told again once the bundle had gone: %+v", registry.told())
	}
}

// A new window that quits while the old one still runs out of the bundle
// leaves it where it is, and the next window's start removes it -- the bundle
// only: the handover file and the new window's log stay.
func TestABundleSwappedOutLeftByAWindowThatQuitIsRemovedAtTheNextStart(t *testing.T) {
	needLsof(t)
	r := newRetireRig(t)
	logs := withStagingLogs(t, r)
	lock := filepath.Join(t.TempDir(), "update.lock")
	said := make(chan string, 64)
	ctx, cancel := context.WithCancel(context.Background())
	tk := &Takeover{
		Staged:        r.staged,
		Canonical:     r.canonical,
		LockPath:      lock,
		OldWindowGone: func() bool { return false },
		RetireWait:    100 * time.Millisecond,
		RetireEvery:   100 * time.Millisecond,
		Logf:          sayTo(said),
	}
	done := make(chan struct{})
	go func() { tk.retire(ctx); close(done) }()
	waitSaid(t, said, "try 1")
	cancel()
	waitClosed(t, done, "removal ending with its window")
	r.mustExist(r.staged)

	registry := &fakeRegistry{handover: filepath.Join(t.TempDir(), "handover")}
	var l logged
	RetireLeftover(context.Background(), r.canonical, lock, registry, l.logf)
	if !gone(t, r.staged) {
		t.Fatalf("the bundle left behind is still there after the next window's start (log: %q)", l.lines)
	}
	r.mustExist(r.canonical)
	for _, path := range logs {
		r.mustExist(path)
	}
	if !toldAfterRemoval(registry.told(), r.staged, r.canonical) {
		t.Fatalf("LaunchServices was not told again once the bundle had gone: %+v", registry.told())
	}
}

// While an update runs, the staging directory may hold the build being
// installed: a start removes nothing then, and says so.
func TestALeftoverBundleIsNotRemovedWhileTheUpdateLockIsHeld(t *testing.T) {
	needLsof(t)
	r := newRetireRig(t)
	lock := filepath.Join(t.TempDir(), "update.lock")
	release, err := Acquire(lock)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	var l logged
	RetireLeftover(context.Background(), r.canonical, lock, nil, l.logf)
	r.mustExist(r.staged)
	if !l.said("lock") {
		t.Fatalf("a start that left the bundle for a held lock said %q", l.lines)
	}
}

// A bundle a process still runs out of -- an old window still quitting -- is not
// removed under it; the next start tries again.
func TestALeftoverBundleIsNotRemovedWhileAProcessRunsOutOfIt(t *testing.T) {
	needLsof(t)
	r := newRetireRig(t)
	self, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(PanelIn(r.staged), self, 0o755); err != nil {
		t.Fatal(err)
	}
	p, err := StartPanel(PanelIn(r.staged), nil, helperEnvFor("listen", freeAddr(t)), filepath.Join(t.TempDir(), "panel.log"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Stop(time.Second) })
	lock := filepath.Join(t.TempDir(), "update.lock")

	var l logged
	RetireLeftover(context.Background(), r.canonical, lock, nil, l.logf)
	r.mustExist(r.staged)
	if !l.said("running") {
		t.Fatalf("a start that left the bundle for a process running out of it said %q", l.lines)
	}

	if err := p.Stop(time.Second); err != nil {
		t.Fatal(err)
	}
	RetireLeftover(context.Background(), r.canonical, lock, nil, l.logf)
	if !gone(t, r.staged) {
		t.Fatalf("the bundle left behind is still there once nothing runs out of it (log: %q)", l.lines)
	}
}
