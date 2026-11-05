// requests.js: what a Codex session waits on — a command or file change to
// approve, a question to answer — drawn next to its terminal. Every answer is
// an explicit press; nothing is approved automatically, and no choice here
// grants anything beyond the one request.

import { sessionRequests, respondRequest } from "./api.js";
import { t } from "./i18n.js";

const DECISIONS = ["accept", "decline", "cancel"];

const el = (tag, className, text) => {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
};

/**
 * createRequests appends the request list to host and hands back refresh,
 * which asks the runtime again. `list` and `respond` are api.js's unless a
 * test replaces them.
 */
export function createRequests(host, sessionId, { list = sessionRequests, respond = respondRequest } = {}) {
  const node = el("div", "s-requests");
  node.hidden = true;
  host.appendChild(node);
  let said = "";
  let busy = false;

  const answer = async (body) => {
    if (busy) return;
    busy = true;
    said = "";
    try {
      await respond(sessionId, body);
    } catch (err) {
      // Stale or of unknown fate: say it in the server's words and show what
      // is pending now; never answer again on the operator's behalf.
      said = String(err?.message ?? err);
    }
    busy = false;
    await refresh();
  };

  const button = (label, data, body) => {
    const b = el("button", "btn btn-sm s-request-answer", label);
    b.type = "button";
    Object.assign(b.dataset, data);
    b.addEventListener("click", () => void answer(body));
    return b;
  };

  const item = (r) => {
    const row = el("div", "s-request");
    if (r.kind === "command" || r.kind === "fileChange") {
      row.appendChild(el("div", "s-request-title", t(r.kind === "command" ? "request_command" : "request_file_change")));
      if (r.command) row.appendChild(el("code", "s-request-command", r.command));
      for (const [key, value] of [["request_cwd", r.cwd], ["request_grant_root", r.grantRoot], ["request_reason", r.reason]]) {
        if (value) row.appendChild(el("div", "s-request-detail", `${t(key)}: ${value}`));
      }
      const actions = el("div", "s-request-actions");
      for (const decision of DECISIONS) {
        actions.appendChild(button(t(`request_${decision}`), { decision }, { token: r.token, decision }));
      }
      row.appendChild(actions);
    } else if (r.kind === "userInput" && (r.questions ?? []).every((q) => (q.options ?? []).length > 0)) {
      // One choice per question; the answer goes once every question has one.
      const chosen = {};
      for (const q of r.questions) {
        row.appendChild(el("div", "s-request-title", q.header || t("request_question")));
        row.appendChild(el("div", "s-request-detail", q.question));
        const actions = el("div", "s-request-actions");
        for (const option of q.options) {
          const b = el("button", "btn btn-sm s-request-answer", option);
          b.type = "button";
          b.dataset.option = option;
          b.addEventListener("click", () => {
            chosen[q.id] = [option];
            for (const other of actions.querySelectorAll("button")) other.classList.toggle("is-chosen", other === b);
            if (r.questions.every((each) => chosen[each.id])) void answer({ token: r.token, answers: chosen });
          });
          actions.appendChild(b);
        }
        row.appendChild(actions);
      }
    } else if (r.kind === "userInput") {
      // Free text the panel does not take: answered where Codex asked it.
      for (const q of r.questions ?? []) row.appendChild(el("div", "s-request-detail", `${q.header || t("request_question")}: ${q.question}`));
      row.appendChild(el("div", "s-request-detail", t("request_in_terminal")));
    } else {
      row.appendChild(el("div", "s-request-detail", `${t("request_in_terminal")}: ${r.method}`));
    }
    return row;
  };

  async function refresh() {
    let pending;
    try {
      pending = await list(sessionId);
    } catch (err) {
      pending = [];
      said = said || String(err?.message ?? err);
    }
    const rows = (pending ?? []).map(item);
    if (said) rows.unshift(el("div", "s-request-error", said));
    node.replaceChildren(...rows);
    node.hidden = rows.length === 0;
  }

  return { node, refresh };
}
