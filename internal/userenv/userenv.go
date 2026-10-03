// Package userenv gives the processes the panel starts the PATH of the person
// who runs it, rather than the one the panel itself was started with.
//
// A Mac app opened from the Dock, or relaunched by an update, is started by
// launchd with PATH=/usr/bin:/bin:/usr/sbin:/sbin and nothing else. The panel's
// children inherit it: git, asked by the operator's configuration to sign a
// commit, looks for gpg on that PATH, does not find Homebrew's, and the commit
// fails. A terminal never shows this, because a terminal's PATH is the one its
// login shell built.
//
// So the panel asks the operator's login shell for its PATH, once, in the
// background, and runs its children with it. The login shell is asked, rather
// than a list of directories guessed here, because it is the one place that
// knows where this operator's tools are: Homebrew on either architecture,
// MacPorts, Nix, a version manager — whatever their terminal finds, the panel's
// children find. The guess is kept only as what happens when the shell does not
// answer.
package userenv

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// timeout bounds the login shell. A profile is expected to take well under a
// second; one that waits on the network or on input it will never get must
// not hold a commit for longer than this. A var solely so a test can shorten
// it.
var timeout = 5 * time.Second

// knownDirs is where package managers put tools on a Mac, added to the PATH
// the panel was started with when the login shell gives no answer.
var knownDirs = []string{"/opt/homebrew/bin", "/opt/homebrew/sbin", "/usr/local/bin", "/usr/local/sbin", "/opt/local/bin"}

// The markers around the PATH the shell prints: a profile may print a greeting
// before it and a logout script a farewell after it.
const (
	begin = "__FLEETDECK_PATH_BEGIN__"
	end   = "__FLEETDECK_PATH_END__"
)

var (
	mu   sync.Mutex
	done chan struct{}
)

// Start finds, in the background, the PATH children are to run with and sets
// it as this process's own, so a child found by name is looked up on it too.
// It returns at once: a panel has seconds to answer the window that started
// it, and a slow profile must not spend them.
func Start() {
	ch := make(chan struct{})
	mu.Lock()
	done = ch
	mu.Unlock()
	go func() {
		defer close(ch)
		inherited := os.Getenv("PATH")
		shell := loginShell(os.Getenv("SHELL"))
		path, err := loginPATH(context.Background(), shell, os.Environ())
		if err != nil {
			path = fallback(inherited, knownDirs)
			log.Printf("fleetdeck: no PATH from the login shell %s (%v); children run with %s", shell, err, path)
		} else {
			path = merge(path, inherited)
		}
		if err := os.Setenv("PATH", path); err != nil {
			log.Printf("fleetdeck: set PATH: %v", err)
		}
	}()
}

// Environ is this process's environment, for a child to run with. After Start
// it waits until the PATH has been found — at most the login shell's timeout,
// once — so the first commit after a launch is not made with launchd's PATH.
// Without Start it is os.Environ.
func Environ() []string {
	mu.Lock()
	ch := done
	mu.Unlock()
	if ch != nil {
		<-ch
	}
	return os.Environ()
}

// loginShell is the shell to ask: the one SHELL names, or the Mac's default
// when launchd handed the panel none.
func loginShell(shell string) string {
	if filepath.IsAbs(shell) {
		return shell
	}
	return "/bin/zsh"
}

// loginPATH is the PATH shell builds as a login shell. It is run with -l and
// a command, not -i: no prompt, no line editor, no interactive rc files. Stdin
// is empty and the shell gets a session of its own, so nothing a profile
// starts can wait on or write to a terminal. On timeout the whole session is
// killed, the shell's children with it.
func loginPATH(ctx context.Context, shell string, environ []string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, shell, "-l", "-c",
		"echo "+begin+"; /usr/bin/printenv PATH; echo "+end)
	cmd.Env = environ
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = time.Second
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "", fmt.Errorf("no answer in %s", timeout)
	}
	// A profile that leaves a child holding stdout open ends in ErrWaitDelay
	// with the answer already printed; anything else is the shell failing.
	if err != nil && !errors.Is(err, exec.ErrWaitDelay) {
		return "", err
	}
	_, after, ok := strings.Cut(out.String(), begin+"\n")
	if !ok {
		return "", errors.New("the shell printed no PATH")
	}
	path, _, ok := strings.Cut(after, "\n"+end)
	if !ok || strings.TrimSpace(path) == "" {
		return "", errors.New("the shell printed no PATH")
	}
	return strings.TrimSpace(path), nil
}

// merge is primary followed by every directory of rest it does not already
// name, in order.
func merge(primary, rest string) string {
	dirs := filepath.SplitList(primary)
	seen := make(map[string]bool, len(dirs))
	for _, d := range dirs {
		seen[d] = true
	}
	for _, d := range filepath.SplitList(rest) {
		if d != "" && !seen[d] {
			seen[d] = true
			dirs = append(dirs, d)
		}
	}
	return strings.Join(dirs, string(filepath.ListSeparator))
}

// fallback is inherited with every directory of known that exists added after
// it.
func fallback(inherited string, known []string) string {
	var there []string
	for _, d := range known {
		if info, err := os.Stat(d); err == nil && info.IsDir() {
			there = append(there, d)
		}
	}
	return merge(inherited, strings.Join(there, string(filepath.ListSeparator)))
}
