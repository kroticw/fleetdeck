package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// serveN answers connections until the test ends, handing each request to fn
// along with how many requests have been answered before it. It is serveOnce's
// many-connection sibling: resuming is one dispatch followed by however many
// list calls the wait takes, over a separate connection each, and a helper that
// answers exactly one of them cannot exercise the wait at all.
//
// Like serveOnce it never calls t.Fatalf — it runs on its own goroutine, where
// FailNow only stops that goroutine and leaves the client blocked until its own
// deadline, turning a clear failure into a hang that looks like something else.
func serveN(t *testing.T, listener net.Listener, fn func(n int, req []byte) []byte) {
	t.Helper()
	var n int
	for {
		conn, err := listener.Accept()
		if err != nil {
			return // listener closed by the test: this is the ordinary end
		}
		reader := bufio.NewReader(conn)
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			t.Errorf("ReadString: %v", err)
			conn.Close()
			return
		}
		resp := fn(n, []byte(strings.TrimSuffix(line, "\n")))
		n++
		if resp != nil {
			if _, err := conn.Write(resp); err != nil {
				t.Errorf("Write: %v", err)
			}
		}
		conn.Close()
	}
}

// fastResume shortens the wait so a test that exercises a timeout takes
// milliseconds rather than the half minute the real one allows a session with a
// long history. The durations themselves are asserted separately, in
// TestResumeWaitDefaultsAreTheMeasuredOnes, so shortening them here cannot hide
// a change to what the panel actually does.
func fastResume(c *Client) {
	c.resumeTimeout = 300 * time.Millisecond
	c.resumeSettle = 20 * time.Millisecond
	c.resumePoll = 5 * time.Millisecond
	c.resumeRetryDelay = 5 * time.Millisecond
}

func testSpec() ResumeSpec {
	return ResumeSpec{
		Short:     "aa11bb22",
		SessionID: "aa11bb22-0000-4000-8000-000000000001",
		ResumeID:  "cc33dd44-0000-4000-8000-000000000002",
		CWD:       "/somewhere/a-worktree",
		Name:      "a stopped session",
		Intent:    "do the thing",
		Flags:     []string{"--name", "a stopped session", "--model", "opus"},
	}
}

// listReply renders a list response holding one session in the given state.
func listReply(short, state, detail string) []byte {
	body, _ := json.Marshal(map[string]any{
		"ok": true, "op": "list",
		"jobs": []map[string]any{{"short": short, "state": state, "detail": detail}},
	})
	return append(body, '\n')
}

var emptyList = []byte(`{"ok":true,"op":"list","jobs":[]}` + "\n")
var dispatchOK = []byte(`{"ok":true,"op":"dispatch","short":"aa11bb22"}` + "\n")

func resumeClient(t *testing.T, listener net.Listener) *Client {
	t.Helper()
	c := New(listener.Addr().String(), func() (string, error) { return "the-key", nil })
	c.proto = 7
	fastResume(c)
	return c
}

