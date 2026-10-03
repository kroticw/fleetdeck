//go:build darwin

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/kroticw/fleetdeck/internal/supervisor"
)

// The update button is on screen only while there is something to update to,
// so its appearing is the notice. These tests hold the window to finding out
// by itself, while it runs, and to never letting a silent network pass for an
// answer either way.

var start = time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) pass(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// releasesPage stands in for a source: what it offers can change between two
// looks, the way a releases page does when somebody publishes.
type releasesPage struct {
	mu    sync.Mutex
	offer string
	err   error
	asked int
}

func (p *releasesPage) set(offer string, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.offer, p.err = offer, err
}

func (p *releasesPage) times() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.asked
}

func (p *releasesPage) Check(context.Context) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.asked++
	return p.offer, p.err
}

func (p *releasesPage) Stage(context.Context, string, string, func(supervisor.Progress)) (string, error) {
	return "", errors.New("looking for a newer version never stages anything")
}

// page records what the window told it.
type page struct {
	mu   sync.Mutex
	told []report
}

func (p *page) tell(r report) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.told = append(p.told, r)
}

func (p *page) reports() []report {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]report(nil), p.told...)
}

var offline = &supervisor.ReleasesUnreachableError{URL: "https://github.com/kroticw/fleetdeck/releases/latest", Err: errors.New("no route to host")}

// newWatch is a window running v0.7.0, with a mark of its own.
func newWatch(t *testing.T, src supervisor.Source, c *clock, p *page) *updateWatch {
	t.Helper()
	return &updateWatch{
		source:   src,
		running:  "v0.7.0",
		markPath: filepath.Join(t.TempDir(), "last-update-check"),
		tell:     p.tell,
		now:      c.Now,
	}
}

func TestTheFirstLookAsksAndTellsThePageWhatItFound(t *testing.T) {
	src := &releasesPage{offer: "v0.8.0"}
	p := &page{}
	w := newWatch(t, src, &clock{now: start}, p)

	w.look(context.Background())

	if src.times() != 1 {
		t.Fatalf("asked %d times, want once", src.times())
	}
	if got := p.reports(); len(got) != 1 || got[0] != (report{Step: "available", Detail: "v0.8.0"}) {
		t.Fatalf("the page was told %+v, want v0.8.0 available", got)
	}
	if got := w.known(); got != (report{Step: "available", Detail: "v0.8.0"}) {
		t.Fatalf("a page loading now would be told %+v", got)
	}
}

// The notice is the button appearing, so it has to appear in a window that is
// already open: nobody restarts an app to find out whether it is out of date.
func TestAReleasePublishedWhileTheWindowRunsIsShownWithoutARestart(t *testing.T) {
	src := &releasesPage{offer: ""}
	p := &page{}
	c := &clock{now: start}
	w := newWatch(t, src, c, p)
	ticks := make(chan time.Time)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		w.run(ctx, ticks)
		close(done)
	}()

	ticks <- c.Now() // the look at start has happened once this is taken
	if got := p.reports(); len(got) != 0 {
		t.Fatalf("told %+v while nothing was newer", got)
	}
	src.set("v0.8.0", nil)
	c.pass(askEvery)
	ticks <- c.Now()
	ticks <- c.Now() // taken only once the look before it has finished
	cancel()
	<-done

	got := p.reports()
	if len(got) != 1 || got[0] != (report{Step: "available", Detail: "v0.8.0"}) {
		t.Fatalf("the page was told %+v, want v0.8.0 available, once", got)
	}
}

// Asked a moment ago is asked: a look between two questions costs no request.
func TestALookSoonAfterTheLastQuestionDoesNotAskAgain(t *testing.T) {
	src := &releasesPage{offer: "v0.8.0"}
	c := &clock{now: start}
	w := newWatch(t, src, c, &page{})

	w.look(context.Background())
	c.pass(askEvery - time.Minute)
	w.look(context.Background())

	if src.times() != 1 {
		t.Fatalf("asked %d times within %s, want once", src.times(), askEvery)
	}
}

