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
  session_not_listed: "session is not currently listed by the daemon",

  // Card panel (web/js/card.js). Field names are deliberately absent: stage,
  // progress and session are frontmatter keys, they appear in the card file
  // exactly as written, and translating them would show the operator a word
  // they cannot type into a card. A dead session reuses session_dead above
  // rather than restating the same sentence under a second name.
  //
  card_close: "close",
  card_waiting: "waiting for the first snapshot",
  card_gone: "card is gone",
  card_parse_error: "this card does not parse, so its fields cannot be edited here",
  card_not_committed: "the change is in the card file and did not reach the git history",
  card_write_refused: "the change was refused and nothing was written",
  backlinks: "linked from",
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
  session_not_listed: "сессия сейчас не в списке демона",

  card_close: "закрыть",
  card_waiting: "ждём первый снимок",
  card_gone: "карточка исчезла",
  card_parse_error: "карточка не разбирается, править её поля отсюда нельзя",
  card_not_committed: "правка лежит в файле карточки и не попала в историю git",
  card_write_refused: "правка отклонена, ничего не записано",
  backlinks: "ссылаются сюда",
};

const lang = (navigator.language || "en").toLowerCase().startsWith("ru") ? ru : en;

// A key with no entry renders as the key itself, never as an empty string: a
// missing entry has to be visible as a defect on screen, and a blank label looks
// like a deliberately empty one. web/tests/i18n.test.js reads the two objects
// above directly, because that fallback makes a key missing from `ru`
// indistinguishable through t() from one translated identically.
export function t(key) {
  return lang[key] ?? en[key] ?? key;
}
