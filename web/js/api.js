// Minimal fetch wrapper until a later task's api.js supersedes this one.
async function post(path, body) {
  const res = await fetch(path, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!res.ok) {
    const payload = await res.json().catch(() => ({}));
    throw new Error(payload.error ?? res.statusText);
  }
}

export function sendText(sessionShort, text, submit = true) {
  return post(`/api/sessions/${encodeURIComponent(sessionShort)}/text`, { text, submit });
}

export async function setOrchestratorSession(id) {
  const res = await fetch("/api/config", {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ orchestratorSession: id }),
  });
  if (!res.ok) {
    const payload = await res.json().catch(() => ({}));
    throw new Error(payload.error ?? res.statusText);
  }
}
