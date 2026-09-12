//go:build darwin

package main

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"time"

	webview "github.com/webview/webview_go"

	"github.com/kroticw/fleetdeck/internal/supervisor"
)

// How often the window asks, without being asked to, whether there is a newer
// version: once a day.
//
// The operator settled this on 2026-09-12, and the shape matters as much as
// the number. This is not a poll: there is no timer while the window runs, and
// nothing repeats. It is one question at startup, at most once a day, and it
// is silent unless the answer is yes -- opening and closing the window ten
// times in an afternoon asks GitHub once. Anybody who wants to ask sooner
// presses the button, which now exists in every build.
const askEvery = 24 * time.Hour

// startCheckTimeout bounds the question. Nothing waits on it -- it runs beside
// the window, not in front of it -- but a request with no bound is a goroutine
// that outlives the reason it was started.
const startCheckTimeout = 30 * time.Second

// askedMarkPath is where the time of the last question is kept: beside the
// configuration, which is the one directory this program already owns in a
// person's home.
func askedMarkPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "fleetdeck", "last-update-check"), nil
}

// askingIsDue says whether enough time has passed since the last question.
//
// Every way of failing to read the mark answers yes. One extra question is the
// cheap mistake; a check that silently stops asking is the expensive one, and
// this whole change is about what silence costs.
func askingIsDue(markPath string, now time.Time) bool {
	data, err := os.ReadFile(markPath)
	if err != nil {
		return true
	}
	asked, err := time.Parse(time.RFC3339, string(data))
	if err != nil {
		return true
	}
	// A mark dated ahead of now -- a clock that was wrong, a home directory
	// copied from another machine -- would otherwise put the next question off
	// until that date arrived.
	if asked.After(now) {
		return true
	}
	return now.Sub(asked) >= askEvery
}

// markAsked records that the question was asked at now.
func markAsked(markPath string, now time.Time) {
	if err := os.MkdirAll(filepath.Dir(markPath), 0o755); err != nil {
		log.Printf("fleetdeck-window: cannot keep the time of the update check: %v", err)
		return
	}
	if err := os.WriteFile(markPath, []byte(now.Format(time.RFC3339)), 0o600); err != nil {
		log.Printf("fleetdeck-window: cannot keep the time of the update check: %v", err)
	}
}

// askAtStart is the whole startup question, from deciding whether to ask to
// telling the page. It runs beside the window rather than in front of it: a
// window must open whatever GitHub is doing.
func askAtStart(w webview.WebView, source supervisor.Source) {
	markPath, err := askedMarkPath()
	if err != nil {
		log.Printf("fleetdeck-window: no home directory to keep the update check's time in: %v", err)
		return
	}
	now := time.Now()
	if !askingIsDue(markPath, now) {
		return
	}
	markAsked(markPath, now)

	ctx, cancel := context.WithTimeout(context.Background(), startCheckTimeout)
	defer cancel()
	if r := checkAtStart(ctx, source); r != nil {
		tell(w, *r)
	}
}

// checkAtStart asks the source what is newest and returns what the page should
// be told, or nil for nothing.
//
// It is silent about everything but a version that is actually there. A person
// did not ask this question, so a refusal is not an answer they are owed:
// being told "GitHub could not be reached" every time a laptop opens on a
// train is noise, and noise is what gets a notice ignored. Pressing the button
// asks the question out loud, and then every refusal is shown.
func checkAtStart(ctx context.Context, source supervisor.Source) *report {
	available, err := source.Check(ctx)
	if err != nil {
		log.Printf("fleetdeck-window: the update check at startup did not finish: %v", err)
		return nil
	}
	if available == "" {
		return nil
	}
	log.Printf("fleetdeck-window: %s is available", available)
	return &report{Step: "available", Detail: available}
}
