package orchestrator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kroticw/fleetdeck/internal/daemon"
)

// fleet is a daemon, a claude and a configuration in one: every call the
// appointer makes is recorded in order, so a test can say what happened and
// in which order.
type fleet struct {
	mu       sync.Mutex
	sessions []daemon.Session
	calls    []string
	sent     map[string][]string
	pinned   string

	// startShort is what a started session is called; startErr fails the start.
	startShort string
	startErr   error
	startCWD   string
	startName  string
	// listedAfter is how many List calls pass before a started session appears;
	// never set, a started session never appears.
	listedAfter int
	neverListed bool
	lists       int
	// sendErrs are returned by Send one per call, then nil.
	sendErrs []error
	pinErr   error
}

func (f *fleet) record(call string) {
	f.calls = append(f.calls, call)
}

func (f *fleet) list(context.Context) ([]daemon.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lists++
	out := slices.Clone(f.sessions)
	if f.startShort != "" && slices.Contains(f.calls, "start") && !f.neverListed && f.lists > f.listedAfter {
		out = append(out, daemon.Session{Short: f.startShort})
	}
	return out, nil
}

func (f *fleet) start(_ context.Context, cwd, name string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("start")
	f.startCWD, f.startName = cwd, name
	if f.startErr != nil {
		return "", f.startErr
	}
	return f.startShort, nil
}

func (f *fleet) send(_ context.Context, short, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("send " + short)
	if len(f.sendErrs) > 0 {
		err := f.sendErrs[0]
		f.sendErrs = f.sendErrs[1:]
		if err != nil {
			return err
		}
	}
	if f.sent == nil {
		f.sent = map[string][]string{}
	}
	f.sent[short] = append(f.sent[short], text)
	return nil
}

func (f *fleet) pin(short string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("pin " + short)
	if f.pinErr != nil {
		return f.pinErr
	}
	f.pinned = short
	return nil
}

