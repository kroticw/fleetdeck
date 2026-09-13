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
