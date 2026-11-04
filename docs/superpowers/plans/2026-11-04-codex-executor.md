# Поддержка Codex: план реализации

> **For agentic workers:** REQUIRED SUB-SKILL: Use $executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. Keep one writer; delegate only bounded read-only investigation when permitted by the session.

**Goal:** Дать карточкам Fleetdeck выбор Claude Code или Codex с полноценным терминалом, управлением, восстановлением, ревью и архивированием в смешанном флоте.

**Architecture:** Общий слой исполнителей оборачивает существующую интеграцию Claude и новый адаптер Codex. Обычный `codex` работает в PTY отдельного runtime-процесса Fleetdeck, переживающего HTTP-панель; структурированное управление выбирается по результатам обязательного прототипа. `--remote` не является исходной зависимостью.

**Tech Stack:** Go из `go.mod` (`1.27.3` на исходной базе), vanilla JavaScript, vendored xterm.js, Unix sockets, PTY, Markdown/YAML, Python-валидаторы, существующие `coder/websocket`, `x/sys` и `yaml.v3`. Новые библиотеки не добавлять до проверки необходимости и версии.

**Spec:** [Дизайн поддержки исполнителей](../specs/2026-11-04-codex-executor-design.md).

Дата: 2026-11-04. База плана: `25217f0` поверх `master` `8440d9c`. Статус: готов к выполнению проверочного этапа; продуктовая реализация не начата.

## Общие ограничения

- Первый выпуск: macOS, локальные управляемые сессии, один источник Claude и один источник Codex, несколько флотов.
- Исходный запуск: обычный `codex` без `--remote` и без принудительного `--no-daemon`. `codex-cli 0.166.0` — проверенная справкой версия, не установленный minimum version.
- Задачи 1–2 являются обязательным этапом 0. Задачи 3–16 выполняются только после его прохождения и фиксации конкретных транспортных решений. Если проверка не пройдена, документировать ограничение и пересмотреть дизайн; не подменять запуск молча.
- Терминал принадлежит runtime-процессу, а не окну, WebSocket или HTTP-панели. Закрытие терминала, interrupt, stop и resume имеют разные эффекты.
- Нельзя парсить экран TUI для определения готовности, ID, вопроса или принятия задачи. Неизвестное состояние сохраняется как неизвестное.
- Старые карточки без `executor` означают Claude; новые карточки записывают выбранного исполнителя явно. Новые Codex-ссылки: `codex:<runtime-uuid>`, нативный thread ID хранится отдельно.
- Сохранить `CAS`, порядок `Start → Link → Send`, семантику отложенных/неудачных коммитов и `LastWords → Archive → Stop`.
- Не перезапускать общий сервер Codex, не обходить доверие hooks, не отключать защиты ради интеграции. Не читать, не копировать и не логировать токены.
- Только один писатель. Документы и план на русском, код, комментарии и сообщения коммитов на английском. Реализация поведения через Red → Green → Refactor.
- Каждый завершённый блок проходит релевантные проверки и требуемые проектом `make test`, `make test-web`, `make lint` перед коммитом. Только явные пути staging; `git commit --signoff`, настроенную GPG-подпись сохранить. Push, PR, merge и живые пользовательские сессии не входят в исполнение этого плана автоматически.

## Порядок и границы файлов

| Задачи | Результат | Основные файлы |
| --- | --- | --- |
| 1–2 | Измеренный контракт Codex и PTY | `scripts/codexprobe/`, `docs/engineering/codex-integration.md` |
| 3–4 | Общие типы и Claude без регрессии | `internal/executor/`, `internal/claude/`, сборщик, state, orchestrator |
| 5–6 | Карточки, настройки и совместимость | `internal/board/`, `internal/config/`, шаблон доски |
| 7–9 | Живущий отдельно runtime и адаптер Codex | `internal/codexruntime/`, `internal/codex/`, служебный режим CLI |
| 10–11 | Надёжный dispatch, API и терминальный мост | `internal/orchestrator/`, `internal/server/`, wiring панели |
| 12–13 | Выбор исполнителя и Codex-only setup | `web/js/`, frontend-тесты, setup и сборщик |
| 14–15 | Общая оркестрация, ревью и архив | CLI, plugin skills, review, cleanup |
| 16 | Сквозная проверка и готовность выпуска | Стенд, CI, упаковка, руководства |

Это последовательные вертикальные блоки одной функции. Не делать параллельные изменения общего state, wiring и терминала. Каждый блок имеет собственный проверяемый результат, но включение Codex для обычного пользователя допускается только после задачи 16. До неё интеграция выключена по умолчанию.

Существующие engineering-документы написаны на английском: `codex-integration.md` документирует измерения и протокол на английском, а этот план и дизайн остаются русскими.

## Задача 1. Проверить регистрацию и управление обычным Codex

**Файлы:** создать `scripts/codexprobe/main.go`, `scripts/codexprobe/control.go`, `scripts/codexprobe/control_test.go`, `docs/engineering/codex-integration.md`.

**Вход:** раздел 11 дизайна и локальная официальная справка CLI. **Выход:** воспроизводимый probe и зафиксированные способ регистрации, источник состояния, основной канал отправки, подтверждение принятия, минимальная проверенная версия. Это исследовательский инструмент, не продуктовый адаптер.

