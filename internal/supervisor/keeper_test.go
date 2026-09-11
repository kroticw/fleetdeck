package supervisor

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// The keeper is what makes the window the panel's owner: something must
// answer at the panel's URL, and when nothing does, the keeper starts the
// panel the window carries. These tests drive it with the same stand-in
// panel as process_test.go -- a real process on a real port.

type keeperRun struct {
	k      *Keeper
	events chan Event
	cancel context.CancelFunc
	done   chan struct{}
}

func newKeeper(t *testing.T, kind, addr string) *Keeper {
	t.Helper()
	return &Keeper{
		URL:          "http://" + addr + "/",
		Bin:          os.Args[0],
		Env:          append(os.Environ(), helperEnv+"="+kind+"@"+addr),
		LogPath:      filepath.Join(t.TempDir(), "panel.log"),
		StartTimeout: 5 * time.Second,
		MinUptime:    300 * time.Millisecond,
		Poll:         100 * time.Millisecond,
	}
}

// run starts k and kills every panel it started when the test ends: the
// keeper deliberately leaves its panel running when it stops.
func run(t *testing.T, k *Keeper) *keeperRun {
	t.Helper()
	r := &keeperRun{k: k, events: make(chan Event, 64), done: make(chan struct{})}
	var mu sync.Mutex
	var pids []int
	k.OnEvent = func(e Event) {
		if e.PID != 0 {
			mu.Lock()
			pids = append(pids, e.PID)
			mu.Unlock()
		}
		r.events <- e
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	go func() {
		k.Run(ctx)
		close(r.done)
	}()
	t.Cleanup(func() {
		cancel()
		<-r.done
		mu.Lock()
		defer mu.Unlock()
		for _, pid := range pids {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
	return r
}

// next is the next event, whatever it is; the test states what it must be.
func (r *keeperRun) next(t *testing.T, within time.Duration) Event {
	t.Helper()
	select {
	case e := <-r.events:
		return e
	case <-time.After(within):
		t.Fatalf("no event within %s", within)
		return Event{}
	}
}

func (r *keeperRun) expect(t *testing.T, want State, within time.Duration) Event {
	t.Helper()
	e := r.next(t, within)
	if e.State != want {
		t.Fatalf("event %s (%+v), want %s", e.State, e, want)
	}
	return e
}

func (r *keeperRun) quiet(t *testing.T, span time.Duration) {
	t.Helper()
	select {
	case e := <-r.events:
		t.Fatalf("unexpected event %s (%+v)", e.State, e)
	case <-time.After(span):
	}
}

func answersNow(url string) bool {
	c := &http.Client{Timeout: 300 * time.Millisecond}
	resp, err := c.Get(url)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return true
}

func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }

// serveElsewhere is a panel the keeper did not start -- one from a launch
// agent, from a terminal, from a window that has since quit.
func serveElsewhere(t *testing.T, addr string) (stop func()) {
	t.Helper()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "someone else's panel") })}
	go func() { _ = srv.Serve(ln) }()
	var once sync.Once
	stop = func() { once.Do(func() { _ = srv.Close() }) }
	t.Cleanup(stop)
	return stop
}

func TestTheKeeperStartsThePanelWhenNothingAnswers(t *testing.T) {
	addr := freeAddr(t)
	if answersNow("http://" + addr + "/") {
		t.Fatal("something answers before the keeper ran: this case cannot see a start")
	}
	r := run(t, newKeeper(t, "listen", addr))

	starting := r.expect(t, Starting, 5*time.Second)
	if starting.PID == 0 {
		t.Fatal("Starting carries no PID")
	}
	up := r.expect(t, Answering, 10*time.Second)
	if !up.Ours || up.PID != starting.PID {
		t.Fatalf("Answering %+v, want the panel just started (pid %d) marked as ours", up, starting.PID)
	}
	if !answersNow("http://" + addr + "/") {
		t.Fatal("the keeper said the panel answers, and nothing does")
	}
}

// A panel already answering is used as it is. Were the keeper to start its
// own anyway, the binary it is given here does not exist, and that start
// would come back as Starting or Failed.
func TestTheKeeperTakesAPanelThatAlreadyAnswers(t *testing.T) {
	addr := freeAddr(t)
	serveElsewhere(t, addr)
	k := newKeeper(t, "listen", addr)
	k.Bin = filepath.Join(t.TempDir(), "no-such-panel")
	r := run(t, k)

	up := r.expect(t, Answering, 5*time.Second)
	if up.Ours || up.PID != 0 {
		t.Fatalf("Answering %+v, want a panel that is not ours", up)
	}
	r.quiet(t, 500*time.Millisecond)
}

