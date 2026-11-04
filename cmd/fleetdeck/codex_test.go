package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kroticw/fleetdeck/internal/server"

	"github.com/kroticw/fleetdeck/internal/codexruntime"
	"github.com/kroticw/fleetdeck/internal/config"
	"github.com/kroticw/fleetdeck/internal/daemon"
	"github.com/kroticw/fleetdeck/internal/executor"
	"github.com/kroticw/fleetdeck/internal/state"
)

func codexRecord(lifecycle string) codexruntime.Record {
	return codexruntime.Record{Key: "codex:" + runtimeTestThread, PID: 42, CWD: "/repo", Board: "/board", Card: "/board/cards/T-007-codex.md", Lifecycle: lifecycle}
}

func TestCodexRowsSayOnlyWhatTheRuntimeReported(t *testing.T) {
	cases := []struct {
		name      string
		record    string
		status    *codexruntime.Status
		held      bool
		lifecycle string
		state     string
		needs     *string
		last      string
	}{
		{"working", "live", &codexruntime.Status{Activity: "working", NeedsHuman: executor.No}, true, state.LifecycleLive, "working", ptr(""), ""},
		{"asks", "live", &codexruntime.Status{Activity: "working", NeedsHuman: executor.Yes}, true, state.LifecycleLive, "working", ptr(codexNeeds), ""},
		{"unobserved", "live", &codexruntime.Status{Activity: "unknown"}, true, state.LifecycleLive, "", nil, ""},
		{"silent owner", "live", nil, true, state.LifecycleLive, "", nil, ""},
		{"on its way out", "stopping", nil, true, state.LifecycleLive, "", nil, ""},
		{"crashed", "live", nil, false, state.LifecycleStopped, "", nil, "unknown"},
		{"stopped", "stopped", nil, false, state.LifecycleStopped, "", nil, "idle"},
		{"tui closed, work unknown", "unknown", nil, false, state.LifecycleStopped, "", nil, "unknown"},
		{"never served", "dead", nil, false, state.LifecycleDead, "", nil, ""},
	}
	for _, c := range cases {
		row := codexRow(codexRecord(c.record), c.status, c.held)
		s := row.session
		if row.lifecycle != c.lifecycle || s.State != c.state || row.lastState != c.last || s.Short != "codex:"+runtimeTestThread || s.Name != "T-007-codex" {
			t.Fatalf("%s: %+v", c.name, row)
		}
		if (s.Needs == nil) != (c.needs == nil) || (s.Needs != nil && *s.Needs != *c.needs) {
			t.Fatalf("%s: needs %v", c.name, s.Needs)
		}
	}
	if w := codexRow(codexRecord("live"), &codexruntime.Status{Activity: "working", NeedsHuman: executor.Yes}, true).session.Waiting(); w != daemon.Yes {
		t.Fatalf("a Codex question must count as waiting, got %v", w)
	}
}

func TestCodexIsOffOnAStandWithoutItsOwnHome(t *testing.T) {
	cfg := config.Config{BoardPath: "/board", Codex: config.CodexConfig{Enabled: true}}
	if codexSessions(runOpts{configPath: "/stand/config.yaml", standSocket: "/stand/daemon.sock"}, cfg) != nil {
		t.Fatal("a stand would reach the operator's Codex home")
	}
	cfg.Codex.Home = "/stand/codex-home"
	if codexSessions(runOpts{configPath: "/stand/config.yaml", standSocket: "/stand/daemon.sock"}, cfg) == nil {
		t.Fatal("a stand with its own Codex home should start Codex")
	}
	if codexSessions(runOpts{configPath: "/c/config.yaml"}, config.Config{}) != nil {
		t.Fatal("disabled Codex started")
	}
	if got := codexStateDir("/stand/config.yaml"); got != filepath.Join("/stand", "runtime") {
		t.Fatalf("state dir %s", got)
	}
}

func ptr(s string) *string { return &s }

type fakeStream struct {
	events []codexruntime.TerminalEvent
	input  [][]byte
}

