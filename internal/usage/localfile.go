package usage

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// LocalFilePath is where cmd/fleetdeck-status writes the rate-limit windows
// Claude Code already hands it on stdin (spec: the statusline JSON's own
// rate_limits object), and where the panel reads them from first -- before
// ever asking the network endpoint Fetcher.Limits calls. The file lives
// beside config.yaml (cmd/fleetdeck's own defaultConfigPath): it is runtime
// state written by whichever session's statusline last ran, not something
// any one session or worktree owns.
func LocalFilePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "rate_limits.json"
	}
	return filepath.Join(home, ".config", "fleetdeck", "rate_limits.json")
}

// TracePath is where cmd/fleetdeck-status records the one case WriteLocal
// silently declines: Claude Code's statusline payload carried some rate_limits
// data, but not the five_hour+seven_day pair WriteLocal requires (see
// writeRateLimitsTo's own comment for why an incomplete pair must never be
// written). Nothing reads this file back -- it exists so that the day the
// stdin schema drifts and the gauges quietly stop moving, whoever comes
// asking why finds a dated line naming exactly what arrived instead of
// nothing at all. Kept beside rate_limits.json for the same reason that file
// lives where it does.
func TracePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "rate_limits_trace.log"
	}
	return filepath.Join(home, ".config", "fleetdeck", "rate_limits_trace.log")
}

// AppendTrace appends one timestamped line to path, creating the file and
// its directory if needed. Best-effort by design: a trace write failing must
// never be escalated into anything a caller has to handle, since the trace
// itself already exists only for a case nothing downstream depends on.
func AppendTrace(path string, line string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	if _, err := fmt.Fprintf(f, "%s %s\n", time.Now().Format(time.RFC3339), line); err != nil {
		_ = f.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}
	return nil
}

// ErrNoLocalFile means no session's statusline has ever written the file --
// distinct from a file that exists but fails to parse, which is a real
// problem worth its own error rather than being read the same as "not
// written yet".
var ErrNoLocalFile = errors.New("no local rate-limit file written yet")

// WriteLocal writes limits to path atomically: a temp file in the same
// directory, then a rename. cmd/fleetdeck-status runs from every session's
// statusline tick, often several at once (spec: four live sessions on this
// machine while this was written) -- a plain write racing another one's
// would leave a half-written file the next reader sees as no data at all,
// which is worse than the write simply losing the race outright.
func WriteLocal(path string, limits Limits) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	body, err := json.Marshal(limits)
	if err != nil {
		return fmt.Errorf("encode local rate limits: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".rate_limits-*.json")
	if err != nil {
		return fmt.Errorf("create temp file in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename %s to %s: %w", tmpPath, path, err)
	}
	return nil
}

// ReadLocal reads limits back. FetchedAt inside the returned Limits is the
// moment WriteLocal's caller recorded, not the moment ReadLocal ran: the
// panel needs to know how old the numbers are, not when it happened to look
// at them -- see header.js's own age display for the network-fetched case,
// which this reuses rather than inventing a second one.
func ReadLocal(path string) (Limits, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Limits{}, ErrNoLocalFile
		}
		return Limits{}, fmt.Errorf("read %s: %w", path, err)
	}
	var l Limits
	if err := json.Unmarshal(body, &l); err != nil {
		return Limits{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if l.FetchedAt.IsZero() {
		return Limits{}, fmt.Errorf("%s carries no fetchedAt", path)
	}
	return l, nil
}
