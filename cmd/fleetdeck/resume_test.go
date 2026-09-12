package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kroticw/fleetdeck/internal/daemon"
	"github.com/kroticw/fleetdeck/internal/server"
)

// resumeStand builds the two directories a resume reads — the job store and
// the transcripts — and returns a resumer wired to them whose daemon call is
// recorded instead of made.
type resumeStand struct {
	jobs        string
	projects    string
	dispatched  []daemon.ResumeSpec
	dispatchErr error
	live        []daemon.Session
	liveErr     error
}

func (s *resumeStand) resumer() func(string) error {
	return resumeSession(resumeDeps{
		jobStore: s.jobs,
		projects: s.projects,
		listed: func(context.Context) ([]daemon.Session, error) {
			return s.live, s.liveErr
		},
		resume: func(_ context.Context, spec daemon.ResumeSpec) error {
			s.dispatched = append(s.dispatched, spec)
			return s.dispatchErr
		},
	})
}

func newResumeStand(t *testing.T) *resumeStand {
	t.Helper()
	return &resumeStand{jobs: t.TempDir(), projects: t.TempDir()}
}

// theShort is the session every test in this file resumes. One id throughout,
// because none of these tests is about telling two sessions apart — the one
// that asks about an unknown id simply asks for an id no record was written
// for.
const theShort = "aaaa1111"

// record writes the session into the stand's job store. cwd is created unless
// it is the literal "gone", which stands for a working directory that is not
// there any more — a deleted worktree, the commonest way a session stops being
// resumable.
func (s *resumeStand) record(t *testing.T, sessionID, cwd, body string) {
	t.Helper()
	dir := filepath.Join(s.jobs, theShort)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if cwd != "gone" && cwd != "" {
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatalf("mkdir cwd: %v", err)
		}
	}
	if body == "" {
		body = `{"sessionId":"` + sessionID + `","cwd":"` + cwd + `",
			"name":"a stopped session","intent":"do the thing",
			"respawnFlags":["--model","opus"]}`
	}
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(body), 0o600); err != nil {
		t.Fatalf("write state.json: %v", err)
	}
}