- [ ] Зафиксировать `codex --version`, справки `codex`, `queue`, `resume`, `app-server proxy` и актуальную официальную документацию из ссылок дизайна. Не запускать restart/start общего сервера как способ исправить probe.
- [ ] Создать одноразовый Git-репозиторий через `os.MkdirTemp` и `exec.CommandContext` с argv; каталог оставить до явной уборки пользователем. Probe принимает явный тестовый CWD и не выбирает текущий рабочий репозиторий автоматически.
- [ ] Добавить тест декодирования ответов: fixture «команда принята, но thread не resumed» не даёт `accepted`; потеря соединения после отправки даёт `unknown`. Фикстуры должны содержать реальные очищенные форматы, полученные на тестовой сессии, а не придуманный wire protocol.
- [ ] Запустить `go test ./scripts/codexprobe -run 'Test(Control|Delivery)' -count=1`: сначала получить провал утверждения поведения, затем реализовать только декодер probe.
- [ ] Запустить два обычных Codex в одном тестовом CWD без задач. Проверить передачу уникального nonce через окружение и регистрацию через разрешённые hooks либо измеренный локальный API. Не выбирать thread по времени последней записи.
- [ ] Проверить доступ через документированный локальный вход, в том числе кандидат `app-server proxy`: подтверждаются принадлежность runtime, список собственных thread, состояние, история, interrupt, stop, resume и приём нового сообщения.
- [ ] На короткой тестовой задаче проверить вопрос человеку, permission request, финальный ответ, ошибку, субагента, обрыв наблюдения и повторное подключение. Отметить, какие состояния источник не различает. Hooks доверяются пользователем штатным способом, без обхода.
- [ ] В engineering-документ записать команды воспроизведения без секретов, версии, реальные источники каждого факта и таблицу прошедших/непрошедших проверок. Проверить, что `session_id` hook не ошибочно принят за ID дочернего thread.

**Критерий:** ID, принятый turn и состояние связаны с конкретным запуском. Выход `0` у `queue` сам по себе не считается успехом. Если структурированный канал не обеспечивает это, остановить основную реализацию и представить ограничение.

**Коммит:** `test(codex): record local session control probe`.

## Задача 2. Проверить PTY, восстановление экрана и живучесть

**Файлы:** добавить `scripts/codexprobe/terminal.go`, `terminal_test.go`, дополнить `main.go` и `docs/engineering/codex-integration.md`.

**Вход:** обычный запуск из задачи 1. **Выход:** выбранный PTY-механизм, проверенный способ screen replay/repaint, требования к runtime и решение о прохождении этапа 0.

- [ ] Сначала написать интеграционный тест с тестовым дочерним процессом: писатель продолжает вывод при отсутствии читателя браузера; закрытие клиента не закрывает master PTY; resize виден ребёнку. Выполнить `go test ./scripts/codexprobe -run TestTerminal -count=1` и подтвердить исходный провал.
- [ ] Реализовать минимальное открытие PTY на macOS. Сначала оценить имеющийся `x/sys`; если нужен пакет, проверить официальный источник, API, лицензию и точную версию, записать решение перед добавлением зависимости. Пробная зависимость не становится производственной автоматически.
- [ ] Проверить TUI в fullscreen и inline: Unicode, Ctrl-C, bracketed paste, многострочный ввод, resize и смену фокуса. Не добавлять `--remote` ради обхода проблемы терминала.
- [ ] Отключить клиента на время вывода, превысить размер replay-буфера и подключиться заново. Сравнить состояние экрана в headless xterm со свежим полным экраном на той же геометрии. Случайный хвост ANSI не принимать за снимок.
- [ ] Если проверенный resize вызывает корректный полный repaint — документировать необходимую последовательность; иначе выбрать терминальную модель и формат снимка, проверить её на тех же сценариях. Зафиксировать верхние границы памяти и отставания клиента.
- [ ] Завершить тестовую HTTP-панель, оставив runtime и Codex; переподключиться из новой панели. Затем отдельно проверить потерю runtime, stop и resume с историей. Не путать живой процесс с доказанной готовностью thread.
- [ ] Сверить все десять строк этапа 0 из дизайна. В engineering-документ записать основной транспорт, обязательность hooks, формат подтверждения, механизм stop, replay и minimum version; обновить соответствующие разделы дизайна и зависимые детали этого плана по измерениям.

**Критерий:** все обязательные проверки прошли. Непройденная проверка означает пересмотр решения, а не разрешение продолжать задачи 3–16 с заглушкой. Последующие контракты ниже — собственный API Fleetdeck; wire-код Codex берётся только из зафиксированного результата этапа 0.

**Коммит:** `test(codex): validate terminal lifetime and screen recovery`.

## Задача 3. Ввести общие ключи и контракт исполнителя

**Файлы:** создать `internal/executor/key.go`, `types.go`, `registry.go`, `key_test.go`, `registry_test.go`.

**Интерфейс:** общий пакет не импортирует `daemon`, `server` или `state`. Начальный контракт:

```go
type ID string
const (
    Claude ID = "claude"
    Codex ID = "codex"
)
type Key string
func ParseKey(raw string) (ID, Key, error)
type Verdict uint8
const (
    Unknown Verdict = iota
    No
    Yes
)
type DeliveryState string
type Receipt struct {
    OperationID string
    State DeliveryState
    NativeMessageID string
}
```

