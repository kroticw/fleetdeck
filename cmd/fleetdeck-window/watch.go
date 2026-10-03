//go:build darwin

package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/kroticw/fleetdeck/internal/supervisor"
)

// The update button is on screen only while there is something to update to
// (decided 2026-09-13): its appearing is the notice that a newer version is
// out. A button that stood there always meant "press and I will go and look",
// was pressed for nothing, and changed nothing on the day a release came out.
//
// So the window finds out by itself, while it runs. It replaced one question
// at startup, at most once a day: a window left open for a week under that
// rule never learned of anything.
//
// askEvery is how old an answer may get before the releases page is asked
// again: a release published in the morning is on screen by the afternoon,
// and the page is asked at most four times a day however long the window
// stays open. One question is one HEAD request answered with a redirect, not
// an API call (docs/engineering/update-from-release.md, section 1).
//
// lookEvery is how often the window looks at whether it is time to ask. The
// look is local: it reads the mark on disk and asks nothing while the answer
// there is fresh. Deciding by the wall clock and the mark, rather than by a
// timer set for askEvery, is deliberate -- a Mac's monotonic clock stops while
// it sleeps, so a six-hour timer on a laptop closed overnight fires hours
// late. It is also how a network coming back is noticed: a question that got
// no answer is not written down, so the next look asks again.
const (
	askEvery  = 6 * time.Hour
	lookEvery = 10 * time.Minute
)

// checkAgainAfter is how soon after the last question a press of Check for
// Updates… asks again (decided by the operator, 2026-10-02). Within it the
// press is answered with what that question found: a person pressing twice,
// or pressing while offline, does not knock on the releases page each time.
const checkAgainAfter = time.Minute

// askTimeout bounds one question. Nothing waits on it -- looking runs beside
// the window, not in front of it -- but a request with no bound is a goroutine
// that outlives the reason it was started.
const askTimeout = 30 * time.Second

// askedMarkPath is where the last answer is kept: beside the configuration,
// which is the one directory this program already owns in a person's home.
func askedMarkPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "fleetdeck", "last-update-check"), nil
}

// mark is the last answer the releases page (or the tree's remote) gave: when,
// to which running version, and what it offered -- "" for nothing newer.
//
// The answer is kept, not only the time of the question. A window opened
// again an hour after the last question does not ask, and without the answer
// it would show no button for the rest of askEvery: a false "nothing newer".
type mark struct {
	Asked   time.Time `json:"asked"`
	Running string    `json:"running"`
	Newest  string    `json:"newest"`
}

// readMark reads the mark, and says whether there was a readable one. A mark
// in the plain time older windows wrote does not read, which is the right
// answer: it holds no answer to show.
func readMark(path string) (mark, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return mark{}, false
	}
	var m mark
	if err := json.Unmarshal(data, &m); err != nil {
		return mark{}, false
	}
	return m, true
}

// answers says whether the mark still answers the question for this running
// version at now.
//
// Every doubt means ask. One extra redirect is the cheap mistake; a check that
// quietly stops asking is the expensive one. A mark dated ahead of now -- a
// clock that was wrong, a home directory copied from another machine -- would
// otherwise put the next question off until that date. A mark about another
// running version is an answer about somebody else: after an update, the
// version it offered is the one running, and using it would put the button
// back for an update that has already happened.
func (m mark) answers(running string, now time.Time) bool {
	if m.Running != running || m.Asked.After(now) {
		return false
	}
	return now.Sub(m.Asked) < askEvery
}

// answersAPress says whether the mark is recent enough to answer a press of
// Check for Updates… at now without asking again.
func (m mark) answersAPress(running string, now time.Time) bool {
	return m.answers(running, now) && now.Sub(m.Asked) < checkAgainAfter
}

func writeMark(path string, m mark) {
	data, err := json.Marshal(m)
	if err != nil {
		log.Printf("fleetdeck-window: cannot keep the answer about a newer version: %v", err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		log.Printf("fleetdeck-window: cannot keep the answer about a newer version: %v", err)
		return
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		log.Printf("fleetdeck-window: cannot keep the answer about a newer version: %v", err)
	}
}

// updateWatch is the window finding out, by itself, whether there is a newer
// version, and telling the page when what it knows changes.
type updateWatch struct {
	source   supervisor.Source
	running  string
	markPath string
	tell     func(report)
	now      func() time.Time

	// asking is held for one question, from reading the mark to writing it, so
	// that a press and a look never ask at the same time: the second would
	// only repeat the first one's answer.
	asking sync.Mutex

	mu     sync.Mutex
	newest string
	// failed is the last press that got no answer, and when; within
	// checkAgainAfter of it a press is shown it again rather than asking.
	failed   report
	failedAt time.Time
}

// run looks once at once, then once for every tick, until ctx ends.
func (u *updateWatch) run(ctx context.Context, ticks <-chan time.Time) {
	u.look(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticks:
			u.look(ctx)
		}
	}
}

