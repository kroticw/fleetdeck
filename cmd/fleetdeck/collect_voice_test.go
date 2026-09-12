package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kroticw/fleetdeck/internal/config"
	"github.com/kroticw/fleetdeck/internal/daemon"
	"github.com/kroticw/fleetdeck/internal/state"
)

func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func writeFile(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func enrichOne(t *testing.T, c *Collector) state.SessionView {
	t.Helper()
	views := []state.SessionView{{Session: daemon.Session{Short: "abc12345", SessionID: sampleUUID}}}
	c.enrich(views, nil)
	return views[0]
}

// TestSilentForIsNotResetByAMessageSentToTheSession is T-047 at the collector. The
// session opened a call an hour ago and has said nothing since; a message sent to it a
// moment ago landed in its transcript and made the file brand new. Silence is how long
// the session has been silent, so it is an hour, not a second.
func TestSilentForIsNotResetByAMessageSentToTheSession(t *testing.T) {
	projects := t.TempDir()
	path := filepath.Join(projects, "some-project", sampleUUID+".jsonl")
	now := time.Now()
	writeFile(t, path,
		`{"type":"assistant","timestamp":"`+stamp(now.Add(-time.Hour))+`","message":{"content":[{"type":"tool_use","id":"t1","name":"Read","input":{}}]}}`,
		`{"type":"queue-operation","operation":"enqueue","timestamp":"`+stamp(now)+`","content":"are you there?"}`,
	)

	got := enrichOne(t, NewCollector(config.Default(), nil, nil, projects))
	if got.SilentFor < 55*time.Minute || got.SilentFor > 65*time.Minute {
		t.Fatalf("the session said nothing for an hour; a message to it must not reset that, got %s", got.SilentFor)
	}
	if got.InCall == nil || got.InCall.Tool != "Read" {
		t.Fatalf("the snapshot must carry the call the session is standing in, got %+v", got.InCall)
	}
}

// While a session is inside an Agent call its own file does not change at all: the work
// is written to the subagent's transcript. A cache keyed on the parent file alone would
// keep serving the reading from the start of the call and let silence grow over a
// subagent that is busy the whole time.
func TestSilentForFollowsTheSubagentWhileTheParentFileStandsStill(t *testing.T) {
	projects := t.TempDir()
	path := filepath.Join(projects, "some-project", sampleUUID+".jsonl")
	subs := filepath.Join(projects, "some-project", sampleUUID, "subagents")
	now := time.Now()
	writeFile(t, path,
		`{"type":"assistant","timestamp":"`+stamp(now.Add(-time.Hour))+`","message":{"content":[{"type":"tool_use","id":"agent1","name":"Agent","input":{}}]}}`,
	)
	writeFile(t, filepath.Join(subs, "agent-x.meta.json"), `{"toolUseId":"agent1"}`)
	writeFile(t, filepath.Join(subs, "agent-x.jsonl"),
		`{"type":"assistant","timestamp":"`+stamp(now.Add(-30*time.Minute))+`","message":{"content":[{"type":"text","text":"working"}]}}`,
	)

	c := NewCollector(config.Default(), nil, nil, projects)
	first := enrichOne(t, c)
	if first.SilentFor < 29*time.Minute || first.SilentFor > 31*time.Minute {
		t.Fatalf("the subagent last spoke half an hour ago, got %s", first.SilentFor)
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(subs, "agent-x.jsonl"),
		`{"type":"assistant","timestamp":"`+stamp(now.Add(-30*time.Minute))+`","message":{"content":[{"type":"text","text":"working"}]}}`,
		`{"type":"assistant","timestamp":"`+stamp(time.Now())+`","message":{"content":[{"type":"text","text":"still working"}]}}`,
	)
	if err := os.Chtimes(path, fi.ModTime(), fi.ModTime()); err != nil {
		t.Fatal(err)
	}

	second := enrichOne(t, c)
	if second.SilentFor > time.Minute {
		t.Fatalf("the subagent just spoke, so the session just spoke, got %s", second.SilentFor)
	}
}

// A session whose transcript is between calls reports no call, so the panel never
// shows one that is not there.
func TestEnrichReportsNoCallWhenEveryCallCameBack(t *testing.T) {
	projects := t.TempDir()
	path := filepath.Join(projects, "some-project", sampleUUID+".jsonl")
	now := time.Now()
	writeFile(t, path,
		`{"type":"assistant","timestamp":"`+stamp(now.Add(-2*time.Minute))+`","message":{"content":[{"type":"tool_use","id":"t1","name":"Bash","input":{}}]}}`,
		`{"type":"user","timestamp":"`+stamp(now.Add(-time.Minute))+`","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}}`,
	)

	got := enrichOne(t, NewCollector(config.Default(), nil, nil, projects))
	if got.InCall != nil {
		t.Fatalf("every call came back, got %+v", got.InCall)
	}
	if got.SilentFor < 50*time.Second || got.SilentFor > 70*time.Second {
		t.Fatalf("the result a minute ago is the last thing said, got %s", got.SilentFor)
	}
}