`DeliveryState`: `queued`, `accepted`, `failed`, `unknown`. Нативный ID допускает отсутствие. `Session` содержит поля из §4 дизайна; `Capabilities` — явные bool возможностей, `ObservedAt` и свежесть находятся в наблюдении, nullable метрики имеют источник. `StartRequest` содержит `OperationID`, `Board`, `Card`, `CWD`, `Name`; выбор настроек принадлежит адаптеру. `Message` содержит `OperationID`, `Text`. Общий `Terminal` переносится из `internal/server/pty.go` с его существующими сигнатурами; server может временно оставить type alias.

Адаптер экспортирует функции `Start(context.Context, StartRequest) (Session, error)`, `List(context.Context) ([]Session, error)`, `Send(context.Context, Key, Message) (Receipt, error)`, `Interrupt(context.Context, Key) error`, `Stop(context.Context, Key) error`, `Resume(context.Context, Key) (Session, error)`, `OpenTerminal(context.Context, Key, int, int) (Terminal, error)`, `History(context.Context, Key) ([]Entry, error)`, `LastWords(context.Context, Key) (Said, error)`. `Entry` содержит native ID, role, text, timestamp; `Said` — words, transcript reference, name, признак отсутствия финального ответа. `Registry` хранит адаптеры по `ID`; узкие consumers получают отдельные функции вместо обязательного большого интерфейса.

- [ ] Написать тест ключей, включая legacy hex и запрет неизвестного префикса:

```go
func TestParseKeyKeepsLegacyClaude(t *testing.T) {
    id, key, err := ParseKey("abc12345")
    if err != nil || id != Claude || key != Key("abc12345") {
        t.Fatalf("got %q %q %v", id, key, err)
    }
}
```

- [ ] Добавить табличные случаи Codex UUID, пустой ключ, некорректный UUID, path separators и коллизия native ID у двух адаптеров. Выполнить `go test ./internal/executor -count=1`, подтвердить RED.
- [ ] Реализовать строгий разбор и registry; ввести типизированные ошибки из дизайна. Не принимать произвольный префикс как Claude. Сделать nullable значения и трёхзначный verdict; проверить отсутствие метрики в JSON.
- [ ] Проверить GREEN и `-race`, убрать дублирование парсинга внутри нового пакета.

**Коммит:** `refactor(executor): define provider-neutral session contracts`.

## Задача 4. Перевести Claude и сборщик на общий контракт

**Файлы:** создать `internal/claude/adapter.go`, `adapter_test.go`, `internal/state/compat.go`, `compat_test.go`; изменить `internal/state/snapshot.go`, `internal/orchestrator/appoint.go`, `dispatch.go`, `cleanup.go`, `cmd/fleetdeck/collect.go`, `orchestrator.go`, `fleets.go`, `resume.go`, `cleanup.go`, `review.go` и соответствующие существующие тесты.

**Вход/выход:** `internal/claude.Adapter` реализует функции задачи 3 через прежние зависимости. `state.SessionView` использует `executor.Session`, а legacy JSON-поля формируются явно в `compat.go`, без embedding `daemon.Session` в общей модели.

- [ ] До изменения wiring добавить characterization-тесты: Claude waiting/stalled/unknown, stopped/dead, short ID против transcript UUID, label, orchestrator pin, state JSON. Сохранить реальную семантику существующих fixtures, не исправлять ожидания ради рефакторинга.
- [ ] Добавить тест сборщика: недоступный второй адаптер не убирает Claude и карточки; независимый timeout оставляет устаревшее наблюдение с причиной. Сначала запустить `go test ./internal/state ./cmd/fleetdeck -run 'Test.*(Executor|Collect|Compatibility)' -count=1`, получить поведенческий RED для нового случая.
- [ ] Обернуть прежние вызовы в адаптер; перенести преобразования в одном направлении Claude → executor. Сохранить `Agent` как роль, политику `SendFirst`, текущие признаки stalled и неизвестности.
- [ ] Перевести orchestrator list-functions на `[]executor.Session`; assembler связывает адаптеры, а не выносит I/O в `state`. Обновить тестовые doubles, сохраняя их входные факты.
- [ ] Выполнить `go test ./internal/claude ./internal/state ./internal/orchestrator ./cmd/fleetdeck -race -count=1` и регрессии frontend snapshots. Этот блок не включает Codex и должен сохранять видимое поведение Claude.

**Коммит:** `refactor(claude): route existing sessions through executor adapter`.

## Задача 5. Добавить executor в карточки и авторство документов

**Файлы:** изменить `internal/board/card.go`, `create.go`, `write.go`, `docmeta.go` и их тесты; `plugin/templates/board/scripts/new_card.py`, `validate_cards.py`, `test_new_card.py`, `test_validate_cards.py`; `plugin/templates/board/README.md`, `plugin/scripts/upgrade-board.sh`, `upgrade_board_test.go`.

**Интерфейс:** `Card.Executor` и `NewCard.Executor` типа `string`; отсутствие в старом файле нормализуется в `claude`. Новый `board.SetExecutor(path, value, expectExecutor, expectSession string) error` проверяет оба поля под блокировкой писателей Fleetdeck. Общие ключи сессий разбираются через `executor.ParseKey`; Python использует эквивалентные fixtures.

