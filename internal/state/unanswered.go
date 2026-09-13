package state

import (
	"time"

	"github.com/kroticw/fleetdeck/internal/daemon"
)

// UnansweredLimit is how long a session may owe its next move, saying nothing, before
// the daemon's "no question outstanding" stops counting as an answer.
//
// Why a limit exists at all. The daemon's words about a session come from the session.
// A session frozen inside a tool call -- ssh asking about a host key nobody will see, a
// network call with no timeout, an MCP call that never returns -- cannot produce any,
// and the daemon goes on relaying the last thing it had: needs "", state working or
// blocked, tempo active, measured on a live frozen session on 2026-09-12. From outside,
// frozen and slow are the same; what can be established is that the session has owed
// its move, silent, for this long, and could not have told anyone if it needed a person.
//
// Why "owes its move" and not "is inside a call". Claude Code does not put every open
// call on disk: on 2.1.269 a Read -- alone, or beside a Bash -- reached the transcript
// only together with its result, so a session frozen inside one shows no call at all,
// only a file ending at its previous result. What the file always shows is a session
// that owes its next move (see internal/transcript.Voice.Unanswered).
//
// The number is measured, not reasoned. Over the fourteen days to 2026-09-13, every
// transcript on this machine was replayed under that write behaviour -- an assistant
// message with two or more calls lands only when its last result does -- and every
// stretch a session looked, on disk, like it owed its move was measured until its next
// own line appeared (AskUserQuestion left out: a question to a person reaches the panel
// in words):
//
//   - 24 430 stretches ending in the model's own next line: p99.99 338 seconds.
//   - 3 744 ending in a withheld batch: longest from a working session 651 seconds, a
//     Bash that ran into its 600-second ceiling plus the model's time before it.
//
// The worst those parts add up to is a Bash at its 601-second ceiling after 338 seconds
// of the model: 939 seconds. Sixteen minutes clears it, and still comes fourteen
// minutes ahead of the thirty-minute silence notification.
const UnansweredLimit = 16 * time.Minute

// LeftUnanswered reports whether the session has owed its move, saying nothing, for at
// least UnansweredLimit. A zero UnansweredFor means it owes nothing, or that nothing
// was measured, and never counts.
func (s SessionView) LeftUnanswered() bool {
	return s.UnansweredFor > 0 && s.UnansweredFor >= UnansweredLimit
}

// Waiting is daemon.Session.Waiting with the one case the daemon cannot see.
//
// The daemon's "no" (needs present and empty) is words, and words come from the
// session. A session left unanswered past UnansweredLimit has not been able to update
// them, so its "no" is not an answer it could have changed -- and the verdict is the one
// docs/protocol/daemon-control-socket.md section 5 gives a source that says nothing: not
// known. Never "yes": nobody established that a person is needed, only that the session
// could not say.
//
// Everything else stands exactly as the daemon's rule has it: words that say something
// decide on their own, a dying session is no, and a session that owes nothing keeps the
// daemon's answer however long it is quiet -- that silence is the silence rule's to
// report.
func (s SessionView) Waiting() daemon.Verdict {
	v := s.Session.Waiting()
	if v == daemon.No && !s.Dying && s.Needs != nil && *s.Needs == "" && s.LeftUnanswered() {
		return daemon.Unknown
	}
	return v
}
