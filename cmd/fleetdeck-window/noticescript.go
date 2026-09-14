//go:build darwin

package main

import "encoding/json"

// noticeMarkID is the style element the orchestrator's surface is given while
// the panel answering is another build.
const noticeMarkID = "fleetdeck-window-notice-mark"

// noticeScriptFor is the notice script for a web view of the glass window. The
// board shows the notice itself, as the window's one web view always has. The
// orchestrator's surface carries the header's brand row, so it gets the mark
// on the build in it and nothing else: the notice box stays the board's. The
// sessions surface shows no build and gets nothing.
func noticeScriptFor(surface string) string {
	switch surface {
	case "board":
		return noticeScript
	case "orchestrator":
		rule, _ := json.Marshal(noticeHeaderRule)
		id, _ := json.Marshal(noticeMarkID)
		return `(() => {
  const repaint = () => {
    if (typeof window.` + noticeBindingName + ` !== "function" || !document.head) return;
    window.` + noticeBindingName + `().then((markup) => {
      const shown = document.getElementById(` + string(id) + `);
      const otherBuild = typeof markup === "string" && markup.includes("build-rev::before");
      if (!otherBuild) {
        if (shown) shown.remove();
        return;
      }
      if (shown) return;
      const style = document.createElement("style");
      style.id = ` + string(id) + `;
      style.textContent = ` + string(rule) + `;
      document.head.appendChild(style);
    });
  };
  window.` + noticeRepaintFunction + ` = repaint;
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", repaint);
  else repaint();
})();`
	}
	return ""
}
