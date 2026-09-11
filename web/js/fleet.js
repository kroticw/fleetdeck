// web/js/fleet.js
// Which fleet this tab shows, and how the sessions of a whole machine are
// grouped around it.
//
// The fleet lives in the tab's address, ?fleet=<name>, and nowhere else: the
// server serves whichever fleet a tab names, so two tabs on two fleets work
// side by side and switching one never touches the other. No parameter is the
// first fleet, which is also every address opened before there were several.
//
// Switching is a navigation. Reloading the page closes every live terminal the
// tab holds — the orchestrator column and an open session's screen — so no
// hidden terminal keeps imposing its size on a session of the fleet just left.
// Nothing is sent to that fleet's sessions and nothing stops them; only this
// tab stops looking.

import { rememberOpenSession } from "./buildcheck.js";

const PARAM = "fleet";

// fleetFromSearch returns the fleet a location's search string names, "" when
// it names none.
export function fleetFromSearch(search) {
  return new URLSearchParams(search).get(PARAM) ?? "";
}

// withFleet adds the fleet to a panel path, so the snapshot a request asks for
// is the tab's fleet. No fleet leaves the path as it is.
export function withFleet(path, fleet) {
  if (!fleet) return path;
  const sep = path.includes("?") ? "&" : "?";
  return `${path}${sep}${PARAM}=${encodeURIComponent(fleet)}`;
}

// fleetSearch is the search string that shows fleet, keeping every other
// parameter search already had.
export function fleetSearch(search, fleet) {
  const params = new URLSearchParams(search);
  params.set(PARAM, fleet);
  return `?${params}`;
}

// isMultiFleet reports whether the snapshot comes from a panel with more than
// one fleet. With one fleet nothing is grouped or switched: the panel looks
// exactly as it did before there were fleets.
export function isMultiFleet(snap) {
  return (snap?.fleets?.length ?? 0) > 1;
}

// groupSessions sorts a machine's sessions around the fleet a tab shows:
// its own (the fleet claims them), the unclaimed ones (no fleet does — shown
// in every fleet, never in none), and the others, one group per other fleet
// in configuration order. A session two other fleets claim is in both of
// their groups; a session this fleet and another claim is this fleet's own.
export function groupSessions(snap) {
  const current = snap?.fleet ?? "";
  const own = [];
  const unclaimed = [];
  const byFleet = new Map((snap?.fleets ?? []).filter((f) => f !== current).map((f) => [f, []]));
  for (const s of snap?.sessions ?? []) {
    const fleets = s.fleets ?? [];
    if (fleets.length === 0) unclaimed.push(s);
    else if (fleets.includes(current)) own.push(s);
    else for (const f of fleets) byFleet.get(f)?.push(s);
  }
  const others = [...byFleet].map(([name, sessions]) => ({ name, sessions }));
  return { own, unclaimed, others };
}

// headerSessions is what the header's counters count: this fleet's sessions
// and the unclaimed ones. Another fleet's waiting session is counted on that
// fleet's switcher entry instead, so it is neither lost nor mistaken for this
// fleet's. With one fleet it is every session, as it always was: each is that
// fleet's or unclaimed, and a snapshot with no fleets tags none of them.
export function headerSessions(snap) {
  const { own, unclaimed } = groupSessions(snap);
  return [...own, ...unclaimed];
}

// fleetEntries is the switcher's list: every fleet, which one this tab shows,
// and how many of each fleet's own sessions wait for an answer.
export function fleetEntries(snap, isWaiting) {
  const current = snap?.fleet ?? "";
  return (snap?.fleets ?? []).map((name) => ({
    name,
    current: name === current,
    waiting: (snap?.sessions ?? []).filter((s) => (s.fleets ?? []).includes(name) && isWaiting(s)).length,
  }));
}

// switchFleet shows another fleet in this tab. The session a reload would
// otherwise reopen is forgotten first: it belongs to the fleet being left.
export function switchFleet(fleet, { storage, location = globalThis.location } = {}) {
  rememberOpenSession(storage, "");
  location.assign(`${location.pathname}${fleetSearch(location.search, fleet)}`);
}
