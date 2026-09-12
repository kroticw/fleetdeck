// web/js/steplist.js
// What a write did, step by step — the one way this panel reports a sequence
// of writes that can each be refused on its own.
//
// The wizard (web/js/setup.js) and the start page (web/js/start.js) both report
// this way, and they report the same shape: the steps `fleetdeck init` prints,
// as JSON. Kept here rather than in either page so that a refused step reads
// identically wherever it is shown — the wording is the contract, not a detail
// of whichever page happened to ask.

import { t } from "./i18n.js";

// showSteps lists what a write did, a refused step with its reason. The reason
// is the panel's own sentence and is never reworded here: it names the path or
// the rule the operator has to act on.
export function showSteps(target, list) {
  target.replaceChildren(
    ...list.map((step) => {
      const item = document.createElement("li");
      item.className = step.error ? "setup-step setup-step-skipped" : "setup-step";
      item.textContent = step.error ? `${step.name}: ${t("setup_skipped")}: ${step.error}` : `${step.name}: ${step.note}`;
      if (step.detail) {
        const detail = document.createElement("div");
        detail.className = "setup-step-detail";
        detail.textContent = step.detail;
        item.append(detail);
      }
      return item;
    }),
  );
}

export default showSteps;