func TestOnceTheAnswerIsOldItAsksAgain(t *testing.T) {
	src := &releasesPage{offer: ""}
	c := &clock{now: start}
	w := newWatch(t, src, c, &page{})

	w.look(context.Background())
	c.pass(askEvery)
	w.look(context.Background())

	if src.times() != 2 {
		t.Fatalf("asked %d times, want again once %s had passed", src.times(), askEvery)
	}
}

// Opening the window again asks nothing new -- and must not forget what the
// last question found either: the button that was there before a restart is
// there after it. Forgetting would be a false "nothing newer" for hours.
func TestAWindowOpenedAgainShowsWhatWasFoundWithoutAsking(t *testing.T) {
	c := &clock{now: start}
	first := newWatch(t, &releasesPage{offer: "v0.8.0"}, c, &page{})
	first.look(context.Background())

	c.pass(time.Hour)
	src := &releasesPage{err: errors.New("a window opened again asked")}
	p := &page{}
	again := &updateWatch{source: src, running: "v0.7.0", markPath: first.markPath, tell: p.tell, now: c.Now}
	again.look(context.Background())

	if src.times() != 0 {
		t.Fatal("a window opened an hour after the last question asked again")
	}
	if got := again.known(); got != (report{Step: "available", Detail: "v0.8.0"}) {
		t.Fatalf("a page loading in the reopened window would be told %+v", got)
	}
	if got := p.reports(); len(got) != 1 || got[0].Step != "available" {
		t.Fatalf("the reopened window told its page %+v", got)
	}
}

// What was found was found for the version that asked. After an update the
// running version is the one that was offered, and the old answer would put
// the button back for an update that has already happened.
func TestAfterAnUpdateTheOldAnswerIsNotUsed(t *testing.T) {
	c := &clock{now: start}
	old := newWatch(t, &releasesPage{offer: "v0.8.0"}, c, &page{})
	old.look(context.Background())

	c.pass(time.Minute)
	src := &releasesPage{offer: ""}
	p := &page{}
	updated := &updateWatch{source: src, running: "v0.8.0", markPath: old.markPath, tell: p.tell, now: c.Now}
	updated.look(context.Background())

	if src.times() != 1 {
		t.Fatalf("the updated window asked %d times, want once: the answer it found was about another version", src.times())
	}
	if got := updated.known(); got.Step != "none" {
		t.Fatalf("the updated window offers %+v", got)
	}
	if got := p.reports(); len(got) != 0 {
		t.Fatalf("the updated window told its page %+v", got)
	}
}

// No network is not an answer. Nothing reaches the page -- neither "there is a
// version" nor "there is none" -- and nothing is written down as asked, so the
// next look asks again instead of waiting out a whole interval.
func TestALookThatCannotReachTheReleasesPageSaysNothingAndMarksNothing(t *testing.T) {
	p := &page{}
	w := newWatch(t, &releasesPage{err: offline}, &clock{now: start}, p)

	w.look(context.Background())

	if got := p.reports(); len(got) != 0 {
		t.Fatalf("the page was told %+v about a question that got no answer", got)
	}
	if _, err := os.Stat(w.markPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a question with no answer was written down as asked (%v)", err)
	}
	if got := w.known(); got.Step != "none" {
		t.Fatalf("offline, a loading page would be told %+v", got)
	}
}

func TestWhenTheNetworkReturnsTheNextLookAsks(t *testing.T) {
	src := &releasesPage{err: offline}
	p := &page{}
	c := &clock{now: start}
	w := newWatch(t, src, c, p)

	w.look(context.Background())
	src.set("v0.8.0", nil)
	c.pass(lookEvery)
	w.look(context.Background())

	if src.times() != 2 {
		t.Fatalf("asked %d times, want again at the next look", src.times())
	}
	if got := p.reports(); len(got) != 1 || got[0] != (report{Step: "available", Detail: "v0.8.0"}) {
		t.Fatalf("once the network was back the page was told %+v", got)
	}
}

