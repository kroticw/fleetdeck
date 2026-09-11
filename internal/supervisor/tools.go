// Package supervisor keeps the panel up to date and running on the operator's
// machine, so that nothing about it needs a terminal: it checks whether the
// source tree has fallen behind, brings it forward, rebuilds, and starts and
// stops the panel process.
//
// It is a plain package with no cgo on purpose. The window that uses it
// (cmd/fleetdeck-window) builds only on darwin, with cgo; this package is
// tested on every leg of CI, including the one that never builds the window.
//
// Everything it runs, it runs by absolute path with an environment it builds
// itself. The program using it is an app started from the Dock, and such an
// app does not have the operator's shell around it: launchd gives it the
// system's four directories as PATH and nothing more, so a tool found by name
// in a terminal is not found at all there.
package supervisor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Tools are the absolute paths of the programs an update runs.
type Tools struct {
	Git  string
	Go   string
	Make string
}

// wellKnown lists where each tool is looked for when the path written in at
// build time is missing or gone, first to last. PATH is not among them: in an
// app started from the Dock it is the system's four directories, and go is in
// none of them on a standard install.
var wellKnown = map[string][]string{
	"git":  {"/usr/bin/git", "/opt/homebrew/bin/git", "/usr/local/bin/git"},
	"go":   {"/usr/local/go/bin/go", "/opt/homebrew/bin/go", "/usr/local/bin/go"},
	"make": {"/usr/bin/make", "/opt/homebrew/bin/make", "/usr/local/bin/make"},
}

// FindTools resolves each tool: the embedded path if it still exists, then
// the well-known places. exists is os.Stat in production and a fake in tests.
func FindTools(embedded Tools, exists func(string) bool) (Tools, error) {
	var found Tools
	var missing []string
	for _, tool := range []struct {
		name string
		from string
		into *string
	}{
		{"git", embedded.Git, &found.Git},
		{"go", embedded.Go, &found.Go},
		{"make", embedded.Make, &found.Make},
	} {
		candidates := append([]string{tool.from}, wellKnown[tool.name]...)
		for _, c := range candidates {
			if c != "" && exists(c) {
				*tool.into = c
				break
			}
		}
		if *tool.into == "" {
			missing = append(missing, tool.name)
		}
	}
	if len(missing) > 0 {
		return Tools{}, fmt.Errorf("cannot find %s: not where it was at build time, and not in %s",
			strings.Join(missing, ", "), strings.Join(wellKnownDirs(missing), ", "))
	}
	return found, nil
}

func wellKnownDirs(tools []string) []string {
	var dirs []string
	for _, t := range tools {
		for _, p := range wellKnown[t] {
			dirs = append(dirs, filepath.Dir(p))
		}
	}
	return dirs
}

// FileExists is the production exists for FindTools.
func FileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// BuildEnv is the environment make runs in. make itself is called by absolute
// path, but the recipes inside the Makefile call go by name, so go's directory
// has to be on the PATH make hands its children -- first, so that an older go
// elsewhere on the system cannot win. Everything else in base is kept: go
// needs HOME for its build cache and module cache.
//
// Git is told never to ask anything. An app has no terminal to ask in, and a
// credential prompt with nowhere to appear is an update that hangs forever.
func BuildEnv(tools Tools, base []string) []string {
	env := make([]string, 0, len(base)+3)
	var path string
	for _, kv := range base {
		k, v, _ := strings.Cut(kv, "=")
		switch k {
		case "PATH":
			path = v
		case "GIT_TERMINAL_PROMPT", "GIT_SSH_COMMAND":
			// Replaced below.
		default:
			env = append(env, kv)
		}
	}
	var dirs []string
	for _, bin := range []string{tools.Go, tools.Git, tools.Make} {
		if bin != "" {
			dirs = append(dirs, filepath.Dir(bin))
		}
	}
	if path != "" {
		dirs = append(dirs, path)
	}
	env = append(env,
		"PATH="+strings.Join(dirs, ":"),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_SSH_COMMAND=ssh -o BatchMode=yes",
	)
	return env
}
