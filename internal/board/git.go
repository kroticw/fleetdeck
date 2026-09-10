package board

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// Commit stages exactly one file and commits it. Other changes in the working
// tree are left alone: the panel commits what it wrote and nothing else.
func Commit(dir, file, message string) error {
	if out, err := run(dir, "rev-parse", "--git-dir"); err != nil {
		return fmt.Errorf("not a git repository: %s", strings.TrimSpace(out))
	}
	if out, err := run(dir, "add", "--", file); err != nil {
		return fmt.Errorf("stage %s: %s", file, strings.TrimSpace(out))
	}
	staged, err := run(dir, "diff", "--cached", "--name-only", "--", file)
	if err != nil {
		return fmt.Errorf("inspect staged changes: %s", strings.TrimSpace(staged))
	}
	if strings.TrimSpace(staged) == "" {
		return fmt.Errorf("nothing to commit for %s", file)
	}
	if out, err := run(dir, "commit", "--signoff", "--message", message, "--", file); err != nil {
		return fmt.Errorf("commit %s: %s", file, strings.TrimSpace(out))
	}
	return nil
}

func run(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	return buf.String(), err
}
