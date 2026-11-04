package codexruntime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func streamClient(t *testing.T, p *terminal) Client {
	t.Helper()
	record := runtimeRecord()
	handle := func(ctx context.Context, method string, params json.RawMessage) (any, error) {
		if method != "terminal.attach" {
			return nil, errors.New("unexpected method")
		}
		var req struct {
			Offset *uint64 `json:"offset"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, err
		}
		return p.attachStream(ctx, req.Offset)
	}
	return Client{Socket: protocolListener(t, record, handle), Record: record}
}

func attachStream(t *testing.T, c Client, offset *uint64) *TerminalStream {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	s, err := c.AttachTerminal(ctx, offset)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// awaitStream reads events until want appears in the data since the last
// reset, failing on an end of stream.
func awaitStream(t *testing.T, s *TerminalStream, want string) string {
	t.Helper()
	var got strings.Builder
	for !strings.Contains(got.String(), want) {
		ev, err := s.Next(time.Now().Add(3 * time.Second))
		if err != nil {
			t.Fatalf("waiting for %q: %v (got %q)", want, err, got.String())
		}
		if ev.End != "" {
			t.Fatalf("stream ended %s before %q", ev.End, want)
		}
		if ev.Reset {
			got.Reset()
		}
		got.Write(ev.Data)
	}
	return got.String()
}

func TestTerminalStreamCarriesInputOutputAndResize(t *testing.T) {
	p := testPTY(t)
	c := streamClient(t, p)
	s := attachStream(t, c, nil)
	if !s.Writable {
		t.Fatal("first view did not get the input lease")
	}
	if err := s.Input([]byte("size\n")); err != nil {
		t.Fatal(err)
	}
	awaitStream(t, s, "SIZE=80x24")
	if err := s.Resize(100, 30); err != nil {
		t.Fatal(err)
	}
	if err := s.Input([]byte("size\n")); err != nil {
		t.Fatal(err)
	}
	awaitStream(t, s, "SIZE=100x30")
}

func TestTerminalStreamDetachMovesLeaseAndKeepsProcess(t *testing.T) {
	p := testPTY(t)
	c := streamClient(t, p)
	first := attachStream(t, c, nil)
	second := attachStream(t, c, nil)
	if second.Writable {
		t.Fatal("observer got the input lease")
	}
	// An observer's keys never reach the process. Frames on two connections
	// are unordered, so the check finishes before the lease can move.
	if err := second.Input([]byte("finish\n")); err != nil {
		t.Fatal(err)
	}
	if err := first.Input([]byte("size\n")); err != nil {
		t.Fatal(err)
	}
	awaitStream(t, first, "SIZE=80x24")
	time.Sleep(100 * time.Millisecond)
	if isDone(p.done) {
		t.Fatal("observer input reached the process")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	var writable []bool
	for len(writable) == 0 {
		ev, err := second.Next(time.Now().Add(3 * time.Second))
		if err != nil {
			t.Fatal(err)
		}
		if ev.End != "" {
			t.Fatalf("process ended (%s): detach or observer input stopped it", ev.End)
		}
		if ev.Writable != nil {
			writable = append(writable, *ev.Writable)
		}
	}
	if !writable[0] {
		t.Fatal("lease change was not announced")
	}
	if err := second.Input([]byte("size\n")); err != nil {
		t.Fatal(err)
	}
	awaitStream(t, second, "SIZE=80x24")
}

func TestTerminalStreamResumesFromOffsetOrRepaintsAfterOverflow(t *testing.T) {
	p := testPTY(t)
	c := streamClient(t, p)
	s := attachStream(t, c, nil)
	if err := s.Input([]byte("tui\n")); err != nil {
		t.Fatal(err)
	}
	awaitStream(t, s, "FRAME=80x24")
	offset := s.Offset()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	again := attachStream(t, c, &offset)
	if again.Reset {
		t.Fatal("a retained offset should resume without a repaint")
	}
	if err := again.Input([]byte("flood\n")); err != nil {
		t.Fatal(err)
	}
	waitPTY(t, p, "FLOOD_DONE")
	if err := again.Close(); err != nil {
		t.Fatal(err)
	}
	late := attachStream(t, c, &offset)
	if !late.Reset {
		t.Fatal("a lost offset must start from a repaint")
	}
	if screen := awaitStream(t, late, "FRAME=80x24"+frameEnd); strings.Contains(screen, "FLOOD_DONE") {
		t.Fatalf("repaint replayed the overflowed stream: %q", screen)
	}
}

func TestTerminalStreamReportsProcessExit(t *testing.T) {
	p := testPTY(t)
	c := streamClient(t, p)
	s := attachStream(t, c, nil)
	if err := s.Input([]byte("finish\n")); err != nil {
		t.Fatal(err)
	}
	for {
		ev, err := s.Next(time.Now().Add(3 * time.Second))
		if err != nil {
			t.Fatal(err)
		}
		if ev.End == "exited" {
			return
		}
	}
}
