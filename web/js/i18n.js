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
  only_orchestrator: "No sessions besides the orchestrator",
  background_task: "background task",
  typed_here: "operator",

  // The controls that size the orchestrator column. "unfold" especially: when
  // the column is folded it is the only thing left on screen, and a button
  // reading "column_unfold" would be a column nobody can get back.
  column_drag: "drag to resize the column",
  column_fold: "fold the column away",
  column_unfold: "bring the column back",
  waiting: "Waiting",
  stalled: "Stalled",
  context_unknown: "no transcript yet",
  open_card: "card",
  open_card_hint: "Open this session's card",
  silent_unmeasured: "not measured",
  waiting_count: "waiting for you",
  stalled_count: "stalled",
  limit_5h: "5h",
  limit_7d: "7d",
  resets_in: "resets in",
  last_known: "last known",
  usage_down: "limits unavailable",
  usage_down_auth: "limits unavailable — sign-in needed",
  usage_down_rate_limited: "limits unavailable — rate limited, usually recovers on its own",
  offline: "disconnected",
  card_broken: "unreadable card",
  session_dead: "session is dead",
  pick_orchestrator: "pick the orchestrator session",
  write_to_orchestrator: "write to the orchestrator…",
  session_not_listed: "session is not currently listed by the daemon",
  not_pinned: "not pinned",
  no_orchestrator_thread: "no session is pinned as the orchestrator — pick one above",
  orchestrator_pin_failed: "the change was not saved — a reload will undo it",

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

  // Centre-column sections (web/js/sections.js) and the documentation section
  // (web/js/docs.js). The two failure keys stay separate because they are
  // separate facts: the list failing means there is no documentation to browse,
  // while one document failing to open says nothing about the others. Both are
  // prefixes to the server's own sentence, which names the directory or the rule
  // — the detail an operator acts on — and is never reworded here.
  tab_board: "Board",
  tab_docs: "Docs",
  pick_doc: "pick a document",
  doc_opening: "opening…",
  docs_empty: "no documents in the configured directories",
  docs_list_failed: "the documentation could not be listed",
  doc_open_failed: "this document could not be opened",
  // Session panel (web/js/session.js). close_session is its own key rather than
  // card_close above: the two buttons close different things, and a single key
  // shared between them would tie one panel's wording to the other's.
  tab_digest: "digest",
  tab_screen: "screen",
  // Says where a press lands, not what the key is called: the glyph on the
  // button already says Esc or ↓, and what no glyph can say is that the press
  // happens in a Claude Code session running elsewhere, where nothing undoes
  // it. The operator read the bare row as window controls for this panel.
  keys_to_session: "these keys are pressed in the live session:",
  // The box types into that same live session, and Enter sends it there with
  // no confirmation — a fact the box otherwise keeps to itself.
  write_to_session: "write to the live session — Enter sends…",
  close_session: "close",
  no_steps: "no readable steps yet",
  terminal_missing: "the terminal library did not load",

  // Attaching an image to a session (web/js/session.js, web/js/imagefile.js).
  // Both refusals are worded as what the panel will accept rather than as what
  // was wrong, because the person is about to pick another file and that is the
  // useful half. The permission line is not a warning: it names an expected step
  // so a session that stops to ask is not read as one that hung.
  image_no_session: "there is no session to attach an image to",
  image_too_large: "that image is too large to attach",
  image_wrong_type: "that file is not an image the panel can attach",
  image_may_ask_permission: "the session will ask your permission the first time it reads from here — answer it in this panel",

  // The session list's own header (web/js/sessions.js) and the theme
  // override (web/js/theme.js's button, rendered by header.js). "auto" is
  // the no-override state — the panel follows the system theme.
  sessions_title: "Sessions",
  theme_auto: "theme: auto",
  theme_light: "theme: light",
  theme_dark: "theme: dark",

  // The operator's own name for a session (web/js/orchestrator.js's picker
  // and pinned title, web/js/sessions.js's row) — edited in place, not
  // through a dialog.
  edit_label: "edit the name",
  label_save_failed: "the name was not saved",

  build_stale: "The panel has been updated; this page has not.",
  build_reload: "Reload",
  build_stale_waiting: "The panel has been updated. This page will reload by itself once the text you are typing is sent.",
  build_reload_now: "Reload now",
  build_reload_failed: "Reloading did not help: the window got the previous page again. Quit the window with Cmd+Q and open it again.",
  build_commit: "commit",
  build_modified: "modified tree",
  build_commit_time: "commit made",
  build_built_at: "built",
  build_executable: "binary",
};

