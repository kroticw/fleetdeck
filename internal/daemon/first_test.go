package daemon

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/kroticw/fleetdeck/internal/daemon/daemontest"
)

// firstStand is a daemon with one session just started: its attach header says
// booting until bootedAfter attaches have been answered, and its list record
// echoes the last reply once one went in.
type firstStand struct {
	mu          sync.Mutex
	bootedAfter int
	attaches    int
	replied     string
	echoes      bool
	repliedAt   int // attaches answered when the reply came
}

func (s *firstStand) handlers() daemontest.Handler {
	return daemontest.Ops(map[string]daemontest.Handler{
		"attach": func(_ map[string]any, c net.Conn, _ *bufio.Reader) {
			s.mu.Lock()
			s.attaches++
			booting := s.attaches <= s.bootedAfter
			s.mu.Unlock()
			_, _ = fmt.Fprintf(c, `{"ok":true,"op":"attach","decModes":[],"via":"spare","booting":%v,"tempo":"active","state":"running","cached":false,"stale":false,"workerCliVersion":"2.1.278"}`+"\n", booting)
		},
		"reply": func(req map[string]any, c net.Conn, _ *bufio.Reader) {
			s.mu.Lock()
			s.replied, _ = req["text"].(string)
			s.repliedAt = s.attaches
			s.mu.Unlock()
			_, _ = fmt.Fprint(c, `{"ok":true,"op":"reply"}`+"\n")
		},
		"list": func(_ map[string]any, c net.Conn, _ *bufio.Reader) {
			s.mu.Lock()
			detail := ""
			if s.echoes {
				detail = s.replied
			}
			s.mu.Unlock()
			_, _ = fmt.Fprintf(c, `{"ok":true,"op":"list","jobs":[{"short":"aaaa1111","state":"running","tempo":"active","detail":%q,"intent":"","needs":""}]}`+"\n", detail)
		},
	})
}

func firstClient(t *testing.T, s *firstStand) (*Client, *fakeDaemon) {
	t.Helper()
	fd := startFakeDaemon(t, s.handlers())
	c := New(fd.socket, keyed("k"))
	c.firstWait = 300 * time.Millisecond
	c.firstPoll = time.Millisecond
	return c, fd
}

// A reply into a session that has not finished booting is acknowledged and
// lost: it is written into a terminal nothing reads yet. The attach header is
// the one place the daemon says the boot is over, so it is asked first.
func TestSendFirstRepliesOnlyOnceTheSessionHasBooted(t *testing.T) {
	s := &firstStand{bootedAfter: 3, echoes: true}
	c, fd := firstClient(t, s)
	if err := c.SendFirst(context.Background(), "aaaa1111", "do the thing"); err != nil {
		t.Fatalf("SendFirst: %v", err)
	}
	if s.repliedAt <= s.bootedAfter {
		t.Fatalf("replied after %d attaches, while the header said booting through the %dth", s.repliedAt, s.bootedAfter)
	}
	if n := len(fd.requests("reply")); n != 1 {
		t.Fatalf("replied %d times, want once", n)
	}
	// The probe is a real attach and a real attach resizes: 200x50 is the size
	// a fresh background session already runs at, so nothing changes.
	first := fd.requests("attach")[0]
	if first["cols"] != 200.0 || first["rows"] != 50.0 {
		t.Fatalf("the probe attached at %vx%v, want 200x50", first["cols"], first["rows"])
	}
}

// Acknowledged is not taken. The list echoes the last message a session took
// in its detail; until it does, the reply is not reported delivered — and it
// is not sent again, because a text sitting unsubmitted in the prompt would
// then go in twice.
func TestSendFirstReportsAReplyTheSessionNeverTook(t *testing.T) {
	s := &firstStand{echoes: false}
	c, fd := firstClient(t, s)
	err := c.SendFirst(context.Background(), "aaaa1111", "do the thing")
	var notTaken *ErrNotTaken
	if !errors.As(err, &notTaken) {
		t.Fatalf("err = %v, want *ErrNotTaken", err)
	}
	if notTaken.Session != "aaaa1111" {
		t.Fatalf("the error names %q", notTaken.Session)
	}
	if n := len(fd.requests("reply")); n != 1 {
		t.Fatalf("replied %d times, want exactly once", n)
	}
}

// A session that never finishes booting is not written into at all.
func TestSendFirstDoesNotReplyIntoASessionStillBooting(t *testing.T) {
	s := &firstStand{bootedAfter: 1 << 30, echoes: true}
	c, fd := firstClient(t, s)
	err := c.SendFirst(context.Background(), "aaaa1111", "do the thing")
	var notTaken *ErrNotTaken
	if !errors.As(err, &notTaken) {
		t.Fatalf("err = %v, want *ErrNotTaken", err)
	}
	if n := len(fd.requests("reply")); n != 0 {
		t.Fatalf("replied %d times into a session still booting", n)
	}
}

// The daemon's own refusal of the reply is passed through as itself: the
// caller retries a passing one, and must not be told the session took nothing
// when the daemon said something else.
func TestSendFirstPassesTheDaemonsRefusalThrough(t *testing.T) {
	fd := startFakeDaemon(t, daemontest.Ops(map[string]daemontest.Handler{
		"attach": func(_ map[string]any, c net.Conn, _ *bufio.Reader) {
			_, _ = fmt.Fprint(c, daemontest.AttachHeader+"\n")
		},
		"reply": func(_ map[string]any, c net.Conn, _ *bufio.Reader) {
			_, _ = fmt.Fprint(c, `{"ok":false,"code":"ENOREPLY"}`+"\n")
		},
	}))
	c := New(fd.socket, keyed("k"))
	c.firstWait, c.firstPoll = 300*time.Millisecond, time.Millisecond
	err := c.SendFirst(context.Background(), "aaaa1111", "do the thing")
	var noreply *ErrNoreply
	if !errors.As(err, &noreply) {
		t.Fatalf("err = %v, want *ErrNoreply", err)
	}
}

func TestSendFirstStopsWithItsContext(t *testing.T) {
	s := &firstStand{bootedAfter: 1 << 30}
	c, _ := firstClient(t, s)
	c.firstWait = time.Minute
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := c.SendFirst(ctx, "aaaa1111", "do the thing"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want the context's", err)
	}
}
