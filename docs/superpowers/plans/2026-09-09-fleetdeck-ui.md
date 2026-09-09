# fleetdeck: интерфейс и поставка — план реализации

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** довести fleetdeck до состояния, в котором человек ставит его одной командой, открывает вкладку и управляет флотом: слева оркестратор, по центру канбан, справа список сессий, сверху лимиты и счётчик ожидающих.

**Architecture:** фронтенд без сборщика и без фреймворка — обычные ES-модули и CSS, встроенные в бинарь через `go:embed`. Единственная сторонняя библиотека, xterm.js, вендорится файлами в репозиторий, поэтому у пользователя нет ни npm, ни node. Состояние приходит одним снимком по WebSocket раз в секунду, записи идут отдельными HTTP-запросами.

**Tech Stack:** Go 1.25 с `embed`, ES-модули без транспиляции, CSS без препроцессора, xterm.js 5.x вендором, launchd для автозапуска, GitHub Actions для релизов.

**Spec:** `docs/superpowers/specs/2026-09-09-fleetdeck-design.md`

**Предшествующий план:** `docs/superpowers/plans/2026-09-09-fleetdeck-server.md` — сервер и данные. Второй план начинается на рабочем сервере и не трогает `internal/`, кроме отдачи статики.

## Global Constraints

- Никакого npm, node и сборщиков: фронтенд отдаётся как есть, сторонние библиотеки вендорятся файлами с их лицензиями.
- Фронтенд читает состояние только из снимка по WebSocket и пишет только двумя способами: ввод в сессию и правка полей `stage` и `progress`.
- Интерфейс не додумывает: значение, помеченное сервером как оценка, показывается с пометкой; отсутствующее значение показывается как отсутствующее, а не как ноль.
- Счётчик «ждут ответа» и полосы лимитов видны всегда и не скрываются ни на одном экране.
- Комментарии в коде, сообщения интерфейса на английском в `web/`, русская локализация — отдельным словарём.
- Документация ведётся на двух языках: `README.md` и `docs/en/` английские, `README.ru.md` и `docs/ru/` русские, обе версии правятся одним коммитом.
- Значения `stage`: `new`, `active`, `review`, `done`, `blocked`. Значения `zone`: `urgent`, `unplanned`, `planned`, `niceToHave`. Значения `progress`: 0, 10, 20, 40, 60, 80, 100.
- Сервер слушает только `127.0.0.1`.

---

## Структура файлов

| Файл | Ответственность |
| --- | --- |
| `internal/server/static.go` | Встраивание `web/` и отдача статики |
| `web/index.html` | Каркас окна: шапка и три колонки |
| `web/app.css` | Вся вёрстка и темы |
| `web/js/store.js` | Подключение к WebSocket, хранение снимка, подписки |
| `web/js/api.js` | Записи: ввод в сессию, правка полей карточки |
| `web/js/i18n.js` | Словари en и ru, выбор языка |
| `web/js/header.js` | Шапка: лимиты и счётчик ожидающих |
| `web/js/sessions.js` | Правая колонка: список сессий с контекстом |
| `web/js/board.js` | Центр: канбан |
| `web/js/card.js` | Панель карточки поверх доски |
| `web/js/orchestrator.js` | Левая колонка: переписка и ввод |
| `web/js/session.js` | Панель сессии: выжимка и живой терминал |
| `web/js/docs.js` | Раздел документации |
| `web/js/markdown.js` | Отрисовка markdown со связями и обратными ссылками |
| `web/vendor/xterm.js` | Вендор, из дистрибутива проекта |
| `web/vendor/xterm.css` | Вендор |
| `web/vendor/LICENSE.xterm` | Лицензия вендора |
| `cmd/fleetdeck/init.go` | Подкоманда `init`: конфигурация, launchd, statusline, доска |
| `plugin/` | Переехавший плагин Claude Code |
| `.github/workflows/release.yaml` | Публикация релизных архивов по тегу |
| `README.md`, `docs/en/`, `docs/ru/` | Документация |

Каждый модуль в `web/js/` экспортирует одну функцию отрисовки и подписывается на хранилище сам. Модули не знают друг о друге и общаются только через хранилище.

---

## Task 1: Встраивание статики и каркас окна

**Files:**
- Create: `internal/server/static.go`, `internal/server/static_test.go`, `web/index.html`, `web/app.css`, `web/js/store.js`
- Modify: `internal/server/server.go` — добавить отдачу статики на `/`

**Interfaces:**
- Consumes: `server.New(d Deps)` из первого плана
- Produces: `//go:embed` файловая система `web`, маршрут `GET /` с отдачей `index.html` и статики; в браузере — глобальный `store` с методами `subscribe(fn)` и `get()`

- [ ] **Step 1: Написать падающий тест отдачи статики**

```go
// internal/server/static_test.go
package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIndexIsServedAtRoot(t *testing.T) {
	d, _ := testDeps()
	rec := httptest.NewRecorder()
	New(d).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 at root, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "fleetdeck") {
		t.Fatal("index.html must be served at root")
	}
}

func TestStaticAssetIsServed(t *testing.T) {
	d, _ := testDeps()
	rec := httptest.NewRecorder()
	New(d).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/app.css", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 for app.css, got %d", rec.Code)
	}
}

func TestMissingAssetIs404NotIndex(t *testing.T) {
	d, _ := testDeps()
	rec := httptest.NewRecorder()
	New(d).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/js/nope.js", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("a missing asset must be 404, not a silent index page: got %d", rec.Code)
	}
}

func TestAPIRoutesStillWinOverStatic(t *testing.T) {
	d, _ := testDeps()
	rec := httptest.NewRecorder()
	New(d).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/snapshot", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "cards") {
		t.Fatalf("static handler must not shadow the API: %d %s", rec.Code, rec.Body.String())
	}
}
```

- [ ] **Step 2: Прогнать и убедиться, что падает.** Команда `go test ./internal/server/ -run Static -v`, ожидается FAIL: маршрута нет.

- [ ] **Step 3: Реализовать встраивание**

```go
// internal/server/static.go
package server

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed all:../../web
var webFS embed.FS

// staticHandler serves the embedded frontend. A missing file is a 404: silently
// falling back to index.html would hide a typo in an import path until runtime.
func staticHandler() http.Handler {
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		panic("embedded web directory is missing: " + err.Error())
	}
	return http.FileServerFS(sub)
}
```

В `internal/server/server.go` добавить последним маршрутом:

```go
	mux.Handle("GET /", staticHandler())
```

- [ ] **Step 4: Написать каркас окна**

```html
<!-- web/index.html -->
<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>fleetdeck</title>
  <link rel="stylesheet" href="/app.css">
  <link rel="stylesheet" href="/vendor/xterm.css">
</head>
<body>
  <header id="header"></header>
  <main>
    <section id="orchestrator" class="col col-orchestrator"></section>
    <section id="center" class="col col-center">
      <nav id="tabs"></nav>
      <div id="board"></div>
      <div id="docs" hidden></div>
      <div id="card-panel" hidden></div>
    </section>
    <aside id="sessions" class="col col-sessions"></aside>
  </main>
  <script type="module" src="/js/main.js"></script>
</body>
</html>
```

- [ ] **Step 5: Написать хранилище**

```js
// web/js/store.js
// One snapshot arrives per second over the socket. Every module reads from
// here; no module fetches state on its own.

const listeners = new Set();
let snapshot = null;
let connected = false;

export function subscribe(fn) {
  listeners.add(fn);
  if (snapshot) fn(snapshot, connected);
  return () => listeners.delete(fn);
}

export function get() {
  return snapshot;
}

export function isConnected() {
  return connected;
}

function emit() {
  for (const fn of listeners) fn(snapshot, connected);
}

export function connect() {
  const url = `ws://${location.host}/ws`;
  let socket;

  const open = () => {
    socket = new WebSocket(url);
    socket.onmessage = (ev) => {
      try {
        snapshot = JSON.parse(ev.data);
      } catch (err) {
        console.error("snapshot is not JSON", err);
        return;
      }
      connected = true;
      emit();
    };
    socket.onclose = () => {
      // A dropped socket is a visible state, not a silent one: a stale board
      // that looks live is worse than an empty one that says it is stale.
      connected = false;
      emit();
      setTimeout(open, 1000);
    };
    socket.onerror = () => socket.close();
  };

  open();
}
```

- [ ] **Step 6: Написать минимальный `main.js` и проверить в браузере**

```js
// web/js/main.js
import { connect, subscribe } from "./store.js";

