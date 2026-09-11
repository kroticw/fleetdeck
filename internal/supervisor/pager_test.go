package supervisor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The old rebuild script hung on a pager: `git log` at its end handed its
// output to less, and the operator watched a frozen screen for minutes. The
// same script run by a program never hung, because git starts a pager only
// when its output is a terminal -- which is exactly why a test cannot
// reproduce it either: there is no terminal here. What can be checked is that
// the pager is switched off on every call, whoever calls, so the day someone
// adds a git command, or runs one with a terminal attached, it cannot come
// back.
//
// What these tests do NOT show: that nothing hangs when a person runs the
// supervisor from a terminal. They check the switch, not the hang. The hang
// lives in how the program is started -- terminal or not -- and no test here
// starts it that way. "There is a pager test" is not "the hang cannot happen";
// it is "the switch is on in every place the switch exists".

// fakeGit writes every invocation's arguments to a log, one line each, and
// answers the few commands Check asks with something plausible.
func fakeGit(t *testing.T) (bin, log string) {
	t.Helper()
	dir := t.TempDir()
	log = filepath.Join(dir, "calls")
	bin = filepath.Join(dir, "git")
	script := `#!/bin/sh
echo "$*" >> '` + log + `'
case "$*" in
  *symbolic-ref*) echo master ;;
  *rev-list*) printf '0\t0\n' ;;
  *rev-parse*) echo 0123456789 ;;
esac
exit 0
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, log
}

func TestEveryGitCallSwitchesThePagerOff(t *testing.T) {
	bin, log := fakeGit(t)
	tree := &Tree{Dir: t.TempDir(), Remote: "origin", Branch: "master", Git: bin, Env: os.Environ()}
	if _, err := tree.Forward(context.Background()); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	calls := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(calls) < 4 {
		t.Fatalf("only %d git calls recorded -- the fake is not being run: %q", len(calls), calls)
	}
	for _, call := range calls {
		if !strings.HasPrefix(call, "--no-pager ") {
			t.Errorf("git called with the pager on: %q", call)
		}
	}
}

// make's recipes may call git by name too, and they run with BuildEnv.
func TestTheBuildEnvironmentSwitchesThePagerOff(t *testing.T) {
	env := BuildEnv(Tools{}, []string{"PAGER=less", "GIT_PAGER=less"})
	if lookup(env, "GIT_PAGER") != "cat" || lookup(env, "PAGER") != "cat" {
		t.Fatalf("GIT_PAGER=%q PAGER=%q, want both cat", lookup(env, "GIT_PAGER"), lookup(env, "PAGER"))
	}
	for _, kv := range env {
		if kv == "PAGER=less" || kv == "GIT_PAGER=less" {
			t.Fatalf("the caller's pager survived: %q", kv)
		}
	}
}
