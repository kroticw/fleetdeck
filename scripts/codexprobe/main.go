// codexprobe inspects the control API for an explicitly selected test directory.
// It never creates threads or submits prompts.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func main() {
	socket := flag.String("socket", "", "existing Codex Unix control socket")
	cwd := flag.String("cwd", "", "absolute disposable test repository")
	flag.Parse()
	if *socket == "" || *cwd == "" || !filepath.IsAbs(*socket) || !filepath.IsAbs(*cwd) {
		fmt.Fprintln(os.Stderr, "--socket and --cwd must be explicit absolute paths")
		os.Exit(2)
	}
	_, err := filepath.EvalSymlinks(*cwd)
	if err != nil {
		fmt.Fprintln(os.Stderr, "test directory is unavailable")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	report, err := inspectControl(ctx, *socket, *cwd)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err = json.NewEncoder(os.Stdout).Encode(report); err != nil {
		fmt.Fprintln(os.Stderr, "cannot write report")
		os.Exit(1)
	}
}