// A version already found stays found while the network is away: losing the
// network is not a reason to take the button away and say, by its absence,
// that there is nothing to update to.
func TestLosingTheNetworkDoesNotTakeAFoundVersionAway(t *testing.T) {
	src := &releasesPage{offer: "v0.8.0"}
	p := &page{}
	c := &clock{now: start}
	w := newWatch(t, src, c, p)

	w.look(context.Background())
	src.set("", offline)
	c.pass(askEvery)
	w.look(context.Background())

	if got := w.known(); got != (report{Step: "available", Detail: "v0.8.0"}) {
		t.Fatalf("offline, a loading page would be told %+v", got)
	}
	if got := p.reports(); len(got) != 1 {
		t.Fatalf("the page was told %+v, want only the first finding", got)
	}
}

// The page hears "none" when it takes something back -- a release withdrawn,
// say -- and not on every look that finds nothing, which would be a report a
// minute about nothing.
func TestNothingNewerIsToldOnlyWhenItTakesSomethingBack(t *testing.T) {
	src := &releasesPage{offer: ""}
	p := &page{}
	c := &clock{now: start}
	w := newWatch(t, src, c, p)

	w.look(context.Background())
	if got := p.reports(); len(got) != 0 {
		t.Fatalf("finding nothing told the page %+v", got)
	}
	src.set("v0.8.0", nil)
	c.pass(askEvery)
	w.look(context.Background())
	src.set("", nil)
	c.pass(askEvery)
	w.look(context.Background())

	got := p.reports()
	if len(got) != 2 || got[1] != (report{Step: "none"}) {
		t.Fatalf("the page was told %+v, want available and then none", got)
	}
}

// Every way of failing to read the mark means ask. One extra redirect is the
// cheap mistake; a check that quietly stops asking is the expensive one.
func TestAMarkThatCannotBeTrustedDoesNotSilenceTheCheck(t *testing.T) {
	cases := map[string]string{
		"unreadable":                         "whenever",
		"the plain time older windows wrote": start.Add(-time.Minute).Format(time.RFC3339),
		"from the future":                    `{"asked":"` + start.Add(72*time.Hour).Format(time.RFC3339) + `","running":"v0.7.0","newest":""}`,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			src := &releasesPage{}
			w := newWatch(t, src, &clock{now: start}, &page{})
			if err := os.WriteFile(w.markPath, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}

			w.look(context.Background())

			if src.times() != 1 {
				t.Fatalf("a mark %s kept the window from asking", name)
			}
		})
	}
}

func TestTheMarkIsWrittenWhereverItsDirectoryIsMissing(t *testing.T) {
	src := &releasesPage{}
	c := &clock{now: start}
	w := newWatch(t, src, c, &page{})
	w.markPath = filepath.Join(t.TempDir(), "deeper", "last-update-check")

	w.look(context.Background())
	c.pass(time.Minute)
	w.look(context.Background())

	if src.times() != 1 {
		t.Fatal("the question was not written down: every look would ask")
	}
}

