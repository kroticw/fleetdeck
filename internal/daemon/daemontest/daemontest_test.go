package daemontest_test

// The stand-in is held to the real client: what the panel would make of its
// answers is what these check, not the bytes alone.

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/kroticw/fleetdeck/internal/daemon"
	"github.com/kroticw/fleetdeck/internal/daemon/daemontest"
)

func key() (string, error) { return "key", nil }

func TestTheClientListsWhatTheStandInIsGiven(t *testing.T) {
	d := daemontest.StartTest(t, daemontest.Ops(map[string]daemontest.Handler{
		"list": daemontest.List(`{"short":"aaaa1111","state":"working","name":"a long session name","needs":""},` +
			`{"short":"bbbb2222","state":"idle","tempo":"blocked","needs":"answer: which one? (A · B)"}`),
	}))
	sessions, err := daemon.New(d.Socket, key).ListSessions(context.Background())
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 2 || sessions[0].Short != "aaaa1111" || sessions[0].Name != "a long session name" || sessions[1].Short != "bbbb2222" {
		t.Fatalf("sessions = %+v", sessions)
	}
	if sessions[1].Waiting() != daemon.Yes {
		t.Fatal("a session with a question in needs does not read as waiting")
	}
	if got := d.Requests("list"); len(got) != 1 {
		t.Fatalf("list requests recorded = %v, want one", got)
	}
}

func TestAnAttachShowsTheScreenAndStaysOpen(t *testing.T) {
	hold := make(chan struct{})
	t.Cleanup(func() { close(hold) })
	screen := []byte("\x1b[2Jhello from the stand\r\n")
	d := daemontest.StartTest(t, daemontest.Ops(map[string]daemontest.Handler{
		"attach": daemontest.Screen(screen, hold),
		"resize": daemontest.Resized,
	}))
	a, err := daemon.New(d.Socket, key).Attach(context.Background(), "aaaa1111", 80, 24)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	defer a.Close()
	got := make([]byte, 0, len(screen))
	buf := make([]byte, 256)
	deadline := time.Now().Add(2 * time.Second)
	for len(got) < len(screen) && time.Now().Before(deadline) {
		n, err := a.Read(buf)
		got = append(got, buf[:n]...)
		if err != nil {
			t.Fatalf("read after %q: %v", got, err)
		}
	}
	if string(got) != string(screen) {
		t.Fatalf("screen = %q, want %q", got, screen)
	}
	if err := a.Resize(context.Background(), 100, 30); err != nil {
		t.Fatalf("Resize on the held attach: %v", err)
	}
	if req := d.Requests("attach"); len(req) != 1 || req[0]["short"] != "aaaa1111" {
		t.Fatalf("attach requests = %v", req)
	}
}

func TestAnOpTheStandInDoesNotKnowIsClosedWithoutAReply(t *testing.T) {
	d := daemontest.StartTest(t, daemontest.Ops(nil))
	c, err := net.Dial("unix", d.Socket)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := io.WriteString(c, `{"proto":1,"op":"reply","short":"x","text":"hi"}`+"\n"); err != nil {
		t.Fatal(err)
	}
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	if b, err := io.ReadAll(c); err != nil || len(b) != 0 {
		t.Fatalf("reply to an unknown op = %q, %v; want the connection closed with nothing", b, err)
	}
}
