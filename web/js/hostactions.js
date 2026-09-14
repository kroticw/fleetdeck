// web/js/hostactions.js — what the window asks of a page.
//
// The fleetdeck window drives its web views with messages through
// window.fleetdeckHost.receive (web/js/host.js). Each surface takes only the
// messages meant for it — the board opens things and lays itself out around the
// panels, the side surfaces follow the theme and their panel's state — and turns
// them into what the page already does when a person asks for the same thing.
import { callHost, onHostMessage } from "./host.js";

const ACCEPTS = {
  board: new Set(["open", "show", "newCard", "cycleTheme", "insets", "glass"]),
  orchestrator: new Set(["theme", "glass", "folded", "focusTerminal", "fullscreen"]),
  sessions: new Set(["theme", "glass", "folded"]),
};

export function wireHostActions(win, host, targets) {
  const accepts = ACCEPTS[host.surface];
  return onHostMessage(win, (message) => {
    if (!accepts.has(message.type)) return;
    switch (message.type) {
      case "open":
        if (message.kind === "card") targets.openCard(message.path);
        else if (message.kind === "doc") targets.openDoc(message.path);
        else if (message.kind === "session") targets.openSession(message.short);
        return;
      case "show":
        targets.showSection(message.section);
        return;
      case "newCard":
        targets.openNewCard();
        return;
      case "cycleTheme":
        callHost(win, "fleetdeckTheme", targets.cycleTheme());
        return;
      case "theme":
        targets.applyTheme(message.choice);
        return;
      case "insets":
        // right is what the board scrolls clear of (nothing: it runs on under the
        // sessions glass); contentRight is what a sheet or the documents keep
        // clear of, the sessions panel itself.
        targets.setInsets({ top: message.top, left: message.left, right: message.right, contentRight: message.contentRight });
        return;
      case "glass":
        targets.setGlass(message.glass);
        return;
      case "folded":
        targets.setFolded(message.folded === true);
        return;
      case "focusTerminal":
        targets.focusTerminal();
        return;
      case "fullscreen":
        targets.setFullscreen(message.on === true);
        return;
    }
  });
}