func (f *fakeStream) Next(time.Time) (codexruntime.TerminalEvent, error) {
	if len(f.events) == 0 {
		return codexruntime.TerminalEvent{}, io.ErrClosedPipe
	}
	ev := f.events[0]
	f.events = f.events[1:]
	return ev, nil
}
func (f *fakeStream) Input(p []byte) error  { f.input = append(f.input, p); return nil }
func (f *fakeStream) Resize(int, int) error { return nil }
func (f *fakeStream) Close() error          { return nil }

func TestCodexTerminalClearsOnRepaintAndEndsWithTheProcess(t *testing.T) {
	no, yes := false, true
	stream := &fakeStream{events: []codexruntime.TerminalEvent{
		{Data: []byte("old")},
		{Reset: true},
		{Data: []byte("new")},
		{Writable: &yes},
		{End: "exited"},
	}}
	term := &codexTerminal{stream: stream}
	term.writable.Store(no)
	got, err := io.ReadAll(term)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "old\x1bcnew" {
		t.Fatalf("screen bytes %q", got)
	}
	if !term.Writable() {
		t.Fatal("lease change was not applied")
	}
	if _, err := term.Write([]byte("k")); err != nil || len(stream.input) != 1 {
		t.Fatalf("input %v %v", stream.input, err)
	}
}

func TestCodexKeysNeverReachClaude(t *testing.T) {
	state := filepath.Join(t.TempDir(), "runtime")
	var claude []string
	stop := codexAwareStop(state, func(_ context.Context, short string) error { claude = append(claude, "stop:"+short); return nil })
	send := codexAwareSend(state, func(_ context.Context, short, _ string) error { claude = append(claude, "send:"+short); return nil })
	ctx := context.Background()
	if err := stop(ctx, "abc12345"); err != nil {
		t.Fatal(err)
	}
	if err := send(ctx, "abc12345", "hi"); err != nil {
		t.Fatal(err)
	}
	key := "codex:" + runtimeTestThread
	if stop(ctx, key) == nil || send(ctx, key, "hi") == nil {
		t.Fatal("a Codex key with no runtime reported success")
	}
	if len(claude) != 2 || claude[0] != "stop:abc12345" || claude[1] != "send:abc12345" {
		t.Fatalf("claude saw %v", claude)
	}
	if codexAwareStop(state, nil) != nil {
		t.Fatal("a panel that stops nothing started stopping")
	}
}

func TestCodexResumeKeepsAStandOffTheOperatorsCodex(t *testing.T) {
	cfg := config.Config{BoardPath: "/board", Codex: config.CodexConfig{Enabled: true}}
	err := codexResume(runOpts{configPath: filepath.Join(t.TempDir(), "config.yaml"), standSocket: "/stand/daemon.sock"}, cfg, "codex:"+runtimeTestThread)
	if !errors.Is(err, server.ErrSessionNotResumable) {
		t.Fatalf("a stand without its own Codex home resumed: %v", err)
	}
}

// A starting runtime's lock is free until the runtime takes it; the panel
// must not take it in that gap, so a starting record is never probed.
func TestCodexRowsNeverProbeAStartingRuntimesLock(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "fdrows-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	store, err := codexruntime.OpenStore(filepath.Join(dir, "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	save := func(key, lifecycle string) {
		owner, err := store.Acquire(executor.Key(key))
		if err != nil {
			t.Fatal(err)
		}
		r := codexRecord(lifecycle)
		r.Key, r.Version, r.Nonce, r.CreatedAt = executor.Key(key), codexruntime.ProtocolVersion, strings.Repeat("n", 32), time.Now()
		if err := owner.Save(r); err != nil {
			t.Fatal(err)
		}
		_ = owner.Close()
	}
	save("codex:"+runtimeTestThread, "starting")
	save("codex:02a1a6d4-90a4-70f3-9130-a59ec873d0ba", "live")
	rows := map[string]string{}
	for _, row := range codexRows(context.Background(), filepath.Join(dir, "runtime")) {
		rows[row.session.Short] = row.lifecycle
	}
	if rows["codex:"+runtimeTestThread] != state.LifecycleLive || rows["codex:02a1a6d4-90a4-70f3-9130-a59ec873d0ba"] != state.LifecycleStopped {
		t.Fatalf("rows %v", rows)
	}
}
