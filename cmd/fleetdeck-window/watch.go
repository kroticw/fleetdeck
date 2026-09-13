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

	mu     sync.Mutex
	newest string
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
