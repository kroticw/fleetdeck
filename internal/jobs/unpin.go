package jobs

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	// pinsFile is where the agents view (ctrl+t) keeps the pin set: a JSON array
	// of short ids at the top of the job store. It is not part of the daemon's
	// control protocol — the protocol document says pinned is not on that wire —
	// and the picker is the other writer of this file.
	pinsFile = "pins.json"

	// staleLock is how long a lock directory may sit untouched before it is
	// taken over. It matches what the picker's own locking uses, so neither
	// side waits out a lock the other abandoned.
	staleLock = 10 * time.Second

	// lockWait bounds waiting for a lock somebody else is holding. Longer than
	// a pin write takes and far shorter than the operator's patience.
	lockWait = 3 * time.Second
)

func nowMinus(d time.Duration) time.Time { return time.Now().Add(-d) }

// Unpin drops short from the pin set, leaving every other pin where it is. A
// session that was not pinned, and a store with no pin file at all, are both
// ordinary and change nothing.
//
// The file is shared with the session picker, which holds a lock directory
// beside it while it writes, so the same lock is taken here: a write that
// ignored it would overwrite a pin the picker had just added, and the operator
// would see it reappear or vanish with no explanation.
func Unpin(dir, short string) error {
	if short == "" {
		return errors.New("unpin names no session")
	}
	path := filepath.Join(dir, pinsFile)
	ids, err := pins(path)
	if err != nil || len(ids) == 0 {
		return err
	}
	if !contains(ids, short) {
		return nil
	}

	release, err := lock(path)
	if err != nil {
		return err
	}
	defer release()

	// Re-read under the lock: the set may have changed between the cheap check
	// above and here, and the whole point of the lock is to write from what the
	// file holds now.
	ids, err = pins(path)
	if err != nil {
		return err
	}
	kept := make([]string, 0, len(ids))
	for _, id := range ids {
		if id != short {
			kept = append(kept, id)
		}
	}
	if len(kept) == len(ids) {
		return nil
	}
	if err := writePinsAtomic(path, kept); err != nil {
		return err
	}
	// The picker polls the session's own record for changes; without this it
	// keeps drawing the session at the top until something else moves.
	now := time.Now()
	_ = os.Chtimes(filepath.Join(dir, short, stateFile), now, now)
	return nil
}

func pins(path string) ([]string, error) {
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read pin set %s: %w", path, err)
	}
	var ids []string
	if err := json.Unmarshal(body, &ids); err != nil {
		return nil, fmt.Errorf("parse pin set %s: %w", path, err)
	}
	return ids, nil
}

func contains(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

// writePinsAtomic writes the set the way the picker writes it — a JSON array
// indented by two spaces — through a temporary file and a rename, so a reader
// never sees half a set.
func writePinsAtomic(path string, ids []string) error {
	body, err := json.MarshalIndent(ids, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return fmt.Errorf("write pin set: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace pin set: %w", err)
	}
	return nil
}

// lock takes the picker's own lock on path: a sibling directory, created by an
// atomic mkdir, taken over once it is older than staleLock.
func lock(path string) (func(), error) {
	name := path + ".lock"
	deadline := time.Now().Add(lockWait)
	for {
		switch err := os.Mkdir(name, 0o700); {
		case err == nil:
			return func() { _ = os.Remove(name) }, nil
		case !os.IsExist(err):
			return nil, fmt.Errorf("lock pin set: %w", err)
		}
		if fi, err := os.Stat(name); err == nil && time.Since(fi.ModTime()) > staleLock {
			_ = os.Remove(name)
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("the pin set is locked by another process: %s", name)
		}
		time.Sleep(25 * time.Millisecond)
	}
}
