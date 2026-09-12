package supervisor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// TreeSource is where an app built from a checkout gets a new one: the source
// tree beside it, brought forward and built again.
//
// This is what the update button did before there was anything else, and it
// still does exactly that. It is a Source so that it and ReleaseSource go
// through one Update, one lock, one handover and one swap.
type TreeSource struct {
	Tree  *Tree
	Tools Tools
	Env   []string
	// Running is the commit the window was built from.
	Running string
}

// Check fetches the tree's remote branch and says which commit an update would
// bring it to, or "" when that is the one already running.
//
// The tree is not moved here. A person's working copy is not something to
// fast-forward for a question, and an update with nothing new in it must leave
// their checkout exactly as it was.
func (s *TreeSource) Check(ctx context.Context) (string, error) {
	st, err := s.Tree.Check(ctx)
	if err != nil {
		return "", err
	}
	if st.Upstream == s.Running {
		return "", nil
	}
	return st.Upstream, nil
}

// Stage brings the tree forward to that commit and builds the app into dir.
func (s *TreeSource) Stage(ctx context.Context, dir, version string, say func(Progress)) (string, error) {
	st, err := s.Tree.Forward(ctx)
	if err != nil {
		return "", err
	}
	if st.Head != version {
		// Somebody pushed between the check and here, or moved the tree by
		// hand. Building it anyway would install a version nobody was told
		// about.
		return "", fmt.Errorf("the tree moved to %s while this update was starting, not %s", st.Head, version)
	}
	if say != nil {
		say(Progress{Step: "build", Detail: st.Head})
	}
	if err := Make(ctx, s.Tree.Dir, s.Tools, s.Env, "window-app", "BINDIR="+dir); err != nil {
		return "", err
	}
	staged := filepath.Join(dir, BundleName)
	if _, err := os.Stat(staged); err != nil {
		return "", fmt.Errorf("the build left no %s in %s", BundleName, dir)
	}
	return staged, nil
}