subscribe((snap, connected) => {
  document.title = connected ? `fleetdeck (${snap.sessions?.length ?? 0})` : "fleetdeck — offline";
});

connect();
```

- [ ] **Step 7: Прогнать тесты и проверить руками.** Команды `go test ./internal/server/` (ожидается PASS, девять тестов) и `make build && ./bin/fleetdeck`, затем открыть `http://127.0.0.1:7777` — заголовок вкладки должен показать число сессий, а при остановленном сервере смениться на `offline` в течение секунды.

- [ ] **Step 8: Коммит**

```bash
git add internal/server web
git commit --signoff -m "feat(web): embed the frontend and stream snapshots over a socket"
```

---

## Task 2: Шапка с лимитами и счётчиком

**Files:**
- Create: `web/js/header.js`, `web/js/i18n.js`
- Modify: `web/js/main.js`, `web/app.css`

**Interfaces:**
- Consumes: `subscribe` из `store.js`; поля снимка `limits`, `sessions`, `usageError`, `daemonError`
- Produces: `renderHeader(root)`; `t(key)` из `i18n.js`

- [ ] **Step 1: Написать словарь**

```js
// web/js/i18n.js
// The interface ships two languages. The dictionary is flat on purpose: a
// missing key must be visible as the key itself, not as an empty string.

const dictionaries = {
  en: {
    waiting_count: "waiting for you",
    limit_5h: "5h",
    limit_7d: "7d",
    resets_in: "resets in",
    daemon_down: "daemon unavailable",
    usage_down: "limits unavailable",
    board_empty: "no cards",
    estimated: "estimate",
    context: "context",
    offline: "disconnected",
  },
  ru: {
    waiting_count: "ждут ответа",
    limit_5h: "5ч",
    limit_7d: "7д",
    resets_in: "сброс через",
    daemon_down: "демон недоступен",
    usage_down: "лимиты недоступны",
    board_empty: "карточек нет",
    estimated: "оценка",
    context: "контекст",
    offline: "нет связи",
  },
};

const lang = (navigator.language || "en").startsWith("ru") ? "ru" : "en";

export function t(key) {
  const value = dictionaries[lang][key];
  return value === undefined ? key : value;
}

export function currentLanguage() {
  return lang;
}
```

- [ ] **Step 2: Написать шапку**

```js
// web/js/header.js
import { subscribe } from "./store.js";
import { t } from "./i18n.js";

// humanDuration turns a future timestamp into a short "3h 20m" string.
function humanDuration(iso) {
  const ms = new Date(iso).getTime() - Date.now();
  if (!isFinite(ms) || ms <= 0) return "0m";
  const minutes = Math.floor(ms / 60000);
  const days = Math.floor(minutes / 1440);
  const hours = Math.floor((minutes % 1440) / 60);
  if (days > 0) return `${days}d ${hours}h`;
  if (hours > 0) return `${hours}h ${minutes % 60}m`;
  return `${minutes}m`;
}

function gauge(label, window) {
  if (!window) return `<span class="gauge gauge-off">${label} —</span>`;
  const pct = Math.round(window.utilization);
  const level = pct >= 90 ? "hot" : pct >= 60 ? "warm" : "cool";
  return `
    <span class="gauge gauge-${level}" title="${t("resets_in")} ${humanDuration(window.resetsAt)}">
      ${label}
      <span class="track"><i style="width:${Math.min(pct, 100)}%"></i></span>
      ${pct}%
    </span>`;
}

export function renderHeader(root) {
  subscribe((snap, connected) => {
    const waiting = (snap.sessions ?? []).filter((s) => s.needs || s.status === "waiting").length;
    const problems = [];
    if (!connected) problems.push(t("offline"));
    if (snap.daemonError) problems.push(t("daemon_down"));
    if (snap.usageError) problems.push(t("usage_down"));

    root.innerHTML = `
      <div class="brand">fleetdeck</div>
      <div class="limits">
        ${snap.limits ? gauge(t("limit_5h"), snap.limits.fiveHour) : gauge(t("limit_5h"), null)}
        ${snap.limits ? gauge(t("limit_7d"), snap.limits.sevenDay) : gauge(t("limit_7d"), null)}
        ${problems.length ? `<span class="problem">${problems.join(" · ")}</span>` : ""}
        <span class="waiting ${waiting > 0 ? "waiting-on" : ""}">${waiting} ${t("waiting_count")}</span>
      </div>`;
  });
}
```

- [ ] **Step 3: Подключить в `main.js`**

```js
// web/js/main.js
import { connect, subscribe } from "./store.js";
import { renderHeader } from "./header.js";

renderHeader(document.getElementById("header"));
connect();
```

- [ ] **Step 4: Проверить три состояния руками.** Запустить `./bin/fleetdeck` и убедиться: при работающих лимитах обе полосы показывают проценты и подсказку со временем сброса; при `usage_enabled: false` в конфигурации полосы показывают прочерк, а не ноль процентов; при остановленном демоне в шапке появляется «демон недоступен», но полосы и счётчик остаются на месте. Ноль процентов вместо прочерка считать провалом задачи: это ровно та ложь, против которой писалась спека.

- [ ] **Step 5: Коммит**

```bash
git add web
git commit --signoff -m "feat(web): header with account limits and the waiting counter"
```

---

## Task 3: Список сессий

**Files:**
- Create: `web/js/sessions.js`
- Modify: `web/js/main.js`, `web/app.css`, `web/js/i18n.js`

**Interfaces:**
- Consumes: `subscribe`; поля сессии `id`, `name`, `state`, `status`, `needs`, `startedAt`, `context`, `cardPath`
- Produces: `renderSessions(root, onSelect)` — вызывает `onSelect(sessionId)` при клике

- [ ] **Step 1: Написать модуль**

```js
// web/js/sessions.js
import { subscribe } from "./store.js";
import { t } from "./i18n.js";

function isWaiting(s) {
  return Boolean(s.needs) || s.status === "waiting";
}

// contextBar renders occupancy. An absent value renders as absent: a zero bar
// would claim the session is empty, which is a different and false statement.
function contextBar(ctx) {
  if (!ctx) return `<div class="ctx ctx-unknown">${t("context")} —</div>`;
  const pct = ctx.percent ?? (ctx.window ? (ctx.tokens / ctx.window) * 100 : null);
  if (pct === null) return `<div class="ctx ctx-unknown">${t("context")} —</div>`;
  const rounded = Math.round(pct);
  const level = rounded >= 80 ? "hot" : rounded >= 50 ? "warm" : "cool";
  const mark = ctx.estimated ? ` <span class="est" title="${t("estimated")}">~</span>` : "";
  return `
    <div class="ctx ctx-${level}">
      <span class="track"><i style="width:${Math.min(rounded, 100)}%"></i></span>
      ${rounded}%${mark}
    </div>`;
}

function silentFor(startedAt) {
  if (!startedAt) return "";
  const minutes = Math.floor((Date.now() - startedAt) / 60000);
  return minutes > 0 ? `${minutes}m` : "";
}

export function renderSessions(root, onSelect) {
  subscribe((snap) => {
    const sessions = [...(snap.sessions ?? [])];
    // Waiting sessions come first: the whole point of the panel is that they
    // are impossible to miss.
    sessions.sort((a, b) => Number(isWaiting(b)) - Number(isWaiting(a)));

    if (snap.daemonError) {
      root.innerHTML = `<div class="empty">${t("daemon_down")}</div>`;
      return;
    }

    root.innerHTML = sessions
      .map(
        (s) => `
        <article class="srow ${isWaiting(s) ? "srow-waiting" : ""}" data-id="${s.id}">
          <div class="sname">${s.name || s.title || s.id}</div>
          <div class="smeta">${s.state ?? ""} ${silentFor(s.startedAt)}</div>
          ${contextBar(s.context)}
          ${s.cardPath ? `<div class="scard" data-card="${s.cardPath}">↗</div>` : ""}
        </article>`,
      )
      .join("");

    for (const el of root.querySelectorAll(".srow")) {
      el.addEventListener("click", () => onSelect(el.dataset.id));
    }
  });
}
```

