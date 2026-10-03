package board

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kroticw/fleetdeck/internal/userenv"
)

// launchdPATH is what a Mac app opened from the Dock is started with, and
// nothing else.
const launchdPATH = "/usr/bin:/bin:/usr/sbin:/sbin"

// A panel opened from the Dock has launchd's PATH, and the operator's gpg is in
// a directory only their login shell puts on it (Homebrew's). A card commit
// signed the way the operator's git configuration asks must still be made, and
// signed — not made unsigned, not refused with "cannot run gpg".
func TestCommitSignsWithGPGOnlyOnTheLoginShellPATH(t *testing.T) {
	if !onPATH(launchdPATH, "git") {
		t.Skipf("no git on %s; this test needs git where a Dock-started panel finds it", launchdPATH)
	}

	// A gpg that git can sign with, in a directory launchd's PATH does not
	// name: it prints the status line git waits for and an armoured block.
	gpgDir := t.TempDir()
	writeScript(t, filepath.Join(gpgDir, "gpg"), `#!/bin/sh
cat >/dev/null
echo '[GNUPG:] SIG_CREATED D 1 8 00 1700000000 0000000000000000000000000000000000000000' >&2
printf -- '-----BEGIN PGP SIGNATURE-----\n\nZmFrZQ==\n-----END PGP SIGNATURE-----\n'
`)

	// The operator's login shell: its profile puts gpgDir on PATH, the way
	// Homebrew's shellenv line in ~/.zprofile does.
	shell := filepath.Join(t.TempDir(), "loginsh")
	writeScript(t, shell, `#!/bin/sh
PATH="`+gpgDir+`:$PATH"
export PATH
[ "$1" = "-l" ] && shift
exec /bin/sh "$@"
`)

	dir := initRepo(t)
	for _, args := range [][]string{
		{"config", "commit.gpgsign", "true"},
		{"config", "user.signingkey", "test"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	writeCard(t, dir, "a.md", sample)

	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("SHELL", shell)
	t.Setenv("PATH", launchdPATH)
	userenv.Start()

	if err := Commit(dir, "a.md", "test: signed commit"); err != nil {
		t.Fatalf("a Dock-started panel must commit a card signed with the operator's gpg: %v", err)
	}
	cmd := exec.Command("git", "cat-file", "commit", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "gpgsig ") {
		t.Fatalf("the commit must carry a signature, got:\n%s", out)
	}
}

func onPATH(path, name string) bool {
	for _, dir := range filepath.SplitList(path) {
		if info, err := os.Stat(filepath.Join(dir, name)); err == nil && !info.IsDir() {
			return true
		}
	}
	return false
}

func writeScript(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}
