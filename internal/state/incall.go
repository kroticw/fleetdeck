package state

import (
	"time"

	"github.com/kroticw/fleetdeck/internal/daemon"
)

// CallSilenceLimit is how long a session may stand inside one tool call, saying nothing,
// before the daemon's "no question outstanding" stops counting as an answer.
//
// Why a limit exists at all. The daemon's words about a session come from the session.
// A session frozen inside a tool call -- ssh asking about a host key nobody will see, a
// network call with no timeout, an MCP call that never returns -- cannot produce any,
// and the daemon goes on relaying the last thing it had: needs "", state working, tempo
// active, measured on a live frozen session on 2026-09-12. From outside, frozen and slow
// are the same; what can be established is the fact that the session has been inside
// one call, silent, for this long, and could not have told anyone if it needed a person.
//
// The number is measured, not reasoned. Over the fourteen days to 2026-09-12, 26 851
// tool calls on this machine (AskUserQuestion left out, since a question to a person
// reaches the panel in words; duplicates by tool_use id removed), measured as the
// longest stretch inside a call with nothing said by the session or its subagent:
//
//   - Bash, 18 215 calls: at most 601 seconds. Claude Code moves a command to the
//     background when its timeout runs out and returns control, and the timeout cannot
//     exceed ten minutes, so this is a ceiling rather than a tail.
//   - Read at most 265 seconds; Skill at most 39; Agent, 134 calls, every one linked to
//     its subagent's transcript, at most 8.
//   - One call went past eleven minutes: an EnterWorktree that stood 29m46s on
//     2026-09-11 and came back refused -- by all appearances a permission prompt, which
//     the daemon relays as words, and words decide before this limit is consulted.
//
// So eleven minutes is one minute clear of the only legitimate long silence there is,
// and nearly twenty minutes ahead of the thirty-minute silence notification.
//
// The thirty days before that also held forked skills silent for up to 14.5 minutes and
// one Agent call for 24.5, all on builds from August that did not link a subagent to the
// call that started it. On such a build this limit would mark those calls unknown, which
// is the cheap side of the trade: "not known" calls nobody and says only what is true.
const CallSilenceLimit = 11 * time.Minute

// SilencedInCall reports whether the session has stood inside one tool call, saying
// nothing, for at least CallSilenceLimit. A zero SilentFor is "not measured" and never
// counts.
func (s SessionView) SilencedInCall() bool {
	return s.InCall != nil && s.SilentFor > 0 && s.SilentFor >= CallSilenceLimit
}

// Waiting is daemon.Session.Waiting with the one case the daemon cannot see.
//
// The daemon's "no" (needs present and empty) is words, and words come from the
// session. A session silenced inside a call has not been able to update them since the
// call began, so after CallSilenceLimit the "no" is not an answer it could have changed
// -- and the verdict is the one docs/protocol/daemon-control-socket.md section 5 gives a
// source that says nothing: not known. Never "yes": nobody established that a person is
// needed, only that the session could not say.
//
// Everything else stands exactly as the daemon's rule has it: words that say something
// decide on their own, a dying session is no, and a session outside any call keeps the
// daemon's answer however long it is quiet -- that silence is the silence rule's to
// report.
func (s SessionView) Waiting() daemon.Verdict {
	v := s.Session.Waiting()
	if v == daemon.No && !s.Dying && s.Needs != nil && *s.Needs == "" && s.SilencedInCall() {
		return daemon.Unknown
	}
	return v
}