- [ ] **Step 2: Подключить и добавить ключи словаря.** В `web/js/main.js` вызвать `renderSessions(document.getElementById("sessions"), openSession)`, где `openSession` пока просто пишет id в `console.log`. В `i18n.js` ключи `context`, `estimated`, `daemon_down` уже есть.

- [ ] **Step 3: Проверить три состояния руками.** Поднять две сессии, одну довести до вопроса. Убедиться: ожидающая стоит первой и выделена; у сессии без транскрипта контекст показан прочерком, а не нулём; при остановленном демоне колонка показывает «демон недоступен», а доска в центре продолжает работать. Ноль вместо прочерка считать провалом задачи.

- [ ] **Step 4: Коммит**

```bash
git add web
git commit --signoff -m "feat(web): session list with context and waiting first"
```

---

## Task 4: Канбан

**Files:**
- Create: `web/js/board.js`
- Modify: `web/js/main.js`, `web/app.css`, `web/js/i18n.js`

**Interfaces:**
- Consumes: `subscribe`; поля карточки `path`, `zone`, `stage`, `progress`, `session`, `title`, `parseError`
- Produces: `renderBoard(root, onOpenCard)`

- [ ] **Step 1: Написать модуль**

```js
// web/js/board.js
import { subscribe } from "./store.js";
import { t } from "./i18n.js";

const STAGES = ["new", "active", "review", "blocked", "done"];

function cardHTML(c, waitingSessions) {
  if (c.parseError) {
    return `<article class="kcard kcard-broken" data-path="${c.path}">
      <div class="ktitle">${c.path.split("/").pop()}</div>
      <div class="kbroken">${t("card_broken")}: ${c.parseError}</div>
    </article>`;
  }
  const waiting = c.session && waitingSessions.has(c.session);
  return `
    <article class="kcard zone-${c.zone || "none"} ${waiting ? "kcard-waiting" : ""}" data-path="${c.path}">
      <div class="ktitle">${c.title || c.path.split("/").pop()}</div>
      ${c.session ? `<div class="ksession">${c.session}</div>` : ""}
      <div class="kprog"><i style="width:${c.progress ?? 0}%"></i></div>
    </article>`;
}

export function renderBoard(root, onOpenCard) {
  subscribe((snap) => {
    if (snap.boardError) {
      root.innerHTML = `<div class="empty">${snap.boardError}</div>`;
      return;
    }
    const cards = snap.cards ?? [];
    const waitingSessions = new Set(
      (snap.sessions ?? []).filter((s) => s.needs || s.status === "waiting").map((s) => s.id),
    );

    root.innerHTML = STAGES.map((stage) => {
      const inStage = cards.filter((c) => c.stage === stage);
      return `
        <div class="kcol" data-stage="${stage}">
          <h5>${stage} <span class="kcount">${inStage.length}</span></h5>
          ${inStage.map((c) => cardHTML(c, waitingSessions)).join("")}
        </div>`;
    }).join("");

    for (const el of root.querySelectorAll(".kcard")) {
      el.addEventListener("click", () => onOpenCard(el.dataset.path));
    }
  });
}
```

- [ ] **Step 2: Добавить ключи и стили.** В `i18n.js` добавить `card_broken` со значениями `"unreadable card"` и `"карточка не разбирается"`. В `app.css` описать колонки, цвет левой кромки по классу `zone-*` (`urgent` красный, `unplanned` оранжевый, `planned` синий, `niceToHave` серый) и полосу прогресса.

- [ ] **Step 3: Проверить руками.** Открыть панель на настоящей доске и убедиться: карточки разложены по пяти колонкам, счётчики в заголовках совпадают с числом карточек, цвет кромки соответствует зоне, у карточек ожидающих сессий видна пометка. Отдельно положить в каталог доски файл с битым frontmatter и убедиться, что он показан как «не разбирается», а остальные карточки на месте.

- [ ] **Step 4: Коммит**

```bash
git add web
git commit --signoff -m "feat(web): kanban board grouped by stage and coloured by zone"
```

---

## Task 5: Панель карточки

**Files:**
- Create: `web/js/card.js`, `web/js/markdown.js`, `web/js/api.js`
- Modify: `web/js/main.js`, `web/app.css`, `web/js/i18n.js`

**Interfaces:**
- Consumes: `subscribe`, `get`; поля карточки; `PATCH /api/cards`
- Produces: `renderCard(root, path, onClose)`; `setCardField(path, field, value)` из `api.js`; `renderMarkdown(text, cardsByName)` из `markdown.js`

Панель открывается поверх доски и закрывается по `Esc` или клику вне её. Поля `stage` и `progress` правятся селектами, тело карточки только показывается.

- [ ] **Step 1: Написать клиент записи**

```js
// web/js/api.js
// The panel performs exactly two kinds of write. Everything else is read-only.

async function post(url, body, method = "POST") {
  const res = await fetch(url, {
    method,
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!res.ok) {
    let detail = res.statusText;
    try {
      detail = (await res.json()).error ?? detail;
    } catch (_) {
      // keep the status text
    }
    throw new Error(detail);
  }
}

export function setCardField(path, field, value) {
  return post("/api/cards", { path, field, value }, "PATCH");
}

export function sendText(sessionId, text, submit = true) {
  return post(`/api/sessions/${encodeURIComponent(sessionId)}/text`, { text, submit });
}

export function sendKeys(sessionId, keys) {
  return post(`/api/sessions/${encodeURIComponent(sessionId)}/keys`, { keys });
}
```

- [ ] **Step 2: Написать отрисовку markdown со связями**

```js
// web/js/markdown.js
// A deliberately small renderer: headings, lists, code, bold and wiki links.
// A full markdown implementation is not needed to read a card, and a vendored
// one would be a dependency we cannot justify.

function escapeHTML(s) {
  return s.replace(/[&<>"]/g, (ch) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[ch]);
}

export function renderMarkdown(text, knownCards) {
  const lines = escapeHTML(text ?? "").split("\n");
  const out = [];
  let inCode = false;
  let inList = false;

  for (const line of lines) {
    if (line.startsWith("```")) {
      out.push(inCode ? "</code></pre>" : "<pre><code>");
      inCode = !inCode;
      continue;
    }
    if (inCode) {
      out.push(line);
      continue;
    }
    if (/^-\s+/.test(line)) {
      if (!inList) {
        out.push("<ul>");
        inList = true;
      }
      out.push(`<li>${inline(line.replace(/^-\s+/, ""), knownCards)}</li>`);
      continue;
    }
    if (inList) {
      out.push("</ul>");
      inList = false;
    }
    const heading = line.match(/^(#{1,4})\s+(.*)$/);
    if (heading) {
      const level = heading[1].length + 1;
      out.push(`<h${level}>${inline(heading[2], knownCards)}</h${level}>`);
      continue;
    }
    out.push(line.trim() === "" ? "" : `<p>${inline(line, knownCards)}</p>`);
  }
  if (inList) out.push("</ul>");
  if (inCode) out.push("</code></pre>");
  return out.join("\n");
}

