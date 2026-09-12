// Whether a session is waiting for a person, or merely stalled.
//
// This is the browser's restatement of internal/daemon/types.go --
// Session.Waiting(), Session.Stalled() and the closed stalledNeedsPrefixes
// vocabulary they read. Go remains the source of truth; this file has to say
// the same thing, and the fixtures in web/js/_tests/header.test.js mirror the
// Go test cases so it cannot drift from it quietly.
//
// It exists as its own module because the restatement used to be written out
// twice, in sessions.js and header.js, as two independent copies of the same
// list and the same three functions. Copies of a rule that decides whether a
// person gets called are the worst kind to keep: they diverge silently, each
// side looks right on its own, and the panel ends up counting one thing in the
// header and showing another in the list. The copies have already had to be
// re-synchronised by hand once. There is one of them now.
//
// See docs/protocol/daemon-control-socket.md section 5 for the rule itself and
// for why the vocabulary below is closed on purpose.

// The closed "no person needed" vocabulary, copied verbatim (case-sensitive
// prefix match, exact order) from daemon.stalledNeedsPrefixes in
// internal/daemon/types.go. Deliberately narrow: an unrecognised prefix must
// fall through to waiting, never to stalled, because calling someone
// unnecessarily gets noticed and corrected while staying silent about someone
// who is actually waiting never does.
export const STALLED_NEEDS_PREFIXES = [
  "usage limit reached",
  "login required",
  "API error",
  "API overloaded",
  "API unavailable",
  "invalid API request",
  "rate limited",
];

export function isStalledNeeds(needs) {
  return STALLED_NEEDS_PREFIXES.some((prefix) => needs.startsWith(prefix));
}

// The three answers `waiting` can give, mirroring daemon.Verdict in Go. Strings rather
// than booleans-plus-null on purpose: every one of them is truthy, so a call site that
// forgets to compare gets an obvious bug on its first render rather than a quiet one
// where "no" reads as yes.
export const WAITING_YES = "yes";
export const WAITING_NO = "no";
export const WAITING_UNKNOWN = "unknown";

// waiting: does a person have to answer before this session can move -- yes, no, or
// "the source never said"? needs is the only signal; never state/tempo, which are set
// by a mechanism the session does not control and cannot tell "waiting on a person"
// apart from "waiting on its own subagents" (spec 3.1).
//
// The third answer is the point of the function. A source with no `needs` field at all
// leaves the key absent (the protocol's own rule for a field that was never set), and
// the panel has to draw that as a question it cannot answer rather than as "nobody is
// waiting". Announcing "not waiting" about a session nobody looked at is the one error
// here that never gets corrected: the person waiting does not know they were dropped,
// and the screen looks calm. See internal/daemon/types.go, Session.Waiting.
//
// Absent and empty are different facts, and this is where the difference is read:
// undefined or null means the source said nothing; "" means the daemon looked and has
// no question outstanding, which is an answer of no.
export function waiting(s) {
  if (s.dying) return WAITING_NO;
  if (s.needs === undefined || s.needs === null) return WAITING_UNKNOWN;
  if (s.needs === "") return WAITING_NO;
  if (isStalledNeeds(s.needs)) return WAITING_NO;
  return WAITING_YES;
}

// isWaiting is `waiting` narrowed to a plain predicate: true only for a definite yes.
// Counters, filters and sorts want a boolean, and they all want the same one -- an
// unknown session must not be counted among those a person has to answer, because
// nobody established that it is. It must not be counted as answered either, which is
// what isWaitingUnknown below is for.
export function isWaiting(s) {
  return waiting(s) === WAITING_YES;
}

// isWaitingUnknown is the other half, for whatever draws the row: the state that used
// to be indistinguishable from "not waiting" and now has to look different from it.
export function isWaitingUnknown(s) {
  return waiting(s) === WAITING_UNKNOWN;
}

// stalled: stopped for a reason no answer fixes, or stopped with no words at all.
// Order matters: needs decides first; the state/tempo flags are only consulted when
// needs has nothing to say (spec 3.1). This mirrors daemon.Session.Stalled() exactly --
// timeless, per-snapshot -- and stays that way for the sake of that mirror; header.js's
// counter does not use it directly, see isNeedsStalled, isFlagOnlyStalled and
// createStalledTracker.
//
// Still a boolean while `waiting` is not, matching Go for the reason given there: a
// stall under a source this client cannot read still reaches a person through the
// silence rule, which measures time rather than trusting the meaning of a field.
export function isStalled(s) {
  if (s.dying) return false;
  if (s.needs) return isStalledNeeds(s.needs);
  return s.state === "blocked" || s.tempo === "blocked";
}

// isStalled's two branches, split apart so the counter can treat them
// differently: a needs-based stall is a word from the daemon and is real the
// instant it appears; a flag-only stall (state/tempo === "blocked" with no
// needs text) is exactly what a session looks like for the length of one
// message delivery too, and has been observed to read as stalled twice in one
// hour on live sessions that were not actually stalled at all -- including the
// orchestrator's own. See createStalledTracker in header.js.
export function isNeedsStalled(s) {
  if (s.dying) return false;
  if (!s.needs) return false;
  return isStalledNeeds(s.needs);
}

export function isFlagOnlyStalled(s) {
  if (s.dying) return false;
  if (s.needs) return false;
  return s.state === "blocked" || s.tempo === "blocked";
}