const ru = {
  context: "контекст",
  estimated: "Оценено по транскрипту — репортер не установлен",
  connecting: "Подключение…",
  daemon_down: "Демон недоступен",
  no_sessions: "Нет сессий",
  only_orchestrator: "Кроме оркестратора сессий нет",
  background_task: "фоновая задача",
  typed_here: "оператор",

  column_drag: "потяните, чтобы изменить ширину колонки",
  column_fold: "свернуть колонку",
  column_unfold: "развернуть колонку",
  waiting: "Ждёт",
  stalled: "Застряла",
  context_unknown: "транскрипта пока нет",
  open_card: "карточка",
  open_card_hint: "Открыть карточку этой сессии",
  silent_unmeasured: "не измерено",
  waiting_count: "ждут ответа",
  stalled_count: "остановились",
  limit_5h: "5ч",
  limit_7d: "7д",
  resets_in: "сброс через",
  last_known: "последнее известное",
  usage_down: "лимиты недоступны",
  usage_down_auth: "лимиты недоступны — нужен вход",
  usage_down_rate_limited: "лимиты недоступны — превышена частота запросов, обычно восстанавливается само",
  offline: "нет связи",
  card_broken: "карточка не разбирается",
  session_dead: "сессия мертва",
  pick_orchestrator: "выберите сессию оркестратора",
  write_to_orchestrator: "написать оркестру…",
  session_not_listed: "сессия сейчас не в списке демона",
  not_pinned: "не закреплено",
  no_orchestrator_thread: "оркестратор не закреплён — выберите его выше",
  orchestrator_pin_failed: "изменение не сохранено — после перезагрузки страницы оно исчезнет",

  card_close: "закрыть",
  card_waiting: "ждём первый снимок",
  card_gone: "карточка исчезла",
  card_parse_error: "карточка не разбирается, править её поля отсюда нельзя",
  card_not_committed: "правка лежит в файле карточки и не попала в историю git",
  card_write_refused: "правка отклонена, ничего не записано",
  backlinks: "ссылаются сюда",

  tab_board: "Доска",
  tab_docs: "Доки",
  pick_doc: "выберите документ",
  doc_opening: "открываем…",
  docs_empty: "в настроенных каталогах нет документов",
  docs_list_failed: "не удалось построить список документации",
  doc_open_failed: "не удалось открыть документ",
  tab_digest: "выжимка",
  tab_screen: "экран",
  keys_to_session: "эти клавиши нажимаются в живой сессии:",
  write_to_session: "написать в живую сессию — Enter отправит…",
  close_session: "закрыть",
  no_steps: "читаемых шагов пока нет",
  terminal_missing: "библиотека терминала не загрузилась",

  image_no_session: "прикреплять картинку не к чему: сессия не выбрана",
  image_too_large: "эта картинка слишком велика, чтобы её прикрепить",
  image_wrong_type: "этот файл — не та картинка, которую пульт умеет прикреплять",
  image_may_ask_permission: "при первом чтении отсюда сессия спросит вашего разрешения — ответьте ей в этом же пульте",

  sessions_title: "Сессии",
  theme_auto: "тема: авто",
  theme_light: "тема: светлая",
  theme_dark: "тема: тёмная",

  edit_label: "изменить имя",
  label_save_failed: "имя не сохранено",

  build_stale: "Панель обновилась, а эта страница — нет.",
  build_reload: "Перезагрузить",
  build_stale_waiting: "Панель обновилась. Страница перезагрузится сама, как только набранный текст будет отправлен.",
  build_reload_now: "Перезагрузить сейчас",
  build_reload_failed: "Перезагрузка не помогла: окно снова получило прежнюю страницу. Закройте окно через Cmd+Q и откройте заново.",
  build_commit: "коммит",
  build_modified: "изменённое дерево",
  build_commit_time: "коммит сделан",
  build_built_at: "собран",
  build_executable: "бинарь",
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
