// web/js/lifecycle.js
// Which of the three states a session is in, and nothing else.
//
// The three are the panel's own reading of two sources the daemon keeps
// separate: its live list, and Claude Code's job store (internal/jobs). The
// control protocol carries neither a `live` nor a `resumable` field and says
// so — a session's presence in a list reply is the whole of what it says
// about being alive — so the answer is derived in Go and arrives here as one
// string on every session (internal/state's Lifecycle).
//
//   live     — the daemon is running it. Everything the panel has always
//              drawn is about these, and only these.
//   stopped  — it is not running, and it comes back with its whole history.
//              Paused work, not lost work.
//   dead     — it is not running and it cannot come back: nothing left to
//              resume by, or its working directory is gone. Still shown, so
//              a person can see the work is there and is not coming back.
//
// The vocabulary lives in one module because three files read it — the
// session list, the board and the fleet counters — and a second copy of
// "which strings mean not running" is a second copy that can disagree.
//
// An absent lifecycle reads as live, matching the Go side: a snapshot from
// before this existed described live sessions and nothing else, and reading
// its sessions as stopped would empty the list rather than fill it.

export const LIVE = "live";
export const STOPPED = "stopped";
export const DEAD = "dead";

export function isLive(session) {
  const lifecycle = session?.lifecycle;
  return lifecycle !== STOPPED && lifecycle !== DEAD;
}

// isResumable is true only for a stopped session. A live one has nothing to
// resume and a dead one cannot be resumed, and the whole point of three
// states rather than two is that those two answers are not the same.
export function isResumable(session) {
  return session?.lifecycle === STOPPED;
}