// How often, in numbers a person can check against what they need. The
// releases page is asked a few times a day at most, however long the window
// stays open; the local look that decides whether to ask is frequent enough
// that a network coming back is noticed within minutes, and costs no request
// when there is nothing to ask.
// A releases page that answers, but not with a version -- 429 or 503, a
// repository with no release, a tag that is not vX.Y.Z -- is not a network
// that is away: the question reached GitHub and will reach it again. Asking
// it every ten minutes for as long as it keeps refusing is 144 requests a
// day for nothing, so the pause between two such questions grows, up to
// askEvery.
func TestARefusingReleasesPageIsAskedLessAndLessOften(t *testing.T) {
	refusals := map[string]error{
		"503":         errors.New("https://github.com/kroticw/fleetdeck/releases/latest answered 503, not a redirect to the newest release"),
		"no releases": &supervisor.NoReleasesError{URL: "https://github.com/kroticw/fleetdeck"},
		"bad tag":     errors.New(`version "latest" is not vMAJOR.MINOR.PATCH`),
		// A checkout with commits of its own refuses after a git fetch: the
		// same standing refusal, at the cost of a fetch each time.
		"diverged tree": &supervisor.DivergedError{Ahead: 2},
	}
	for name, refusal := range refusals {
		t.Run(name, func(t *testing.T) {
			src := &releasesPage{err: refusal}
			c := &clock{now: start}
			w := newWatch(t, src, c, &page{})

			var asked []time.Time
			for c.Now().Before(start.Add(24 * time.Hour)) {
				before := src.times()
				w.look(context.Background())
				if src.times() != before {
					asked = append(asked, c.Now())
				}
				c.pass(lookEvery)
			}

			if len(asked) > 12 {
				t.Fatalf("a page refusing all day was asked %d times, want at most 12", len(asked))
			}
			for i := 2; i < len(asked); i++ {
				if asked[i].Sub(asked[i-1]) < asked[i-1].Sub(asked[i-2]) {
					t.Fatalf("the pause between questions shrank: %v", asked)
				}
			}
			if last := asked[len(asked)-1].Sub(asked[len(asked)-2]); last < askEvery {
				t.Fatalf("after a day of refusals the pause is %s, want it to reach askEvery (%s)", last, askEvery)
			}
		})
	}
}

// A network that is away is still asked about at every look: the question
// fails before it leaves the machine, and asking is how its coming back is
// noticed.
func TestAnOfflineLaptopStillAsksAtEveryLook(t *testing.T) {
	src := &releasesPage{err: offline}
	c := &clock{now: start}
	w := newWatch(t, src, c, &page{})

	for i := 0; i < 36; i++ {
		w.look(context.Background())
		c.pass(lookEvery)
	}

	if src.times() != 36 {
		t.Fatalf("offline for six hours, asked %d times in 36 looks", src.times())
	}
}

// Once the page answers again, the pause is forgotten: the next refusal
// starts from ten minutes, not from where the last run of them stopped.
func TestAnAnswerEndsTheGrowingPause(t *testing.T) {
	refusal := errors.New("answered 429")
	src := &releasesPage{err: refusal}
	c := &clock{now: start}
	w := newWatch(t, src, c, &page{})

	for c.Now().Before(start.Add(12 * time.Hour)) {
		w.look(context.Background())
		c.pass(lookEvery)
	}
	src.set("", nil)
	c.pass(askEvery)
	w.look(context.Background())
	asked := src.times()

	src.set("", refusal)
	c.pass(askEvery)
	w.look(context.Background())
	c.pass(lookEvery)
	w.look(context.Background())

	if got := src.times() - asked; got != 2 {
		t.Fatalf("after an answer, a refusal and a look ten minutes later asked %d times, want 2", got)
	}
}

// A press is a person asking: it is not held back by the pause, and its
// answer ends it.
func TestAPressIsNotHeldBackByARefusingPage(t *testing.T) {
	src := &releasesPage{err: errors.New("answered 503")}
	c := &clock{now: start}
	p := &page{}
	w := newWatch(t, src, c, p)

	for i := 0; i < 6; i++ {
		w.look(context.Background())
		c.pass(lookEvery)
	}
	before := src.times()
	src.set("v0.8.0", nil)
	w.checkNow(context.Background())

	if src.times() != before+1 {
		t.Fatalf("a press during the pause asked %d times, want 1", src.times()-before)
	}
	if got := w.known(); got != (report{Step: "available", Detail: "v0.8.0"}) {
		t.Fatalf("after the press a loading page would be told %+v", got)
	}
}

