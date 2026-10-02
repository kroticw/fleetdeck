package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/kroticw/fleetdeck/internal/config"
	"github.com/kroticw/fleetdeck/internal/fleet"
	"github.com/kroticw/fleetdeck/internal/state"
)

// fleetDeleter is server.Deps.DeleteFleet: it takes a listed fleet out of the
// configuration and the running panel, stops the sessions that belong to it
// alone, and removes its workspace from the disk. It returns the directories
// it left in place.
//
// Removing directories is the one thing here that cannot be undone, so what is
// removed is bounded before anything happens, and a refusal changes nothing:
//
//   - the first fleet is the configuration itself and is never deleted;
//   - the workspace is the board's parent directory, and it must not be the
//     home directory or above it;
//   - only the board and documentation inside the workspace are removed, and
//     documentation configured elsewhere — somebody's project — is kept and
//     named in the answer;
//   - nothing removed may overlap a directory another fleet uses;
//   - the fleet's own sessions are stopped first, and a panel that cannot stop
//     them (stop is nil: a stand given no claude) or a stop that fails ends
//     the deletion before the configuration or the disk is touched. A session
//     another fleet also claims is left running.
func fleetDeleter(configPath string, live *liveFleets, snapshot func() state.Snapshot, refresh func(context.Context), stop func(ctx context.Context, short string) error, home string) func(name string) ([]string, error) {
	var mu sync.Mutex
	return func(name string) ([]string, error) {
		mu.Lock()
		defer mu.Unlock()

		cfg := live.collector.Config()
		all := cfg.FleetList()
		fl, err := fleet.Select(all, name)
		if err != nil {
			return nil, err
		}
		if fl.Name == all[0].Name {
			return nil, fmt.Errorf("fleet %q is the first fleet, the configuration's own, and cannot be deleted", name)
		}
		remove, kept, err := deletablePaths(fl, all, home)
		if err != nil {
			return nil, err
		}

		view, err := state.ForFleet(snapshot(), name)
		if err != nil {
			return nil, err
		}
		var own []string
		for _, s := range view.Sessions {
			if s.Live() && len(s.Fleets) == 1 && s.Fleets[0] == name {
				own = append(own, s.Short)
			}
		}
		if len(own) > 0 && stop == nil {
			return nil, fmt.Errorf("fleet %q has running sessions (%s) and this panel cannot stop sessions; stop them and delete it again", name, strings.Join(own, ", "))
		}
		for _, short := range own {
			if err := stop(context.Background(), short); err != nil {
				return nil, err
			}
		}

		if _, err := config.RemoveFleet(configPath, name); err != nil {
			return nil, err
		}
		live.remove(name)
		var errs []error
		for _, path := range remove {
			if err := os.RemoveAll(path); err != nil {
				errs = append(errs, fmt.Errorf("remove %s: %w", path, err))
			}
		}
		// The workspace itself only when nothing else is left in it.
		_ = os.Remove(filepath.Dir(fl.BoardPath))
		refresh(context.Background())
		return kept, errors.Join(errs...)
	}
}

// deletablePaths splits a fleet's directories into the ones deleting it removes
// and the ones it keeps, or refuses the deletion as a whole.
func deletablePaths(fl fleet.Fleet, all []fleet.Fleet, home string) (remove, kept []string, err error) {
	if fl.BoardPath == "" {
		return nil, nil, fmt.Errorf("fleet %q names no board", fl.Name)
	}
	workspace := filepath.Clean(filepath.Dir(fl.BoardPath))
	if workspace == string(filepath.Separator) || workspace == "." {
		return nil, nil, fmt.Errorf("refusing to delete fleet %q: its workspace would be %q", fl.Name, workspace)
	}
	if home != "" && within(workspace, filepath.Clean(home)) {
		return nil, nil, fmt.Errorf("refusing to delete fleet %q: its workspace %s is the home directory or holds it", fl.Name, workspace)
	}
	for _, path := range append([]string{fl.BoardPath}, fl.DocsPaths...) {
		clean := filepath.Clean(path)
		if !within(workspace, clean) || clean == workspace {
			kept = append(kept, path)
			continue
		}
		for _, other := range all {
			if other.Name == fl.Name {
				continue
			}
			for _, theirs := range append([]string{other.BoardPath}, other.DocsPaths...) {
				if theirs != "" && (within(clean, theirs) || within(theirs, clean)) {
					return nil, nil, fmt.Errorf("refusing to delete fleet %q: %s overlaps %s, which fleet %q uses", fl.Name, path, theirs, other.Name)
				}
			}
		}
		remove = append(remove, clean)
	}
	return remove, kept, nil
}

// within reports whether path is dir or lies under it.
func within(dir, path string) bool {
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