- [ ] Написать тест старой карточки после смены default: она остаётся Claude. Проверить новую Codex-карточку через существующие `emptyBoard`, `createDay`:

```go
path, err := CreateCard(emptyBoard(t), NewCard{
    Title: "Check integration", Zone: "planned", Executor: "codex",
}, createDay)
if err != nil { t.Fatal(err) }
card, err := ParseCard(path)
if err != nil || card.Executor != "codex" { t.Fatalf("%+v %v", card, err) }
```

- [ ] Добавить RED: смена executor после link запрещена; неизвестный executor и несовпадающая ссылка отклоняются; добавление отсутствующего поля сохраняет комментарии и тело; Codex автор документа открывается по своему ключу.
- [ ] Реализовать точечную запись и проверку ожидаемых полей. Изменение формата `session` провести одновременно через Go writer, `DocSession` и обе ветки Python validator.
- [ ] Обновить new-card script и upgrade живой доски: обнаруживать устаревший валидатор до Codex dispatch, показывать требуемое обновление; не выполнять upgrade на пользовательских досках во время теста.
- [ ] Запустить `go test ./internal/board ./plugin/... -race -count=1`. Проверить fixtures старой доски без массового переписывания и права панели на поля.

**Коммит:** `feat(board): support executor selection and Codex session references`.

## Задача 6. Разделить настройки исполнителей

**Файлы:** изменить `internal/config/config.go`, `write.go`, добавить `executors_test.go`; обновить `save_shape_test.go`, `documented_test.go`, `internal/userenv/userenv_test.go`; создать `internal/codex/launch.go`, `launch_test.go`.

**Интерфейс:** `Config.DefaultExecutor executor.ID`, `Config.Codex CodexConfig`; YAML `default_executor` и `executors.codex`. `CodexConfig`: `Enabled bool`, `Command []string`, `Home`, `Profile`, `Model`, `Sandbox`, `ApprovalPolicy string`. `codex.Launch` строит argv/env на основе измерений задач 1–2. Старые `Agent`/`Workers` остаются Claude-only.

- [ ] Тестировать загрузку/сохранение старого файла, комментариев, неизвестных полей и отсутствие преобразования `workers.model: opus` в Codex model. RED-сценарии: относительный home, пустой argv-элемент, неподдерживаемые значения, default для новых карточек не меняет старую.
- [ ] Реализовать сохранение без потери ручных секций; command — argv, не shell string. Не создавать новый пустой `CODEX_HOME` ради изоляции. Codex выключен по умолчанию; пустая модель сохраняет его native default.
- [ ] Добавить launch-тест с wrapper argv, пробелом в пути, Dock PATH, двумя явно разными config home; эффективные sandbox/approval не слабее выбранных пользователем.
- [ ] Выполнить `go test ./internal/config ./internal/codex ./internal/userenv -race -count=1`; документацию новых ключей добавить в обе версии configuration в этом же блоке.

**Коммит:** `feat(config): configure Codex independently from Claude workers`.

## Задача 7. Реализовать хранилище и приватный протокол runtime

**Файлы:** создать `internal/codexruntime/store.go`, `protocol.go`, `lock.go`, `store_test.go`, `protocol_test.go`, `lock_test.go`.

**Вход:** типы `executor`, измеренный протокол из этапа 0. **Выход:** durable runtime record и локальный канал panel ↔ runtime. Собственный протокол имеет version, request ID, runtime key; native Codex wire не просачивается в браузер.

- [ ] RED-тесты: файл с правами шире `0600` и каталог шире `0700` не принимаются; symlink на чужой socket/record отклоняется; устаревший PID без handshake не считается живым; запись, прерванная до rename, не заменяет корректную запись.
- [ ] Реализовать атомарную запись, межпроцессный lock и handshake; запись содержит поля §6 дизайна и журнал операции. Тест межпроцессного lock использует два subprocess, а не две goroutine.
- [ ] Добавить negotiation тесты: текущая/предыдущая версия работают, неизвестная даёт typed incompatibility без stop, секреты отсутствуют в errors/logs. Проверить обрезанные/слишком большие frames и timeout молчащего клиента.
- [ ] Выполнить `go test ./internal/codexruntime -run 'Test(Store|Protocol|Lock)' -race -count=1`. Native ID не выводить из PID; canonical board path входит в запись владения.

**Коммит:** `feat(runtime): persist Codex ownership and local protocol`.

## Задача 8. Реализовать PTY-процесс и восстановление экрана

**Файлы:** создать `internal/codexruntime/process.go`, `terminal.go`, `screen.go`, `process_test.go`, `terminal_test.go`, `screen_test.go`; добавить `cmd/fleetdeck/runtime.go`, `runtime_test.go`, изменить разбор аргументов в `main.go`.

**Интерфейс:** служебный режим `fleetdeck runtime --key <key> --state-dir <directory>` запускается до обычной инициализации панели. `codexruntime.Run(context.Context, key executor.Key, stateDir string) error` владеет PTY и приватным socket. В аргументах нет токенов или текста задачи; запуск читает ранее записанный record.

