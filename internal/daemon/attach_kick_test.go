package daemon

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// The kick rules, pinned on the held attach — the only reader of an attach stream
// there is. They were pinned through ReadScreen and SendKeys while those existed;
// each case here is one of those rules, restated against the path that stays.

// The daemon writes the marker flush against whatever terminal bytes were already
// in flight, with no newline before it. TestAttachKickIsTypedAndItsMarkerNeverReachesTheReader
// sends the marker in a write of its own, so it arrives at the start of a read; this
// one puts screen, marker and close in one write, so the marker is found in the
// middle of what was read. A search anchored to the start of a read, or of a line,
// passes the first test and fails this one.
func TestAttachKickFlushAgainstTheScreenIsFound(t *testing.T) {
	const screen = "\x1b[2J\x1b[H> waiting for input"
	f := startFakeDaemon(t, func(_ map[string]any, c net.Conn, _ *bufio.Reader) {
		fmt.Fprint(c, `{"ok":true,"op":"attach"}`+"\n"+screen+"EKICKED: another connection attached")
	})
	a, err := New(f.socket, keyed("k")).Attach(context.Background(), "abc", 80, 24)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	defer a.Close()
	got, err := readAll(t, a)
	var kicked *ErrKicked
	if !errors.As(err, &kicked) {
		t.Fatalf("stream ended with %v, want *ErrKicked (read %q)", err, got)
	}
	if kicked.Detail != "another connection attached" {
		t.Errorf("kick detail = %q", kicked.Detail)
	}
	if got != screen {
		t.Errorf("reader got %q, want the screen before the marker, %q", got, screen)
	}
}

// A marker at the very end of a stream that stays open is not a kick: the daemon
// closes right after a real one, and a session can display the marker text and then
// sit idle. The reader gets neither the marker nor an error while the connection is
// open. Once it closes, the same bytes are a kick — the control that shows the
// silence before it was the rule, not a reader that saw nothing at all.
func TestAttachMarkerOnAnOpenStreamIsNotAKickUntilItCloses(t *testing.T) {
	closeNow := make(chan struct{})
	f := startFakeDaemon(t, func(_ map[string]any, c net.Conn, _ *bufio.Reader) {
		fmt.Fprint(c, `{"ok":true,"op":"attach"}`+"\n"+"EKICKED: another connection attached")
		<-closeNow
	})
	a, err := New(f.socket, keyed("k")).Attach(context.Background(), "abc", 80, 24)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	defer a.Close()

	type result struct {
		s   string
		err error
	}
	out := make(chan result, 1)
	go func() {
		s, err := readAll(t, a)
		out <- result{s, err}
	}()
	select {
	case r := <-out:
		t.Fatalf("an open stream ended with %q, %v; a marker is a kick only when the connection closes", r.s, r.err)
	case <-time.After(200 * time.Millisecond):
	}

	close(closeNow)
	select {
	case r := <-out:
		var kicked *ErrKicked
		if !errors.As(r.err, &kicked) {
			t.Fatalf("after the close the stream ended with %v, want *ErrKicked", r.err)
		}
		if r.s != "" {
			t.Errorf("the marker reached the reader as screen: %q", r.s)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the stream did not end after the daemon closed it")
	}
}

// What follows a real marker is a short reason with no newline. Text after it that
// is longer than any reason is screen, even without a newline in it, and the stream
// ends as an ordinary end with all of it shown.
func TestAttachLongTextAfterTheMarkerIsNotAKick(t *testing.T) {
	screen := "EKICKED: " + strings.Repeat("b", maxKickReasonBytes+44)
	f := startFakeDaemon(t, func(_ map[string]any, c net.Conn, _ *bufio.Reader) {
		fmt.Fprint(c, `{"ok":true,"op":"attach"}`+"\n"+screen)
	})
	a, err := New(f.socket, keyed("k")).Attach(context.Background(), "abc", 80, 24)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	defer a.Close()
	got, err := readAll(t, a)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("stream ended with %v, want io.EOF", err)
	}
	if got != screen {
		t.Errorf("reader got %d bytes, want the whole %d-byte screen", len(got), len(screen))
	}
}