// The descriptor is this client's half of an operation the protocol document
// does not describe, so every field of it is asserted literally here. A field
// quietly dropped or renamed does not fail loudly against the real daemon — it
// resumes the session as something slightly other than itself.
func TestResumeSendsTheDescriptorTheDaemonExpects(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	var mu sync.Mutex
	var dispatch map[string]any
	go serveN(t, listener, func(n int, req []byte) []byte {
		if n == 0 {
			mu.Lock()
			if err := json.Unmarshal(req, &dispatch); err != nil {
				t.Errorf("dispatch request is not JSON: %v", err)
			}
			mu.Unlock()
			return dispatchOK
		}
		return listReply("aa11bb22", "working", "")
	})

	spec := testSpec()
	spec.TranscriptPath = "/home/p/projects/-x/cc33dd44-0000-4000-8000-000000000002.jsonl"
	if err := resumeClient(t, listener).Resume(context.Background(), spec); err != nil {
		t.Fatalf("Resume: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if got := dispatch["op"]; got != "dispatch" {
		t.Errorf(`op = %v, want "dispatch"`, got)
	}
	if got := dispatch["proto"]; got != float64(7) {
		t.Errorf("proto = %v, want the negotiated 7", got)
	}
	if got := dispatch["auth"]; got != "the-key" {
		t.Errorf("auth = %v, want the control key", got)
	}
	if got := dispatch["timeoutMs"]; got != float64(5000) {
		t.Errorf("timeoutMs = %v, want 5000", got)
	}

	d, ok := dispatch["d"].(map[string]any)
	if !ok {
		t.Fatalf("descriptor is missing or not an object: %#v", dispatch["d"])
	}
	for field, want := range map[string]any{
		"proto":     float64(7),
		"short":     "aa11bb22",
		"sessionId": "aa11bb22-0000-4000-8000-000000000001",
		"cwd":       "/somewhere/a-worktree",
		"source":    "fleet",
		"isolation": "none",
	} {
		if got := d[field]; got != want {
			t.Errorf("descriptor %s = %v, want %v", field, got, want)
		}
	}
	if nonce, _ := d["nonce"].(string); len(nonce) != 8 {
		t.Errorf("descriptor nonce = %q, want eight characters", nonce)
	}
	if createdAt, _ := d["createdAt"].(float64); createdAt <= 0 {
		t.Errorf("descriptor createdAt = %v, want a timestamp", d["createdAt"])
	}

	launch, ok := d["launch"].(map[string]any)
	if !ok {
		t.Fatalf("launch is missing or not an object: %#v", d["launch"])
	}
	if got := launch["mode"]; got != "resume" {
		t.Errorf(`launch.mode = %v, want "resume"`, got)
	}
	// The id resumed BY, which is not the id the session is known by when it
	// was itself resumed from another. Getting these two the wrong way round
	// resumes the wrong conversation, and both are valid UUIDs, so nothing
	// downstream would object.
	if got := launch["sessionId"]; got != "cc33dd44-0000-4000-8000-000000000002" {
		t.Errorf("launch.sessionId = %v, want the resume id", got)
	}
	// In place, never a copy: a fork leaves the stopped session where it was
	// and starts a second one beside it, which is the outcome this whole
	// button exists to avoid.
	if got := launch["fork"]; got != false {
		t.Errorf("launch.fork = %v, want false", got)
	}
	if got := launch["transcriptPath"]; got != spec.TranscriptPath {
		t.Errorf("launch.transcriptPath = %v, want %v", got, spec.TranscriptPath)
	}
	for _, key := range []string{"flagArgs"} {
		flags, ok := launch[key].([]any)
		if !ok || len(flags) != 4 || flags[2] != "--model" {
			t.Errorf("launch.%s = %#v, want the session's own flags", key, launch[key])
		}
	}
	if seed, ok := d["seed"].(map[string]any); !ok || seed["name"] != "a stopped session" || seed["intent"] != "do the thing" {
		t.Errorf("seed = %#v, want the session's name and intent", d["seed"])
	}
}

// An absent transcript is an absent key, not an empty string. The daemon reads
// the key's presence, and an empty path would point the resumed worker at
// nothing instead of letting it fall back to its own lookup.
func TestResumeOmitsTranscriptPathWhenThereIsNone(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	var mu sync.Mutex
	var launch map[string]any
	go serveN(t, listener, func(n int, req []byte) []byte {
		if n == 0 {
			var body struct {
				D struct {
					Launch map[string]any `json:"launch"`
				} `json:"d"`
			}
			if err := json.Unmarshal(req, &body); err != nil {
				t.Errorf("dispatch request is not JSON: %v", err)
			}
			mu.Lock()
			launch = body.D.Launch
			mu.Unlock()
			return dispatchOK
		}
		return listReply("aa11bb22", "working", "")
	})

	if err := resumeClient(t, listener).Resume(context.Background(), testSpec()); err != nil {
		t.Fatalf("Resume: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if _, present := launch["transcriptPath"]; present {
		t.Errorf("launch.transcriptPath is present as %#v, want the key absent entirely", launch["transcriptPath"])
	}
}

// The daemon refusing the dispatch is reported in its own words. This is the
// one failure the operator can sometimes act on — a daemon mid-restart, a key
// that no longer matches — and a generic "resume failed" throws away the only
// thing that says which.
func TestResumeReportsTheDaemonsRefusalInItsOwnWords(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	go serveN(t, listener, func(_ int, _ []byte) []byte {
		return []byte(`{"ok":false,"op":"dispatch","error":"job already exists"}` + "\n")
	})

	err = resumeClient(t, listener).Resume(context.Background(), testSpec())
	if err == nil {
		t.Fatal("Resume succeeded against a daemon that refused it")
	}
	if !strings.Contains(err.Error(), "job already exists") {
		t.Errorf("Resume error = %q, want it to carry the daemon's own words", err)
	}
}

// A worker that comes up and crashes is terminal, and the crash reason is the
// whole message. This is the shape of the known trap: a session that was never
// prompted has no transcript, and its resumed worker dies at startup with
// "exit 1" in the roster's detail. Waiting out the full timeout on it would
// leave the operator staring at a spinner for half a minute before learning
// nothing.
func TestResumeStopsAtOnceWhenTheWorkerCrashes(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	go serveN(t, listener, func(n int, _ []byte) []byte {
		if n == 0 {
			return dispatchOK
		}
		return listReply("aa11bb22", "crashed", "exit 1")
	})

	start := time.Now()
	err = resumeClient(t, listener).Resume(context.Background(), testSpec())
	if err == nil {
		t.Fatal("Resume succeeded against a worker that crashed")
	}
	if !strings.Contains(err.Error(), "exit 1") {
		t.Errorf("Resume error = %q, want the crash reason from the roster", err)
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Errorf("Resume took %v to report a crash, want it to stop at the first crashed reading", elapsed)
	}
}

// Appearing and then leaving the roster is the other terminal failure: the
// worker registered, died, and was retired. Indistinguishable from the crash
// above to anyone reading the list one tick too late, which is why the wait
// remembers having seen it.
func TestResumeReportsAWorkerThatLeavesTheRoster(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	go serveN(t, listener, func(n int, _ []byte) []byte {
		switch {
		case n == 0:
			return dispatchOK
		case n == 1:
			return listReply("aa11bb22", "resuming", "")
		default:
			return emptyList
		}
	})

	err = resumeClient(t, listener).Resume(context.Background(), testSpec())
	if err == nil {
		t.Fatal("Resume succeeded against a worker that left the roster")
	}
	if !strings.Contains(err.Error(), "exited during startup") {
		t.Errorf("Resume error = %q, want it to say the worker exited during startup", err)
	}
}

// "resuming" is not "live": a worker replaying a long history sits in it for
// as long as the replay takes. Reporting success there would hand the operator
// a session that is not yet answering, and the row would go back to looking
// stopped if it then died.
func TestResumeDoesNotAcceptAWorkerStillResuming(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	go serveN(t, listener, func(n int, _ []byte) []byte {
		if n == 0 {
			return dispatchOK
		}
		return listReply("aa11bb22", "resuming", "")
	})

	err = resumeClient(t, listener).Resume(context.Background(), testSpec())
	if err == nil {
		t.Fatal("Resume succeeded while the worker was still resuming")
	}
	if !strings.Contains(err.Error(), "resuming") {
		t.Errorf("Resume error = %q, want it to name the state it was stuck in", err)
	}
}

// A worker that never registers at all is its own failure, and says so. The
// dispatch was accepted, so "the daemon refused it" would be a lie.
func TestResumeReportsAWorkerThatNeverRegisters(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	go serveN(t, listener, func(n int, _ []byte) []byte {
		if n == 0 {
			return dispatchOK
		}
		return emptyList
	})

	err = resumeClient(t, listener).Resume(context.Background(), testSpec())
	if err == nil {
		t.Fatal("Resume succeeded against a worker that never appeared")
	}
	if !strings.Contains(err.Error(), "never registered") {
		t.Errorf("Resume error = %q, want it to say the worker never registered", err)
	}
}

// A session must hold a usable state for the settle window before the panel
// calls it live. A single usable reading is not enough: a worker flickers
// through states as it comes up, and the row would go back to stopped right
// after the button reported success.
func TestResumeWaitsOutTheSettleWindow(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	var mu sync.Mutex
	lists := 0
	go serveN(t, listener, func(n int, _ []byte) []byte {
		if n == 0 {
			return dispatchOK
		}
		mu.Lock()
		lists++
		mu.Unlock()
		return listReply("aa11bb22", "working", "")
	})

	c := resumeClient(t, listener)
	if err := c.Resume(context.Background(), testSpec()); err != nil {
		t.Fatalf("Resume: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	// settle is four poll intervals here, so one reading cannot have been
	// enough however the loop is arranged.
	if lists < 2 {
		t.Errorf("the wait made %d list calls, want it to hold the session usable across the settle window", lists)
	}
}

// A resume needs the control key, and a panel without one must refuse the
// whole write rather than dial the daemon and be told no. The key never
// reaches the error either: that rule (protocol section 6) is why this asserts
// the message as well as the failure.
func TestResumeRefusesWithoutAControlKey(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	go serveN(t, listener, func(_ int, _ []byte) []byte {
		t.Error("the client dialled the daemon with no control key in hand")
		return nil
	})

	c := New(listener.Addr().String(), func() (string, error) { return "", errors.New("the key file is not there") })
	c.proto = 7
	fastResume(c)

	err = c.Resume(context.Background(), testSpec())
	if !errors.Is(err, ErrNoControlKey) {
		t.Fatalf("Resume error = %v, want ErrNoControlKey", err)
	}
}

// A resume with no id to resume by is refused before anything is sent. The
// daemon would answer some refusal of its own, but the panel already knows
// this one and can say it in words that mean something here.
func TestResumeRefusesWithNoResumeID(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	go serveN(t, listener, func(_ int, _ []byte) []byte {
		t.Error("the client dispatched a resume with no id to resume by")
		return nil
	})

	spec := testSpec()
	spec.ResumeID = ""
	if err := resumeClient(t, listener).Resume(context.Background(), spec); err == nil {
		t.Fatal("Resume succeeded with no id to resume by")
	}
}

// The three durations the wait runs on are the ones measured against a real
// daemon by claude-agents-mcp, whose resume this one mirrors deliberately.
// They are asserted here because every other test in this file shortens them,
// and a change to the real values would otherwise pass unnoticed.
func TestResumeWaitDefaultsAreTheMeasuredOnes(t *testing.T) {
	c := New("/nowhere.sock", stubKeyFunc)
	if c.resumeTimeout != 30*time.Second {
		t.Errorf("resumeTimeout = %v, want 30s", c.resumeTimeout)
	}
	if c.resumeSettle != 3*time.Second {
		t.Errorf("resumeSettle = %v, want 3s", c.resumeSettle)
	}
	if c.resumePoll != 300*time.Millisecond {
		t.Errorf("resumePoll = %v, want 300ms", c.resumePoll)
	}
}