function inline(text, knownCards) {
  return text
    .replace(/\[\[([^\]|#]+)\]\]/g, (_, name) => {
      const known = knownCards.has(name.trim());
      const cls = known ? "wikilink" : "wikilink wikilink-missing";
      return `<a class="${cls}" data-link="${name.trim()}">${name.trim()}</a>`;
    })
    .replace(/`([^`]+)`/g, "<code>$1</code>")
    .replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>");
}
```

- [ ] **Step 3: Написать панель карточки**

```js
// web/js/card.js
import { get, subscribe } from "./store.js";
import { setCardField } from "./api.js";
import { renderMarkdown } from "./markdown.js";
import { t } from "./i18n.js";

const STAGES = ["new", "active", "review", "blocked", "done"];
const PROGRESS = [0, 10, 20, 40, 60, 80, 100];

function baseName(path) {
  return path.split("/").pop().replace(/\.md$/, "");
}

export function renderCard(root, path, onClose) {
  const draw = (snap) => {
    const card = (snap.cards ?? []).find((c) => c.path === path);
    if (!card) {
      root.innerHTML = `<div class="empty">${t("card_gone")}</div>`;
      return;
    }
    const known = new Set((snap.cards ?? []).map((c) => baseName(c.path)));
    const backlinks = (snap.cards ?? []).filter((c) => (c.links ?? []).includes(baseName(path)));

    root.hidden = false;
    root.innerHTML = `
      <div class="card-head">
        <h3>${card.title || baseName(path)}</h3>
        <button class="card-close">✕</button>
      </div>
      <div class="card-fields">
        <label>stage
          <select data-field="stage">
            ${STAGES.map((s) => `<option ${s === card.stage ? "selected" : ""}>${s}</option>`).join("")}
          </select>
        </label>
        <label>progress
          <select data-field="progress">
            ${PROGRESS.map((p) => `<option ${p === card.progress ? "selected" : ""}>${p}</option>`).join("")}
          </select>
        </label>
        ${card.session ? `<span class="card-session">${card.session}</span>` : ""}
      </div>
      <div class="card-error" hidden></div>
      <div class="card-body">${renderMarkdown(card.body, known)}</div>
      ${
        backlinks.length
          ? `<div class="card-backlinks"><h4>${t("backlinks")}</h4>${backlinks
              .map((b) => `<a data-link="${baseName(b.path)}">${b.title || baseName(b.path)}</a>`)
              .join("")}</div>`
          : ""
      }`;

    root.querySelector(".card-close").addEventListener("click", onClose);

    for (const sel of root.querySelectorAll("select[data-field]")) {
      sel.addEventListener("change", async () => {
        const err = root.querySelector(".card-error");
        err.hidden = true;
        try {
          await setCardField(path, sel.dataset.field, sel.value);
        } catch (e) {
          // A refused write must be visible and must not leave the control
          // showing a value the file does not have.
          err.textContent = e.message;
          err.hidden = false;
          draw(get());
        }
      });
    }
  };

  const unsubscribe = subscribe(draw);
  const onKey = (e) => {
    if (e.key === "Escape") onClose();
  };
  document.addEventListener("keydown", onKey);
  return () => {
    unsubscribe();
    document.removeEventListener("keydown", onKey);
  };
}
```

- [ ] **Step 4: Добавить ключи словаря.** В `i18n.js` добавить `card_gone` (`"card is gone"` / `"карточка исчезла"`) и `backlinks` (`"linked from"` / `"ссылаются сюда"`).

- [ ] **Step 5: Проверить руками, включая отказ.** Открыть карточку с доски: тело отрисовано, связи `[[...]]` кликабельны, несуществующие подсвечены иначе, обратные ссылки перечислены. Сменить стадию — значение уезжает в файл и появляется коммит. Затем остановить сервер и сменить стадию ещё раз: под полями должна появиться ошибка, а селект вернуться к прежнему значению. Молчаливый откат без сообщения считать провалом задачи.

- [ ] **Step 6: Коммит**

```bash
git add web
git commit --signoff -m "feat(web): card panel with links, backlinks and field editing"
```

---

## Task 6: Маршрут выжимки и колонка оркестратора

Первый план оставил пробел: пакет `internal/transcript` умеет строить выжимку, но наружу её никто не отдаёт. Эта задача закрывает пробел и сразу использует маршрут в левой колонке.

**Files:**
- Create: `web/js/orchestrator.js`
- Modify: `internal/server/server.go`, `internal/server/api.go`, `internal/server/api_test.go`, `cmd/fleetdeck/main.go`, `web/js/main.js`, `web/app.css`, `web/js/i18n.js`

**Interfaces:**
- Consumes: `sendText` из `api.js`, `subscribe`
- Produces: `GET /api/sessions/{id}/digest?limit=N`, поле `Deps.Digest func(sessionID string, limit int) ([]transcript.Step, error)`, `renderOrchestrator(root)`

- [ ] **Step 1: Написать падающий тест маршрута**

```go
// добавить в internal/server/api_test.go
func TestDigestIsServed(t *testing.T) {
	d, _ := testDeps()
	d.Digest = func(sessionID string, limit int) ([]transcript.Step, error) {
		if limit != 7 {
			t.Errorf("limit must reach the source, got %d", limit)
		}
		return []transcript.Step{{Role: "assistant", Text: "hello"}}, nil
	}
	rec := httptest.NewRecorder()
	New(d).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/sessions/abc/digest?limit=7", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "hello") {
		t.Fatalf("digest not served: %d %s", rec.Code, rec.Body.String())
	}
}

func TestDigestWithoutTranscriptIsNotAnEmptyList(t *testing.T) {
	d, _ := testDeps()
	d.Digest = func(string, int) ([]transcript.Step, error) { return nil, transcript.ErrNoTranscript }
	rec := httptest.NewRecorder()
	New(d).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/sessions/abc/digest", nil))
	if rec.Code == http.StatusOK {
		t.Fatal("a missing transcript must be an error, not an empty digest that looks like a quiet session")
	}
}
```

- [ ] **Step 2: Прогнать и убедиться, что падает.** Команда `go test ./internal/server/ -run Digest`, ожидается FAIL: поля и маршрута нет.

- [ ] **Step 3: Реализовать маршрут**

В `Deps` добавить поле `Digest func(sessionID string, limit int) ([]transcript.Step, error)`, в `New` — маршрут `mux.HandleFunc("GET /api/sessions/{id}/digest", d.handleDigest)`, в `api.go`:

```go
func (d Deps) handleDigest(w http.ResponseWriter, r *http.Request) {
	limit := 20
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	steps, err := d.Digest(r.PathValue("id"), limit)
	if err != nil {
		fail(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, steps)
}
```

В `cmd/fleetdeck/main.go` заполнить поле:

```go
		Digest: func(sessionID string, limit int) ([]transcript.Step, error) {
			path, err := transcript.Locate(filepath.Join(home, ".claude", "projects"), sessionID)
			if err != nil {
				return nil, err
			}
			return transcript.Digest(path, limit)
		},
```

- [ ] **Step 4: Написать колонку оркестратора**

```js
// web/js/orchestrator.js
import { subscribe, get } from "./store.js";
import { sendText } from "./api.js";
import { t } from "./i18n.js";

// The orchestrator is not one session among many: it is the standing place of
// conversation, so it keeps its own column and its own input.
export function renderOrchestrator(root, orchestratorId) {
  let currentId = orchestratorId;
  let steps = [];
  let error = "";

  const draw = () => {
    const snap = get() ?? {};
    const session = (snap.sessions ?? []).find((s) => s.id === currentId);
    const ctx = session?.context;
    const pct = ctx?.percent ?? (ctx?.window ? (ctx.tokens / ctx.window) * 100 : null);

    root.innerHTML = `
      <div class="o-head">
        <span class="o-name">${session ? session.name || session.id : t("pick_orchestrator")}</span>
        ${pct === null || pct === undefined ? "" : `<span class="o-ctx">${Math.round(pct)}%</span>`}
      </div>
      ${error ? `<div class="o-error">${error}</div>` : ""}
      <div class="o-thread">
        ${steps
          .map((s) => `<div class="o-msg o-${s.role}">${escapeHTML(s.text)}</div>`)
          .join("")}
      </div>
      <form class="o-form">
        <textarea rows="3" placeholder="${t("write_to_orchestrator")}"></textarea>
      </form>`;

    const form = root.querySelector(".o-form");
    const area = form.querySelector("textarea");
    area.addEventListener("keydown", async (e) => {
      if (e.key !== "Enter" || e.shiftKey) return;
      e.preventDefault();
      const text = area.value.trim();
      if (!text || !currentId) return;
      area.value = "";
      try {
        await sendText(currentId, text, true);
        error = "";
      } catch (err) {
        error = err.message;
        area.value = text;
      }
      await refresh();
    });
    const thread = root.querySelector(".o-thread");
    thread.scrollTop = thread.scrollHeight;
  };

  const refresh = async () => {
    if (!currentId) return;
    try {
      const res = await fetch(`/api/sessions/${encodeURIComponent(currentId)}/digest?limit=20`);
      if (!res.ok) throw new Error((await res.json()).error ?? res.statusText);
      steps = await res.json();
      error = "";
    } catch (err) {
      steps = [];
      error = err.message;
    }
    draw();
  };

  subscribe(draw);
  refresh();
  setInterval(refresh, 3000);
}

function escapeHTML(s) {
  return String(s).replace(/[&<>]/g, (ch) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;" })[ch]);
}
```

- [ ] **Step 5: Добавить ключи и выбор сессии.** В `i18n.js` добавить `pick_orchestrator` (`"pick the orchestrator session"` / `"выберите сессию оркестратора"`) и `write_to_orchestrator` (`"write to the orchestrator…"` / `"написать оркестру…"`). Идентификатор берётся из `orchestrator_session` конфигурации и приезжает в снимке новым полем `orchestratorSession`, которое `Collect` копирует из конфигурации. Если поле пусто, колонка показывает список сессий на выбор, а выбор сохраняется запросом `PATCH /api/config` с телом `{"orchestratorSession":"<id>"}` — маршрут и его обработчик добавить по образцу `handlePatchCard`, запись выполняет `config.Save`.

- [ ] **Step 6: Прогнать тесты и проверить руками.** Команда `go test ./...` — ожидается PASS. Затем открыть панель: в левой колонке видна переписка оркестратора, ввод отправляет сообщение и оно появляется в потоке в течение трёх секунд. Остановить сервер и отправить ещё раз: текст обязан остаться в поле ввода, а ошибка — появиться над ним. Потерянный текст считать провалом задачи.

- [ ] **Step 7: Коммит**

```bash
git add internal/server cmd/fleetdeck web
git commit --signoff -m "feat: serve session digests and add the orchestrator column"
```

---

## Task 7: Панель сессии с живым терминалом

**Files:**
- Create: `web/js/session.js`, `web/vendor/xterm.js`, `web/vendor/xterm.css`, `web/vendor/LICENSE.xterm`
- Modify: `web/js/main.js`, `web/app.css`, `web/js/i18n.js`

**Interfaces:**
- Consumes: `GET /api/sessions/{id}/digest`, `GET /api/sessions/{id}/screen`, `sendText`, `sendKeys`
- Produces: `renderSession(root, sessionId, onClose)`

- [ ] **Step 1: Вендорить xterm.js**

```bash
mkdir -p web/vendor
curl --silent --location --output web/vendor/xterm.js  https://cdn.jsdelivr.net/npm/@xterm/xterm@5.5.0/lib/xterm.js
curl --silent --location --output web/vendor/xterm.css https://cdn.jsdelivr.net/npm/@xterm/xterm@5.5.0/css/xterm.css
curl --silent --location --output web/vendor/LICENSE.xterm https://raw.githubusercontent.com/xtermjs/xterm.js/master/LICENSE
```

Проверить, что файлы непустые и что лицензия скачалась: вендор без лицензии в публичном репозитории недопустим. Записать точную версию в `web/vendor/README.md` одной строкой, чтобы обновление было воспроизводимым.

- [ ] **Step 2: Написать панель**

```js
// web/js/session.js
import { sendKeys, sendText } from "./api.js";
import { t } from "./i18n.js";

// Two tabs, two sources, never mixed: the digest comes from the transcript and
// is readable; the screen comes from the daemon and is the terminal as it is.
export function renderSession(root, sessionId, onClose) {
  let tab = "digest";
  let term = null;
  let timer = null;

  const stop = () => {
    if (timer) clearInterval(timer);
    timer = null;
  };

  const drawShell = () => {
    root.hidden = false;
    root.innerHTML = `
      <div class="s-head">
        <div class="s-tabs">
          <button data-tab="digest" class="${tab === "digest" ? "on" : ""}">${t("tab_digest")}</button>
          <button data-tab="screen" class="${tab === "screen" ? "on" : ""}">${t("tab_screen")}</button>
        </div>
        <div class="s-actions">
          <button data-key="escape">Esc</button>
          <button data-key="up">↑</button>
          <button data-key="down">↓</button>
          <button data-key="enter">Enter</button>
        </div>
        <button class="s-close">✕</button>
      </div>
      <div class="s-body"></div>
      <form class="s-form"><textarea rows="2" placeholder="${t("write_to_session")}"></textarea></form>`;

    root.querySelector(".s-close").addEventListener("click", () => {
      stop();
      onClose();
    });
    for (const b of root.querySelectorAll("[data-tab]")) {
      b.addEventListener("click", () => {
        tab = b.dataset.tab;
        stop();
        term = null;
        drawShell();
        load();
      });
    }
    for (const b of root.querySelectorAll("[data-key]")) {
      b.addEventListener("click", () => sendKeys(sessionId, b.dataset.key).catch(showError));
    }
    const area = root.querySelector(".s-form textarea");
    area.addEventListener("keydown", async (e) => {
      if (e.key !== "Enter" || e.shiftKey) return;
      e.preventDefault();
      const text = area.value.trim();
      if (!text) return;
      area.value = "";
      try {
        await sendText(sessionId, text, true);
      } catch (err) {
        area.value = text;
        showError(err);
      }
    });
  };

  const showError = (err) => {
    root.querySelector(".s-body").innerHTML = `<div class="s-error">${err.message}</div>`;
  };

  const load = async () => {
    const body = root.querySelector(".s-body");
    if (tab === "digest") {
      try {
        const res = await fetch(`/api/sessions/${encodeURIComponent(sessionId)}/digest?limit=30`);
        if (!res.ok) throw new Error((await res.json()).error ?? res.statusText);
        const steps = await res.json();
        body.innerHTML = steps
          .map((s) => `<div class="s-step s-${s.role}">${escapeHTML(s.text)}</div>`)
          .join("");
        body.scrollTop = body.scrollHeight;
      } catch (err) {
        showError(err);
      }
      timer = setInterval(load, 3000);
      return;
    }

    if (!term) {
      body.innerHTML = `<div class="s-term"></div>`;
      term = new window.Terminal({ convertEol: true, fontSize: 12, scrollback: 2000 });
      term.open(body.querySelector(".s-term"));
    }
    try {
      const res = await fetch(`/api/sessions/${encodeURIComponent(sessionId)}/screen?tail=400`);
      if (!res.ok) throw new Error((await res.json()).error ?? res.statusText);
      const { screen } = await res.json();
      term.reset();
      term.write(screen);
    } catch (err) {
      showError(err);
      return;
    }
    timer = setInterval(load, 1000);
  };

  drawShell();
  load();
  return stop;
}

function escapeHTML(s) {
  return String(s).replace(/[&<>]/g, (ch) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;" })[ch]);
}
```

- [ ] **Step 3: Подключить вендор и ключи.** В `index.html` добавить `<script src="/vendor/xterm.js"></script>` до модуля `main.js` — библиотека не является ES-модулем и кладёт `Terminal` в `window`. В `i18n.js` добавить `tab_digest`, `tab_screen`, `write_to_session`.

- [ ] **Step 4: Учесть результат проверки из первого плана.** Если задача 3 первого плана показала, что поля `needs`, `intent`, `detail`, `options` несут ожидающий вопрос с вариантами, добавить над вкладками блок ответа: заголовок вопроса из `intent` либо `detail` и кнопки по числу `options`, каждая отправляет свой `value` через `sendText`. Если поля оказались пустыми, оставить как есть: вопрос виден на вкладке экрана, а ответ даётся кнопками стрелок и Enter. Отчёт исполнителя первого плана — источник этого решения, гадать не нужно.

- [ ] **Step 5: Проверить руками обе вкладки.** Открыть сессию из правой колонки: вкладка выжимки показывает читаемые шаги без спиннеров, вкладка экрана рисует терминал. Отправить текст в живую сессию и убедиться, что он дошёл. Открыть сессию, у которой нет транскрипта: вкладка выжимки обязана показать ошибку, а не пустоту, притворяющуюся тишиной.

- [ ] **Step 6: Коммит**

```bash
git add web
git commit --signoff -m "feat(web): session panel with digest and live terminal"
```

---

## Task 8: Раздел документации

Второй пробел первого плана: каталоги документации есть в конфигурации, но наружу не отдаются. Задача закрывает и его.

**Files:**
- Create: `internal/server/docs.go`, `internal/server/docs_test.go`, `web/js/docs.js`
- Modify: `internal/server/server.go`, `cmd/fleetdeck/main.go`, `web/js/main.js`, `web/js/i18n.js`

**Interfaces:**
- Consumes: `config.DocsPaths`, `renderMarkdown` из задачи 5
- Produces: `GET /api/docs` — плоский список документов, `GET /api/docs/content?path=...` — текст одного, `renderDocs(root)`

- [ ] **Step 1: Написать падающие тесты**

```go
// internal/server/docs_test.go
package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDocsListsMarkdownOnly(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.md"), []byte("# a"), 0o600)
	os.WriteFile(filepath.Join(dir, "b.png"), []byte("x"), 0o600)

	d, _ := testDeps()
	d.DocsRoots = []string{dir}
	rec := httptest.NewRecorder()
	New(d).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/docs", nil))
	body := rec.Body.String()
	if !strings.Contains(body, "a.md") || strings.Contains(body, "b.png") {
		t.Fatalf("only markdown belongs in the docs list: %s", body)
	}
}

func TestDocsRefusesPathsOutsideRoots(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.md"), []byte("# a"), 0o600)

	d, _ := testDeps()
	d.DocsRoots = []string{dir}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/docs/content?path="+filepath.Join(dir, "..", "etc", "passwd"), nil)
	New(d).ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("a path outside the configured roots must be refused, got %d", rec.Code)
	}
}

func TestDocsWithNoRootsConfiguredSaysSo(t *testing.T) {
	d, _ := testDeps()
	d.DocsRoots = nil
	rec := httptest.NewRecorder()
	New(d).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/docs", nil))
	if rec.Code == http.StatusOK && rec.Body.String() == "[]\n" {
		t.Fatal("no configured roots must be stated, not shown as an empty documentation set")
	}
}
```

- [ ] **Step 2: Прогнать и убедиться, что падает.** Команда `go test ./internal/server/ -run Docs`, ожидается FAIL: маршрутов нет.

- [ ] **Step 3: Реализовать**

```go
// internal/server/docs.go
package server

import (
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

type Doc struct {
	Path  string `json:"path"`
	Title string `json:"title"`
	Root  string `json:"root"`
}

func (d Deps) handleDocsList(w http.ResponseWriter, r *http.Request) {
	if len(d.DocsRoots) == 0 {
		fail(w, http.StatusNotFound, "no documentation roots are configured")
		return
	}
	var out []Doc
	for _, root := range d.DocsRoots {
		filepath.WalkDir(root, func(p string, e fs.DirEntry, err error) error {
			if err != nil || e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				return nil
			}
			rel, _ := filepath.Rel(root, p)
			out = append(out, Doc{Path: p, Title: rel, Root: root})
			return nil
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// within reports whether p resolves inside one of the configured roots.
// Without this check a crafted path would read any file the process can open.
func (d Deps) within(p string) bool {
	abs, err := filepath.Abs(p)
	if err != nil {
		return false
	}
	for _, root := range d.DocsRoots {
		rootAbs, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(rootAbs, abs)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func (d Deps) handleDocsContent(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	if !d.within(p) {
		fail(w, http.StatusForbidden, fmt.Sprintf("path is outside the configured documentation roots"))
		return
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		fail(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"path": p, "body": string(raw)})
}
```

В `Deps` добавить `DocsRoots []string`, в `New` — маршруты `GET /api/docs` и `GET /api/docs/content`, в `cmd/fleetdeck/main.go` заполнить `DocsRoots: cfg.DocsPaths`.

- [ ] **Step 4: Добавить ключ словаря.** В `i18n.js` добавить `pick_doc` (`"pick a document"` / `"выберите документ"`).

- [ ] **Step 5: Написать раздел в интерфейсе**

```js
// web/js/docs.js
import { renderMarkdown } from "./markdown.js";
import { t } from "./i18n.js";

export function renderDocs(root) {
  let docs = [];
  let error = "";

  const draw = (selected, body) => {
    root.innerHTML = `
      <div class="docs">
        <nav class="docs-list">
          ${error ? `<div class="empty">${error}</div>` : ""}
          ${docs.map((d) => `<a data-path="${d.path}" class="${d.path === selected ? "on" : ""}">${d.title}</a>`).join("")}
        </nav>
        <article class="docs-body">${body ?? `<div class="empty">${t("pick_doc")}</div>`}</article>
      </div>`;
    for (const a of root.querySelectorAll("[data-path]")) {
      a.addEventListener("click", () => open(a.dataset.path));
    }
  };

  const open = async (path) => {
    try {
      const res = await fetch(`/api/docs/content?path=${encodeURIComponent(path)}`);
      if (!res.ok) throw new Error((await res.json()).error ?? res.statusText);
      const { body } = await res.json();
      draw(path, renderMarkdown(body, new Set()));
    } catch (err) {
      draw(path, `<div class="empty">${err.message}</div>`);
    }
  };

  (async () => {
    try {
      const res = await fetch("/api/docs");
      if (!res.ok) throw new Error((await res.json()).error ?? res.statusText);
      docs = await res.json();
    } catch (err) {
      error = err.message;
    }
    draw(null, null);
  })();
}
```

- [ ] **Step 6: Прогнать тесты и проверить руками.** Команда `go test ./internal/server/` — ожидается PASS. Затем указать в конфигурации каталог с документацией, открыть раздел «Доки» и убедиться: список построен, документ открывается, связи отрисованы. Убрать каталоги из конфигурации и убедиться, что раздел говорит «каталоги не настроены», а не показывает пустой список: пустой список выглядит как «документации нет», и это другое утверждение.

- [ ] **Step 7: Коммит**

```bash
git add internal/server cmd/fleetdeck web
git commit --signoff -m "feat: serve documentation and render it in the panel"
```

---

## Task 9: Команда init и автозапуск

**Files:**
- Create: `cmd/fleetdeck/init.go`, `cmd/fleetdeck/init_test.go`
- Modify: `cmd/fleetdeck/main.go`

**Interfaces:**
- Consumes: `config.Save`, `config.Load`
- Produces: подкоманда `fleetdeck init`, `func writeLaunchAgent(path, binary string) error`, `func wireStatusline(settingsPath, binary string) error`

`init` обязан быть повторяемым: второй запуск ничего не ломает и не дублирует. Настройки Claude Code правятся хирургически — меняется одно поле, остальное сохраняется.

- [ ] **Step 1: Написать падающие тесты**

```go
// cmd/fleetdeck/init_test.go
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWireStatuslineKeepsOtherSettings(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	os.WriteFile(p, []byte(`{"model":"opus","permissions":{"allow":["Read"]}}`), 0o600)

	if err := wireStatusline(p, "/usr/local/bin/fleetdeck-status"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(p)
	var got map[string]any
	json.Unmarshal(raw, &got)
	if got["model"] != "opus" {
		t.Fatal("existing settings must survive: the panel edits one key, not the file")
	}
	if !strings.Contains(string(raw), "fleetdeck-status") {
		t.Fatal("statusline command was not written")
	}
}

func TestWireStatuslineIsIdempotent(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	os.WriteFile(p, []byte(`{}`), 0o600)
	wireStatusline(p, "/bin/fd-status")
	first, _ := os.ReadFile(p)
	wireStatusline(p, "/bin/fd-status")
	second, _ := os.ReadFile(p)
	if string(first) != string(second) {
		t.Fatal("running init twice must not change anything the second time")
	}
}

func TestWireStatuslineRefusesBrokenSettings(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	os.WriteFile(p, []byte(`{"model":`), 0o600)
	if err := wireStatusline(p, "/bin/fd-status"); err == nil {
		t.Fatal("a malformed settings file must stop init, not be overwritten")
	}
}

func TestLaunchAgentContainsBinaryAndKeepAlive(t *testing.T) {
	p := filepath.Join(t.TempDir(), "agent.plist")
	if err := writeLaunchAgent(p, "/usr/local/bin/fleetdeck"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(p)
	for _, want := range []string{"/usr/local/bin/fleetdeck", "KeepAlive", "RunAtLoad"} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("launch agent missing %q", want)
		}
	}
}
```

- [ ] **Step 2: Прогнать и убедиться, что падает.** Команда `go test ./cmd/fleetdeck/ -run 'Wire|Launch'`, ожидается FAIL: функций нет.

- [ ] **Step 3: Реализовать**

```go
// cmd/fleetdeck/init.go
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/kroticw/fleetdeck/internal/config"
)

const launchAgentTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>dev.fleetdeck.panel</string>
  <key>ProgramArguments</key><array><string>%s</string></array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>%s/fleetdeck.log</string>
  <key>StandardErrorPath</key><string>%s/fleetdeck.log</string>
</dict>
</plist>
`

func writeLaunchAgent(path, binary string) error {
	logDir := filepath.Dir(path)
	body := fmt.Sprintf(launchAgentTemplate, binary, logDir, logDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create launch agent dir: %w", err)
	}
	return os.WriteFile(path, []byte(body), 0o644)
}

// wireStatusline sets the statusLine command and leaves every other setting
// untouched. A malformed file stops init: overwriting someone's settings to
// make our own feature work is not a trade we get to make.
func wireStatusline(settingsPath, binary string) error {
	settings := map[string]any{}
	raw, err := os.ReadFile(settingsPath)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return fmt.Errorf("read settings: %w", err)
	default:
		if err := json.Unmarshal(raw, &settings); err != nil {
			return fmt.Errorf("settings file is malformed, refusing to overwrite: %w", err)
		}
	}
	settings["statusLine"] = map[string]any{"type": "command", "command": binary}
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("encode settings: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o700); err != nil {
		return fmt.Errorf("create settings dir: %w", err)
	}
	return os.WriteFile(settingsPath, append(out, '\n'), 0o600)
}

// runInit is idempotent by construction: every step either creates what is
// missing or leaves what exists alone.
func runInit(home string) error {
	cfgPath := filepath.Join(home, ".config", "fleetdeck", "config.yaml")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	if err := config.Save(cfgPath, cfg); err != nil {
		return err
	}
	// The board convention no longer ships an Obsidian view file, so the
	// directory has to come from somewhere: it comes from here.
	if err := os.MkdirAll(cfg.BoardPath, 0o700); err != nil {
		return fmt.Errorf("create board dir: %w", err)
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate own binary: %w", err)
	}
	statusBinary := filepath.Join(filepath.Dir(exe), "fleetdeck-status")
	if err := wireStatusline(filepath.Join(home, ".claude", "settings.json"), statusBinary); err != nil {
		return err
	}
	agent := filepath.Join(home, "Library", "LaunchAgents", "dev.fleetdeck.panel.plist")
	if err := writeLaunchAgent(agent, exe); err != nil {
		return err
	}
	fmt.Printf("config:       %s\nboard:        %s\nlaunch agent: %s\nstatusline:   %s\n", cfgPath, cfg.BoardPath, agent, statusBinary)
	fmt.Println("load the agent with: launchctl load", agent)
	return nil
}
```

В `main.go` добавить разбор подкоманды: если первый аргумент `init`, вызвать `runInit(home)` и выйти.

- [ ] **Step 4: Прогнать тесты и проверить руками.** Команда `go test ./cmd/fleetdeck/` — ожидается PASS. Затем `./bin/fleetdeck init` дважды подряд: второй запуск обязан не менять ни один файл, проверить через `git status` в домашнем каталоге настроек либо сверкой хешей до и после.

- [ ] **Step 5: Коммит**

```bash
git add cmd/fleetdeck
git commit --signoff -m "feat(init): idempotent setup of config, statusline and launch agent"
```

---

## Task 10: Переезд конвенции

**Files:**
- Create: `plugin/` — содержимое `agent-fleet` из приватного репозитория
- Modify: `plugin/.claude-plugin/plugin.json`, `plugin/templates/board/README.md`, `plugin/templates/board/scripts/test_validate_cards.py`
- Delete: `plugin/templates/board/Доска.base`

**Interfaces:**
- Consumes: ничего
- Produces: плагин Claude Code, устанавливаемый из этого репозитория

- [ ] **Step 1: Перенести файлы**

```bash
cp -R ~/claude/krotic-claude-code/plugins/agent-fleet/ plugin/
rm plugin/templates/board/Доска.base
```

- [ ] **Step 2: Обезличить три места.** В `plugin/.claude-plugin/plugin.json` заменить автора на `fleetdeck contributors`. В `plugin/templates/board/README.md` убрать упоминание `krotic`. В `plugin/templates/board/scripts/test_validate_cards.py` заменить фикстуру `bigcountry` на нейтральное `example/project`.

- [ ] **Step 3: Проверить, что приватного не осталось**

```bash
grep --recursive --ignore-case --extended-regexp "balalaika|bigcountry|krotic|nevalyashka|denisdavydov|remnawave|homelab|eluosizhiyou" plugin/ || echo "чисто"
```

Ожидается `чисто`. Любое совпадение — блокирующее: репозиторий публичный.

- [ ] **Step 4: Убрать из скилов упоминания Obsidian.** В `plugin/skills/fleet-orchestrator/SKILL.md` раздел про развёртывание доски переписать под `fleetdeck init`: доска создаётся командой, виды даёт пульт, `Доска.base` больше не существует. В `plugin/skills/card-keeping/SKILL.md` заменить упоминания волта Obsidian на каталог доски.

- [ ] **Step 5: Прогнать валидатор шаблона.** Команды `python3 plugin/templates/board/scripts/test_validate_cards.py` и `python3 plugin/templates/board/scripts/validate_cards.py plugin/templates/board/cards` — ожидается, что тесты зелёные, а валидатор на пустом каталоге карточек сообщает об отсутствии карточек, а не молча выходит с нулём.

- [ ] **Step 6: Коммит**

```bash
git add plugin
git commit --signoff -m "feat(plugin): move the board convention into the public repository"
```

- [ ] **Step 7: Удалить оригинал из приватного репозитория.** Это отдельная работа в `~/claude/krotic-claude-code`: убрать `plugins/agent-fleet`, вычистить запись из `.claude-plugin/marketplace.json`, поднять версию маркетплейса и открыть PR. Две копии конвенции разъедутся, и это вопрос времени. Выполнять после того, как плагин отсюда установится и заработает хотя бы на одной машине.

---

## Task 11: Документация на двух языках

Спека, раздел 10.1: английский — основной, русский — полноценный перевод, а не огрызок. Английский текст пишется первым, русский переводится с него.

**Files:**
- Create: `README.md`, `docs/en/getting-started.md`, `docs/en/board-convention.md`, `docs/en/configuration.md`, `docs/ru/README.md`, `docs/ru/getting-started.md`, `docs/ru/board-convention.md`, `docs/ru/configuration.md`
- Modify: `.github/workflows/ci.yaml`

**Interfaces:**
- Consumes: конфигурация из плана 1, `fleetdeck init` из задачи 9
- Produces: ничего для кода

- [ ] **Step 1: Написать `README.md`.** Разделы: что это (пульт для флота фоновых сессий Claude Code), почему (TUI показывает снимок, а не состояние; вопрос сессии не видно, пока не откроешь), снимок экрана, установка, `fleetdeck init`, ссылка на `docs/ru/README.md` первой строкой под заголовком. Ограничения назвать честно: только macOS, только локальный демон, читает домашний каталог пользователя.

- [ ] **Step 2: Написать `docs/en/getting-started.md`.** Установка бинарника, `fleetdeck init`, `launchctl load`, открыть `http://127.0.0.1:7717`, создать первую карточку, связать её с сессией через поле `session`. Отдельный абзац: почему `session` — единственное поле, которым доска связана с демоном.

- [ ] **Step 3: Написать `docs/en/board-convention.md`.** Формат карточки, все поля фронтматтера с допустимыми значениями (`zone`, `stage`, `progress`, `session`, `repo`, `created`), правило разделения: поля правит пульт, тело правят агенты. Показать пример карточки целиком.

- [ ] **Step 4: Написать `docs/en/configuration.md`.** Все ключи `config.yaml` с типами, значениями по умолчанию и тем, что сломается при неверном значении. Отдельно — где живут файлы: конфигурация, launch agent, лог.

- [ ] **Step 5: Перевести на русский.** Четыре файла в `docs/ru/`, полный перевод, не сокращённый пересказ. Термины: session — сессия, board — доска, card — карточка, stage — стадия, zone — зона, digest — выжимка.

- [ ] **Step 6: Проверить, что переводы не разъехались.** Команда — сверить число заголовков в каждой паре:

```bash
for f in getting-started board-convention configuration; do
  en=$(grep --count '^#' docs/en/$f.md)
  ru=$(grep --count '^#' docs/ru/$f.md)
  [ "$en" = "$ru" ] || echo "$f: en=$en ru=$ru"
done
```

Ожидается пустой вывод. Расхождение означает, что перевод потерял раздел.

- [ ] **Step 7: Добавить проверку в CI.** В `.github/workflows/ci.yaml` шаг `docs-parity`, выполняющий тот же цикл и падающий при непустом выводе. Без этого переводы разойдутся на третьем изменении.

- [ ] **Step 8: Коммит**

```bash
git add README.md docs .github
git commit --signoff -m "docs: english and russian documentation with a parity check"
```

---

## Task 12: Релизы

Каркас сборки уже стоит: первый план создаёт `Makefile` с целями `build`, `test`, `lint`, `run`, версию через `internal/version` и `.github/workflows/ci.yaml`. Эта задача добавляет только то, чего там нет: подкоманду `version`, цель `dist` и публикацию релиза по тегу.

**Files:**
- Create: `cmd/fleetdeck/version.go`, `cmd/fleetdeck/version_test.go`, `.github/workflows/release.yaml`
- Modify: `Makefile`, `.gitignore`, `cmd/fleetdeck/main.go`

**Interfaces:**
- Consumes: `version.String()` из первого плана, `make build`
- Produces: подкоманда `fleetdeck version`, цель `make dist`

- [ ] **Step 1: Написать падающий тест**

```go
// cmd/fleetdeck/version_test.go
package main

import (
	"strings"
	"testing"
)

func TestRunVersionPrintsBuildVersion(t *testing.T) {
	var out strings.Builder
	runVersion(&out)
	got := strings.TrimSpace(out.String())
	if got == "" {
		t.Fatal("version output must never be empty: an unversioned binary cannot be supported")
	}
	if strings.Contains(got, "\n") {
		t.Fatalf("version must be a single line, got %q", got)
	}
}
```

- [ ] **Step 2: Прогнать и убедиться, что падает.** Команда `go test ./cmd/fleetdeck/ -run Version`, ожидается FAIL: функции `runVersion` нет.

- [ ] **Step 3: Реализовать подкоманду**

```go
// cmd/fleetdeck/version.go
package main

import (
	"fmt"
	"io"

	"github.com/kroticw/fleetdeck/internal/version"
)

// runVersion writes the build version. It takes a writer rather than printing
// directly so the behaviour is testable without capturing os.Stdout.
func runVersion(w io.Writer) {
	fmt.Fprintln(w, version.String())
}
```

В `cmd/fleetdeck/main.go`, рядом с разбором подкоманды `init` из задачи 9, добавить ветку:

```go
if len(os.Args) > 1 && os.Args[1] == "version" {
	runVersion(os.Stdout)
	return
}
```

- [ ] **Step 4: Прогнать тест.** Команда `go test ./cmd/fleetdeck/ -run Version`, ожидается PASS.

- [ ] **Step 5: Добавить цель `dist` в существующий `Makefile`.** Переменные `BINARIES`, `VERSION` и `LDFLAGS` уже объявлены первым планом — переиспользовать их, не объявлять заново. Добавить `dist` в список `.PHONY` и следующую цель:

```makefile
dist:
	@for arch in arm64 amd64; do \
		for b in $(BINARIES); do \
			GOOS=darwin GOARCH=$$arch go build -ldflags "$(LDFLAGS)" -o dist/darwin-$$arch/$$b ./cmd/$$b; \
		done; \
		tar --create --gzip --file dist/fleetdeck-$(VERSION)-darwin-$$arch.tar.gz --directory dist/darwin-$$arch .; \
	done
```

В `.gitignore` добавить `dist/`.

- [ ] **Step 6: Написать `.github/workflows/release.yaml`**

```yaml
name: release

on:
  push:
    tags:
      - "v*"

permissions:
  contents: write

jobs:
  release:
    runs-on: macos-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
      - name: Test
        run: make test
      - name: Build archives
        run: make dist VERSION=${{ github.ref_name }}
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
      - name: Publish
        run: gh release create "$GITHUB_REF_NAME" dist/*.tar.gz --generate-notes
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
```

- [ ] **Step 7: Проверить руками.** Команда `make dist VERSION=v0.1.0`, затем распаковать `dist/fleetdeck-v0.1.0-darwin-arm64.tar.gz` во временный каталог и запустить `./fleetdeck version` — ожидается `v0.1.0`. Значение `dev` означает, что `-ldflags` не доехали до сборки, и релизные архивы врут о своей версии.

- [ ] **Step 8: Коммит**

```bash
git add Makefile .gitignore .github cmd/fleetdeck
git commit --signoff -m "build: version subcommand, release archives and publish workflow"
```

---

## Готовность плана

План закончен, когда выполнено всё перечисленное:

- пульт открывается на `http://127.0.0.1:7717` и показывает три колонки: оркестр слева, канбан по центру, список сессий справа;
- в шапке живут обе шкалы лимитов и счётчик ждущих ответа сессий, и они обновляются без перезагрузки страницы;
- у каждой сессии в списке видна занятость контекста, а отсутствие данных показано прочерком, а не нулём;
- карточка открывается панелью поверх доски, стадия и прогресс правятся из панели, отказ записи виден в интерфейсе и значение возвращается назад;
- у сессии есть выжимка и живой экран, вопрос с вариантами отвечается кнопкой;
- раздел документации читает настроенные каталоги, а ненастроенные каталоги отличимы от пустых;
- `fleetdeck init` повторяем, чужие настройки Claude Code переживают его без потерь;
- конвенция доски лежит в этом репозитории и не содержит следов приватной инфраструктуры;
- документация есть на двух языках, и CI падает при расхождении;
- `make dist` собирает архивы под обе архитектуры, и распакованный бинарник знает свою версию.

Что намеренно не делается в этом плане: аутентификация (пульт слушает только `127.0.0.1`), поддержка Linux и Windows, работа с удалёнными демонами, редактирование тела карточки из пульта. Первое и последнее — сознательные решения спеки, остальное — вопрос спроса.