// transcript writes the session's transcript where Claude Code keeps them.
func (s *resumeStand) transcript(t *testing.T, sessionID string) string {
	t.Helper()
	dir := filepath.Join(s.projects, "-a-project")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	path := filepath.Join(dir, sessionID+".jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	return path
}

const aSessionID = "aaaa1111-0000-4000-8000-000000000001"

func TestResumeSessionBuildsTheSpecFromTheStore(t *testing.T) {
	s := newResumeStand(t)
	cwd := filepath.Join(t.TempDir(), "worktree")
	s.record(t, aSessionID, cwd, "")
	path := s.transcript(t, aSessionID)

	if err := s.resumer()(theShort); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if len(s.dispatched) != 1 {
		t.Fatalf("want one dispatch, got %d", len(s.dispatched))
	}
	spec := s.dispatched[0]
	if spec.Short != "aaaa1111" || spec.SessionID != aSessionID || spec.ResumeID != aSessionID {
		t.Errorf("identity wrong: %+v", spec)
	}
	if spec.CWD != cwd {
		t.Errorf("CWD = %q, want %q", spec.CWD, cwd)
	}
	if spec.Name != "a stopped session" || spec.Intent != "do the thing" {
		t.Errorf("name/intent not carried: %+v", spec)
	}
	// Carried verbatim: without them the session comes back on another model.
	if len(spec.Flags) != 2 || spec.Flags[0] != "--model" || spec.Flags[1] != "opus" {
		t.Errorf("Flags = %v, want the session's own", spec.Flags)
	}
	if spec.TranscriptPath != path {
		t.Errorf("TranscriptPath = %q, want %q", spec.TranscriptPath, path)
	}
}

// The trap this whole pre-flight exists for. A session that was never prompted
// leaves a job record and no transcript at all: the job store calls it
// resumable, the panel shows it as stopped, and dispatching it starts a worker
// that dies at once with "resumed worker crashed during startup: exit 1".
// Verified live on 2026-09-12 before any of this was written.
//
// Nothing is dispatched. Waiting thirty seconds to relay a crash that was
// knowable beforehand is not honesty, it is a slow way of saying the same
// thing — and it leaves a dead worker behind to be cleaned up by hand.
func TestResumeSessionRefusesASessionWithNoTranscript(t *testing.T) {
	s := newResumeStand(t)
	s.record(t, aSessionID, filepath.Join(t.TempDir(), "worktree"), "")

	err := s.resumer()(theShort)
	if !errors.Is(err, server.ErrSessionNotResumable) {
		t.Fatalf("err = %v, want it to wrap ErrSessionNotResumable", err)
	}
	if !strings.Contains(err.Error(), "never prompted") {
		t.Errorf("err = %q, want it to say the session was never prompted", err)
	}
	if len(s.dispatched) != 0 {
		t.Errorf("a doomed resume was dispatched anyway: %+v", s.dispatched)
	}
}

func TestResumeSessionRefusesAMissingWorkingDirectory(t *testing.T) {
	s := newResumeStand(t)
	s.record(t, aSessionID, "gone", `{"sessionId":"`+aSessionID+`","cwd":"/no/such/worktree"}`)
	s.transcript(t, aSessionID)

	err := s.resumer()(theShort)
	if !errors.Is(err, server.ErrSessionNotResumable) {
		t.Fatalf("err = %v, want it to wrap ErrSessionNotResumable", err)
	}
	// The directory is named, because which one it was is the difference
	// between the operator knowing what happened and guessing.
	if !strings.Contains(err.Error(), "/no/such/worktree") {
		t.Errorf("err = %q, want it to name the directory that is gone", err)
	}
	if len(s.dispatched) != 0 {
		t.Errorf("a doomed resume was dispatched anyway: %+v", s.dispatched)
	}
}

func TestResumeSessionRefusesASessionWithNoIDToResumeBy(t *testing.T) {
	s := newResumeStand(t)
	s.record(t, "", "", `{"name":"no id here"}`)

	if err := s.resumer()(theShort); !errors.Is(err, server.ErrSessionNotResumable) {
		t.Fatalf("err = %v, want it to wrap ErrSessionNotResumable", err)
	}
	if len(s.dispatched) != 0 {
		t.Errorf("a resume with no id was dispatched anyway: %+v", s.dispatched)
	}
}

// A session the daemon is already running has nothing to resume, and the
// descriptor would claim a short id that is taken. Refused here so the answer
// names what is actually true rather than relaying whatever the daemon says
// about a duplicate job.
func TestResumeSessionRefusesASessionThatIsAlreadyRunning(t *testing.T) {
	s := newResumeStand(t)
	s.record(t, aSessionID, filepath.Join(t.TempDir(), "worktree"), "")
	s.transcript(t, aSessionID)
	s.live = []daemon.Session{{Short: "aaaa1111", State: "working"}}

	err := s.resumer()(theShort)
	if !errors.Is(err, server.ErrSessionNotResumable) {
		t.Fatalf("err = %v, want it to wrap ErrSessionNotResumable", err)
	}
	if !strings.Contains(err.Error(), "already running") {
		t.Errorf("err = %q, want it to say the session is already running", err)
	}
	if len(s.dispatched) != 0 {
		t.Errorf("a running session was dispatched anyway: %+v", s.dispatched)
	}
}

// A session with no record in the store is not a session this panel can resume
// at all, and saying so is better than dispatching a descriptor built from
// nothing.
func TestResumeSessionRefusesAnUnknownShortID(t *testing.T) {
	s := newResumeStand(t)
	if err := s.resumer()("nosuchid"); err == nil {
		t.Fatal("resume succeeded for a session the store has never heard of")
	}
	if len(s.dispatched) != 0 {
		t.Errorf("an unknown session was dispatched anyway: %+v", s.dispatched)
	}
}

// The daemon's own failure is relayed as it is, not reshaped into a refusal
// about the session: it is worth pressing again, and a session that cannot be
// resumed is not.
func TestResumeSessionRelaysADaemonFailureUnchanged(t *testing.T) {
	s := newResumeStand(t)
	s.record(t, aSessionID, filepath.Join(t.TempDir(), "worktree"), "")
	s.transcript(t, aSessionID)
	s.dispatchErr = errors.New("resumed worker crashed during startup: exit 1")

	err := s.resumer()(theShort)
	if err == nil {
		t.Fatal("resume succeeded against a failing daemon")
	}
	if errors.Is(err, server.ErrSessionNotResumable) {
		t.Errorf("a daemon failure must not read as the session being unresumable: %v", err)
	}
	if !strings.Contains(err.Error(), "crashed during startup: exit 1") {
		t.Errorf("err = %q, want the daemon's own words", err)
	}
}

// The store's note of where the transcript is takes precedence when it still
// holds, and is ignored when it does not. It is a hint written when the session
// last ran; a project directory renamed since then leaves it pointing at
// nothing, and following it would send the worker somewhere empty instead of
// letting the ordinary search answer.
func TestResumeSessionIgnoresAStaleTranscriptHint(t *testing.T) {
	s := newResumeStand(t)
	cwd := filepath.Join(t.TempDir(), "worktree")
	s.record(t, aSessionID, cwd,
		`{"sessionId":"`+aSessionID+`","cwd":"`+cwd+`","linkScanPath":"/gone/projects/-old/`+aSessionID+`.jsonl"}`)
	found := s.transcript(t, aSessionID)

	if err := s.resumer()(theShort); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if got := s.dispatched[0].TranscriptPath; got != found {
		t.Errorf("TranscriptPath = %q, want the transcript that exists (%q)", got, found)
	}
}

// A session resumed from another carries two ids, and they do different jobs:
// the transcript that gets replayed is the one named by the resume id.
func TestResumeSessionResumesByTheResumeID(t *testing.T) {
	const resumeID = "bbbb2222-0000-4000-8000-000000000002"
	s := newResumeStand(t)
	cwd := filepath.Join(t.TempDir(), "worktree")
	s.record(t, aSessionID, cwd,
		`{"sessionId":"`+aSessionID+`","resumeSessionId":"`+resumeID+`","cwd":"`+cwd+`"}`)
	path := s.transcript(t, resumeID)

	if err := s.resumer()(theShort); err != nil {
		t.Fatalf("resume: %v", err)
	}
	spec := s.dispatched[0]
	if spec.ResumeID != resumeID || spec.SessionID != aSessionID {
		t.Errorf("ids the wrong way round: %+v", spec)
	}
	if spec.TranscriptPath != path {
		t.Errorf("TranscriptPath = %q, want the resume id's transcript %q", spec.TranscriptPath, path)
	}
}

// The daemon being unreachable must not become "this session cannot be
// resumed": the check that needs it is the already-running one, and failing it
// open lets the daemon itself refuse a duplicate, which it is in a position to
// know about and this panel is not.
func TestResumeSessionProceedsWhenTheLiveListCannotBeRead(t *testing.T) {
	s := newResumeStand(t)
	cwd := filepath.Join(t.TempDir(), "worktree")
	s.record(t, aSessionID, cwd, "")
	s.transcript(t, aSessionID)
	s.liveErr = errors.New("daemon unavailable")

	if err := s.resumer()(theShort); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if len(s.dispatched) != 1 {
		t.Errorf("want the resume attempted anyway, got %d dispatches", len(s.dispatched))
	}
}