// The panel from a window that has quit is nobody's to restart but the next
// window's. When a taken panel goes away, the keeper starts its own.
func TestTheKeeperStartsItsOwnWhenATakenPanelGoesAway(t *testing.T) {
	addr := freeAddr(t)
	stop := serveElsewhere(t, addr)
	r := run(t, newKeeper(t, "listen", addr))
	if up := r.expect(t, Answering, 5*time.Second); up.Ours {
		t.Fatalf("Answering %+v, want the panel already there", up)
	}

	stop()
	r.expect(t, Starting, 5*time.Second)
	if up := r.expect(t, Answering, 10*time.Second); !up.Ours {
		t.Fatalf("Answering %+v, want the keeper's own panel", up)
	}
}

// A panel busy for a moment -- one look at it taking longer than the keeper
// waits -- is not a panel gone: the keeper neither starts another nor says
// anything. Only two missed looks in a row mean gone.
func TestTheKeeperDoesNotTakeOneMissedLookForAPanelGone(t *testing.T) {
	addr := freeAddr(t)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	requests := 0
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		requests++
		n := requests
		mu.Unlock()
		if n == 3 {
			// Longer than one look waits, so this look is a miss.
			time.Sleep(answerTimeout + 300*time.Millisecond)
		}
		fmt.Fprint(w, "busy panel")
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	r := run(t, newKeeper(t, "listen", addr))
	if up := r.expect(t, Answering, 5*time.Second); up.Ours {
		t.Fatalf("Answering %+v, want the panel already there", up)
	}
	r.quiet(t, 2*time.Second)

	// Control: the slow look really happened inside the quiet window --
	// otherwise quiet proves nothing about a miss.
	mu.Lock()
	defer mu.Unlock()
	if requests < 5 {
		t.Fatalf("only %d looks at the panel: the missed one may not have happened", requests)
	}
}

// What launchd's KeepAlive did for the launch agent: a panel that dies after
// it has been up a while is started again.
func TestTheKeeperRestartsAPanelThatDiesAfterItsMinimumUptime(t *testing.T) {
	addr := freeAddr(t)
	k := newKeeper(t, "listen", addr)
	r := run(t, k)
	r.expect(t, Starting, 5*time.Second)
	first := r.expect(t, Answering, 10*time.Second)

	time.Sleep(2 * k.MinUptime)
	_ = syscall.Kill(first.PID, syscall.SIGKILL)

	again := r.expect(t, Starting, 5*time.Second)
	if again.PID == first.PID {
		t.Fatalf("Starting pid %d, the one that died", again.PID)
	}
	if up := r.expect(t, Answering, 10*time.Second); !up.Ours || up.PID != again.PID {
		t.Fatalf("Answering %+v, want the restarted panel (pid %d)", up, again.PID)
	}
}

// ... and, again as launchd does, a panel that dies soon after starting is not
// started over and over: the keeper stops, says why, and waits to be asked.
func TestTheKeeperGivesUpOnAPanelThatDiesTooSoonAndWaitsToBeAsked(t *testing.T) {
	addr := freeAddr(t)
	k := newKeeper(t, "listen", addr)
	k.MinUptime = time.Minute
	r := run(t, k)
	r.expect(t, Starting, 5*time.Second)
	first := r.expect(t, Answering, 10*time.Second)

	_ = syscall.Kill(first.PID, syscall.SIGKILL)
	failed := r.expect(t, Failed, 5*time.Second)
	if failed.Err == nil || !strings.Contains(failed.Err.Error(), "killed") {
		t.Fatalf("Failed err %v, want how the panel ended", failed.Err)
	}
	if !strings.Contains(failed.LogTail, "helper stderr: listening on "+addr) {
		t.Fatalf("Failed log tail %q, want the panel's own last words", failed.LogTail)
	}
	r.quiet(t, 1*time.Second)
	if answersNow(k.URL) {
		t.Fatal("something answers after the keeper gave up: it started the panel again unasked")
	}

	k.Retry()
	r.expect(t, Starting, 5*time.Second)
	r.expect(t, Answering, 10*time.Second)
}

func TestTheKeeperSaysWhyAPanelDiedBeforeItAnswered(t *testing.T) {
	addr := freeAddr(t)
	r := run(t, newKeeper(t, "crash", addr))
	r.expect(t, Starting, 5*time.Second)
	failed := r.expect(t, Failed, 10*time.Second)
	if !strings.Contains(failed.LogTail, crashNotice) {
		t.Fatalf("Failed log tail %q, want %q from the panel's log", failed.LogTail, crashNotice)
	}
	if failed.Err == nil || !strings.Contains(failed.Err.Error(), "exit status 3") {
		t.Fatalf("Failed err %v, want the panel's exit status", failed.Err)
	}
}

