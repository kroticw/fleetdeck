package supervisor

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// tailLines is how much of a failed build's output an error carries: enough
// to show a compiler error and the lines around it, not the whole log.
const tailLines = 20

// Make runs make with targets in dir, by absolute path, in the environment
// BuildEnv makes from base -- so the recipes find go even where the caller's
// PATH would not have it.
func Make(ctx context.Context, dir string, tools Tools, base []string, targets ...string) error {
	cmd := exec.CommandContext(ctx, tools.Make, append([]string{"-C", dir}, targets...)...)
	cmd.Env = BuildEnv(tools, base)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("make %s failed: %w\n%s", strings.Join(targets, " "), err, tail(out.String(), tailLines))
	}
	return nil
}

func tail(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
