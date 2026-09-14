package supervisor

import (
	"context"
	"strings"
	"testing"
	"time"
)

// The window binary hears its keeper through KeeperEvents, and a takeover
// through Take. If the takeover heard the keeper only once the window read its
// events -- or read them after the takeover began -- it would never hear its
// own panel answer, and the old window would give up on the update by its
// deadline. Here the window never reads at all, and the update still ends done.
func TestAnUpdateEndsDoneThroughTheWindowsKeeperEventsUnread(t *testing.T) {
	r := newUpdateRig(t)
	r.viaKeeperEvents = true
	head := r.f.run(r.f.other, "rev-parse", "HEAD")

	u := r.update("old")
	u.HandoverTimeout = 10 * time.Second
	if err := u.Run(context.Background()); err != nil {
		t.Fatalf("update: %v (steps %v)", err, r.steps())
	}
	want := []string{"check", "build", "handover", "handover:alive", "handover:panel", "handover:swapped", "handover:done", "done"}
	if got := r.steps(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("steps %v, want %v", got, want)
	}
	if !waitRevision(r.url, head, 5*time.Second) {
		t.Fatal("the panel answering after the update is not the new build")
	}
}