// The first bytes of a marker at the end of a read are held back only when more
// output is already waiting. Nothing waiting, they are released at once: they are
// as likely to be the echo of a typed capital E, and holding that would make the
// operator's typing lag.
func TestKickHoldbackHoldsAPartialMarkerOnlyWhenMoreIsWaiting(t *testing.T) {
	if got := kickHoldback([]byte("abcEKI"), true); got != 3 {
		t.Errorf("more waiting: held %d bytes, want the 3 that could start the marker", got)
	}
	if got := kickHoldback([]byte("abcEKI"), false); got != 0 {
		t.Errorf("nothing waiting: held %d bytes, want none", got)
	}
}

// A complete marker is held back only while what follows it could still be a
// reason: short and without a newline. These are the same two rules
// parseKickedMarker applies at the close, applied here while the stream is open —
// pinned on their own, so that neither copy survives only because of the other.
func TestKickHoldbackHoldsACompleteMarkerOnlyWhileItCouldBeAReason(t *testing.T) {
	cases := []struct {
		name string
		data string
		held int
	}{
		{"a short reason is held", "screenEKICKED: bye", len("EKICKED: bye")},
		{"a newline after it is screen, released", "screenEKICKED: marker\n$ ", 0},
		{"a tail longer than any reason is screen, released", "EKICKED: " + strings.Repeat("b", maxKickReasonBytes+1), 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := kickHoldback([]byte(tc.data), false); got != tc.held {
				t.Errorf("kickHoldback(%q) held %d bytes, want %d", tc.data, got, tc.held)
			}
		})
	}
}

// parseKickedMarker is the second of two places every kick rule is applied on the
// held attach: kickHoldback applies them while the stream is open, releasing
// whatever does not look like a marker, and parseKickedMarker applies them again to
// what was held when the connection closes. Through Attach, what reaches it has
// always passed kickHoldback first, so its own conditions cannot be seen failing
// there — they were visible only through ReadScreen and SendKeys, which held
// nothing back. Pinned here directly, so that each copy of a rule stays loud on its
// own rather than surviving only because the other copy is still there.
func TestParseKickedMarkerRules(t *testing.T) {
	cases := []struct {
		name   string
		data   string
		kicked bool
		prefix string
		detail string
	}{
		{"marker flush against the screen", "> waiting for inputEKICKED: another connection attached", true, "> waiting for input", "another connection attached"},
		{"marker alone", "EKICKED: bye", true, "", "bye"},
		{"a newline after the marker is screen", "EKICKED: marker\n$ ", false, "", ""},
		{"text after the marker longer than any reason is screen", "EKICKED: " + strings.Repeat("b", maxKickReasonBytes+1), false, "", ""},
		{"the last marker counts, not the first", "EKICKED: shown\n$ grep\nEKICKED: real", true, "EKICKED: shown\n$ grep\n", "real"},
		{"no marker", "plain screen", false, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prefix, detail, kicked := parseKickedMarker([]byte(tc.data))
			if kicked != tc.kicked || string(prefix) != tc.prefix || detail != tc.detail {
				t.Errorf("parseKickedMarker(%q) = %q, %q, %v; want %q, %q, %v", tc.data, prefix, detail, kicked, tc.prefix, tc.detail, tc.kicked)
			}
		})
	}
}

// A daemon that refuses the control key answers with EAUTH. The refusal comes back
// typed, and the key, which the request did carry, appears nowhere in the error.
func TestAttachRefusedForTheKeyIsTypedAndNeverRepeatsIt(t *testing.T) {
	const key = "my-secret-key-12345"
	f := startFakeDaemon(t, func(_ map[string]any, c net.Conn, _ *bufio.Reader) {
		fmt.Fprint(c, `{"ok":false,"code":"EAUTH","error":"invalid auth"}`+"\n")
	})
	_, err := New(f.socket, keyed(key)).Attach(context.Background(), "abc", 80, 24)
	if got := f.requests("attach"); len(got) != 1 || got[0]["auth"] != key {
		t.Fatalf("precondition: the attach request must carry the key; saw %v", got)
	}
	var auth *ErrAuth
	if !errors.As(err, &auth) {
		t.Fatalf("Attach refused for the key = %v, want *ErrAuth", err)
	}
	if strings.Contains(err.Error(), key) {
		t.Errorf("the control key leaked into the error: %q", err.Error())
	}
}
