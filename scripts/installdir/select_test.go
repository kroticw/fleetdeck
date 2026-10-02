// Package installdir tests scripts/select-fleetdeck-install-dir.sh, the prompt
// `make fleetdeck` asks where to put fleetdeck.app.
package installdir

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// choose runs the script for a checkout at project with home as $HOME and
// answers it with input. stdout is what make passes on as BINDIR, so it must
// hold the directory and nothing else; the questions go to stderr.
func choose(t *testing.T, project, home, input string) (stdout, stderr string, err error) {
	t.Helper()
	script, err := filepath.Abs("../select-fleetdeck-install-dir.sh")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", script, project)
	cmd.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin"}
	cmd.Stdin = strings.NewReader(input)
	var out, errOut strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err = cmd.Run()
	return out.String(), errOut.String(), err
}

func TestTheInstallDirectoryIsChosenFromTheAnswer(t *testing.T) {
	const project, home = "/src/fleetdeck", "/Users/someone"
	for _, tc := range []struct {
		name, input, want string
	}{
		{"Enter takes the checkout's bin", "\n", project + "/bin"},
		{"1 is the checkout's bin", "1\n", project + "/bin"},
		{"no answer at all is the default", "", project + "/bin"},
		{"2 is the user's Applications", "2\n", home + "/Applications"},
		{"3 takes a directory as typed", "3\n/opt/apps\n", "/opt/apps"},
		{"3 expands a leading ~/", "3\n~/Apps\n", home + "/Apps"},
		{"3 expands a lone ~", "3\n~\n", home},
		{"an empty directory asks again", "3\n\n2\n", home + "/Applications"},
		{"an unknown choice asks again", "9\n2\n", home + "/Applications"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, err := choose(t, project, home, tc.input)
			if err != nil {
				t.Fatalf("exit: %v\n%s", err, stderr)
			}
			if stdout != tc.want+"\n" {
				t.Errorf("stdout = %q, want only %q", stdout, tc.want+"\n")
			}
			if !strings.Contains(stderr, "Where should fleetdeck.app be installed?") {
				t.Errorf("the question must be on stderr, where make leaves it visible: %q", stderr)
			}
		})
	}
}

func TestARepeatedQuestionSaysWhatWasWrong(t *testing.T) {
	_, stderr, _ := choose(t, "/src/fleetdeck", "/Users/someone", "9\n3\n\n1\n")
	for _, want := range []string{"Choose 1, 2, or 3.", "Directory cannot be empty."} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr must say %q: %q", want, stderr)
		}
	}
}

func TestTheScriptNeedsTheCheckout(t *testing.T) {
	cmd := exec.Command("sh", "../select-fleetdeck-install-dir.sh")
	if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "usage:") {
		t.Errorf("without the checkout it must refuse with its usage: %v %q", err, out)
	}
}
