package codex

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/kroticw/fleetdeck/internal/executor"
)

// Rollout is what a thread's rollout file says about it: the context of its
// last model response, the account limits Codex saw with it and when, and
// when Codex last wrote to the file.
type Rollout struct {
	Tokens, Window int64
	Limits         []executor.Limit
	LimitsAt       time.Time
	WrittenAt      time.Time
}

// rolloutTail bounds how much of a rollout is read: its token counts are
// written after every response, so the last one is near the end.
const rolloutTail = 4 << 20

var rolloutCache = struct {
	sync.Mutex
	paths    map[string]string
	readings map[string]rolloutRead
}{paths: map[string]string{}, readings: map[string]rolloutRead{}}

type rolloutRead struct {
	size    int64
	modTime time.Time
	r       Rollout
	ok      bool
}

// ReadRollout reads the rollout Codex keeps for threadID under home
// (CODEX_HOME). Its format is Codex's own and not an API: the app-server
// reports context only as turns run, and this is the reading for a runtime
// that has not seen one yet. ok is false when there is no reading.
func ReadRollout(home, threadID string) (Rollout, bool) {
	if home == "" || threadID == "" || strings.ContainsAny(threadID, `*?[]\/`) {
		return Rollout{}, false
	}
	path := rolloutPath(home, threadID)
	fi, err := os.Stat(path)
	if err != nil {
		return Rollout{}, false
	}
	rolloutCache.Lock()
	defer rolloutCache.Unlock()
	if c, hit := rolloutCache.readings[path]; hit && c.size == fi.Size() && c.modTime.Equal(fi.ModTime()) {
		return c.r, c.ok
	}
	r, ok := lastTokenCount(path, fi.Size())
	r.WrittenAt = fi.ModTime()
	rolloutCache.readings[path] = rolloutRead{size: fi.Size(), modTime: fi.ModTime(), r: r, ok: ok}
	return r, ok
}

// rolloutPath finds a thread's rollout once; Codex files it under the day
// the thread started, which nothing else here knows.
func rolloutPath(home, threadID string) string {
	rolloutCache.Lock()
	path, hit := rolloutCache.paths[home+"\x00"+threadID]
	rolloutCache.Unlock()
	if hit {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	matches, _ := filepath.Glob(filepath.Join(home, "sessions", "*", "*", "*", "rollout-*-"+threadID+".jsonl"))
	if len(matches) == 0 {
		return ""
	}
	rolloutCache.Lock()
	rolloutCache.paths[home+"\x00"+threadID] = matches[len(matches)-1]
	rolloutCache.Unlock()
	return matches[len(matches)-1]
}

func lastTokenCount(path string, size int64) (Rollout, bool) {
	f, err := os.Open(path)
	if err != nil {
		return Rollout{}, false
	}
	defer func() { _ = f.Close() }()
	start := max(size-rolloutTail, 0)
	data, err := io.ReadAll(io.NewSectionReader(f, start, size-start))
	if err != nil {
		return Rollout{}, false
	}
	marker := []byte(`"token_count"`)
	for len(data) > 0 {
		i := bytes.LastIndexByte(data[:len(data)-1], '\n')
		line := data[i+1:]
		data = data[:max(i, 0)]
		if !bytes.Contains(line, marker) {
			continue
		}
		var event struct {
			Timestamp time.Time `json:"timestamp"`
			Payload   struct {
				Type string `json:"type"`
				Info *struct {
					Last struct {
						Total int64 `json:"total_tokens"`
					} `json:"last_token_usage"`
					Window int64 `json:"model_context_window"`
				} `json:"info"`
				RateLimits *struct {
					Primary, Secondary *rolloutLimit
				} `json:"rate_limits"`
			} `json:"payload"`
		}
		if json.Unmarshal(line, &event) != nil || event.Payload.Type != "token_count" || event.Payload.Info == nil || event.Payload.Info.Window <= 0 {
			continue
		}
		r := Rollout{Tokens: event.Payload.Info.Last.Total, Window: event.Payload.Info.Window}
		if l := event.Payload.RateLimits; l != nil {
			for _, w := range []*rolloutLimit{l.Primary, l.Secondary} {
				if w != nil && w.Minutes > 0 {
					r.Limits = append(r.Limits, w.limit())
				}
			}
			r.LimitsAt = event.Timestamp
		}
		return r, true
	}
	return Rollout{}, false
}

type rolloutLimit struct {
	UsedPercent float64 `json:"used_percent"`
	Minutes     int64   `json:"window_minutes"`
	ResetsAt    int64   `json:"resets_at"`
}

func (l rolloutLimit) limit() executor.Limit {
	out := executor.Limit{Name: windowName(l.Minutes), Minutes: l.Minutes, UsedPercent: l.UsedPercent}
	if l.ResetsAt > 0 {
		at := time.Unix(l.ResetsAt, 0)
		out.ResetsAt = &at
	}
	return out
}
