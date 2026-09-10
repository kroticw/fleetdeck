// web/js/i18n.js
// The interface ships two languages. The dictionary is flat on purpose: a
// missing key must be visible as the key itself (or the English fallback),
// never as an empty string.

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
  waiting_count: "waiting for you",
  stalled_count: "stalled",
  limit_5h: "5h",
  limit_7d: "7d",
  resets_in: "resets in",
  usage_down: "limits unavailable",
  offline: "disconnected",
  card_broken: "unreadable card",
  session_dead: "session is dead",
  pick_orchestrator: "pick the orchestrator session",
  write_to_orchestrator: "write to the orchestrator…",
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
  waiting_count: "ждут ответа",
  stalled_count: "остановились",
  limit_5h: "5ч",
  limit_7d: "7д",
  resets_in: "сброс через",
  usage_down: "лимиты недоступны",
  offline: "нет связи",
  card_broken: "карточка не разбирается",
  session_dead: "сессия мертва",
  pick_orchestrator: "выберите сессию оркестратора",
  write_to_orchestrator: "написать оркестру…",
};

const lang = (navigator.language || "en").toLowerCase().startsWith("ru") ? ru : en;

export function t(key) {
  return lang[key] ?? en[key] ?? key;
}
