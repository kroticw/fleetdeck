// web/js/hostroutes.js — where an action from one surface goes.
//
// In a browser tab every action stays in the page. In the fleetdeck window the
// board still opens cards, documents and sessions itself, but the side surfaces
// cannot: the sheet lives over the board, in another web view. They hand the
// action to the window, which passes it to the board. Switching fleet and
// folding a panel go through the window from every surface, because they change
// all three web views at once.
import { callHost } from "./host.js";

export function routesFor(win, host, local) {
  if (!host) return local;
  const onBoard = host.surface === "board";
  const open = (payload, localCall) => (onBoard ? localCall() : callHost(win, "fleetdeckOpen", payload));
  return {
    openCard: (path) => open({ kind: "card", path }, () => local.openCard(path)),
    openDoc: (path) => open({ kind: "doc", path }, () => local.openDoc(path)),
    openSession: (short) => open({ kind: "session", short }, () => local.openSession(short)),
    switchFleet: (name) => callHost(win, "fleetdeckSwitchFleet", name),
    fold: (side, folded) => callHost(win, "fleetdeckPanel", { side, folded }),
    openOrchestrator: () => callHost(win, "fleetdeckOpen", { kind: "orchestrator" }),
  };
}
