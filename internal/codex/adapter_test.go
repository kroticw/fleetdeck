package codex

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kroticw/fleetdeck/internal/codexruntime"
	"github.com/kroticw/fleetdeck/internal/executor"
)

const adapterThread = "01a1a6d4-90a4-70f3-9130-a59ec873d0ba"

func record(lifecycle string) codexruntime.Record {
	return codexruntime.Record{Key: "codex:" + adapterThread, PID: 42, CWD: "/repo", Board: "/board", Card: "/board/cards/T-007-codex.md", Lifecycle: lifecycle}
}

func TestObserveSaysOnlyWhatTheRuntimeReported(t *testing.T) {
	cases := []struct {
		name, record   string
		status         *codexruntime.Status
		held           bool
		lifecycle, act string
		needs          executor.Verdict
	}{
		{"working", "live", &codexruntime.Status{Activity: "working", NeedsHuman: executor.No}, true, "live", "working", executor.No},
		{"asks", "live", &codexruntime.Status{Activity: "working", NeedsHuman: executor.Yes}, true, "live", "working", executor.Yes},
		{"silent owner", "live", nil, true, "live", "unknown", executor.Unknown},
		{"on its way out", "stopping", nil, true, "live", "unknown", executor.Unknown},
		{"crashed", "live", nil, false, "stopped", "unknown", executor.Unknown},
		{"stopped", "stopped", nil, false, "stopped", "idle", executor.Unknown},
		{"tui closed, work unknown", "unknown", nil, false, "stopped", "unknown", executor.Unknown},
		{"never served", "dead", nil, false, "dead", "", executor.Unknown},
	}
	for _, c := range cases {
		s := Observe(record(c.record), c.status, c.held)
		if s.Lifecycle != c.lifecycle || s.Activity != c.act || s.NeedsHuman != c.needs || s.Key != "codex:"+adapterThread || s.Name != "T-007-codex" || s.Executor != executor.Codex {
			t.Fatalf("%s: %+v", c.name, s)
		}
		if (c.needs == executor.Yes) != (s.Reason == Needs) {
			t.Fatalf("%s: reason %q", c.name, s.Reason)
		}
	}
}

// A starting runtime's lock is free until the runtime takes it; listing must
// not take it in that gap, so a starting record is never probed.
func TestListNeverProbesAStartingRuntimesLock(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "fdrows-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	state := filepath.Join(dir, "runtime")
	store, err := codexruntime.OpenStore(state)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	save := func(key, lifecycle string) {
		owner, err := store.Acquire(executor.Key(key))
		if err != nil {
			t.Fatal(err)
		}
		r := record(lifecycle)
		r.Key, r.Version, r.Nonce, r.CreatedAt = executor.Key(key), codexruntime.ProtocolVersion, strings.Repeat("n", 32), time.Now()
		if err := owner.Save(r); err != nil {
			t.Fatal(err)
		}
		_ = owner.Close()
	}
	other := "codex:02a1a6d4-90a4-70f3-9130-a59ec873d0ba"
	save("codex:"+adapterThread, "starting")
	save(other, "live")
	sessions, err := NewAdapter(Runtimes{StateDir: state}).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := map[executor.Key]string{}
	for _, s := range sessions {
		got[s.Key] = s.Lifecycle
	}
	if got["codex:"+adapterThread] != "live" || got[executor.Key(other)] != "stopped" {
		t.Fatalf("sessions %v", got)
	}
}

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

func TestTerminalClearsOnRepaintAndEndsWithTheProcess(t *testing.T) {
	yes := true
	stream := &fakeStream{events: []codexruntime.TerminalEvent{{Data: []byte("old")}, {Reset: true}, {Data: []byte("new")}, {Writable: &yes}, {End: "exited"}}}
	term := &Terminal{stream: stream}
	got, err := io.ReadAll(term)
	if err != nil || string(got) != "old\x1bcnew" || !term.Writable() {
		t.Fatalf("screen %q writable=%v err=%v", got, term.Writable(), err)
	}
	if _, err := term.Write([]byte("k")); err != nil || len(stream.input) != 1 {
		t.Fatalf("input %v %v", stream.input, err)
	}
}

func TestAnUnreachableRuntimeNeverClaimsSuccess(t *testing.T) {
	a := NewAdapter(Runtimes{StateDir: filepath.Join(t.TempDir(), "runtime")})
	key := executor.Key("codex:" + adapterThread)
	ctx := context.Background()
	if a.Stop(ctx, key) == nil || a.Interrupt(ctx, key) == nil || a.Respond(ctx, key, executor.Answer{Token: "t", Decision: "accept"}) == nil {
		t.Fatal("an operation on a missing runtime succeeded")
	}
	if r, err := a.Send(ctx, key, executor.Message{OperationID: "op-12345678", Text: "x"}); err == nil || r.State == executor.Accepted {
		t.Fatalf("send %+v %v", r, err)
	}
}
