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

  // The three states a session can be in (web/js/lifecycle.js). A stopped
  // session is paused work: it comes back with its whole history, which is
  // why it is worded as a pause and not as an ending. A dead one cannot come
  // back, and the row says why — naming the directory that is gone is the
  // difference between knowing what happened and guessing.
  stopped_group: "Stopped",
  stopped_badge: "Stopped",
  stopped_resume_hint: "resumes with its history",
  gone_group: "Cannot be resumed",
  gone_badge: "Gone",
  gone_no_cwd: "its working directory no longer exists",
  last_state: "last state",
  stopped_unknown: "stopped sessions are unknown — the job store could not be read",
  session_stopped: "session is stopped",
  pick_orchestrator: "pick the orchestrator session",
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
  new_card: "+ card",
  new_card_title: "Card title",
  new_card_zone: "Zone",
  new_card_create: "Create",
  new_card_cancel: "Cancel",
  new_card_title_required: "A card needs a title",
  new_card_not_committed: "Card created, but not recorded in the board's git history",
  setup_title: "Set up fleetdeck",
  setup_intro: "Choose the folder fleetdeck keeps its board and documentation in. It creates two folders inside it:",
  setup_board: "board — an empty board: a git repository with the board's README, archive and card validator (the validator needs python3)",
  setup_docs: "docs — documentation, shown in the Docs tab",
  setup_outside:
    "Outside that folder it writes ~/.config/fleetdeck/config.yaml, adds the folder to permissions.additionalDirectories in ~/.claude/settings.json so Claude Code sessions can write their cards, and points Claude Code's status line at fleetdeck-status unless you already have a status line of your own.",
  setup_path: "Workspace folder",
  setup_choose: "Choose…",
  setup_choose_message: "Choose the folder to make the fleetdeck workspace in. A folder named fleetdeck is created inside it.",
  setup_choose_prompt: "Choose",
  setup_create: "Create",
  setup_path_required: "Name a folder",
  setup_skipped: "skipped",
  setup_opening: "Done. Opening the panel…",
  setup_failed: "The board could not be made. Choose another folder, or fix what is named above.",
  setup_no_handover: "The panel did not take over. Reload this page.",

  // The orchestrator step of the same page (web/js/setup.js). The wizard_warn_*
  // lines are the card's point: appointing an existing session adds to a
  // conversation that already has its own history and its own task, and the
  // person reads that before the button, not after it.
  wizard_setup_done: "The folder is made:",
  wizard_title: "The orchestrator",
  wizard_intro:
    "The orchestrator is the Claude Code session you run the fleet through: it sets other sessions their tasks, takes their work in and keeps the board. To make a session the orchestrator, fleetdeck writes the orchestrator's working order into a file and sends the session one message telling it to read that file. The message is the same whichever session you choose.",
  wizard_message: "The message:",
  wizard_brief: "The working order is written to",
  wizard_brief_show: "Show it as the session will read it",
  wizard_new_title: "A new session",
  wizard_new_text: "fleetdeck starts a background session named «{name}» in {path}. It has no history of its own: the working order is the first thing it reads.",
  wizard_new_button: "Start a session and send it the message",
  wizard_new_unavailable: "This panel does not start sessions: it is a test stand given no claude of its own. Choose an existing session.",
  wizard_existing_title: "An existing session",
  wizard_existing_text: "Choose a session to read what appointing it means for the work it is doing.",
  wizard_no_sessions: "No sessions are running.",
  wizard_daemon_down: "The sessions cannot be listed: {detail}",
  wizard_current: "the orchestrator now",
  wizard_waiting_for_you: "waiting for your answer",
  wizard_context: "context {n}% full",
  wizard_doing_unknown: "the daemon does not say what it is doing",
  wizard_warn_title: "You are adding to a conversation that is already under way.",
  wizard_warn_lead: "«{name}» has a history and a task of its own — right now: {doing}. The message goes onto the end of that conversation, as if you had typed it yourself:",
  wizard_warn_kept: "its history and the work it has done stay where they are: fleetdeck erases nothing and restarts nothing;",
  wizard_warn_context: "the working order takes up part of its context, and from then on it carries its own task and the fleet together — one can get in the way of the other;",
  wizard_warn_context_now: "its context is {n}% full now;",
  wizard_warn_busy: "if it is working right now, it reads the message once it finishes the step it is on; if it is waiting for your answer, answer it first — while a question is on its screen the message may not go in;",
  wizard_warn_final: "it cannot be taken back: the message stays in its history even if you later make another session the orchestrator.",
  wizard_warn_replaces: "«{old}» is the orchestrator now. Appointed, the session you chose takes its place in the orchestrator column; the message «{old}» was sent before stays in its history.",
  wizard_warn_again: "This session is the orchestrator already. Appointing it again sends it the same message once more — after its context was compacted, for instance.",
  wizard_appoint_button: "Add the message to «{name}»",
  wizard_skip: "Skip — appoint one later",
  wizard_rerun_hint: "The wizard can be run again from the orchestrator column.",
  wizard_working: "Appointing…",
  wizard_starting: "Starting the session — this can take up to a minute…",
  wizard_done: "Done: the session has its message and is pinned to the orchestrator column.",
  wizard_failed: "No orchestrator was appointed. What stopped it is named above; nothing after that step was done.",
  wizard_open: "Open the panel",
  orchestrator_wizard: "wizard…",
  orchestrator_wizard_hint: "Appoint the orchestrator: send a session the working order and pin it here",
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

  // The live terminal (web/js/liveterminal.js), on the session panel's screen
  // tab and in the orchestrator column. Each ending is named apart, because each
  // asks something different of the operator: wait, look elsewhere, fix the
  // key, start the daemon, or simply reopen the tab. The column reconnects by
  // itself and has no tab to reopen, so what it says while it tries again ends
  // in terminal_reconnecting instead of in that advice.
  terminal_not_connected: "the terminal is not connected yet",
  terminal_read_only: "this terminal only shows the session: the control key is unavailable, so typing is off",
  terminal_not_fitted: "the terminal could not measure this pane, so it is drawn at its default size — and the session runs at that size too",
  terminal_session_ended: "the session has ended",
  terminal_kicked: "the session was opened in another window",
  terminal_stream_dropped: "the daemon closed this terminal's connection, but the session is still running — reopen the tab to reconnect",
  terminal_stream_unexplained: "the terminal stream ended and the daemon could not say whether the session is still running — reopen the tab to find out",
  terminal_token_refused: "the panel did not accept this terminal's token — reopen the tab to try again",
  terminal_token_unavailable: "the terminal could not get its token from the panel",
  terminal_no_session: "there is no such session any more",
  terminal_key_refused: "the daemon refused the control key",
  terminal_daemon_unavailable: "the daemon is not running",
  terminal_connection_lost: "the terminal connection was lost — reopen the tab to reconnect",
  terminal_reconnecting: "reconnecting by itself…",
  terminal_link_lost: "the terminal lost its connection",
  terminal_token_stale: "the panel did not accept this terminal's token",
  // The size shown over a terminal whose type changed size: "13 px · session
  // 76 × 25". It names the session because that is what changed for everyone
  // watching it, not only the picture here.
  terminal_font_session: "session",
  terminal_font_limit: "the limit",
  // The font buttons (web/js/fontcontrols.js). Each names the key that does
  // the same, so the buttons also teach the keys.
  terminal_font_smaller: "smaller type (⌘−)",
  terminal_font_bigger: "bigger type (⌘+)",
  terminal_font_reset: "back to 12 px (⌘0)",

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
  // --- fleets (web/js/fleet.js, header.js's switcher, sessions.js's groups) ---
  fleet_switch: "fleets",
  fleet_none: "No fleet",
  fleet_sessions: "sessions",
  fleet_waiting: "waiting",
  // --- end of fleets ---
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
  update_button: "Update",
  update_confirm_unsent: "There is unsent text — update anyway?",
  update_step_press: "Starting…",
  update_step_check: "Checking for a new version…",
  update_step_build: "Building {rev}…",
  update_step_handover: "Starting the new version…",
  update_step_alive: "The new window is up…",
  update_step_panel: "The new panel answers…",
  update_step_swapped: "Putting the new version in place…",
  update_elapsed: "{n} s",
  update_done: "Updated to {rev}",
  update_current: "Already up to date ({rev})",
  update_busy: "An update is already running",
  update_failed: "Update failed: {detail}",
  build_version: "version",
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

  stopped_group: "Остановлены",
  stopped_badge: "Остановлена",
  stopped_resume_hint: "поднимается с историей",
  gone_group: "Не поднять",
  gone_badge: "Не поднять",
  gone_no_cwd: "рабочего каталога больше нет",
  last_state: "последнее состояние",
  stopped_unknown: "остановленные сессии неизвестны — каталог заданий не прочитан",
  session_stopped: "сессия остановлена",
  pick_orchestrator: "выберите сессию оркестратора",
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
  new_card: "+ карточка",
  new_card_title: "Заголовок карточки",
  new_card_zone: "Зона",
  new_card_create: "Создать",
  new_card_cancel: "Отмена",
  new_card_title_required: "У карточки должен быть заголовок",
  new_card_not_committed: "Карточка создана, но не записана в историю git доски",
  setup_title: "Настройка fleetdeck",
  setup_intro: "Выберите папку, в которой fleetdeck будет держать доску и документацию. В ней появятся две папки:",
  setup_board: "board — пустая доска: git-репозиторий с README доски, архивом и валидатором карточек (валидатору нужен python3)",
  setup_docs: "docs — документация, её показывает вкладка «Доки»",
  setup_outside:
    "Вне этой папки будет записан ~/.config/fleetdeck/config.yaml, папка будет добавлена в permissions.additionalDirectories файла ~/.claude/settings.json, чтобы сессии Claude Code могли вести свои карточки, а статусная строка Claude Code будет направлена на fleetdeck-status, если у вас нет своей.",
  setup_path: "Папка для рабочего пространства",
  setup_choose: "Выбрать…",
  setup_choose_message: "Выберите папку, в которой создать рабочее пространство fleetdeck. Внутри неё появится папка fleetdeck.",
  setup_choose_prompt: "Выбрать",
  setup_create: "Создать",
  setup_path_required: "Укажите папку",
  setup_skipped: "пропущено",
  setup_opening: "Готово. Открываю панель…",
  setup_failed: "Доску создать не удалось. Выберите другую папку или устраните то, что названо выше.",
  setup_no_handover: "Панель не открылась. Перезагрузите страницу.",

  wizard_setup_done: "Папка создана:",
  wizard_title: "Оркестратор",
  wizard_intro:
    "Оркестратор — сессия Claude Code, через которую вы ведёте флот: она ставит задачи другим сессиям, принимает их работу и ведёт доску. Чтобы сессия стала оркестратором, fleetdeck запишет порядок работы оркестратора в файл и отправит сессии одно сообщение — прочитать этот файл. Сообщение одно и то же, какую бы сессию вы ни выбрали.",
  wizard_message: "Сообщение:",
  wizard_brief: "Порядок работы будет записан в",
  wizard_brief_show: "Показать его так, как его прочтёт сессия",
  wizard_new_title: "Новая сессия",
  wizard_new_text: "fleetdeck запустит фоновую сессию «{name}» в папке {path}. Своей истории у неё нет: порядок работы станет первым, что она прочтёт.",
  wizard_new_button: "Запустить сессию и отправить ей сообщение",
  wizard_new_unavailable: "Эта панель не запускает сессий: это тестовый стенд без своего claude. Выберите существующую сессию.",
  wizard_existing_title: "Существующая сессия",
  wizard_existing_text: "Выберите сессию — ниже будет сказано, что назначение значит для её работы.",
  wizard_no_sessions: "Запущенных сессий нет.",
  wizard_daemon_down: "Список сессий недоступен: {detail}",
  wizard_current: "сейчас оркестратор",
  wizard_waiting_for_you: "ждёт вашего ответа",
  wizard_context: "контекст занят на {n}%",
  wizard_doing_unknown: "чем она занята, демон не сообщает",
  wizard_warn_title: "Вы доливаете контекст в чужой разговор.",
  wizard_warn_lead: "У «{name}» своя история и своё дело — сейчас: {doing}. Сообщение встанет в конец этого разговора, как если бы вы напечатали его сами:",
  wizard_warn_kept: "прежняя история и сделанная работа останутся на месте: fleetdeck ничего не стирает и не перезапускает;",
  wizard_warn_context: "порядок работы займёт часть её контекста, и дальше она будет вести и своё дело, и флот — одно может мешать другому;",
  wizard_warn_context_now: "сейчас её контекст занят на {n}%;",
  wizard_warn_busy: "если она сейчас работает, то прочтёт сообщение, когда закончит текущий шаг; если ждёт вашего ответа — сначала ответьте ей: пока на её экране вопрос, сообщение может не пройти;",
  wizard_warn_final: "отменить нельзя: сообщение останется в её истории, даже если потом назначить оркестратором другую сессию.",
  wizard_warn_replaces: "Сейчас оркестратор — «{old}». После назначения её место в колонке оркестратора займёт выбранная сессия, а сообщение, отправленное ей раньше, останется в её истории.",
  wizard_warn_again: "Эта сессия уже оркестратор. Повторное назначение отправит ей то же сообщение ещё раз — например, после того как её контекст сжали.",
  wizard_appoint_button: "Долить сообщение в «{name}»",
  wizard_skip: "Пропустить — назначить позже",
  wizard_rerun_hint: "Мастер можно пройти снова из колонки оркестратора.",
  wizard_working: "Назначаю…",
  wizard_starting: "Запускаю сессию — это может занять до минуты…",
  wizard_done: "Готово: сессия получила сообщение и закреплена в колонке оркестратора.",
  wizard_failed: "Оркестратор не назначен. Что помешало — написано выше; шаги после него не выполнялись.",
  wizard_open: "Открыть панель",
  orchestrator_wizard: "мастер…",
  orchestrator_wizard_hint: "Назначить оркестратора: отправить сессии порядок работы и закрепить её здесь",
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

  terminal_not_connected: "терминал ещё не подключён",
  terminal_read_only: "этот терминал только показывает сессию: ключа управления нет, поэтому набор выключен",
  terminal_not_fitted: "терминал не смог измерить панель и нарисован в размере по умолчанию — в этом же размере теперь работает и сессия",
  terminal_session_ended: "сессия завершилась",
  terminal_kicked: "сессию открыли в другом окне",
  terminal_stream_dropped: "демон закрыл соединение этого терминала, но сессия продолжает работать — откройте вкладку заново, чтобы переподключиться",
  terminal_stream_unexplained: "поток терминала закончился, и демон не смог сказать, работает ли сессия — откройте вкладку заново, чтобы узнать",
  terminal_token_refused: "пульт не принял токен этого терминала — откройте вкладку заново, чтобы попробовать ещё раз",
  terminal_token_unavailable: "терминал не смог получить свой токен у пульта",
  terminal_no_session: "такой сессии больше нет",
  terminal_key_refused: "демон отверг ключ управления",
  terminal_daemon_unavailable: "демон не запущен",
  terminal_connection_lost: "связь с терминалом потеряна — откройте вкладку заново, чтобы переподключиться",
  terminal_reconnecting: "переподключаюсь сам…",
  terminal_link_lost: "терминал потерял связь",
  terminal_token_stale: "пульт не принял токен этого терминала",
  terminal_font_session: "сессия",
  terminal_font_limit: "предел",
  terminal_font_smaller: "шрифт мельче (⌘−)",
  terminal_font_bigger: "шрифт крупнее (⌘+)",
  terminal_font_reset: "вернуть 12 px (⌘0)",

  image_no_session: "прикреплять картинку не к чему: сессия не выбрана",
  image_too_large: "эта картинка слишком велика, чтобы её прикрепить",
  image_wrong_type: "этот файл — не та картинка, которую пульт умеет прикреплять",
  image_may_ask_permission: "при первом чтении отсюда сессия спросит вашего разрешения — ответьте ей в этом же пульте",

  sessions_title: "Сессии",
  // --- fleets (web/js/fleet.js, header.js's switcher, sessions.js's groups) ---
  fleet_switch: "флоты",
  fleet_none: "Без флота",
  fleet_sessions: "сессий",
  fleet_waiting: "ждут ответа",
  // --- end of fleets ---
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
  update_button: "Обновить",
  update_confirm_unsent: "Есть неотправленный текст — всё равно обновить?",
  update_step_press: "Начинаю…",
  update_step_check: "Проверяю, есть ли новая версия…",
  update_step_build: "Собираю {rev}…",
  update_step_handover: "Запускаю новую версию…",
  update_step_alive: "Новое окно открылось…",
  update_step_panel: "Новый пульт отвечает…",
  update_step_swapped: "Ставлю новую версию на место…",
  update_elapsed: "{n} с",
  update_done: "Обновлено до {rev}",
  update_current: "Уже последняя версия ({rev})",
  update_busy: "Обновление уже идёт",
  update_failed: "Обновление не удалось: {detail}",
  build_version: "версия",
  build_commit: "коммит",
  build_modified: "изменённое дерево",
  build_commit_time: "коммит сделан",
  build_built_at: "собран",
  build_executable: "бинарь",
};

// The language the page speaks, as a code the panel understands: what the
// orchestrator wizard asks the working order in, so the session reads it in the
// same language the person read the wizard in.
export const langCode = (navigator.language || "en").toLowerCase().startsWith("ru") ? "ru" : "en";

const lang = langCode === "ru" ? ru : en;

// A key with no entry renders as the key itself, never as an empty string: a
// missing entry has to be visible as a defect on screen, and a blank label looks
// like a deliberately empty one. web/tests/i18n.test.js reads the two objects
// above directly, because that fallback makes a key missing from `ru`
// indistinguishable through t() from one translated identically.
export function t(key) {
  return lang[key] ?? en[key] ?? key;
}