- [ ] RED-тест на тестовом ребёнке: закрытие panel connection не закрывает PTY; процесс пишет без клиентов; slow reader отключается; stdout служебного процесса не блокирует агента. Проверить, что runtime-режим не пытается занять HTTP-порт панели.
- [ ] Реализовать отделение от жизненного цикла HTTP-панели и обработку сигналов. Терминальный ребёнок имеет корректные session/controlling TTY; plain detached exec без PTY не считать реализацией.
- [ ] Реализовать выбранный в задаче 2 снимок/repaint, bounded output и явный overflow. Результат screen replay проверить в headless xterm, включая частичные ANSI sequences и другую геометрию.
- [ ] Реализовать lease одного писателя и владельца resize. Тестировать передачу lease, отключение владельца, поздний resize старого клиента и пассивного наблюдателя. Разрыв связи не делает stop.
- [ ] Выполнить `go test ./internal/codexruntime ./cmd/fleetdeck -run 'Test(Runtime|Terminal|Screen)' -race -count=1` и отдельный subprocess-тест рестарта панели. Не добавлять terminal dump на диск по умолчанию.

**Коммит:** `feat(runtime): keep Codex terminals alive across panel restarts`.

## Задача 9. Подключить наблюдение и жизненный цикл Codex

**Файлы:** создать `internal/codex/adapter.go`, `control.go`, `observe.go`, `history.go`, `adapter_test.go`, `observe_test.go`, `history_test.go`; `internal/codexruntime/events.go`, `events_test.go`; адаптер регистрации hooks — `plugin/codex/hooks/report.py`, `test_report.py`, если hooks признаны обязательными на этапе 0.

**Интерфейс:** `codex.Adapter` реализует контракт задачи 3 и использует runtime задачи 8. В wire-слое реализуются только подтверждённые методы; transport, обязательность hooks и фактический minimum version уже записаны в задаче 2.

- [ ] RED: SessionStart с nonce другого запуска не связывает thread; повтор события не дублирует сессию; SubagentStop не делает родителя idle; устаревшее наблюдение даёт Unknown, а не No.
- [ ] Реализовать state reducer и сверку после reconnect, отдельные lifecycle/activity. Неизвестные события не ломают снимок, но версия и тип события доступны в безопасной диагностике.
- [ ] Для model/context/cost/account limits передавать только подтверждённые значения со source и timestamp; unsupported оставить отсутствующими. Добавить fixtures отсутствующих и устаревших метрик, модельного переключения и двух поставщиков: их окна лимитов не объединяются.
- [ ] Реализовать Send с Receipt: запись queued не равна accepted; неопределённость после отправки не вызывает автоповтор. Если источник не позволяет сопоставить подтверждение, capability отключена и dispatch запрещён.
- [ ] Реализовать interrupt/stop/resume по измеренному контракту, проверяя владение: resume того же thread сохраняет Fleetdeck key и создаёт максимум одного владельца. История и LastWords используют проверенный API; отсутствие ответа и ошибка чтения различны.
- [ ] Если нужны hooks: report отправляет ограниченный набор полей в приватный runtime IPC, не возвращает управляющих инструкций, не сохраняет tool arguments и секреты; Python-тесты запускаются из Go так же, как существующие template-тесты.
- [ ] Выполнить `go test ./internal/codex ./internal/codexruntime -race -count=1`; подготовить одинаковые contract-сценарии List/Send/Stop/Resume/History для Claude и Codex с provider-specific fixtures.

**Коммит:** `feat(codex): manage session state delivery and recovery`.

## Задача 10. Сделать выдачу задачи восстанавливаемой

**Файлы:** создать `internal/orchestrator/operation.go`, `operation_test.go`; изменить `dispatch.go`, `dispatch_test.go`, `cmd/fleetdeck/fleets.go`, `internal/server/dispatch.go`, `dispatch_test.go`, `starting.go`.

**Интерфейс:** `orchestrator.Work` получает `OperationID string`; выбранный executor читается из карточки. Operation journal хранит key, шаги и Receipt. Локальный lock привязан к canonical board + permanent card ID; lease с журналом охватывает и повтор HTTP-запроса.

- [ ] На fake adapters записывать события; утверждение порядка:

```go
want := []string{"start", "ready", "link", "stage", "send", "accepted"}
if !slices.Equal(events, want) { t.Fatalf("events=%v", events) }
```

Fake callbacks записывают имя только при выполнении операции; send отдельно проверяет, что карточка уже содержит реальный key. Ожидаемые события не генерируются самой проверяемой функцией.

- [ ] RED для двух subprocess-панелей, повторного operation ID, новой операции на связанной карточке, сбоя записи session, `ErrWrittenNotCommitted`, падения после send до accepted и закрытия браузера во время старта.
- [ ] Реализовать `Start → Ready → Link → Stage → Send → Confirm`, durable journal и recovery без второго запуска. До Link пользовательская задача отсутствует; при неудаче Link возвращается видимая неподвязанная сессия.
- [ ] При delivery unknown сверять измеренный message ID/operation ID; если доказательств нет, оставить unknown и явное действие пользователя. Не обещать exactly-once без поддержки Codex.
- [ ] Обновить серверный body `{card, lang, operationId}`: старый клиент без operationId получает созданный сервером ID и ту же защиту от повторного запуска по карточке. Клиент нового UI сохраняет ID до финального результата.
- [ ] Выполнить `go test ./internal/orchestrator ./internal/server ./cmd/fleetdeck -run 'Test.*(Dispatch|Operation|Starting)' -race -count=1`.

