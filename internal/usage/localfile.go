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

// traceState is the small piece of bookkeeping AppendTraceOnce keeps beside
// the trace log itself, in <log path>.state.json -- a process with no
// memory of its own (cmd/fleetdeck-status runs fresh on every statusline
// tick) needs somewhere persistent to recognize "the same cause as last
// time, still open" without re-reading and parsing the log's own free-text
// lines. Kept unexported: nothing outside this file reads or writes it
// directly, only through AppendTraceOnce.
type traceState struct {
	Message string    `json:"message"`
	Count   int       `json:"count"`
	FirstAt time.Time `json:"firstAt"`
	LastAt  time.Time `json:"lastAt"`
}

func tracePathStatePath(logPath string) string {
	return logPath + ".state.json"
}

func loadTraceState(path string) traceState {
	body, err := os.ReadFile(path)
	if err != nil {
		return traceState{}
	}
	var s traceState
	if err := json.Unmarshal(body, &s); err != nil {
		return traceState{}
	}
	return s
}

func saveTraceState(path string, s traceState) error {
	body, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("encode trace state: %w", err)
	}
	return atomicWriteFile(path, body, ".rate_limits_trace_state-*.json")
}

// closeTraceState writes the one line that says a streak held and for how
// long, then removes the state file so the next occurrence (if any) starts
// a fresh one.
func closeTraceState(logPath string, s traceState) error {
	if err := AppendTrace(logPath, fmt.Sprintf(
		"resolved (was %q; occurred %d time(s), first at %s, last at %s)",
		s.Message, s.Count, s.FirstAt.Format(time.RFC3339), s.LastAt.Format(time.RFC3339),
	)); err != nil {
		return err
	}
	if err := os.Remove(tracePathStatePath(logPath)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove %s: %w", tracePathStatePath(logPath), err)
	}
	return nil
}

// AppendTraceOnce is AppendTrace with the failure this repository hit on a
// real machine fixed: appending a fresh timestamped line on every single
// call turns a loud, honest signal into an unbounded one. A real cause left
// unconfigured on a live machine wrote 68 identical lines in seventeen
// minutes -- roughly one every fifteen seconds, without end for as long as
// the cause held, which projects to thousands of lines a day for a cause
// left in place longer than an evening.
//
// message is the current fact worth recording, or "" when there is nothing
// wrong right now. Call this on every invocation regardless of branch, not
// only when something is wrong -- recognizing that the cause went away
// depends on being told "" just as much as recognizing that it is new
// depends on being told the message.
//
//   - The same message as last time, with a streak still open: the log
//     gets nothing new; only the persisted count and last-seen time move.
//   - A different message than an open streak (including "" -- the cause
//     resolved): the open streak's own line, naming how many times it
//     fired and over what span, is written once, then (for a genuinely new
//     message, not a plain resolution) that message's own first line.
//   - No open streak and message == "": nothing happens at all -- this is
//     the ordinary case on every healthy invocation, and must cost nothing.
//
// Best-effort, matching AppendTrace: a failure here is never escalated into
// something a caller has to handle.
func AppendTraceOnce(logPath, message string) error {
	statePath := tracePathStatePath(logPath)
	state := loadTraceState(statePath)

	if message == "" {
		if state.Message == "" {
			return nil
		}
		return closeTraceState(logPath, state)
	}

	now := time.Now()
	if state.Message == message {
		state.Count++
		state.LastAt = now
		return saveTraceState(statePath, state)
	}

	if state.Message != "" {
		if err := closeTraceState(logPath, state); err != nil {
			return err
		}
	}
	if err := AppendTrace(logPath, message); err != nil {
		return err
	}
	return saveTraceState(statePath, traceState{Message: message, Count: 1, FirstAt: now, LastAt: now})
}

// ErrNoLocalFile means no session's statusline has ever written the file --
// distinct from a file that exists but fails to parse, which is a real
// problem worth its own error rather than being read the same as "not
// written yet".
var ErrNoLocalFile = errors.New("no local rate-limit file written yet")

// WriteLocal writes limits to path atomically -- see atomicWriteFile, which
// does the actual temp-file-plus-rename work shared with TraceState's own
// writes below. cmd/fleetdeck-status runs from every session's statusline
// tick, often several at once (spec: four live sessions on this machine
// while this was written) -- a plain write racing another one's would leave
// a half-written file the next reader sees as no data at all, which is
// worse than the write simply losing the race outright.
func WriteLocal(path string, limits Limits) error {
	body, err := json.Marshal(limits)
	if err != nil {
		return fmt.Errorf("encode local rate limits: %w", err)
	}
	return atomicWriteFile(path, body, ".rate_limits-*.json")
}

// atomicWriteFile writes body to path as a temp file in path's own
// directory, then renames it into place: os.Rename is only atomic within a
// single filesystem, and the default temp directory ($TMPDIR) can be a
// different one from path's own on this machine, so the temp file must be
// created beside path itself, never with the default empty directory.
func atomicWriteFile(path string, body []byte, tmpPattern string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, tmpPattern)
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
