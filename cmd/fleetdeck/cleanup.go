package main

import (
	"context"
	"fmt"
	"time"

	"github.com/kroticw/fleetdeck/internal/board"
	"github.com/kroticw/fleetdeck/internal/config"
	"github.com/kroticw/fleetdeck/internal/daemon"
	"github.com/kroticw/fleetdeck/internal/jobs"
	"github.com/kroticw/fleetdeck/internal/orchestrator"
	"github.com/kroticw/fleetdeck/internal/transcript"
)

// archiveWords bounds what goes into the archive entry. The entry is one row of
// a markdown table a person reads on a board, and a session's closing answer is
// a paragraph or two; past that it is a transcript, and the entry names where
// that is instead of copying it.
const archiveWords = 1500

// cleanupDeps is everything tidying a session away reaches outside for. Every
// entry is a value or a function, so the whole sequence is testable against
// three temporary directories and no daemon, no claude and no board repository.
type cleanupDeps struct {
	// jobStore is Claude Code's job store, where a short id is turned into the
	// session's transcript id and its name; projects is where that transcript
	// is; board is the board whose archive records it.
	jobStore string
	projects string
	board    string

	list func(ctx context.Context) ([]daemon.Session, error)
	stop func(ctx context.Context, short string) error
	now  func() time.Time
}

// fleetCleaner is how a fleet tidies away the session behind a card the
// operator has accepted (internal/orchestrator.Cleaner), or nil when it must
// not: a fleet with no board has nowhere to record the session it would put
// out, and a panel that stops no sessions (sessionStopper) has nothing to put
// it out with. Nil leaves a card moved into done with its session running.
func fleetCleaner(o runOpts, agent config.AgentConfig, boardDir string, dc *daemon.Client, projects string) *orchestrator.Cleaner {
	stop := sessionStopper(o, agent)
	if boardDir == "" || stop == nil {
		return nil
	}
	return newCleaner(cleanupDeps{
		jobStore: jobStoreDir,
		projects: projects,
		board:    boardDir,
		list:     dc.ListSessions,
		stop:     stop,
		now:      time.Now,
	})
}

// newCleaner is the cleanup an accepted card starts: the pin, the session's own
// words, the board's archive, and then the session itself. The order and the
// rule about it are internal/orchestrator.Cleaner's; what is here is only how
// each of its four reaches is answered on this machine.
func newCleaner(d cleanupDeps) *orchestrator.Cleaner {
	return &orchestrator.Cleaner{
		Unpin: func(short string) error { return jobs.Unpin(d.jobStore, short) },
		Words: func(short string) (orchestrator.Said, error) { return d.said(short) },
		Archive: func(a orchestrator.Accepted, said orchestrator.Said) error {
			return d.archive(a, said)
		},
		Stop: d.stop,
		List: d.list,
	}
}

// said reads what the session leaves behind. Every failure here keeps the
// session running (see the Cleaner), so nothing is guessed at: a store that
// does not know the session, a transcript that cannot be found and a transcript
// with nothing in it are each reported as themselves.
func (d cleanupDeps) said(short string) (orchestrator.Said, error) {
	rec, err := jobRecord(d.jobStore, short)
	if err != nil {
		return orchestrator.Said{}, err
	}
	// The store knows a session by its short id and the transcript by the
	// session's own UUID; only the store joins the two.
	path, err := transcript.Locate(d.projects, rec.SessionID)
	if err != nil {
		return orchestrator.Said{}, fmt.Errorf("%s: %w", short, err)
	}
	words, err := transcript.LastWords(path, archiveWords)
	if err != nil {
		return orchestrator.Said{}, err
	}
	return orchestrator.Said{Words: words, Transcript: path, Name: rec.Name}, nil
}

// archive records the session in the board's archive.
//
// The row is written and not committed, unlike the card writes beside it. A
// card write is the operator's edit and answers for its own commit on the spot
// (setCardField); this row is part of a cleanup, and the board's own rule is
// that the accumulated edits go in one commit when the fleet is tidied up
// (plugin/templates/board/README.md). A commit here would also have nowhere to
// report a failure to: it cannot fail the step, because the row is already on
// disk and nothing the session knew is lost — which is the only question the
// step's failure answers.
func (d cleanupDeps) archive(a orchestrator.Accepted, said orchestrator.Said) error {
	_, err := board.AppendArchive(d.board, board.ArchiveEntry{
		When:       d.now(),
		Session:    a.Session,
		Name:       said.Name,
		Card:       a.Card,
		Words:      said.Words,
		Transcript: said.Transcript,
	})
	return err
}

// jobRecord is the store's record for short. findRecord answers the same
// question for a resume and wraps its refusal in "cannot be resumed", which is
// not what a cleanup is about: here a session the store never heard of means
// there is no transcript to read, and that is what has to reach the operator.
func jobRecord(dir, short string) (jobs.Record, error) {
	if dir == "" {
		return jobs.Record{}, fmt.Errorf("this panel cannot find Claude Code's job store, so it cannot read what %s said", short)
	}
	records, err := jobs.Load(dir)
	if err != nil {
		return jobs.Record{}, fmt.Errorf("read the job store: %w", err)
	}
	for _, r := range records {
		if r.Short == short {
			return r, nil
		}
	}
	return jobs.Record{}, fmt.Errorf("the job store has no session %s, so there is no transcript of it to read", short)
}