**Коммит:** `feat(dispatch): recover executor starts without duplicate tasks`.

## Задача 11. Подключить общий API и терминальный мост

**Файлы:** изменить `internal/server/server.go`, `fleetdeps.go`, `api.go`, `pty.go`, `session_cards.go`, `docs.go`, соответствующие тесты; `cmd/fleetdeck/conns.go`, `fleets.go`; добавить `internal/server/executors.go`, `executors_test.go`.

**Интерфейс:** `GET /api/executors` возвращает ID, available, reason, capabilities; существующие `/api/sessions/{id}` принимают общий Key. Запросы управления и PTY проверяют принадлежность флоту. Новые ключи URL-encode целиком; старые Claude short IDs остаются aliases.

- [ ] RED-тесты маршрутов для `codex:<uuid>`, legacy ID, неизвестного executor, чужого флота и отсутствующей capability; отдельный тест документа с Codex author session.
- [ ] Переключить deps на registry/resolver. Наивный поиск Codex ID через Claude list запрещён. Ошибки транспорта отображаются как типизированные API-ошибки без содержимого credentials.
- [ ] Сохранить origin guard и терминальный token handshake; добавить динамический `writable` при смене lease. Закрытие WebSocket вызывает Close подключения Terminal, а не Stop сессии.
- [ ] Нормализовать stream ending; временный обрыв не возвращает окончательный код «сессия завершена». Claude-specific kick остаётся в его адаптере; старые frontend close codes поддерживаются.
- [ ] Выполнить `go test ./internal/server ./cmd/fleetdeck -run 'Test.*(Executor|Terminal|Fleet|Session|Docs)' -race -count=1`; проверить метрики без источника не заполняются нулями.

**Коммит:** `feat(api): expose mixed executor sessions and terminals`.

## Задача 12. Добавить выбор исполнителя и capabilities в интерфейс

**Файлы:** изменить `web/js/newcard.js`, `card.js`, `api.js`, `sessions.js`, `session.js`, `liveterminal.js`, `carddock.js`, `reader.js`, `orchestrator.js`, `main.js`, `i18n.js`, `header.js`, `web/app.css`; тесты в `web/tests/` и `web/js/_tests/`, добавить `web/tests/executors.test.js` и `web/js/executors.js`.

**Интерфейс:** `executors.js` экспортирует `canChangeExecutor(card)` и `executorOption(id, executors)`; выбор берётся из readiness API, не из модели. Новый `createCard` получает executor отдельным последним параметром, не меняя позиционные параметры старого интерфейса. Snapshot несёт общий key и capabilities.

- [ ] Написать чистый тест запрета смены:

```js
import { test } from "node:test";
import assert from "node:assert/strict";
import { canChangeExecutor } from "../js/executors.js";
test("linked card keeps its executor", () => {
  assert.equal(canChangeExecutor({ executor: "codex", session: "codex:00000000-0000-4000-8000-000000000001" }), false);
});
```

- [ ] В существующем fake DOM добавить RED: выбранный executor попадает в запрос и сохраняется после ошибки; недоступный Codex нельзя запустить; старые карточки остаются Claude; незавершённый dispatch сохраняет operationId.
- [ ] Реализовать форму и label исполнителя отдельно от модели/роли. Не показывать пользователю названия IPC, hooks или внутренних thread, кроме понятных действий setup и диагностики.
- [ ] Передавать capabilities в terminal: `followSize` включать только для измеренного Claude поведения; для Codex resize задаёт владелец. Read-only клиент не отправляет клавиши. Смена writable обновляет UI без пересоздания сессии.
- [ ] Проверить terminal reconnect, hidden pane, font resize и правило единственной поверхности оркестратора; Codex terminal из документов работает. Метрики accounts разделены, отсутствующая стоимость/лимиты показываются как недоступные.
- [ ] Запустить `node --test web/tests/executors.test.js web/tests/newcard.test.js web/tests/liveterminal.test.js web/tests/docauthor.test.js web/tests/terminalwidth.test.js`, затем полный `make test-web`. Существующие проверки геометрии Claude сохраняются.

**Коммит:** `feat(ui): select card executors and operate Codex terminals`.

## Задача 13. Сделать setup и запуск независимыми от Claude

**Файлы:** изменить `cmd/fleetdeck/main.go`, `init.go`, `init_fleet.go`, `collect.go`, `notify.go`, `notify_test.go`, `noclaude_test.go`, `setup_test.go`, `internal/server/setup.go`, `setup_test.go`, `web/js/setup.js`, `web/tests/setup.test.js`; добавить `cmd/fleetdeck/executor_setup.go`, `executor_setup_test.go`.

**Интерфейс:** readiness каждой интеграции отдельно: binary, version, control, registration/trust, board format. `codex only` не требует Anthropic Keychain/statusline или демона Claude. Setup сохраняет Codex настройки через Config задачи 6.