// workspace is a board and a docs directory on disk.
func workspace(t *testing.T) Paths {
	t.Helper()
	root := t.TempDir()
	p := Paths{Board: filepath.Join(root, "board"), Docs: []string{filepath.Join(root, "docs")}, Config: filepath.Join(root, "config.yaml")}
	for _, dir := range []string{p.Board, p.Docs[0]} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

func appointer(f *fleet, p Paths) *Appointer {
	return &Appointer{
		Paths: p,
		List:  f.list,
		Send:  f.send,
		Start: f.start,
		Pin:   f.pin,
		// The waits are counted in polls, not seconds, so a test that waits
		// out a deadline is fast.
		Poll:      time.Millisecond,
		StartWait: 50 * time.Millisecond,
		SendWait:  20 * time.Millisecond,
	}
}

func stepNames(steps []Step) []string {
	names := make([]string, len(steps))
	for i, s := range steps {
		names[i] = s.Name
		if s.Error != "" {
			names[i] += " (refused)"
		}
	}
	return names
}

func TestAppointANewSession(t *testing.T) {
	p := workspace(t)
	f := &fleet{startShort: "0a1b2c3d", listedAfter: 2}
	res, err := appointer(f, p).Appoint(context.Background(), Request{New: true, Lang: "ru"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || res.Session != "0a1b2c3d" {
		t.Fatalf("result = %+v, want ok with the started session", res)
	}
	if want := []string{"start", "send 0a1b2c3d", "pin 0a1b2c3d"}; !slices.Equal(f.calls, want) {
		t.Errorf("calls = %v, want %v", f.calls, want)
	}
	if want := filepath.Dir(p.Board); f.startCWD != want {
		t.Errorf("the session started in %q, want the workspace %q", f.startCWD, want)
	}
	if f.startName != "оркестратор" {
		t.Errorf("the session was named %q", f.startName)
	}
	if f.lists < 3 {
		t.Errorf("the message went out after %d lists; the session appears on the third", f.lists)
	}
	if want := []string{"brief", "session", "message", "pin"}; !slices.Equal(stepNames(res.Steps), want) {
		t.Errorf("steps = %v, want %v", stepNames(res.Steps), want)
	}
	brief, err := os.ReadFile(BriefPath(p))
	if err != nil {
		t.Fatalf("no brief on disk: %v", err)
	}
	want, _ := Brief("ru", p)
	if string(brief) != string(want) {
		t.Error("the brief on disk is not Brief(ru)")
	}
}

func TestAppointAnExistingSession(t *testing.T) {
	p := workspace(t)
	f := &fleet{sessions: []daemon.Session{{Short: "11111111"}, {Short: "22222222"}}}
	res, err := appointer(f, p).Appoint(context.Background(), Request{Session: "22222222", Lang: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || res.Session != "22222222" {
		t.Fatalf("result = %+v", res)
	}
	if want := []string{"send 22222222", "pin 22222222"}; !slices.Equal(f.calls, want) {
		t.Errorf("calls = %v, want %v — an existing session is not started", f.calls, want)
	}
	if want := []string{"brief", "message", "pin"}; !slices.Equal(stepNames(res.Steps), want) {
		t.Errorf("steps = %v, want %v", stepNames(res.Steps), want)
	}
}

// Requirement 4 of the card: the knowledge goes in the same way on both paths.
// The message is compared as sent, not as built, so a path that built its own
// would be caught here.
func TestBothPathsSendTheSameMessage(t *testing.T) {
	for _, lang := range []string{"en", "ru"} {
		p := workspace(t)
		f := &fleet{sessions: []daemon.Session{{Short: "22222222"}}, startShort: "0a1b2c3d"}
		a := appointer(f, p)
		if _, err := a.Appoint(context.Background(), Request{New: true, Lang: lang}); err != nil {
			t.Fatal(err)
		}
		if _, err := a.Appoint(context.Background(), Request{Session: "22222222", Lang: lang}); err != nil {
			t.Fatal(err)
		}
		fresh, existing := f.sent["0a1b2c3d"], f.sent["22222222"]
		if len(fresh) != 1 || len(existing) != 1 {
			t.Fatalf("%s: sent %v to the new session and %v to the existing one, want one message each", lang, fresh, existing)
		}
		if fresh[0] != existing[0] {
			t.Errorf("%s: the paths sent different messages:\nnew:      %q\nexisting: %q", lang, fresh[0], existing[0])
		}
		if want := Message(lang, BriefPath(p)); fresh[0] != want {
			t.Errorf("%s: sent %q, want %q", lang, fresh[0], want)
		}
	}
}

func TestAppointRefusesARequestThatIsNotOneOfTheTwo(t *testing.T) {
	for name, req := range map[string]Request{
		"neither":        {},
		"both":           {New: true, Session: "22222222"},
		"not a short":    {Session: "22222222; rm"},
		"short too long": {Session: "222222222"},
		"upper case":     {Session: "ABCDEF12"},
	} {
		f := &fleet{sessions: []daemon.Session{{Short: "22222222"}}}
		_, err := appointer(f, workspace(t)).Appoint(context.Background(), req)
		if !errors.Is(err, ErrBadRequest) {
			t.Errorf("%s: err = %v, want ErrBadRequest", name, err)
		}
		if len(f.calls) != 0 {
			t.Errorf("%s: a refused request still did %v", name, f.calls)
		}
	}
}

// A panel that cannot start sessions (a stand given no claude of its own)
// refuses the new-session path before it writes anything.
func TestAppointWithoutStartRefusesANewSession(t *testing.T) {
	p := workspace(t)
	f := &fleet{}
	a := appointer(f, p)
	a.Start = nil
	_, err := a.Appoint(context.Background(), Request{New: true})
	if !errors.Is(err, ErrCannotStart) {
		t.Fatalf("err = %v, want ErrCannotStart", err)
	}
	if _, statErr := os.Stat(BriefPath(p)); statErr == nil {
		t.Error("the brief was written for a session that could not be started")
	}
}

func TestAppointRefusesASessionTheDaemonDoesNotList(t *testing.T) {
	for name, sessions := range map[string][]daemon.Session{
		"absent": {{Short: "11111111"}},
		"dying":  {{Short: "22222222", Dying: true}},
	} {
		p := workspace(t)
		f := &fleet{sessions: sessions}
		res, err := appointer(f, p).Appoint(context.Background(), Request{Session: "22222222"})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if res.OK {
			t.Errorf("%s: appointed a session the daemon does not list alive", name)
		}
		if len(f.calls) != 0 {
			t.Errorf("%s: still did %v", name, f.calls)
		}
		if _, statErr := os.Stat(BriefPath(p)); statErr == nil {
			t.Errorf("%s: wrote the brief for a session it will not appoint", name)
		}
	}
}

// A brief that cannot be written stops everything: no session is started for
// a message that points at nothing.
func TestAppointStopsWhenTheBriefCannotBeWritten(t *testing.T) {
	p := workspace(t)
	if err := os.WriteFile(BriefPath(p), []byte("# mine\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := &fleet{startShort: "0a1b2c3d", sessions: []daemon.Session{{Short: "22222222"}}}
	for _, req := range []Request{{New: true}, {Session: "22222222"}} {
		res, err := appointer(f, p).Appoint(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		if res.OK {
			t.Errorf("%+v: ok over a brief that was refused", req)
		}
		if got := stepNames(res.Steps); !slices.Equal(got, []string{"brief (refused)"}) {
			t.Errorf("%+v: steps = %v", req, got)
		}
	}
	if len(f.calls) != 0 {
		t.Errorf("calls = %v, want none", f.calls)
	}
}

func TestAppointReportsASessionThatWouldNotStart(t *testing.T) {
	f := &fleet{startErr: errors.New("claude --bg: exit status 1: not logged in")}
	res, err := appointer(f, workspace(t)).Appoint(context.Background(), Request{New: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.OK {
		t.Error("ok after a failed start")
	}
	if got := stepNames(res.Steps); !slices.Equal(got, []string{"brief", "session (refused)"}) {
		t.Errorf("steps = %v", got)
	}
	if !strings.Contains(res.Steps[1].Error, "not logged in") {
		t.Errorf("the refusal lost claude's own words: %q", res.Steps[1].Error)
	}
}

// A session that was started and never appeared in the daemon's list is
// named, so the person can find it; nothing is sent to it and nothing pinned.
func TestAppointReportsAStartedSessionThatNeverAppears(t *testing.T) {
	f := &fleet{startShort: "0a1b2c3d", neverListed: true}
	res, err := appointer(f, workspace(t)).Appoint(context.Background(), Request{New: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.OK {
		t.Error("ok for a session that never appeared")
	}
	if want := []string{"start"}; !slices.Equal(f.calls, want) {
		t.Errorf("calls = %v, want %v", f.calls, want)
	}
	last := res.Steps[len(res.Steps)-1]
	if last.Error == "" || !strings.Contains(last.Error, "0a1b2c3d") {
		t.Errorf("the last step does not name the started session: %+v", last)
	}
}

// A session still coming up, or momentarily not taking input, is asked
// again; the message is delivered once.
func TestAppointAsksAgainWhileTheSessionIsNotTakingInput(t *testing.T) {
	f := &fleet{
		sessions: []daemon.Session{{Short: "22222222"}},
		sendErrs: []error{&daemon.ErrStarting{}, &daemon.ErrNoreply{}, &daemon.ErrRespawning{}},
	}
	res, err := appointer(f, workspace(t)).Appoint(context.Background(), Request{Session: "22222222"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("not ok: %+v", res.Steps)
	}
	if got := len(f.sent["22222222"]); got != 1 {
		t.Errorf("delivered %d times, want once", got)
	}
}

// A session that keeps refusing is not pinned, and the refusal says what to
// look at — most often the session is asking its own question on its screen.
func TestAppointDoesNotPinASessionThatNeverTookTheMessage(t *testing.T) {
	refusals := make([]error, 1000)
	for i := range refusals {
		refusals[i] = &daemon.ErrNoreply{}
	}
	f := &fleet{sessions: []daemon.Session{{Short: "22222222"}}, sendErrs: refusals}
	res, err := appointer(f, workspace(t)).Appoint(context.Background(), Request{Session: "22222222"})
	if err != nil {
		t.Fatal(err)
	}
	if res.OK {
		t.Error("ok though the message never went in")
	}
	if slices.Contains(f.calls, "pin 22222222") {
		t.Error("pinned a session that never took the message")
	}
	if got := stepNames(res.Steps); !slices.Equal(got, []string{"brief", "message (refused)"}) {
		t.Errorf("steps = %v", got)
	}
	if !strings.Contains(res.Steps[1].Error, "its own screen") {
		t.Errorf("the refusal does not say where to look: %q", res.Steps[1].Error)
	}
}

func TestAppointDoesNotRetryAnErrorThatWillNotPass(t *testing.T) {
	f := &fleet{sessions: []daemon.Session{{Short: "22222222"}}, sendErrs: []error{&daemon.ErrAuth{}}}
	res, err := appointer(f, workspace(t)).Appoint(context.Background(), Request{Session: "22222222"})
	if err != nil {
		t.Fatal(err)
	}
	if res.OK {
		t.Error("ok after an authentication failure")
	}
	if got := strings.Count(strings.Join(f.calls, ","), "send"); got != 1 {
		t.Errorf("sent %d times, want once", got)
	}
}

// The message went in and the pin did not: said as exactly that, because the
// session has read its rules whether or not the column shows it.
func TestAppointReportsAPinThatFailedAfterTheMessage(t *testing.T) {
	f := &fleet{sessions: []daemon.Session{{Short: "22222222"}}, pinErr: errors.New("config is read-only")}
	res, err := appointer(f, workspace(t)).Appoint(context.Background(), Request{Session: "22222222"})
	if err != nil {
		t.Fatal(err)
	}
	if res.OK {
		t.Error("ok without the pin")
	}
	if got := stepNames(res.Steps); !slices.Equal(got, []string{"brief", "message", "pin (refused)"}) {
		t.Errorf("steps = %v", got)
	}
}

// One appointment at a time: two at once would pin whichever finished last
// and leave the other with rules and no column.
func TestAppointRefusesASecondAppointmentWhileOneRuns(t *testing.T) {
	p := workspace(t)
	release := make(chan struct{})
	entered := make(chan struct{})
	f := &fleet{sessions: []daemon.Session{{Short: "22222222"}}}
	a := appointer(f, p)
	a.Send = func(ctx context.Context, short, text string) error {
		close(entered)
		<-release
		return f.send(ctx, short, text)
	}
	done := make(chan error, 1)
	go func() {
		_, err := a.Appoint(context.Background(), Request{Session: "22222222"})
		done <- err
	}()
	<-entered
	_, err := a.Appoint(context.Background(), Request{Session: "22222222"})
	close(release)
	if !errors.Is(err, ErrBusy) {
		t.Errorf("second appointment: err = %v, want ErrBusy", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
