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
//
// It resolves the tools it needs itself, when it needs them, rather than being
// handed them. That is not tidiness: the caller deciding which way an app
// updates must not have to know whether git and go are where they were at
// build time. CI caught this the other way round -- on a runner where go sits
// somewhere FindTools does not look, a build with a checkout written into it
// called itself a build with no checkout, and offered a person the one piece
// of advice that could not help them: rebuild from your clone.
type TreeSource struct {
	// Dir, Remote and Branch are the checkout and the branch of its remote it
	// follows.
	Dir, Remote, Branch string
	// Embedded are the tool paths written into the build; where they no
	// longer are, FindTools looks in the well-known places.
	Embedded Tools
	Env      []string
	// Running is the commit the window was built from.
	Running string
	// Exists is FileExists in production, and a stand-in in tests.
	Exists func(string) bool
}

// tools resolves this source's tools and the tree that uses them. Its error is
// the one a person reads when git or go has gone: it names what is missing and
// where it was looked for.
func (s *TreeSource) tools() (Tools, *Tree, error) {
	exists := s.Exists
	if exists == nil {
		exists = FileExists
	}
	found, err := FindTools(s.Embedded, exists)
	if err != nil {
		return Tools{}, nil, err
	}
	return found, &Tree{
		Dir: s.Dir, Remote: s.Remote, Branch: s.Branch,
		Git: found.Git, Env: BuildEnv(found, s.Env),
	}, nil
}

// Check fetches the tree's remote branch and says which commit an update would
// bring it to, or "" when that is the one already running.
//
// The tree is not moved here. A person's working copy is not something to
// fast-forward for a question, and an update with nothing new in it must leave
// their checkout exactly as it was.
func (s *TreeSource) Check(ctx context.Context) (string, error) {
	_, tree, err := s.tools()
	if err != nil {
		return "", err
	}
	st, err := tree.Check(ctx)
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
	tools, tree, err := s.tools()
	if err != nil {
		return "", err
	}
	st, err := tree.Forward(ctx)
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
	if err := Make(ctx, s.Dir, tools, s.Env, "window-app", "BINDIR="+dir); err != nil {
		return "", err
	}
	staged := filepath.Join(dir, BundleName)
	if _, err := os.Stat(staged); err != nil {
		return "", fmt.Errorf("the build left no %s in %s", BundleName, dir)
	}
	return staged, nil
}