// A panel that runs but never answers at the URL -- a port in its
// configuration other than the one the window looks at -- is not left
// running where nobody would find it.
func TestTheKeeperStopsAPanelThatNeverAnswers(t *testing.T) {
	addr := freeAddr(t)
	k := newKeeper(t, "silent", addr)
	k.StartTimeout = 600 * time.Millisecond
	r := run(t, k)
	starting := r.expect(t, Starting, 5*time.Second)
	// Control: the stand-in is alive and silent, not dead -- otherwise the
	// case would be the crash case under another name.
	time.Sleep(200 * time.Millisecond)
	if !alive(starting.PID) {
		t.Fatal("the silent stand-in died on its own: this case cannot see the keeper stop it")
	}

	failed := r.expect(t, Failed, 5*time.Second)
	if failed.Err == nil || !strings.Contains(failed.Err.Error(), k.URL) {
		t.Fatalf("Failed err %v, want it to name the URL nothing answered at", failed.Err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for alive(starting.PID) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if alive(starting.PID) {
		t.Fatalf("pid %d still runs after the keeper gave up on it", starting.PID)
	}
}

func TestTheKeeperFailsInWordsWhenThePanelCannotBeStarted(t *testing.T) {
	addr := freeAddr(t)
	k := newKeeper(t, "listen", addr)
	k.Bin = filepath.Join(t.TempDir(), "no-such-panel")
	r := run(t, k)
	failed := r.expect(t, Failed, 5*time.Second)
	if failed.Err == nil || !strings.Contains(failed.Err.Error(), k.Bin) {
		t.Fatalf("Failed err %v, want it to name the binary", failed.Err)
	}
}

// Two windows opened at once, each with its keeper, end with one panel: the
// second one's panel cannot take the port, and its keeper takes the first's.
func TestTwoKeepersAtOnceEndWithOnePanelAndNoFailure(t *testing.T) {
	addr := freeAddr(t)
	a := run(t, newKeeper(t, "listen", addr))
	b := run(t, newKeeper(t, "listen", addr))

	final := func(r *keeperRun) Event {
		var last Event
		deadline := time.After(5 * time.Second)
		for {
			select {
			case e := <-r.events:
				if e.State == Failed {
					t.Fatalf("a keeper failed: %+v", e)
				}
				last = e
			case <-deadline:
				return last
			}
		}
	}
	ea, eb := final(a), final(b)
	if ea.State != Answering || eb.State != Answering {
		t.Fatalf("final events %+v and %+v, want both answering", ea, eb)
	}
	if ea.Ours == eb.Ours {
		t.Fatalf("final events %+v and %+v, want exactly one panel owned", ea, eb)
	}
}

// The panel outlives the window: when the keeper stops, its panel does not.
func TestTheKeeperLeavesItsPanelRunningWhenItStops(t *testing.T) {
	addr := freeAddr(t)
	r := run(t, newKeeper(t, "listen", addr))
	r.expect(t, Starting, 5*time.Second)
	up := r.expect(t, Answering, 10*time.Second)

	r.cancel()
	select {
	case <-r.done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after its context ended")
	}
	if !alive(up.PID) || !answersNow("http://"+addr+"/") {
		t.Fatal("the panel went down with the keeper")
	}
}

// Any HTTP answer at all means a server listens there -- a panel answering 404
// or 500 is a panel, not an empty port the keeper should start another on.
// Only a failure to connect means nothing is there.
func TestAnyHTTPAnswerCountsAsSomethingAnswering(t *testing.T) {
	addr := freeAddr(t)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.NotFoundHandler()}
	go func() { _ = srv.Serve(ln) }()
	if !answers(context.Background(), "http://"+addr+"/") {
		t.Fatal("a server answering 404 does not count as answering")
	}
	_ = srv.Close()
	if answers(context.Background(), "http://"+addr+"/") {
		t.Fatal("a closed port counts as answering")
	}
}

func TestLogTailIsTheLastLinesOfTheLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "panel.log")
	var b strings.Builder
	for i := 1; i <= 50; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	got := LogTail(path, 3)
	if got != "line 48\nline 49\nline 50" {
		t.Fatalf("LogTail = %q", got)
	}
	if got := LogTail(filepath.Join(t.TempDir(), "absent.log"), 3); got != "" {
		t.Fatalf("LogTail of a missing file = %q, want empty", got)
	}
}
