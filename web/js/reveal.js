// The window's Finder button: fleetdeck-window binds window.fleetdeckReveal,
// which opens Finder with a file selected. A browser binds nothing, and then
// there is no button (cmd/fleetdeck-window/choose_darwin.go).
export function revealer() {
  const reveal = globalThis.fleetdeckReveal;
  return typeof reveal === "function" ? (path) => reveal(path) : null;
}