- [ ] RED-сценарии: Codex ready при отсутствии Claude; Claude ready при отсутствии Codex; обе интеграции недоступны; hooks ожидают доверия; board validator старой версии; CLI несовместим; бинарник найден из Dock.
- [ ] Реализовать необязательные адаптеры и понятные причины недоступности. Auth выполняет Codex штатно; Fleetdeck не копирует credential files и не пытается чинить login самостоятельно.
- [ ] Добавить явное действие установки hook-интеграции, только если задача 2 установила необходимость. Setup показывает содержание и штатный шаг доверия, сохраняет чужие настройки. Изменённый hash hooks снова требует review.
- [ ] Проверить уведомления Codex о подтверждённом ожидании, ошибке и тишине через существующие настройки. Повтор события после reconnect не создаёт второго баннера; потеря наблюдения не изображается новым вопросом пользователя. Добавить эти сценарии в `notify_test.go`.
- [ ] Проверить запуск панели, чтение доски, notifications и отключённые метрики без Claude. Выполнить `go test ./cmd/fleetdeck ./internal/server -run 'Test.*(Setup|Claude|Executor|Init)' -race -count=1` и `node --test web/tests/setup.test.js`.

**Коммит:** `feat(setup): support Codex-only and mixed installations`.

## Задача 14. Дать обоим оркестраторам общий интерфейс команд

**Файлы:** создать `cmd/fleetdeck/sessionctl.go`, `sessionctl_test.go`; изменить `main.go`, `internal/orchestrator/brief.go`, `brief_test.go`, `docs/en/orchestrator.md`, `docs/ru/orchestrator.md`; `plugin/skills/fleet-orchestrator/SKILL.md`, `card-keeping/SKILL.md`; добавить `plugin/codex/README.md` и manifest только по подтверждённому на этапе реализации официальному формату установки.

**Контракт CLI:** команды ниже обращаются к HTTP API Fleetdeck, принимают `--fleet`, адрес панели из конфигурации, а локальный token читают из приватного credential provider. Token не передаётся argv; HTTP клиент не обходит origin/auth policy.

```text
fleetdeck sessions list --fleet NAME --json
fleetdeck sessions start --fleet NAME --card PATH --operation-id UUID --json
fleetdeck sessions send --fleet NAME --session KEY --operation-id UUID --stdin --json
fleetdeck sessions resume --fleet NAME --session KEY --json
```

`send` читает содержимое stdin, возвращает Receipt и завершает работу ненулевым кодом при failed/unknown; queued показывается отдельно и не называется accepted. `start` возвращает существующий Result и operation ID. Эти команды — новый интерфейс Fleetdeck, а не утверждение о синтаксисе Codex.

- [ ] RED: fake HTTP server проверяет fleet и operation ID; auth отсутствует в URL/argv/ошибках; unknown delivery даёт ненулевой exit; repeated start возвращает тот же key. Проверить EOF stdin и ограничение объёма по серверному лимиту body.
- [ ] Реализовать команды и добавить их в brief; оркестратор выбирает executor из карточки, не запускает `claude-agents` для Codex и не меняет session сам.
- [ ] Обновить оба навыка: `Start → Link → Send` принадлежит Fleetdeck, key непрозрачный, ошибку отправки нельзя исправлять слепым повтором. Упаковка Codex использует тот же источник инструкций, а не копию, расходящуюся по смыслу.
- [ ] Проверить закрепление Codex оркестратора, legacy Claude pin и labels с native UUID fallback. Инструкции не перезаписывают пользовательские `AGENTS.md` и сторонние навыки.
- [ ] Выполнить `go test ./cmd/fleetdeck ./internal/orchestrator ./plugin/... -race -count=1`. На тестовом флоте проверить Claude orchestrator → Codex worker и обратное направление отдельными живыми сценариями.

**Коммит:** `feat(orchestrator): dispatch mixed workers through Fleetdeck commands`.

## Задача 15. Замкнуть ревью, последнее слово и архив

**Файлы:** изменить `cmd/fleetdeck/review.go`, `cleanup.go`, `internal/server/review.go`, `stagebyhand.go`, `internal/orchestrator/cleanup.go`, `internal/board/archive.go`, соответствующие тесты и `web/tests/review.test.js`.

**Вход:** executor LastWords/History/Stop и Receipt. **Выход:** полный цикл review reply → delivery confirmation → archive → graceful stop, без удаления истории.

- [ ] RED-сценарии: отзыв отправляется Codex; чужой fleet/worktree отклонён; ошибка History оставляет runtime работающим; отсутствие final answer сохраняет пометку; повтор cleanup не дублирует запись архива.
- [ ] Записать порядок callbacks и проверить:

```go
want := []string{"words", "archive", "stop"}
if !slices.Equal(events, want) { t.Fatalf("cleanup order: %v", events) }
```

Для ошибки archive отдельный тест требует отсутствие `stop` в events и живую сессию в List; существующий Unpin проверяется независимо, его ошибка не теряет результат.

- [ ] Реализовать маршрут через adapter, сохраняя git review/replies формат и ограничения worktree. При unknown delivery не считать отзыв доставленным и не создавать второй turn автоматически.
- [ ] Записать в архив key, executor, последнее слово и историю; Stop подтверждается состоянием runtime, а не только закрытием TUI. Повтор уже выполненной операции возвращает её результат.
- [ ] Выполнить `go test ./internal/orchestrator ./internal/board ./internal/server ./cmd/fleetdeck -run 'Test.*(Review|Cleanup|Archive)' -race -count=1` и `node --test web/tests/review.test.js`.

