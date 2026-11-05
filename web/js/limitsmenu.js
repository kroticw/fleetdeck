// web/js/limitsmenu.js — the limits menu in the fleetdeck window.
//
// In the window the limits are a capsule the window draws (web/js/capsules.js);
// pressing it asks the board for this menu. It is the header's own table
// (header.js limitsTableHTML), in a popover under the capsule row, following
// the snapshot while it is open and closing on Escape or a click outside it.
import { limitsTableHTML } from "./header.js";
import { t } from "./i18n.js";

export function createLimitsPopover(doc, getSnap, now = () => Date.now()) {
  const node = doc.createElement("div");
  node.className = "limits-popover";
  node.setAttribute("role", "dialog");
  node.setAttribute("aria-label", t("limits_title"));
  node.hidden = true;
  doc.body.appendChild(node);

  const refresh = () => {
    if (node.hidden) return;
    node.innerHTML = limitsTableHTML(getSnap() ?? {}, now());
  };
  const close = () => {
    node.hidden = true;
  };
  const toggle = () => {
    node.hidden = !node.hidden;
    refresh();
  };
  doc.addEventListener("keydown", (event) => {
    if (event.key === "Escape") close();
  });
  doc.addEventListener(
    "click",
    (event) => {
      if (!node.hidden && !node.contains?.(event.target)) close();
    },
    true,
  );
  return { toggle, close, refresh };
}
