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

// waiting: a person must answer before this session can move. needs is the
// only signal -- never state/tempo, which are set by a mechanism the session
// does not control and cannot tell "waiting on a person" apart from "waiting
// on its own subagents" (spec 3.1).
export function isWaiting(s) {
  if (s.dying) return false;
  if (!s.needs) return false;
  return !isStalledNeeds(s.needs);
}

// stalled: stopped for a reason no answer fixes, or stopped with no words at
// all. Order matters: needs decides first; the state/tempo flags are only
// consulted when needs is empty (spec 3.1). This mirrors
// daemon.Session.Stalled() exactly -- timeless, per-snapshot -- and stays that
// way for the sake of that mirror; header.js's counter does not use it
// directly, see isNeedsStalled, isFlagOnlyStalled and createStalledTracker.
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