**Коммит:** `feat(review): complete Codex work with preserved history`.

## Задача 16. Проверить смешанный флот и упаковку

**Файлы:** добавить `scripts/codexstand/main.go`, `main_test.go`; изменить `cmd/fleetdeck/dist_test.go`, `shutdown_test.go`, `cmd/fleetdeck-window/watch_test.go`, `internal/supervisor/update_test.go` по требуемым сценариям, `.github/workflows/ci.yaml`, `Makefile`; обновить `README.md`, `README.ru.md`, `docs/en/{getting-started,configuration,board-convention,limitations}.md`, соответствующие `docs/ru/`, `docs/engineering/{packages,live-terminal,window-and-panel,update-from-release}.md`.

**Интерфейс стенда:** `go run ./scripts/codexstand --live --board <test-board>` запускает только явно разрешённые тестовые сессии; без `--live` используются тестовые дочерние процессы и fixtures, без вызовов модели. Стенд записывает исходы и версии, не credentials и не полные терминальные логи.

- [ ] RED упаковки: release binary содержит служебный runtime-режим; его запуск не зависит от checkout; новая панель принимает runtime прежней версии и отказывает несовместимой без остановки. Тест не заменяет живой runtime пользователя.
- [ ] Реализовать стенд сценариев: две карточки разных исполнителей → два терминала → сообщение → вопрос/approval → рестарт панели → продолжение остановленной сессии → review → archive. Добавить документ автора Codex, отсутствие Claude и две вкладки с одним писателем.
- [ ] В crash-тестах пройти каждую границу dispatch и archive, replay overflow, потерю control API и runtime. Зафиксировать отсутствие дубликатов, сохранность session link и различение unknown/dead. Проверки не полагаются на задержку фиксированного sleep: ждать наблюдаемое условие с deadline.
- [ ] В CI включить все новые Go/Python/frontend-тесты в существующие runners. Обычный CI не требует Codex account или доступа к модели. macOS PTY-тесты выполняются на macOS, общий код и fixtures продолжают собираться на Linux.
- [ ] Пройти обязательные команды:

```sh
make test
make test-web
make lint
make build
make verify-ldflags
```

- [ ] Выполнить отдельный live stand с тестовыми аккаунтами/сессиями, записать измеренные результаты в engineering-документ; packaging gates запускать согласно текущим Makefile и CI, без публикации или notarization автоматически.
- [ ] Сверить главы руководств: setup обоих исполнителей, выбор карточки, метрики, авторство документов, CLI оркестрации, stop/resume, неизвестная доставка, старые валидаторы, невозможность downgrade с Codex-ссылками. Обновить design status и список реальных ограничений.
- [ ] Только после всех проверок разрешить обычное включение интеграции. Подготовить minor-release описание на английском без private details; сам выпуск, push и PR выполняются по отдельной авторизации.

**Коммит:** `test(executor): verify mixed fleets recovery and release packaging`.

## Проверка покрытия перед завершением

| Требование дизайна | Задачи и доказательство |
| --- | --- |
| Обычный CLI, контроль и minimum version | 1–2: воспроизводимые измерения, без предположения о `--remote` |
| Общие типы и регрессии Claude | 3–4: parser, adapter contract, snapshots, native state semantics |
| Карточки, документы и legacy | 5: Go/Python fixtures, upgrade старой доски; 11–12: документный terminal |
| Живой терминал и независимый процесс | 2, 7–8: subprocess tests, screen comparison, protocol negotiation |
| Состояния, history, metrics | 4, 9, 11–13: Unknown и freshness, nullable values, Codex-only setup |
| Нет повторной выдачи задачи | 7, 10: межпроцессный lock, journal, crash boundaries, Receipt |
| Смешанные оркестраторы и pins | 3–4, 14: resolver, общий CLI и двунаправленный live scenario |
| Review и безопасное завершение | 15: Archive до Stop, ошибки истории, idempotent cleanup |
| Доверие hooks и границы доступа | 1–2, 7, 9, 11, 13–14: trust readiness, private IPC, fleet isolation |
| Обновление приложения | 7–8, 16: совместимость runtime, checkout-independent bundle |
| Полная функция | 16: отдельный live stand, оба терминала и весь рабочий цикл |

## Правила выполнения и завершения плана

Здесь не отмечены выполненными проверки, которых ещё не было. Сейчас подтверждены только база проекта и справки команд, перечисленные в дизайне. При исполнении отмечать checkbox после наблюдаемого результата, сохранять причину непрошедшего сценария и не ослаблять валидный тест ради продвижения.

На входе исполнения повторно проверить status/worktrees, актуальные `AGENTS.md`, README, Makefile и изменения master с даты плана. Работать на feature-ветке в изоляции; не менять ветку поверх чужих правок. Технические факты этапа 0 и новые зависимости сверять с текущими официальными источниками. После двух неудачных попыток исследовать причину по логам и документации, а не продолжать подбор флагов.

После задачи 2 — обязательная проверка достаточности доказательств и актуализация зависимых контрактов. Между дальнейшими задачами — проверка соответствия дизайну, изменение только назначенных файлов и небольшой подписанный коммит. Перед финальным заявлением о готовности — свежие обязательные проверки и evidence live stand. Этот документ сам по себе не подтверждает работу Codex-интеграции.