func TestItAsksNoMoreOftenThanAPersonNeeds(t *testing.T) {
	if askEvery < 4*time.Hour {
		t.Errorf("askEvery = %s: the releases page would be asked more than six times a day", askEvery)
	}
	if askEvery > 12*time.Hour {
		t.Errorf("askEvery = %s: a release would go unnoticed for most of a working day", askEvery)
	}
	if lookEvery > 15*time.Minute {
		t.Errorf("lookEvery = %s: a network coming back would go unnoticed too long", lookEvery)
	}
	if lookEvery < time.Minute {
		t.Errorf("lookEvery = %s: an offline laptop would retry every few seconds", lookEvery)
	}
}

// Check for Updates… in the app menu is a person asking now, rather than
// waiting out askEvery after a release they know is out.

func TestAPressAsksEvenWhileTheMarkIsFresh(t *testing.T) {
	src := &releasesPage{offer: ""}
	p := &page{}
	c := &clock{now: start}
	w := newWatch(t, src, c, p)
	w.look(context.Background())

	src.set("v0.8.0", nil)
	c.pass(48 * time.Minute)
	w.checkNow(context.Background())

	if src.times() != 2 {
		t.Fatalf("asked %d times, want the press to ask although the mark was %s old", src.times(), 48*time.Minute)
	}
	got := p.reports()
	if len(got) != 2 || got[0] != (report{Step: "checking"}) || got[1] != (report{Step: "available", Detail: "v0.8.0"}) {
		t.Fatalf("the page was told %+v, want checking and then v0.8.0 available", got)
	}
	if got := w.known(); got != (report{Step: "available", Detail: "v0.8.0"}) {
		t.Fatalf("a page loading now would be told %+v", got)
	}
}

// The press answers in words even when there is nothing newer: the person
// asked, and silence would read as a press that did nothing.
func TestAPressThatFindsNothingNewerSaysSo(t *testing.T) {
	p := &page{}
	w := newWatch(t, &releasesPage{offer: ""}, &clock{now: start}, p)

	w.checkNow(context.Background())

	got := p.reports()
	if len(got) != 2 || got[1] != (report{Step: "latest", Detail: "v0.7.0"}) {
		t.Fatalf("the page was told %+v, want that v0.7.0 is the latest", got)
	}
}

// The press asks the same question the window asks by itself, so its answer
// is written down the same way and starts askEvery again: the window asks
// nothing more for six hours after a press.
func TestAPressWritesItsAnswerDownAndTheNextLookUsesIt(t *testing.T) {
	src := &releasesPage{offer: "v0.8.0"}
	c := &clock{now: start}
	w := newWatch(t, src, c, &page{})

	w.checkNow(context.Background())
	c.pass(askEvery - time.Minute)
	w.look(context.Background())

	if src.times() != 1 {
		t.Fatalf("asked %d times, want the look after a press to answer from the mark", src.times())
	}
	m, ok := readMark(w.markPath)
	if !ok || !m.Asked.Equal(start) || m.Running != "v0.7.0" || m.Newest != "v0.8.0" {
		t.Fatalf("the mark after a press is %+v (%v)", m, ok)
	}
}

// Pressed again within a minute of the last question -- the press before, or
// the window's own look -- the releases page is not asked: the answer is a
// minute old at most and is shown again as it is.
func TestAPressWithinAMinuteOfTheLastQuestionAnswersWithoutAsking(t *testing.T) {
	src := &releasesPage{offer: "v0.8.0"}
	p := &page{}
	c := &clock{now: start}
	w := newWatch(t, src, c, p)

	w.checkNow(context.Background())
	c.pass(checkAgainAfter - time.Second)
	w.checkNow(context.Background())

	if src.times() != 1 {
		t.Fatalf("asked %d times within %s, want once", src.times(), checkAgainAfter)
	}
	got := p.reports()
	if len(got) != 4 || got[3] != (report{Step: "available", Detail: "v0.8.0"}) {
		t.Fatalf("the second press told the page %+v, want the answer again", got)
	}

	c.pass(time.Second)
	w.checkNow(context.Background())
	if src.times() != 2 {
		t.Fatalf("asked %d times, want again once %s had passed", src.times(), checkAgainAfter)
	}
}