// look asks the source what is newest, unless the mark already answers that.
//
// A question that gets no answer changes nothing: the page is told neither
// that there is a version nor that there is none, a version already found
// stays found, and nothing is written down, so the next look asks again. No
// network is not an answer, and a person did not ask this question, so a
// refusal is not something they are owed on screen either -- being told
// "GitHub could not be reached" every time a laptop opens on a train is
// noise. Pressing the button, once there is one, shows every refusal.
func (u *updateWatch) look(ctx context.Context) {
	u.asking.Lock()
	defer u.asking.Unlock()
	now := u.now()
	if m, ok := readMark(u.markPath); ok && m.answers(u.running, now) {
		u.learn(m.Newest)
		return
	}
	ctx, cancel := context.WithTimeout(ctx, askTimeout)
	defer cancel()
	newest, err := u.source.Check(ctx)
	if err != nil {
		log.Printf("fleetdeck-window: looking for a newer version got no answer, asking again within %s: %v", lookEvery, err)
		return
	}
	writeMark(u.markPath, mark{Asked: now, Running: u.running, Newest: newest})
	u.learn(newest)
}

// checkNow is a press of Check for Updates… in the app menu: the releases page
// is asked now, whatever the mark says, so a release published an hour after
// the last look need not wait out askEvery.
//
// It is the same question a look asks, and its answer is written down the
// same way, so askEvery starts again from the press. What differs is who
// asked: a person did, so every outcome is said -- that it is being checked,
// what was found, that nothing newer is out, or why there was no answer. A
// question with no answer is still not written down and does not take a
// found version away.
//
// Within checkAgainAfter of the last question -- a press or a look -- the
// press is answered with what that question found, and nothing is asked.
func (u *updateWatch) checkNow(ctx context.Context) {
	u.tell(report{Step: "checking"})
	u.asking.Lock()
	defer u.asking.Unlock()
	now := u.now()
	if m, ok := readMark(u.markPath); ok && m.answersAPress(u.running, now) {
		u.answer(m.Newest)
		return
	}
	u.mu.Lock()
	failed, failedAt := u.failed, u.failedAt
	u.mu.Unlock()
	if failed.Step != "" && !failedAt.After(now) && now.Sub(failedAt) < checkAgainAfter {
		u.tell(failed)
		return
	}
	ctx, cancel := context.WithTimeout(ctx, askTimeout)
	defer cancel()
	newest, err := u.source.Check(ctx)
	if err != nil {
		log.Printf("fleetdeck-window: checking for a newer version, as asked, got no answer: %v", err)
		r := report{Step: "check-failed", Reason: reasonOf(err), Detail: err.Error()}
		u.mu.Lock()
		u.failed, u.failedAt = r, now
		u.mu.Unlock()
		u.tell(r)
		return
	}
	writeMark(u.markPath, mark{Asked: now, Running: u.running, Newest: newest})
	u.answer(newest)
}

// answer tells the page what a press found, whether or not it changes what
// the page shows.
func (u *updateWatch) answer(newest string) {
	u.mu.Lock()
	u.newest = newest
	u.failed = report{}
	u.mu.Unlock()
	if newest == "" {
		log.Printf("fleetdeck-window: checked as asked: nothing newer than %s", u.running)
		u.tell(report{Step: "latest", Detail: u.running})
		return
	}
	log.Printf("fleetdeck-window: checked as asked: %s is available", newest)
	u.tell(report{Step: "available", Detail: newest})
}

// learn takes what is newest, and tells the page only when that changes what
// it shows: "available" when a version appears, "none" when one is taken back.
func (u *updateWatch) learn(newest string) {
	u.mu.Lock()
	changed := newest != u.newest
	u.newest = newest
	u.mu.Unlock()
	if !changed {
		return
	}
	if newest == "" {
		log.Printf("fleetdeck-window: nothing newer than %s any more", u.running)
		u.tell(report{Step: "none"})
		return
	}
	log.Printf("fleetdeck-window: %s is available", newest)
	u.tell(report{Step: "available", Detail: newest})
}

// known is what a page that has just loaded is told: it missed every report
// sent before it was there to take one.
func (u *updateWatch) known() report {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.newest == "" {
		return report{Step: "none"}
	}
	return report{Step: "available", Detail: u.newest}
}
