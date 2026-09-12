package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kroticw/fleetdeck/internal/daemon"
	"github.com/kroticw/fleetdeck/internal/jobs"
	"github.com/kroticw/fleetdeck/internal/server"
	"github.com/kroticw/fleetdeck/internal/transcript"
)

// Resuming a stopped session, from the panel's side: everything that has to be
// known before the daemon is asked.
//
// The daemon is not the one that knows how to resume a session it is no longer
// running — it has no record of one at all (internal/jobs' package doc). What
// it needs comes off disk here, and so does the answer to whether the resume
// can work, which is the whole reason this is a pre-flight and not a relay.
//
// Three things are checked before anything is dispatched, and each of them is a
// resume that would otherwise start a worker doomed to die at startup:
//
//   - an id to resume by, and a working directory that still exists — the
//     rule internal/jobs already applies to decide the session is stopped
//     rather than dead, re-applied here because the store is read again and
//     could have changed under a panel that has been open for hours;
//   - a transcript. This is the one the card was opened for: a session that
//     was never prompted has a job record, reads as resumable to every rule
//     anyone has written, and dies with "resumed worker crashed during
//     startup: exit 1" because there is no conversation to replay. It is
//     knowable here, off a file that either exists or does not;
//   - not already running, which the daemon would refuse anyway but in words
//     about a duplicate job rather than about this session.
//
// claude-agents-mcp deliberately dispatches the no-transcript case and lets it
// crash. That is the right call there — it is the CLI's own tooling and must
// not get ahead of what the CLI will one day support. It is the wrong call for
// a button: the operator gets thirty seconds of "resuming…" and then a crash
// report about something that was knowable before the press, and a dead worker
// is left behind that this panel has no way to clean up.

// resumeDeps is what resuming needs from the rest of the program. Every entry
// is a value or a function so the whole pre-flight is testable against two
// temporary directories and no daemon at all.
type resumeDeps struct {
	// jobStore is Claude Code's job store; projects is where it keeps
	// transcripts.
	jobStore string
	projects string

	// listed answers what the daemon is running right now. Its failure is not
	// fatal: see the already-running check below.
	listed func(context.Context) ([]daemon.Session, error)

	// resume performs the dispatch and does not return until the session is
	// up or certainly not coming.
	resume func(context.Context, daemon.ResumeSpec) error
}

// resumeSession returns the function internal/server calls for
// POST /api/sessions/{short}/resume.
//
// A refusal that is about the session wraps server.ErrSessionNotResumable, so
// the route can answer it apart from a failure of the attempt; everything else
// is relayed as it arrived, in the daemon's own words.
func resumeSession(d resumeDeps) func(string) error {
	return func(short string) error {
		if short == "" {
			return fmt.Errorf("%w: no session named", server.ErrSessionNotResumable)
		}
		// Background, not the request's context, deliberately. A resume that
		// has been dispatched is a process starting: cancelling the wait
		// because the operator closed the tab would not stop the worker, it
		// would only stop anyone watching whether it came up — and a browser
		// that gives up on a slow request would abandon a resume halfway
		// through the one window in which it can be reported on.
		ctx := context.Background()

		record, err := findRecord(d.jobStore, short)
		if err != nil {
			return err
		}

		// Re-checked rather than trusted from the snapshot the browser was
		// looking at: a panel left open overnight offers a button built from
		// a reading that is hours old, and the directory it is about can
		// have been deleted in between.
		resumeID := record.ResumeID()
		if resumeID == "" {
			return fmt.Errorf("%w: the job store has no session id for %s to resume by", server.ErrSessionNotResumable, short)
		}
		if !record.Resumable {
			return fmt.Errorf("%w: its working directory no longer exists (%s)", server.ErrSessionNotResumable, record.CWD)
		}

		// The daemon being unreachable fails this check open. The resume
		// itself is about to fail loudly in that case anyway, and refusing
		// here would report a session as unresumable when the only thing
		// wrong is that nothing answered — two different facts that must
		// not be told to the operator as one.
		if d.listed != nil {
			if sessions, err := d.listed(ctx); err == nil && isListed(sessions, short) {
				return fmt.Errorf("%w: %s is already running", server.ErrSessionNotResumable, short)
			}
		}

		path := locateTranscript(d.projects, record, resumeID)
		if path == "" {
			return fmt.Errorf("%w: %s has no transcript to resume from — it was never prompted, so there is no conversation to bring back", server.ErrSessionNotResumable, short)
		}

		return d.resume(ctx, daemon.ResumeSpec{
			Short:          record.Short,
			SessionID:      record.SessionID,
			ResumeID:       resumeID,
			CWD:            record.CWD,
			Name:           record.Name,
			Intent:         record.Intent,
			Flags:          record.RespawnFlags,
			TranscriptPath: path,
		})
	}
}

// findRecord reads the one record this resume is about.
//
// The whole store is read and the one record picked out, rather than the
// record's own file being read directly: Load is where the store's shape and
// its resumability rule live, and a second reader of the same directory here
// would be a second place for both to be got wrong.
func findRecord(dir, short string) (jobs.Record, error) {
	if dir == "" {
		return jobs.Record{}, errors.New("this panel cannot find Claude Code's job store, so it cannot resume anything")
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
	return jobs.Record{}, fmt.Errorf("%w: the job store has no session %s", server.ErrSessionNotResumable, short)
}

func isListed(sessions []daemon.Session, short string) bool {
	for _, s := range sessions {
		if s.Short == short && !s.Dying {
			return true
		}
	}
	return false
}

// locateTranscript finds the conversation the resumed worker will replay, or
// returns empty when there is none.
//
// The store's own note of where it last saw the transcript is tried first and
// only counts when it still names this session and still exists: it is written
// when the session runs and goes stale when a project directory is renamed,
// and a stale hint followed blindly points the worker at nothing. Otherwise
// the ordinary search under ~/.claude/projects answers, which is also what the
// worker itself would fall back to.
//
// It is the resume id that names the transcript, not the session id. They are
// the same on an ordinary session and differ on one resumed from another,
// where the conversation to replay is the one the resume id names.
func locateTranscript(projectsDir string, r jobs.Record, resumeID string) string {
	if hint := r.LinkScanPath; hint != "" && filepath.Base(hint) == resumeID+".jsonl" {
		if info, err := os.Stat(hint); err == nil && !info.IsDir() {
			return hint
		}
	}
	path, err := transcript.Locate(projectsDir, resumeID)
	if err != nil {
		return ""
	}
	return path
}