// A press is a question somebody asked, so a failure is theirs to see, as a
// code the page can put in their language. It is still not an answer: it is
// not written down, and a version already found stays found.
func TestAPressThatCannotReachTheReleasesPageSaysWhyAndMarksNothing(t *testing.T) {
	src := &releasesPage{offer: "v0.8.0"}
	p := &page{}
	c := &clock{now: start}
	w := newWatch(t, src, c, p)
	w.look(context.Background())
	if err := os.Remove(w.markPath); err != nil {
		t.Fatal(err)
	}

	src.set("", offline)
	w.checkNow(context.Background())

	got := p.reports()
	last := got[len(got)-1]
	if last.Step != "check-failed" || last.Reason != reasonOffline || last.Detail != offline.Error() {
		t.Fatalf("the page was told %+v, want check-failed, offline, with the particulars", last)
	}
	if _, err := os.Stat(w.markPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a press with no answer was written down as asked (%v)", err)
	}
	if got := w.known(); got != (report{Step: "available", Detail: "v0.8.0"}) {
		t.Fatalf("after a failed press a loading page would be told %+v", got)
	}
}

// Pressing again and again while offline does not knock on the network each
// time either: within a minute the failure is shown again as it was.
func TestAPressWithinAMinuteOfAFailedPressShowsTheFailureWithoutAsking(t *testing.T) {
	src := &releasesPage{err: offline}
	p := &page{}
	c := &clock{now: start}
	w := newWatch(t, src, c, p)

	w.checkNow(context.Background())
	c.pass(30 * time.Second)
	w.checkNow(context.Background())

	if src.times() != 1 {
		t.Fatalf("asked %d times within %s of a failed press, want once", src.times(), checkAgainAfter)
	}
	got := p.reports()
	if len(got) != 4 || got[3].Step != "check-failed" || got[3].Reason != reasonOffline {
		t.Fatalf("the second press told the page %+v", got)
	}

	c.pass(checkAgainAfter)
	src.set("", nil)
	w.checkNow(context.Background())
	if src.times() != 2 {
		t.Fatalf("asked %d times, want again a minute after the failure", src.times())
	}
}

// The press and the window's own look never ask at the same time: the second
// question would only repeat the first one's answer.
func TestAPressDuringALookTakesTheLooksAnswer(t *testing.T) {
	src := &heldPage{releasesPage: releasesPage{offer: "v0.8.0"}, entered: make(chan struct{}), release: make(chan struct{})}
	p := &page{}
	w := newWatch(t, src, &clock{now: start}, p)

	looked := make(chan struct{})
	go func() {
		w.look(context.Background())
		close(looked)
	}()
	<-src.entered
	pressed := make(chan struct{})
	go func() {
		w.checkNow(context.Background())
		close(pressed)
	}()
	close(src.release)
	<-looked
	<-pressed

	if n := src.times(); n != 1 {
		t.Fatalf("asked %d times, want the press to take the look's answer", n)
	}
	got := p.reports()
	if last := got[len(got)-1]; last != (report{Step: "available", Detail: "v0.8.0"}) {
		t.Fatalf("the press ended with %+v", last)
	}
}

func TestAPressAsksNoMoreOftenThanOnceAMinute(t *testing.T) {
	if checkAgainAfter != time.Minute {
		t.Fatalf("checkAgainAfter = %s, the operator decided on a minute", checkAgainAfter)
	}
}

// heldPage holds the first question until it is released, so that a test can
// press while a look is asking.
type heldPage struct {
	releasesPage
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (p *heldPage) Check(ctx context.Context) (string, error) {
	p.once.Do(func() {
		close(p.entered)
		<-p.release
	})
	return p.releasesPage.Check(ctx)
}
