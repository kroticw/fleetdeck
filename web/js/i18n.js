// Minimal i18n shim for the session list. A sibling task (header) will
// likely replace or merge this file later — keep it small and self-contained
// until then, no external dependency on that task's shape.

const en = {
  context: "context",
  estimated: "Estimated from transcript — no reporter installed",
  connecting: "Connecting…",
  daemon_down: "Daemon unavailable",
  no_sessions: "No sessions",
  waiting: "Waiting",
  stalled: "Stalled",
  context_unknown: "no transcript yet",
  silent_unmeasured: "not measured",
};

const ru = {
  context: "контекст",
  estimated: "Оценено по транскрипту — репортер не установлен",
  connecting: "Подключение…",
  daemon_down: "Демон недоступен",
  no_sessions: "Нет сессий",
  waiting: "Ждёт",
  stalled: "Застряла",
  context_unknown: "транскрипта пока нет",
  silent_unmeasured: "не измерено",
};

const lang = (navigator.language || "en").toLowerCase().startsWith("ru") ? ru : en;

export function t(key) {
  return lang[key] ?? en[key] ?? key;
}
