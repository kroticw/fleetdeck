# fleetdeck: окно с жидким стеклом — план реализации

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** окно fleetdeck в раскладке E — доска на всё окно, колонки оркестратора и сессий в стеклянных панелях `NSGlassEffectView` со своими веб-видами, капсулы из системных контролов в стекле, карточка, сессия и документ листом над доской, доки островами.

**Architecture:** веб-вид доски `webview_go` остаётся нижним слоем. Окно ставит над ним раму из C поверх Objective-C runtime: стекло (или `NSVisualEffectView`, или простой вид), два прозрачных `WKWebView` поверхностей и капсулы. Все веб-виды открывают один адрес панели; какую часть страницы смонтировать, страница узнаёт из `window.fleetdeckHost`, который подмешивает окно. Решения окна принимает чистый контроллер на Go, который выдаёт список эффектов; нативный код их только исполняет.

**Tech Stack:** Go 1.27, cgo, C поверх `objc/message.h` (без `.m`), AppKit (`NSGlassEffectView`, `NSGlassEffectContainerView`, `NSVisualEffectView`, `NSSegmentedControl`, `NSLevelIndicator`), WebKit (`WKWebView`, `WKUserContentController`, `WKUserScript`), ES-модули без сборщика, `node --test`.

**Spec:** `docs/superpowers/specs/2026-09-14-liquid-glass-window-design.md`

## Global Constraints

- Часть Б идёт от master после мержа T-057 (PR #161, 659f50d; тег v0.9.2); задачи 1–9 выполнены до мержа и перенесены rebase. Задача 0 — пересверка ниже, раздел «Сверка с master после T-057». T-058 (нижняя версия macOS окна) идёт параллельно и правит `Makefile`, `scripts/build-dist-app.sh`, `scripts/verify-dist-app.sh`, `release.yaml` и `cmd/fleetdeck-window/Info.plist`: эти файлы часть Б не трогает, кроме `ci.yaml` в задаче 17.
- Страница без `window.fleetdeckHost` (браузер, старое окно) показывает нынешнюю раскладку из трёх колонок без единого изменения поведения.
- Окно показывает раму только после `fleetdeckLayout({ version: 1, mode: "panel", fleet })` от доски. Неизвестная `version` — это «не сообщила».
- Стекло — только `NSGlassEffectView` со `style` 0 (Regular). Clear (1) не используется.
- Никакого `.m` и режима Objective-C; классы AppKit ищутся строкой `objc_getClass`; в коде нигде нет своего имени `NSGlassEffectViewStyle` (Wails #4541). Заголовок `AppKit/NSGlassEffectView.h` не подключается.
- Весь AppKit — на главном потоке; в тестах нативные вызовы делаются в `TestMain` (`testsupport_darwin.go`), тестовые окна не показываются на экране.
- Имена существующих привязок (`fleetdeckReload`, `fleetdeckStartPanel`, `fleetdeckChooseFolder`, `fleetdeckPanelNotice`, `fleetdeckPanelNoticeShown`, `fleetdeckReplacePanel`, `fleetdeckUpdateKnown`, `fleetdeckUpdate`) не меняются.
- CSP страницы (`script-src 'self'`) не ослабляется: встроенных скриптов в страницах нет; скрипты окна — `WKUserScript`.
- Никакого npm и зависимостей в `web/`. Строки интерфейса — в `web/js/i18n.js`, английский и русский; окно подписи капсул не хранит, а получает от страницы.
- Цвета — только токены `web/app.css`; окно получает готовые цвета от страницы.
- Стенды — по правилам T-057: окно передаёт `FLEETDECK_STAND_SOCKET` каждой панели, которую запускает; в логе каждой панели стенда есть строка `daemon discovery disabled`, без неё стенд останавливается; бандл с `BUNDLE_ID=dev.fleetdeck.stand`; свой `HOME`, порт не 7777. Всё, что появится на экране оператора, — с его согласия.
- Коммиты подписаны GPG, с `--signoff`, сообщения по-английски. Документация `docs/en` и `docs/ru` правится одним коммитом.
- Проверки перед коммитом: `make test`, `make test-web`, `make lint`, `golangci-lint run`.
- Этот план закрепляет интерфейсы, тесты и порядок. Код нативных задач (9, 10, 12, 13, 14) описан по шагам с точными вызовами AppKit, а не целиком: он пишется от master после T-057 и T-058. Места файлов и имена из T-057 и T-058 подтверждает задача 0.

## Сверка с master после T-057 (задача 0)

Сверено с `origin/master` 659f50d. Имена T-057, на которые опираются задачи 10–12, 16 и 17:

- `pageLoadScript(panelURL string) string` (`owner.go`) сообщает `fleetdeckPageLoaded(state, href)` — два аргумента. Состояний четыре: `loading` (документ начался), `panel`, `broken`, `leaving`; константы `pageLoading`, `pagePanel`, `pageBroken`, `pageLeaving`.
- Ожидание два: `pageLoadWait` (531 мс) — пока документ не начался, `pageLoadingWait` (840 мс) — после `loading`; `pageLoadTries = 3`, `pageLoadTick = 100 мс`. После T-059 `pageLoadWait` нет: его место заняли `navSilentWait` и `navFailedRetryPause` (раздел «Сверка с master после T-058 и T-059»).
- `pageFailedPage(pageURL string, wait time.Duration) string` — страница «страница панели не загрузилась».
- `screen` ведёт адрес, на котором страница себя назвала: `href`, `left`, `target()`; навигация доски в `main.go` — `w.Navigate(scr.target())`. Страница, ушедшая сама (`leaving`), ждёт следующую, и окно её не переспрашивает. Поля `takingOver`, `handed` и метод `handedOver()` — как в спеке 8.
- Стенд: `standSocketEnv = "FLEETDECK_STAND_SOCKET"`, `panelArgs(window int, standSocket string)` передаёт `--stand-socket`; лог панелей — `$HOME/Library/Logs/fleetdeck.log`; строка `daemon discovery disabled` — в `cmd/fleetdeck/main.go`. `supervisor.StandBundleID = "dev.fleetdeck.stand"`; `scripts/verify-dist-app.sh` принимает идентификатор седьмым аргументом, подписанный бандл обязан нести собственный идентификатор приложения.
- `Takeover.retire` (`internal/supervisor/update.go`) удаляет подменённый бандл после ухода старого окна; окна часть Б это не меняет.

Что это меняет в задачах: задача 11 — `pageLoaded` принимает `loading` и ничего на него не отдаёт; задача 12 — таймер поверхности по двум ожиданиям и `pageFailedPage(url, pageLoadingWait)`; `navigateBoard` — `w.Navigate(url)` без правки `screen`: доска скажет `leaving`, затем `loading` с новым `href`, и `screen` пойдёт за ней сам.

## Сверка с master после T-058 и T-059 (задача 0, второй проход)

Сверено с `origin/master` 2603fde.

- T-059 (853809b): `navscreen.go` и `nav_darwin.{c,h,go}` — делегат навигации WebKit у доски; ожидание `navSilentWait` (1,69 с × 3) до коммита навигации, `pageLoadingWait` от коммита, `navFailedRetryPause` 250 мс после провала, −999 и 102 — не провал, одна перезагрузка после потери процесса WebContent в пределах 30 с. Лог окна — с миллисекундами (`log.Lmicroseconds`). Как это переняли поверхности — в разделе ниже.
- T-058 (2603fde): нижняя версия — `LSMinimumSystemVersion` 13.0 в `cmd/fleetdeck-window/Info.plist`; `Makefile` читает её в `MACOS_MIN_VERSION` и собирает окно и панель с `DARWIN_CGO_ENV` (`-mmacosx-version-min` в `CGO_CFLAGS`, `CGO_CXXFLAGS`, `CGO_LDFLAGS`) в `dist-app` и `window-app`.
- Проверка `minos` — `app_runs_on_its_minimum_macos` в `scripts/dist-app-checks.sh`; `scripts/verify-dist-app.sh` и `verify-dist-dmg.sh` принимают нижнюю версию восьмым аргументом.
- `ci.yaml`: задача `oldest-macos-app` (`macos-latest`, `make dist-app … SIGN_IDENTITY= BUNDLE_ID=dev.fleetdeck.stand`, артефакт `oldest-macos-app`) и `window-on-oldest-macos` (`macos-15`): окно этой сборки запускается `scripts/ci-window-stand.sh new/fleetdeck.app 7791 out/new-exec exec`, v0.9.1 и запуск через LaunchServices снимаются как есть, артефакт `window-on-oldest-macos` — каталог `out`.
- `scripts/ci-window-stand.sh <app> <port> <out-dir> <exec|open>`: свой `HOME` с `server.port`, `FLEETDECK_STAND_SOCKET`, до 60 с ждёт в логе окна строку `the panel's page says "panel"`, через 3 с снимает экран в `window.png`, выходит с 0, только если страница сказала `panel`, окно живо и в логе панели есть `daemon discovery disabled`.
- Окно ветки, собранное с `-mmacosx-version-min=13.0`, проверено `nm -m -u`: символов AppKit и WebKit не импортирует вовсе (классы ищутся строкой), `std::bad_function_call` нет, weak — `__availability_version_check` и `_dispatch_once_f`. `NSGlassEffectView` и `NSGlassEffectContainerView` проверяются по классу, ряд капсул без контейнера стекла наследует `NSView` (спека 7.1).

### Что изменилось при исполнении задач 1–9

- Каждый новый модуль `web/js` добавлен в точный список `TestEmbeddedFSContainsExpectedFiles` (`internal/server/static_test.go`) в своей задаче.
- Задача 4: крестик закрытия — `closeCrossHTML` в `web/js/icon.js`, как остальные SVG страницы, через `innerHTML`; тест проверяет разметку, потому что `fake-dom.js` её не разбирает. Затемнение `#sheet-scrim` показывается одним правилом `:has` в `app.css` по атрибуту `hidden` трёх оверлеев — `main.js` не меняется.
- Задача 5: `applyTheme(choice)` добавлен в `theme.js`; `createNewCard` возвращает `{ open }`; на доске прячутся кнопки вкладок и «+ карточка», а не весь `#tabs` — форма новой карточки живёт в нём.
- Задача 6: переход на другой флот в колонке сессий — делегированный клик по корню колонки (разметка колонки — строка, `fake-dom.js` её не разбирает); свёртывание в боковой поверхности пересылается окну наблюдателем `data-folded` в `main.js`, `columnresize.js` не меняется.
- Задача 7: `delegate` в `buildcheck.js` не вводится. Сборку проверяет только доска: её `fleetdeckReload` перезагружает все три вида, а потолок попыток (`ATTEMPTS_KEY`) остаётся в хранилище одного вида — перезагрузка без записей его сняла бы. Шапка не делится на функции, а получает `parts` (`HEADER_PARTS`, `joinHeaderParts`): поверхность оркестратора — `brand` и `update`, сессий — `counters`; добавлены `limitsOf(snap, nowMs)` и `themeLabelText()`. Плашка сборки на доске стоит под капсулами.
- Задача 7: проверку сборки закрепляет регион `build` в `regionsFor` (`web/js/surfaces.js`): он есть у доски и во вкладке браузера, у боковых поверхностей его нет.
- Задачи 5 и 9: у доски `Board.Right = 0` (последняя колонка уходит под стекло сессий, спека 5.2), а лист, доки и плашка сборки держатся в стороне от панели по `Board.ContentRight` — ширине панели сессий и поля. Сообщение `insets` несёт `contentRight`, страница ставит `--host-inset-content-right`.
- Задача 9: контейнера стекла на всё окно нет; у ряда капсул контейнер размером с ряд (задача 13), панели без контейнера (спека 5.1). Корневой вид рамы и вид ряда капсул — класс `FleetdeckFrameView`: перевёрнутый и пропускающий щелчок, где под указателем нет подвида. Класс стекла проверяется `isKindOfClass:`: AppKit подменяет класс наблюдаемого вида на `NSKVONotifying_`.
- Задача 9: вид, в который идёт веб-вид поверхности, лежит над стеклом, а не внутри: `NSGlassEffectView` не пропускает клики в свой `contentView` (нашёл тест попадания в `surface_darwin_test.go`). Спека 5.1.
- Задачи 9, 11 и 12, решение оркестратора: ширину панели меняет нативная полоса у её внутреннего края (`FleetdeckResizeStrip`, `draggedWidth`, `controller.resizeStart`, `resizeTo`, `resizeEnd`); доске отступы уходят по отпускании. `col-grip` страницы в поверхностях скрыт CSS. Спека 5.1.
- После T-059 (853809b): таймеры поверхностей по `pageLoadWait` сняты. Правила ожидания вынесены из `screen` в `pageWatch` (`navscreen.go`), контроллер держит `pageWatch` на каждую поверхность и получает события WebKit от делегата поверхности (`fleetdeck_navigation_reporting`), тик — общий тикер `main.go`. Рама убирала доску из content view окна, где делегат T-059 её искал, — делегат теперь ставится на саму доску (`frame.board()`). Спека 6.6.

---

## Структура файлов

| Файл | Ответственность | Задача |
| --- | --- | --- |
| `web/js/host.js` | чтение `window.fleetdeckHost`, вызов окна, приём сообщений | 1 |
| `web/js/surfaces.js` | какие части страницы монтирует поверхность; отчёт `fleetdeckLayout` | 2 |
| `web/js/main.js` | монтирование по поверхности, проводка маршрутов и сообщений | 2, 5, 6, 7 |
| `web/app.css` | блок E: токены стекла, острова, лист, доки, поверхности | 3 |
| `web/js/card.js`, `session.js`, `reader.js`, `docs.js`, `web/index.html` | разметка листа и островов | 4 |
| `web/js/hostactions.js` | сообщения окна → существующие действия страницы | 5 |
| `web/js/hostroutes.js` | действия боковых поверхностей → окно | 6 |
| `web/js/header.js`, `web/js/sessions.js` | смена флота через переданный `switchFleet` | 6 |
| `web/js/capsules.js` | модель капсул из снимка и выбранной вкладки | 7 |
| `web/js/header.js`, `buildcheck.js` | части шапки для поверхностей; делегирование перезагрузки | 7 |
| `cmd/fleetdeck-window/bridge.go` | один реестр привязок для всех веб-видов | 8 |
| `cmd/fleetdeck-window/geometry.go` | геометрия панелей, капсул и отступов доски | 9 |
| `cmd/fleetdeck-window/frame_darwin.{c,h,go}` | корневой вид, панели: стекло, vibrancy, непрозрачные | 9 |
| `cmd/fleetdeck-window/hostscript.go` | текст `WKUserScript` для каждой поверхности | 10 |
| `cmd/fleetdeck-window/navigation.go` | куда уходят переходы из поверхностей | 10 |
| `cmd/fleetdeck-window/surface_darwin.{c,h,go}` | прозрачные `WKWebView` поверхностей и их обработчик сообщений | 10 |
| `cmd/fleetdeck-window/controller.go` | решения окна: состояние → эффекты | 11 |
| `cmd/fleetdeck-window/effects.go`, `main.go`, `owner.go` | исполнение эффектов, проводка в `main` и `screen.on` | 12 |
| `cmd/fleetdeck-window/capsulemodel.go`, `capsules_darwin.{c,h,go}` | модель и системные контролы капсул | 13 |
| `cmd/fleetdeck-window/menu_darwin.c`, `foreign.go` | «Reload» для всех видов; метка сборки в поверхности | 14 |
| `docs/engineering/window-and-panel.md`, `live-terminal.md`, `docs/en`, `docs/ru` | заметки и пользовательские доки | 15 |
| `scripts/stand-glass-window.sh` | стенд части Б, замеры | 16 |
| `scripts/ci-window-stand.sh` (T-058) | проверка окна со стеклом на macos-15 | 17 |
| `docs/superpowers/plans/2026-09-14-liquid-glass-window.md` | пересверка плана с master | 0 |

---

### Task 0: Пересверка плана с master

**Files:**

- Modify: `docs/superpowers/plans/2026-09-14-liquid-glass-window.md`, при расхождениях — `docs/superpowers/specs/2026-09-14-liquid-glass-window-design.md`
- Test: diff плана — единственный результат задачи

**Interfaces:**

- Consumes: смерженные T-057 и T-058, выпуск v0.9.2.
- Produces: план, в котором места файлов, имена и сигнатуры совпадают с master, — для задач 1–17.

- [ ] **Step 1:** Ветка части Б — `feat/liquid-glass-window` от свежего `origin/master`; спека и план переносятся в неё первым коммитом из ветки `docs/liquid-glass-window`:

```bash
git fetch origin
git switch --create feat/liquid-glass-window origin/master
git checkout origin/docs/liquid-glass-window -- docs/superpowers/specs/2026-09-14-liquid-glass-window-design.md docs/superpowers/specs/2026-09-14-liquid-glass-window docs/superpowers/plans/2026-09-14-liquid-glass-window.md
```

- [ ] **Step 2:** Выписать, что изменилось с основы плана:

```bash
git diff --stat c0ea864 origin/master -- cmd/fleetdeck-window web internal/server internal/supervisor Makefile scripts .github
```

- [ ] **Step 3:** Сверить с master каждое место, на которое опирается план, и поправить текст задач:
  - `pageLoadScript`, `fleetdeckPageLoaded` (`pageLoadedBindingName`), `pageLoadWait`, `pageLoadTries`, метка `data-fleetdeck-page`, страница отказа загрузки (`pageFailedPage`), поля `takingOver` и `handed` в `screen` — T-057, `cmd/fleetdeck-window/owner.go`;
  - передача `FLEETDECK_STAND_SOCKET` панелям, строка `daemon discovery disabled`, где окно пишет лог панели, переменная `BUNDLE_ID` и путь бандла после `make window-app` — T-057;
  - нижняя версия macOS окна и проверка `minos` — T-058, `Makefile`, `scripts/build-dist-app.sh`;
  - вызовы `switchFleet` (`web/js/header.js`, `web/js/sessions.js`), сигнатуры `renderBuildBanner`, `reloadNow`, `renderSessions`, `renderHeader`;
  - `push` и `fleetView` — `internal/server/ws.go`, `internal/server/api.go`;
  - путь вывода панелей (`$HOME/Library/Logs/fleetdeck.log`), флаг `--stand-socket`, `supervisor.StandBundleID`, процедура `cmd/fleetdeck-window/stand_test.go` — для задач 16 и 17;
  - закреплённые версии `actions/checkout`, `actions/setup-go` (`ci.yaml`) и `actions/upload-artifact` (`release.yaml`) — для задачи 17.
- [ ] **Step 4:** Каждое расхождение — правка текста задачи, а не пометка «сверить потом». Если механизм T-057 устроен иначе, чем описано в спеке (разделы 6.6, 8, 10.1), — правка спеки в том же коммите.
- [ ] **Step 5: Commit**

```bash
git add docs/superpowers
git commit --signoff --message "docs(window): re-anchor the Liquid Glass plan on master after T-057 and T-058"
```

---

### Task 1: Хост в странице

**Files:**

- Create: `web/js/host.js`
- Test: `web/js/_tests/host.test.js`

**Interfaces:**

- Consumes: —
- Produces:
  - `HOST_VERSION = 1` — для задачи 2;
  - `readHost(win): { surface: "board" | "orchestrator" | "sessions", glass: "glass" | "vibrancy" | "opaque" } | null` — для задач 2, 5, 6, 7;
  - `callHost(win, name: string, payload?: any): Promise<any> | null` — `null`, если привязки нет; для задач 2, 5, 6, 7;
  - `onHostMessage(win, handler: (message: { type: string }) => void): () => void` — для задачи 5.

- [ ] **Step 1: Write the failing test**

```js
// web/js/_tests/host.test.js
//
// Run with: node --test web/js/_tests/host.test.js

import test from "node:test";
import assert from "node:assert/strict";
import { HOST_VERSION, readHost, callHost, onHostMessage } from "../host.js";

test("a page with no host is the browser layout", () => {
  assert.equal(readHost({}), null);
});

test("a host of an unknown version is treated as no host", () => {
  assert.equal(readHost({ fleetdeckHost: { version: 2, surface: "board", glass: "glass" } }), null);
});

test("a host with an unknown surface or glass is treated as no host", () => {
  assert.equal(readHost({ fleetdeckHost: { version: HOST_VERSION, surface: "header", glass: "glass" } }), null);
  assert.equal(readHost({ fleetdeckHost: { version: HOST_VERSION, surface: "board", glass: "frosted" } }), null);
});

test("a valid host names its surface and glass", () => {
  const win = { fleetdeckHost: { version: HOST_VERSION, surface: "sessions", glass: "opaque" } };
  assert.deepEqual(readHost(win), { surface: "sessions", glass: "opaque" });
});

test("calling the window without its binding answers null, not a rejected promise", () => {
  assert.equal(callHost({}, "fleetdeckOpen", { kind: "card", path: "a.md" }), null);
});

test("calling the window passes the payload and resolves with the answer", async () => {
  const seen = [];
  const win = { fleetdeckOpen: (payload) => { seen.push(payload); return "ok"; } };
  assert.equal(await callHost(win, "fleetdeckOpen", { kind: "doc", path: "/d.md" }), "ok");
  assert.deepEqual(seen, [{ kind: "doc", path: "/d.md" }]);
});

test("a message from the window reaches the handler; a malformed one does not", () => {
  const win = { fleetdeckHost: { version: HOST_VERSION, surface: "board", glass: "glass", receive() {} } };
  const got = [];
  const stop = onHostMessage(win, (m) => got.push(m.type));
  win.fleetdeckHost.receive({ type: "show", section: "docs" });
  win.fleetdeckHost.receive({ section: "docs" });
  win.fleetdeckHost.receive(null);
  stop();
  win.fleetdeckHost.receive({ type: "show", section: "board" });
  assert.deepEqual(got, ["show"]);
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `node --test web/js/_tests/host.test.js`

Expected: FAIL — `Cannot find module '../host.js'`.

- [ ] **Step 3: Write minimal implementation**

```js
// web/js/host.js — the window around the page, when there is one.
//
// The fleetdeck window injects window.fleetdeckHost before the page loads and
// tells the page which part of itself to be. A page without it — a browser tab,
// or an older window — is the three-column panel it has always been.

export const HOST_VERSION = 1;

const SURFACES = new Set(["board", "orchestrator", "sessions"]);
const GLASSES = new Set(["glass", "vibrancy", "opaque"]);

export function readHost(win) {
  const host = win?.fleetdeckHost;
  if (!host || host.version !== HOST_VERSION) return null;
  if (!SURFACES.has(host.surface) || !GLASSES.has(host.glass)) return null;
  return { surface: host.surface, glass: host.glass };
}

export function callHost(win, name, payload) {
  const fn = win?.[name];
  if (typeof fn !== "function") return null;
  return Promise.resolve(fn(payload));
}

export function onHostMessage(win, handler) {
  const host = win?.fleetdeckHost;
  if (!host) return () => {};
  const previous = host.receive;
  host.receive = (message) => {
    if (message && typeof message.type === "string") handler(message);
  };
  return () => {
    host.receive = previous;
  };
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `node --test web/js/_tests/host.test.js`

Expected: PASS, 7 tests.

- [ ] **Step 5: Commit**

```bash
git add web/js/host.js web/js/_tests/host.test.js
git commit --signoff --message "feat(web): read the window's host object and talk to it"
```

---

### Task 2: Поверхности в `main.js`

**Files:**

- Create: `web/js/surfaces.js`
- Modify: `web/js/main.js` (блок монтирования модулей)
- Test: `web/js/_tests/surfaces.test.js`

**Interfaces:**

- Consumes: `readHost`, `callHost`, `HOST_VERSION` (задача 1).
- Produces:
  - `regionsFor(host): Set<"header" | "orchestrator" | "center" | "sessions" | "brand" | "counters">` — для задачи 7 (что монтировать в боковых поверхностях);
  - `layoutReport(host, snapshot): { version: 1, mode: "panel", fleet: string } | null` — вызов `fleetdeckLayout` для задачи 11;
  - атрибуты `data-surface` и `data-glass` на `<html>` при наличии хоста — для задачи 3.

- [ ] **Step 1: Write the failing test**

```js
// web/js/_tests/surfaces.test.js
import test from "node:test";
import assert from "node:assert/strict";
import { regionsFor, layoutReport } from "../surfaces.js";

test("without a host every region is mounted, as in a browser tab", () => {
  assert.deepEqual([...regionsFor(null)].sort(), ["center", "header", "orchestrator", "sessions"]);
});

test("the board surface mounts the centre column only", () => {
  assert.deepEqual([...regionsFor({ surface: "board", glass: "glass" })], ["center"]);
});

test("the orchestrator surface mounts its column and the brand row", () => {
  assert.deepEqual([...regionsFor({ surface: "orchestrator", glass: "glass" })].sort(), ["brand", "orchestrator"]);
});

test("the sessions surface mounts its column and the counters", () => {
  assert.deepEqual([...regionsFor({ surface: "sessions", glass: "glass" })].sort(), ["counters", "sessions"]);
});

test("only the board reports the panel layout, and only once it knows its fleet", () => {
  const board = { surface: "board", glass: "glass" };
  assert.equal(layoutReport(null, { fleet: "work" }), null);
  assert.equal(layoutReport({ surface: "sessions", glass: "glass" }, { fleet: "work" }), null);
  assert.equal(layoutReport(board, null), null);
  assert.equal(layoutReport(board, {}), null);
  assert.deepEqual(layoutReport(board, { fleet: "work" }), { version: 1, mode: "panel", fleet: "work" });
  assert.deepEqual(layoutReport(board, { fleet: "" }), { version: 1, mode: "panel", fleet: "" });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `node --test web/js/_tests/surfaces.test.js`

Expected: FAIL — `Cannot find module '../surfaces.js'`.

- [ ] **Step 3: Write minimal implementation**

```js
// web/js/surfaces.js — which part of the panel a web view is.
import { HOST_VERSION } from "./host.js";

const ALL = ["header", "orchestrator", "center", "sessions"];
const BY_SURFACE = {
  board: ["center"],
  orchestrator: ["orchestrator", "brand"],
  sessions: ["sessions", "counters"],
};

export function regionsFor(host) {
  return new Set(host ? BY_SURFACE[host.surface] : ALL);
}

export function layoutReport(host, snapshot) {
  if (!host || host.surface !== "board") return null;
  if (!snapshot || typeof snapshot.fleet !== "string") return null;
  return { version: HOST_VERSION, mode: "panel", fleet: snapshot.fleet };
}
```

В `main.js`: в начале прочитать `const host = readHost(window)`; при хосте поставить `document.documentElement.dataset.surface = host.surface` и `dataset.glass = host.glass`; каждый вызов `renderSessions`, `renderHeader`, `renderOrchestrator`, `renderBoard`, `createSections` и панелей обернуть проверкой `regions.has(...)`; в подписке на снимок один раз вызвать `callHost(window, "fleetdeckLayout", layoutReport(host, snapshot))`, когда отчёт не `null`. Элементы невыбранных регионов удаляются из DOM (`remove()`), а не прячутся.

- [ ] **Step 4: Run tests**

Run: `node --test web/js/_tests/surfaces.test.js && make test-web`

Expected: PASS; существующие тесты модулей не меняются.

- [ ] **Step 5: Commit**

```bash
git add web/js/surfaces.js web/js/_tests/surfaces.test.js web/js/main.js
git commit --signoff --message "feat(web): mount only the window surface a web view shows"
```

---

### Task 3: Стили E

**Files:**

- Modify: `web/app.css` (новый блок в конце файла)
- Test: `web/tests/app-css.test.js`

**Interfaces:**

- Consumes: `data-surface`, `data-glass` (задача 2); CSS-переменные `--host-inset-top`, `--host-inset-left`, `--host-inset-right`, которые ставит задача 5.
- Produces: классы `island`, `sheet`, `docs-island`, `doc-island`, элемент `#sheet-scrim`; токены `--glass-control`, `--glass-control-edge`, `--on-glass-muted`, `--surface-on-glass`, `--island-shadow`, `--radius-lg: 12px`, `--radius-xl: 18px` — для задачи 4.

Значения токенов — с канваса этапа 1 (генератор канваса, `TOKENS_LIGHT` и `TOKENS_DARK`). Светлая: `--on-glass-muted: #434a54`, `--surface-on-glass: rgb(255 255 255 / 80%)`, `--glass-control: rgb(255 255 255 / 52%)`, `--glass-control-edge: rgb(20 22 26 / 11%)`, `--island-shadow: 0 1px 2px rgb(20 22 26 / 8%), 0 4px 14px rgb(20 22 26 / 9%)`. Тёмная: `--on-glass-muted: #c1c7cf`, `--surface-on-glass: rgb(27 30 36 / 80%)`, `--glass-control: rgb(255 255 255 / 9%)`, `--glass-control-edge: rgb(255 255 255 / 15%)`, `--island-shadow: 0 1px 2px rgb(0 0 0 / 30%), 0 4px 16px rgb(0 0 0 / 28%)`. Каждое — в трёх блоках палитры, как требует шапка `app.css`.

- [ ] **Step 1: Write the failing test**

```js
// web/tests/app-css.test.js — добавить в конец; ruleBody и countDefinitions — помощники из шага ниже
test("a web view on glass or vibrancy paints no background of its own", () => {
  assert.match(ruleBody(':root[data-glass="glass"] body'), /background:\s*transparent/);
  assert.match(ruleBody(':root[data-glass="vibrancy"] body'), /background:\s*transparent/);
});

test("with reduced transparency a side surface paints its own panel", () => {
  const body = ruleBody(':root[data-glass="opaque"]:not([data-surface="board"]) body');
  assert.match(body, /background:\s*var\(--surface\)/);
});

test("the board keeps clear of the panels by the insets the window sends", () => {
  const board = ruleBody(':root[data-surface="board"] #board');
  assert.match(board, /padding-top:\s*var\(--host-inset-top/);
  assert.match(board, /padding-left:\s*var\(--host-inset-left/);
});

test("every glass token has a light and both dark definitions", () => {
  for (const token of ["--on-glass-muted", "--surface-on-glass", "--glass-control", "--island-shadow"]) {
    assert.equal(countDefinitions(token), 3, token);
  }
});
```

В файле есть только `scan`: он отдаёт селекторы верхнего уровня, но не тела правил. Добавить после `scan` два помощника:

```js
// The declarations of the rule whose selector list contains selector.
function ruleBody(selector) {
  const stripped = css.replace(/\/\*[\s\S]*?\*\//g, "");
  let from = 0;
  for (;;) {
    const at = stripped.indexOf(selector, from);
    if (at < 0) throw new Error(`web/app.css has no rule for ${selector}`);
    const open = stripped.indexOf("{", at);
    const tail = stripped.slice(at + selector.length, open).trim();
    if (tail === "" || tail.startsWith(",")) return stripped.slice(open + 1, stripped.indexOf("}", open));
    from = at + selector.length;
  }
}

// How many times a custom property is defined: the light palette and both dark ones.
function countDefinitions(token) {
  return (css.match(new RegExp(`${token}\\s*:`, "g")) ?? []).length;
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `node --test web/tests/app-css.test.js`

Expected: FAIL — правила не найдены.

- [ ] **Step 3: Write minimal implementation**

В конец `web/app.css` добавить блок `/* --- the fleetdeck window: E layout --- */` с правилами из теста, токенами в трёх блоках палитры и стилями островов, листа и доков по значениям канваса. Карточка: `border-radius: 10px`, полоса зоны — `box-shadow: inset 3px 0 0` цветом зоны. Лист: `position: absolute; top: calc(var(--host-inset-top) - 4px); left: var(--host-inset-left); right: calc(var(--host-inset-right) + 16px); bottom: 16px; border-radius: var(--radius-xl); background: var(--surface-raised)`. Затемнение: `rgb(20 22 26 / 22%)` в светлой, `rgb(0 0 0 / 48%)` в тёмной. Капсулы в листе: `border-radius: 999px; background: var(--surface-hover); box-shadow: inset 0 0 0 1px var(--border)`. Все правила — под `:root[data-surface]`, чтобы раскладка без хоста не менялась.

- [ ] **Step 4: Run tests**

Run: `make test-web`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/app.css web/tests/app-css.test.js
git commit --signoff --message "feat(web): add the E layout styles for the window surfaces"
```

---

### Task 4: Лист K1 и S1, доки Д1

**Files:**

- Modify: `web/js/card.js`, `web/js/session.js`, `web/js/reader.js`, `web/js/docs.js`, `web/index.html`, `web/js/main.js`
- Test: `web/tests/card.test.js`, `web/tests/session.test.js`, `web/tests/docs.test.js`

**Interfaces:**

- Consumes: классы и токены задачи 3.
- Produces: `<div id="sheet-scrim" hidden>` в `#center`; разметка закрытия и островов, которую проверяет задача 16 на стенде.

- [ ] **Step 1: Write the failing tests**

Тесты пользуются помощниками своих файлов: `open(snap, path, options)` в `web/tests/card.test.js` (возвращает `root`), `mount()` в `web/tests/session.test.js` (возвращает `root`), `stubFetch`, `ok`, `DOCS` и `root` в `web/tests/docs.test.js`. `fake-dom.js` понимает только одиночный класс, тег и атрибут в селекторах, поэтому составные классы проверяются через `classList`.

```js
// web/tests/card.test.js — добавить
test("the close control is a round button with a drawn cross, not a bare glyph", () => {
  const { root } = open(JSON.parse(FIXTURE));
  const close = root.querySelector(".card-close");
  assert.equal(close.getAttribute("aria-label"), t("card_close"));
  assert.ok(close.querySelector("svg"), "the cross is an svg so it sits centred in the round button");
});
```

```js
// web/tests/session.test.js — добавить
test("the session head keeps its order: font buttons, name, close", async () => {
  const { root } = await mount();
  const head = root.querySelector(".s-head").children.map((node) => node.className.split(" ")[0]);
  assert.deepEqual(head, ["term-font", "s-who", "s-close"]);
});
```

```js
// web/tests/docs.test.js — добавить
test("the list and the document are two islands", async () => {
  stubFetch([["/api/docs", ok(DOCS)]]);
  renderDocs(root);
  await settle();
  assert.ok(root.querySelector(".docs-island")?.classList.contains("docs-list"));
  assert.ok(root.querySelector(".doc-island")?.classList.contains("docs-main"));
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `node --test web/tests/card.test.js web/tests/session.test.js web/tests/docs.test.js`

Expected: FAIL на тестах карточки и доков. Тест порядка шапки сессии проходит сразу: он закрепляет нынешний порядок, который лист S1 обязан сохранить.

- [ ] **Step 3: Implement**

- `card.js` и `reader.js`: кнопка закрытия получает `<svg width="12" height="12" viewBox="0 0 12 12"><path d="M3 3 L9 9 M9 3 L3 9" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" fill="none"></path></svg>` вместо текста `✕`; `aria-label` не меняется.
- `docs.js`: `nav.docs-list` получает класс `docs-island`, `div.docs-main` — `doc-island`.
- `index.html`: в `#center` добавить `<div id="sheet-scrim" hidden></div>`; `main.js` показывает его вместе с любой из трёх панелей и прячет при закрытии.

- [ ] **Step 4: Run tests**

Run: `make test-web`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/js/card.js web/js/session.js web/js/reader.js web/js/docs.js web/index.html web/js/main.js web/tests/card.test.js web/tests/session.test.js web/tests/docs.test.js
git commit --signoff --message "feat(web): open cards, sessions and documents as a sheet over the board"
```

---

### Task 5: Сообщения окна в странице

**Files:**

- Create: `web/js/hostactions.js`
- Modify: `web/js/main.js`
- Test: `web/js/_tests/hostactions.test.js`

**Interfaces:**

- Consumes: `onHostMessage`, `callHost` (задача 1); `cardPanel.open`, `reader.open`, `openSession`, `sections.show`, открытие формы новой карточки из `main.js`; смена темы из `theme.js`.
- Produces: `wireHostActions(win, host, targets): () => void`, где `targets = { openCard(path), openDoc(path), openSession(short), showSection(id), openNewCard(), cycleTheme(): string, applyTheme(choice), setInsets({ top, left, right }), setGlass(glass), setFolded(folded), focusTerminal() }`. Принимаемые сообщения — те, что шлёт контроллер задачи 11.

Какие `type` принимает каждая поверхность — строго по таблице спеки 6.2: доска — `open`, `show`, `newCard`, `cycleTheme`, `insets`, `glass`; оркестратор — `theme`, `glass`, `folded`, `focusTerminal`, `fullscreen`; сессии — `theme`, `glass`, `folded`.

- [ ] **Step 1: Write the failing test**

```js
// web/js/_tests/hostactions.test.js
import test from "node:test";
import assert from "node:assert/strict";
import { wireHostActions } from "../hostactions.js";

function setup(surface) {
  const calls = [];
  const win = { fleetdeckHost: { version: 1, surface, glass: "glass", receive() {} }, fleetdeckTheme: (c) => calls.push(["report", c]) };
  const record = (name) => (...args) => { calls.push([name, ...args]); return "dark"; };
  const targets = Object.fromEntries(
    ["openCard", "openDoc", "openSession", "showSection", "openNewCard", "cycleTheme", "applyTheme", "setInsets", "setGlass", "setFolded", "focusTerminal", "setFullscreen"].map((n) => [n, record(n)]),
  );
  wireHostActions(win, { surface, glass: "glass" }, targets);
  return { send: (m) => win.fleetdeckHost.receive(m), calls };
}

test("the board opens what the window asks it to open", () => {
  const { send, calls } = setup("board");
  send({ type: "open", kind: "card", path: "cards/T-1.md" });
  send({ type: "open", kind: "doc", path: "/docs/r.md" });
  send({ type: "open", kind: "session", short: "abc12345" });
  assert.deepEqual(calls, [["openCard", "cards/T-1.md"], ["openDoc", "/docs/r.md"], ["openSession", "abc12345"]]);
});

test("the board cycles the theme and reports the result to the window", () => {
  const { send, calls } = setup("board");
  send({ type: "cycleTheme" });
  assert.deepEqual(calls, [["cycleTheme"], ["report", "dark"]]);
});

test("a side surface ignores what only the board handles", () => {
  const { send, calls } = setup("sessions");
  send({ type: "open", kind: "card", path: "x.md" });
  send({ type: "show", section: "docs" });
  send({ type: "theme", choice: "light" });
  assert.deepEqual(calls, [["applyTheme", "light"]]);
});

test("only the orchestrator surface focuses its terminal and makes room for the window buttons", () => {
  const orchestrator = setup("orchestrator");
  orchestrator.send({ type: "focusTerminal" });
  orchestrator.send({ type: "fullscreen", on: true });
  const sessions = setup("sessions");
  sessions.send({ type: "focusTerminal" });
  assert.deepEqual(orchestrator.calls, [["focusTerminal"], ["setFullscreen", true]]);
  assert.deepEqual(sessions.calls, []);
});

test("an unknown message is skipped", () => {
  const { send, calls } = setup("board");
  send({ type: "teleport" });
  assert.deepEqual(calls, []);
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `node --test web/js/_tests/hostactions.test.js`

Expected: FAIL — модуль не найден.

- [ ] **Step 3: Write minimal implementation**

```js
// web/js/hostactions.js — what the window asks of a page.
import { callHost, onHostMessage } from "./host.js";

const ACCEPTS = {
  board: new Set(["open", "show", "newCard", "cycleTheme", "insets", "glass"]),
  orchestrator: new Set(["theme", "glass", "folded", "focusTerminal", "fullscreen"]),
  sessions: new Set(["theme", "glass", "folded"]),
};

export function wireHostActions(win, host, targets) {
  const accepts = ACCEPTS[host.surface];
  return onHostMessage(win, (message) => {
    if (!accepts.has(message.type)) return;
    switch (message.type) {
      case "open":
        if (message.kind === "card") targets.openCard(message.path);
        else if (message.kind === "doc") targets.openDoc(message.path);
        else if (message.kind === "session") targets.openSession(message.short);
        return;
      case "show":
        targets.showSection(message.section);
        return;
      case "newCard":
        targets.openNewCard();
        return;
      case "cycleTheme":
        callHost(win, "fleetdeckTheme", targets.cycleTheme());
        return;
      case "theme":
        targets.applyTheme(message.choice);
        return;
      case "insets":
        targets.setInsets({ top: message.top, left: message.left, right: message.right });
        return;
      case "glass":
        targets.setGlass(message.glass);
        return;
      case "folded":
        targets.setFolded(message.folded === true);
        return;
      case "focusTerminal":
        targets.focusTerminal();
        return;
      case "fullscreen":
        targets.setFullscreen(message.on === true);
        return;
    }
  });
}
```

В `main.js` при хосте вызвать `wireHostActions(window, host, targets)` синхронно, на верхнем уровне модуля, до первого `await`: состояние `panel` от `pageLoadScript` должно означать, что приём сообщений уже подключён (спека 6.6). Параметры: `setInsets` ставит `--host-inset-top/left/right` на `<html>` в пикселях, `setGlass` меняет `data-glass`, `setFolded` — `data-folded` на колонке поверхности, `setFullscreen` — `data-fullscreen` на `<html>` (верхняя строка оркестратора убирает место под светофоры), `applyTheme` — установка темы из `theme.js` без записи в `localStorage`.

- [ ] **Step 4: Run tests**

Run: `make test-web`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/js/hostactions.js web/js/_tests/hostactions.test.js web/js/main.js
git commit --signoff --message "feat(web): act on what the window asks of each surface"
```

---

### Task 6: Действия боковых поверхностей через окно

**Files:**

- Create: `web/js/hostroutes.js`
- Modify: `web/js/main.js`; `web/js/header.js` (`renderHeader` и переход по пункту меню флота, на c0ea864 — `header.js:520`); `web/js/sessions.js` (`renderSessions` и кнопка `.fleet-other`, на c0ea864 — `sessions.js:724`); `web/js/columnresize.js` (кнопки свёртывания)
- Test: `web/js/_tests/hostroutes.test.js`, `web/js/_tests/sessions.test.js`, `web/js/_tests/header.test.js`

**Interfaces:**

- Consumes: `callHost` (задача 1).
- Produces:
  - `routesFor(win, host, local): { openCard(path), openDoc(path), openSession(short), switchFleet(name), fold(side, folded), openOrchestrator() }` — для `main.js`, который раздаёт маршруты модулям; вызовы `fleetdeckOpen`, `fleetdeckSwitchFleet`, `fleetdeckPanel` — для задачи 11;
  - `renderSessions(root, onSelect, onOpenCard, { now, switchFleet })` — по умолчанию `switchFleet: (name) => switchFleet(name, { storage: pageStorage() })` из `fleet.js`;
  - `renderHeader(root, { switchFleet })` с тем же умолчанием и `menuGo(go, { switchFleet, assign })` — чистый переход по пункту меню флота, экспортируется из `header.js`.

Смена флота на странице сегодня происходит в трёх местах: стартовая страница (`start.js`), меню флота в шапке и кнопка другого флота в колонке сессий. Стартовая страница живёт одним видом и не меняется. Шапка и колонка сессий получают `switchFleet` из `main.js`, иначе смена флота из строки бренда и из колонки сессий пошла бы мимо окна.

- [ ] **Step 1: Write the failing tests**

```js
// web/js/_tests/hostroutes.test.js
import test from "node:test";
import assert from "node:assert/strict";
import { routesFor } from "../hostroutes.js";

function localSpy() {
  const calls = [];
  const local = new Proxy({}, { get: (_, name) => (...args) => calls.push([name, ...args]) });
  return { local, calls };
}

test("without a host every route stays in the page", () => {
  const { local, calls } = localSpy();
  const routes = routesFor({}, null, local);
  routes.openCard("a.md");
  routes.switchFleet("work");
  assert.deepEqual(calls, [["openCard", "a.md"], ["switchFleet", "work"]]);
});

test("a side surface sends opening, fleet switching and folding to the window", () => {
  const sent = [];
  const win = {
    fleetdeckOpen: (p) => sent.push(["open", p]),
    fleetdeckSwitchFleet: (n) => sent.push(["fleet", n]),
    fleetdeckPanel: (p) => sent.push(["panel", p]),
  };
  const { local, calls } = localSpy();
  const routes = routesFor(win, { surface: "sessions", glass: "glass" }, local);
  routes.openSession("abc12345");
  routes.openCard("cards/T-1.md");
  routes.openDoc("/docs/r.md");
  routes.switchFleet("home");
  routes.fold("sessions", true);
  assert.deepEqual(calls, []);
  assert.deepEqual(sent, [
    ["open", { kind: "session", short: "abc12345" }],
    ["open", { kind: "card", path: "cards/T-1.md" }],
    ["open", { kind: "doc", path: "/docs/r.md" }],
    ["fleet", "home"],
    ["panel", { side: "sessions", folded: true }],
  ]);
});

test("the board opens locally but switches fleet and focuses the orchestrator through the window", () => {
  const sent = [];
  const win = { fleetdeckSwitchFleet: (n) => sent.push(n), fleetdeckOpen: (p) => sent.push(p) };
  const { local, calls } = localSpy();
  const routes = routesFor(win, { surface: "board", glass: "glass" }, local);
  routes.openCard("a.md");
  routes.switchFleet("home");
  routes.openOrchestrator();
  assert.deepEqual(calls, [["openCard", "a.md"]]);
  assert.deepEqual(sent, ["home", { kind: "orchestrator" }]);
});
```

```js
// web/js/_tests/sessions.test.js — добавить после тестов «several fleets»;
// в импорт из "../../tests/fake-dom.js" добавить fireEvent
test("several fleets: pressing another fleet goes through the switcher the column was given", async () => {
  const went = [];
  const { root } = await list(structuredClone(TWO_FLEETS), { switchFleet: (name) => went.push(name) });
  fireEvent(root.querySelector(".fleet-other"), "click");
  assert.deepEqual(went, ["A"]);
});
```

```js
// web/js/_tests/header.test.js — добавить; menuGo — в импорт из "../header.js"
test("the fleet menu switches fleet through the given switcher and goes elsewhere through assign", () => {
  const went = [];
  const assigned = [];
  const deps = { switchFleet: (name) => went.push(name), assign: (path) => assigned.push(path) };
  menuGo({ fleet: "B" }, deps);
  menuGo({ path: "/" }, deps);
  assert.deepEqual(went, ["B"]);
  assert.deepEqual(assigned, ["/"]);
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `node --test web/js/_tests/hostroutes.test.js web/js/_tests/sessions.test.js web/js/_tests/header.test.js`

Expected: FAIL — нет `hostroutes.js` и `menuGo`; колонка сессий зовёт `switchFleet` из `fleet.js`, а не переданный.

- [ ] **Step 3: Implement**

```js
// web/js/hostroutes.js — where an action from one surface goes.
import { callHost } from "./host.js";

export function routesFor(win, host, local) {
  if (!host) return local;
  const onBoard = host.surface === "board";
  const open = (payload, localCall) => (onBoard ? localCall() : callHost(win, "fleetdeckOpen", payload));
  return {
    openCard: (path) => open({ kind: "card", path }, () => local.openCard(path)),
    openDoc: (path) => open({ kind: "doc", path }, () => local.openDoc(path)),
    openSession: (short) => open({ kind: "session", short }, () => local.openSession(short)),
    switchFleet: (name) => callHost(win, "fleetdeckSwitchFleet", name),
    fold: (side, folded) => callHost(win, "fleetdeckPanel", { side, folded }),
    openOrchestrator: () => callHost(win, "fleetdeckOpen", { kind: "orchestrator" }),
  };
}
```

```js
// web/js/header.js — рядом с nextMenuState
export function menuGo(go, { switchFleet: goFleet, assign }) {
  if (go.fleet) goFleet(go.fleet);
  else assign(go.path);
}
```

- `header.js`: `renderHeader(root, { switchFleet: goFleet = (name) => switchFleet(name, { storage: pageStorage() }) } = {})`; строка `if (next.go.fleet) switchFleet(...) else globalThis.location.assign(next.go.path)` заменяется на `menuGo(next.go, { switchFleet: goFleet, assign: (path) => globalThis.location.assign(path) })`. Переход не во флот (`/`, мастер) в поверхности отменит делегат навигации и отправит в доску (спека 6.7).
- `sessions.js`: `renderSessions(root, onSelect, onOpenCard, { now = Date.now, switchFleet: goFleet = (name) => switchFleet(name, { storage: pageStorage() }) } = {})`; обработчик `.fleet-other` зовёт `goFleet(el.dataset.fleet)`.
- `main.js`: собрать `local` из нынешних замыканий (`cardPanel.open`, `reader.open`, `openSession`, свёртывание из `columnresize.js`, `switchFleet` с `pageStorage()`), построить `routes = routesFor(window, host, local)` и передать `routes.*` в `renderSessions` (`onSelect`, `onOpenCard`, `switchFleet`), `renderHeader` (`switchFleet`), `renderOrchestrator` (`terminalLinks.open`) и `card.js` (`onOpenSession`). Доска перед открытием сессии сверяет `short` с `snapshot.orchestratorSession`; совпало — `routes.openOrchestrator()` вместо листа (спека 6.3).

- [ ] **Step 4: Run tests**

Run: `make test-web`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/js/hostroutes.js web/js/_tests/hostroutes.test.js web/js/main.js web/js/header.js web/js/_tests/header.test.js web/js/sessions.js web/js/_tests/sessions.test.js web/js/columnresize.js
git commit --signoff --message "feat(web): route opening, fleet switching and folding through the window"
```

---

### Task 7: Модель капсул, части шапки, перезагрузка

**Files:**

- Create: `web/js/capsules.js`
- Modify: `web/js/header.js` (вынести `limitsOf`, `renderBrandRow`, `renderCounters`), `web/js/buildcheck.js` (`reloadNow`, `renderBuildBanner`), `web/js/main.js`
- Test: `web/js/_tests/capsules.test.js`, `web/tests/buildcheck.test.js`

**Interfaces:**

- Consumes: `regionsFor` (задача 2), `callHost` (задача 1), `t` из `i18n.js`.
- Produces:
  - `capsuleModel({ section, themeLabel, limits, t, colors }): { version: 1, tabs: [{ id, label, selected }], newCard: { label }, theme: { label }, limits: [{ label, text, level, color }] }` — вызов `fleetdeckCapsules` для задач 11 и 13;
  - `limitsOf(snapshot): [{ label, pct, level }]` — выносится из `header.js`, им пользуются `renderHeader` и доска для `capsuleModel`;
  - `renderBrandRow(root, deps)` и `renderCounters(root, deps)` из `header.js` — для `main.js` в поверхностях оркестратора и сессий; строка бренда рисуется внутри `<header id="header">`, на это опирается задача 14;
  - `reloadNow(storage, own, reload, fleet = "", { delegate = false } = {})` — при `delegate` только вызывает `reload()` и ничего не пишет в `sessionStorage` своего вида;
  - `renderBuildBanner(root, subscribe, { doc, storage, reload, hostReload, delegate })` — третий аргумент уже объект параметров; к нему добавляется `delegate` и передаётся в `reloadNow`. `main.js` передаёт `delegate: true` в поверхностях оркестратора и сессий (спека 6.4).

- [ ] **Step 1: Write the failing tests**

```js
// web/js/_tests/capsules.test.js
import test from "node:test";
import assert from "node:assert/strict";
import { capsuleModel } from "../capsules.js";

const t = (key) => ({ tab_board: "Доска", tab_docs: "Доки", new_card: "+ карточка" })[key] ?? key;
const colors = { cool: "#2f9e44", warm: "#a15c00", hot: "#c92a2a", stale: "#5b6470", off: "#8791a0" };

test("the capsules carry the page's own words and the selected tab", () => {
  const m = capsuleModel({ section: "docs", themeLabel: "тема: авто", limits: [], t, colors });
  assert.deepEqual(m.tabs, [
    { id: "board", label: "Доска", selected: false },
    { id: "docs", label: "Доки", selected: true },
  ]);
  assert.equal(m.newCard.label, "+ карточка");
  assert.equal(m.theme.label, "тема: авто");
  assert.equal(m.version, 1);
});

test("a limit carries its level and the colour of that level in the current theme", () => {
  const m = capsuleModel({ section: "board", themeLabel: "", limits: [{ label: "5ч", pct: 37, level: "cool" }], t, colors });
  assert.deepEqual(m.limits, [{ label: "5ч", text: "37%", level: "cool", color: "#2f9e44" }]);
});

test("a limit with no number says so instead of a zero", () => {
  const m = capsuleModel({ section: "board", themeLabel: "", limits: [{ label: "7д", pct: null, level: "off" }], t, colors });
  assert.equal(m.limits[0].text, "—");
});
```

```js
// web/tests/buildcheck.test.js — добавить; fakeStorage, reloadNow и RELOAD_FROM_KEY в этом файле уже есть
test("a delegated reload asks the window and leaves this web view's storage alone", () => {
  const storage = fakeStorage();
  const reloads = [];
  reloadNow(storage, "abc1234", () => reloads.push(1), "work", { delegate: true });
  assert.equal(reloads.length, 1);
  assert.equal(storage.getItem(RELOAD_FROM_KEY) ?? null, null);
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `node --test web/js/_tests/capsules.test.js web/tests/buildcheck.test.js`

Expected: FAIL — нет `capsules.js`; `reloadNow` записывает, откуда уходит страница, и при `delegate`.

- [ ] **Step 3: Implement**

```js
// web/js/capsules.js — what the window draws in its capsules, in the page's words.
export function capsuleModel({ section, themeLabel, limits, t, colors }) {
  return {
    version: 1,
    tabs: [
      { id: "board", label: t("tab_board"), selected: section === "board" },
      { id: "docs", label: t("tab_docs"), selected: section === "docs" },
    ],
    newCard: { label: t("new_card") },
    theme: { label: themeLabel },
    limits: limits.map(({ label, pct, level }) => ({
      label,
      text: typeof pct === "number" ? `${Math.round(pct)}%` : "—",
      level,
      color: colors[level],
    })),
  };
}
```

```js
// web/js/buildcheck.js
export function reloadNow(storage, own, reload, fleet = "", { delegate = false } = {}) {
  // In a surface of the fleetdeck window the board runs the reload and keeps its
  // own account of it; this web view only asks (spec 6.4).
  if (delegate) {
    reload();
    return;
  }
  rememberReloadFrom(storage, own);
  try {
    if (fleet) storage?.setItem(RELOAD_FLEET_KEY, fleet);
    else storage?.removeItem(RELOAD_FLEET_KEY);
  } catch {
    // The page comes back on the start page, one click from where it was.
  }
  reload();
}
```

- `renderBuildBanner`: в объект параметров добавить `delegate = false`; обработчик щелчка зовёт `reloadNow(storage, own, doReload, fleet, { delegate })`.
- `header.js`: `limitsOf(snapshot)` — то, что сегодня внутри отрисовки лимитов, в виде данных `{ label, pct, level }`; `renderHeader` рисует лимиты из `limitsOf`. `renderBrandRow(root, deps)` рисует бренд, меню флота и кнопку обновления внутри `<header id="header">`; `renderCounters(root, deps)` — счётчики «ждут ответа» и «остановились».
- Доска: `colors` из `getComputedStyle(document.documentElement)` — `cool` → `--ok`, `warm` → `--attn`, `hot` → `--danger`, `stale` → `--text-muted`, `off` → `--text-faint`; `callHost(window, "fleetdeckCapsules", model)` при первом снимке и когда модель изменилась (сравнение `JSON.stringify`).

- [ ] **Step 4: Run tests**

Run: `make test-web`

Expected: PASS; существующие тесты шапки и `buildcheck` проходят без изменения ожиданий.

- [ ] **Step 5: Commit**

```bash
git add web/js/capsules.js web/js/_tests/capsules.test.js web/js/header.js web/js/buildcheck.js web/tests/buildcheck.test.js web/js/main.js
git commit --signoff --message "feat(web): describe the capsules and split the header for the window surfaces"
```

---

### Task 8: Реестр привязок

**Files:**

- Create: `cmd/fleetdeck-window/bridge.go`
- Test: `cmd/fleetdeck-window/bridge_test.go`

**Interfaces:**

- Consumes: —
- Produces:
  - `type bridgeHandler func(surface string, args json.RawMessage) (any, error)`;
  - `newBridge() *bridge`, `(*bridge).handle(name string, h bridgeHandler)`, `(*bridge).names() []string` (по алфавиту), `(*bridge).call(surface, name string, args json.RawMessage) (any, error)`;
  - `errUnknownBinding` — для задач 10 (обработчик сообщений поверхностей) и 12 (регистрация всех привязок).

- [ ] **Step 1: Write the failing test**

```go
//go:build darwin

package main

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestABindingIsCalledWithTheSurfaceThatAskedAndItsArguments(t *testing.T) {
	b := newBridge()
	var gotSurface string
	var gotArgs map[string]string
	b.handle("fleetdeckOpen", func(surface string, args json.RawMessage) (any, error) {
		gotSurface = surface
		return "ok", json.Unmarshal(args, &gotArgs)
	})
	answer, err := b.call("sessions", "fleetdeckOpen", json.RawMessage(`{"kind":"card","path":"a.md"}`))
	if err != nil || answer != "ok" {
		t.Fatalf("call = %v, %v", answer, err)
	}
	if gotSurface != "sessions" || !reflect.DeepEqual(gotArgs, map[string]string{"kind": "card", "path": "a.md"}) {
		t.Fatalf("handler saw %q %v", gotSurface, gotArgs)
	}
}

func TestAnUnknownBindingIsRefusedByName(t *testing.T) {
	_, err := newBridge().call("board", "fleetdeckTeleport", nil)
	if !errors.Is(err, errUnknownBinding) {
		t.Fatalf("err = %v, want errUnknownBinding", err)
	}
}

func TestBindingNamesAreSortedSoTheInjectedScriptIsStable(t *testing.T) {
	b := newBridge()
	for _, n := range []string{"fleetdeckReload", "fleetdeckOpen", "fleetdeckLayout"} {
		b.handle(n, func(string, json.RawMessage) (any, error) { return nil, nil })
	}
	if got := b.names(); !reflect.DeepEqual(got, []string{"fleetdeckLayout", "fleetdeckOpen", "fleetdeckReload"}) {
		t.Fatalf("names = %v", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/fleetdeck-window -run 'Binding' -count=1`

Expected: FAIL — `undefined: newBridge`.

- [ ] **Step 3: Write minimal implementation**

```go
//go:build darwin

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// errUnknownBinding is a call to a name the window never registered: a page of
// another build asking for something this window does not have.
var errUnknownBinding = errors.New("unknown binding")

type bridgeHandler func(surface string, args json.RawMessage) (any, error)

// bridge is the one place a binding is defined. The board's web view reaches it
// through webview_go's Bind, each surface's web view through its own message
// handler; both land here, so a binding is written once for all three.
type bridge struct {
	mu       sync.Mutex
	handlers map[string]bridgeHandler
}

func newBridge() *bridge { return &bridge{handlers: map[string]bridgeHandler{}} }

func (b *bridge) handle(name string, h bridgeHandler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers[name] = h
}

func (b *bridge) names() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, 0, len(b.handlers))
	for n := range b.handlers {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func (b *bridge) call(surface, name string, args json.RawMessage) (any, error) {
	b.mu.Lock()
	h, ok := b.handlers[name]
	b.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", errUnknownBinding, name)
	}
	return h(surface, args)
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./cmd/fleetdeck-window -run 'Binding' -count=1 -race`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/fleetdeck-window/bridge.go cmd/fleetdeck-window/bridge_test.go
git commit --signoff --message "feat(window): keep every binding in one registry for all web views"
```

---

### Task 9: Геометрия и рама

**Files:**

- Create: `cmd/fleetdeck-window/geometry.go`, `cmd/fleetdeck-window/frame_darwin.c`, `cmd/fleetdeck-window/frame_darwin.h`, `cmd/fleetdeck-window/frame_darwin.go`
- Modify: `cmd/fleetdeck-window/testsupport_darwin.go`
- Test: `cmd/fleetdeck-window/geometry_test.go`, `cmd/fleetdeck-window/frame_darwin_test.go`

**Interfaces:**

- Consumes: —
- Produces:
  - `type rect struct{ X, Y, W, H float64 }` (начало — левый верхний угол), `type insets struct{ Top, Left, Right float64 }`, `type panelWidths struct{ Orchestrator, Sessions float64; OrchestratorFolded, SessionsFolded bool }`, `type geometry struct{ Orchestrator, Sessions, Capsules rect; Board insets }`;
  - `layoutFor(width, height float64, w panelWidths) geometry` — для задачи 11;
  - `type glassMode string` с `glassModeGlass = "glass"`, `glassModeVibrancy = "vibrancy"`, `glassModeOpaque = "opaque"` — для задач 10, 11, 13;
  - C: `void *fd_frame_install(void *window)`, `void fd_frame_set_mode(void *frame, const char *mode)`, `void fd_frame_layout(void *frame, fd_rect orchestrator, fd_rect sessions, fd_rect capsules)`, `void *fd_frame_panel_content(void *frame, int side)`, `void *fd_frame_capsules(void *frame)`, `void *fd_frame_board(void *frame)`, `int fd_glass_available(void)`, `int fd_reduce_transparency(void)`;
  - Go: `installFrame(window unsafe.Pointer) *frame`, `(*frame).setMode(glassMode)`, `(*frame).layout(geometry)`, `(*frame).panelContent(side string) unsafe.Pointer`, `(*frame).capsules() unsafe.Pointer`, `(*frame).board() unsafe.Pointer`, `currentGlassMode() glassMode` — для задач 10, 12, 13.

- [ ] **Step 1: Write the failing geometry test**

```go
//go:build darwin

package main

import "testing"

func TestTheDefaultLayoutMatchesTheChosenDesign(t *testing.T) {
	g := layoutFor(1512, 982, panelWidths{Orchestrator: 368, Sessions: 348})
	want := geometry{
		Orchestrator: rect{X: 8, Y: 8, W: 368, H: 966},
		Sessions:     rect{X: 1156, Y: 8, W: 348, H: 966},
		Capsules:     rect{X: 386, Y: 12, W: 758, H: 32},
		Board:        insets{Top: 64, Left: 394, Right: 0},
	}
	if g != want {
		t.Fatalf("layout = %+v\nwant     %+v", g, want)
	}
}

func TestAFoldedSessionsPanelIsTheStripOfMarks(t *testing.T) {
	g := layoutFor(1512, 982, panelWidths{Orchestrator: 368, Sessions: 348, SessionsFolded: true})
	if g.Sessions.W != 48 || g.Sessions.X != 1512-8-48 {
		t.Fatalf("folded sessions = %+v", g.Sessions)
	}
	if g.Capsules.X+g.Capsules.W != g.Sessions.X-12 {
		t.Fatalf("capsules do not follow the folded panel: %+v", g.Capsules)
	}
}

func TestAPanelNeverGoesBelowItsReadableWidthOrAboveSixtyPercent(t *testing.T) {
	g := layoutFor(1512, 982, panelWidths{Orchestrator: 100, Sessions: 5000})
	if g.Orchestrator.W != 220 {
		t.Fatalf("orchestrator = %v, want the 220 floor", g.Orchestrator.W)
	}
	if g.Sessions.W != 1512*0.6 {
		t.Fatalf("sessions = %v, want 60%% of the window", g.Sessions.W)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./cmd/fleetdeck-window -run 'DefaultLayout|FoldedSessions|ReadableWidth' -count=1`

Expected: FAIL — `undefined: layoutFor`.

- [ ] **Step 3: Implement `geometry.go`**

```go
//go:build darwin

package main

// The numbers are the chosen design's (canvas page "Выбрано", spec 5.2), in points.
const (
	panelMargin     = 8.0
	capsuleTop      = 12.0
	capsuleHeight   = 32.0
	capsuleGapLeft  = 10.0
	capsuleGapRight = 12.0
	boardInsetTop   = 64.0
	boardGapLeft    = 18.0
	foldedWidth     = 48.0
	minPanelWidth   = 220.0 // web/js/columnwidth.js MIN_PIXELS
	maxPanelShare   = 0.6   // web/js/columnwidth.js MAX_PERCENT
)

type rect struct{ X, Y, W, H float64 }
type insets struct{ Top, Left, Right float64 }

type panelWidths struct {
	Orchestrator, Sessions             float64
	OrchestratorFolded, SessionsFolded bool
}

type geometry struct {
	Orchestrator, Sessions, Capsules rect
	Board                            insets
}

type glassMode string

const (
	glassModeGlass    glassMode = "glass"
	glassModeVibrancy glassMode = "vibrancy"
	glassModeOpaque   glassMode = "opaque"
)

func clampPanel(w, window float64, folded bool) float64 {
	if folded {
		return foldedWidth
	}
	if w < minPanelWidth {
		w = minPanelWidth
	}
	if limit := window * maxPanelShare; w > limit {
		w = limit
	}
	return w
}

func layoutFor(width, height float64, w panelWidths) geometry {
	ow := clampPanel(w.Orchestrator, width, w.OrchestratorFolded)
	sw := clampPanel(w.Sessions, width, w.SessionsFolded)
	h := height - 2*panelMargin
	o := rect{X: panelMargin, Y: panelMargin, W: ow, H: h}
	s := rect{X: width - panelMargin - sw, Y: panelMargin, W: sw, H: h}
	capX := o.X + o.W + capsuleGapLeft
	return geometry{
		Orchestrator: o,
		Sessions:     s,
		Capsules:     rect{X: capX, Y: capsuleTop, W: s.X - capsuleGapRight - capX, H: capsuleHeight},
		Board:        insets{Top: boardInsetTop, Left: o.X + o.W + boardGapLeft, Right: 0},
	}
}
```

- [ ] **Step 4: Run geometry tests**

Run: `go test ./cmd/fleetdeck-window -run 'DefaultLayout|FoldedSessions|ReadableWidth' -count=1`

Expected: PASS.

- [ ] **Step 5: Write the failing frame test**

В `testsupport_darwin.go` добавить помощники над скрытым окном из `t_create_hidden_window`, возвращающие: имя класса панели (`object_getClassName`), `style` и `cornerRadius` стекла, `blendingMode` и `material` у `NSVisualEffectView`, первый ли подвид корня — веб-вид доски, лежат ли панели выше него в `subviews`. В `frame_darwin_test.go` все нативные вызовы — в `TestMain`, результаты — в переменной `frameResult`, как `menu_darwin_test.go`:

```go
func TestPanelsAreRegularGlassWithTheDesignRadius(t *testing.T) {
	if !frameResult.glassAvailable {
		t.Skip("NSGlassEffectView is not on this system; the vibrancy test covers it")
	}
	for side, name := range []string{"orchestrator", "sessions"} {
		if frameResult.classes[side] != "NSGlassEffectView" {
			t.Fatalf("%s panel is %s", name, frameResult.classes[side])
		}
		if frameResult.styles[side] != 0 {
			t.Fatalf("%s panel style = %d, want 0 (Regular): Clear makes the text under it unreadable", name, frameResult.styles[side])
		}
		if frameResult.radii[side] != 18 {
			t.Fatalf("%s panel radius = %v", name, frameResult.radii[side])
		}
	}
}

func TestTheBoardStaysUnderneathThePanels(t *testing.T) {
	if !frameResult.boardFirst || !frameResult.panelsAbove {
		t.Fatalf("board first = %v, panels above = %v", frameResult.boardFirst, frameResult.panelsAbove)
	}
}

func TestVibrancyBlursTheBoardNotTheDesktop(t *testing.T) {
	if frameResult.vibrancyClass != "NSVisualEffectView" || frameResult.vibrancyBlending != 1 {
		t.Fatalf("vibrancy panel = %s blending %d, want NSVisualEffectView withinWindow (1)", frameResult.vibrancyClass, frameResult.vibrancyBlending)
	}
}

func TestOpaqueModeLeavesNoMaterialBehindTheSurfaces(t *testing.T) {
	if frameResult.opaqueClass != "NSView" {
		t.Fatalf("opaque panel = %s, want a plain NSView", frameResult.opaqueClass)
	}
}
```

- [ ] **Step 6: Run to verify it fails**

Run: `go test ./cmd/fleetdeck-window -run 'PanelsAre|BoardStays|Vibrancy|OpaqueMode' -count=1`

Expected: FAIL — нет помощников и нативных функций.

- [ ] **Step 7: Implement `frame_darwin.c`**

Порядок в `fd_frame_install`, всё на главном потоке:

1. `board = [window contentView]` — это `WKWebView` `webview_go` (`webview.h`, `setContentView:`).
2. Класс корневого вида `FleetdeckFrameRoot` создаётся один раз: `objc_allocateClassPair(objc_getClass("NSView"), "FleetdeckFrameRoot", 0)` и `class_addMethod(cls, sel_registerName("isFlipped"), (IMP)root_is_flipped, "c@:")`, где `root_is_flipped` возвращает `YES`, — так прямоугольники геометрии идут от левого верхнего угла.
3. `root = [[FleetdeckFrameRoot alloc] initWithFrame:[board frame]]`, `setAutoresizingMask:18`; `[window setContentView:root]`; `[board setFrame:[root bounds]]`, `setAutoresizingMask:18`; `[root addSubview:board]`.
4. `[window setStyleMask:[window styleMask] | (1UL << 15)]` (full-size content view), `setTitlebarAppearsTransparent:YES`, `setTitleVisibility:1` (hidden).
5. Контейнер: `objc_getClass("NSGlassEffectContainerView")` есть — его `contentView` получает перевёрнутый вид на весь корень, и панели кладутся туда; нет — панели кладутся прямо в `root` выше доски через `addSubview:positioned:relativeTo:` с `NSWindowAbove = 1` относительно `board`.
6. Обёртка панели по режиму:
   - `glass` — `objc_getClass("NSGlassEffectView")`, `setStyle:0L`, `setCornerRadius:18.0`; содержимое — перевёрнутый вид через `setContentView:`;
   - `vibrancy` — `NSVisualEffectView`, `setBlendingMode:1L` (withinWindow), `setMaterial:7L` (sidebar), `setState:1L` (active), `setWantsLayer:YES`, у слоя `setCornerRadius:18.0` и `setMasksToBounds:YES`; содержимое — подвид на всю панель;
   - `opaque` — простой `NSView` без фона; содержимое — подвид на всю панель.
7. `fd_frame_set_mode` пересоздаёт обёртки, перенося в новые те же виды содержимого (`removeFromSuperview`, затем `setContentView:` или `addSubview:`), и не трогает веб-виды внутри.
8. `fd_glass_available` — `objc_getClass("NSGlassEffectView") != NULL`. `fd_reduce_transparency` — `[[NSWorkspace sharedWorkspace] accessibilityDisplayShouldReduceTransparency]`. `currentGlassMode()` в Go: прозрачность уменьшена — `opaque`; стекла нет — `vibrancy`; иначе `glass`.

- [ ] **Step 8: Run tests**

Run: `go test ./cmd/fleetdeck-window -count=1 -race`

Expected: PASS; на системе без стекла `TestPanelsAreRegularGlassWithTheDesignRadius` пропускается, остальные проходят.

- [ ] **Step 9: Commit**

```bash
git add cmd/fleetdeck-window/geometry.go cmd/fleetdeck-window/geometry_test.go cmd/fleetdeck-window/frame_darwin.c cmd/fleetdeck-window/frame_darwin.h cmd/fleetdeck-window/frame_darwin.go cmd/fleetdeck-window/frame_darwin_test.go cmd/fleetdeck-window/testsupport_darwin.go
git commit --signoff --message "feat(window): put glass panels over the board web view"
```

---

### Task 10: Поверхности: скрипт хоста, переходы, жизненный цикл

**Files:**

- Create: `cmd/fleetdeck-window/hostscript.go`, `cmd/fleetdeck-window/navigation.go`, `cmd/fleetdeck-window/surface_darwin.c`, `cmd/fleetdeck-window/surface_darwin.h`, `cmd/fleetdeck-window/surface_darwin.go`
- Modify: `cmd/fleetdeck-window/testsupport_darwin.go`
- Test: `cmd/fleetdeck-window/hostscript_test.go`, `cmd/fleetdeck-window/navigation_test.go`, `cmd/fleetdeck-window/surface_darwin_test.go`

**Interfaces:**

- Consumes: `(*bridge).names`, `(*bridge).call` (задача 8); `(*frame).panelContent`, `(*frame).board`, `glassMode` (задача 9); `pageLoadScript(panelURL string) string` из T-057 (`owner.go`, имя подтверждено задачей 0).
- Produces:
  - `hostScript(surface string, glass glassMode, bindings []string) string` — для задачи 12;
  - `type navDecision struct{ Allow bool; Board, External string }` и `navigationDecision(panelURL, pageURL, target string) navDecision` — для задачи 11;
  - C: `void *fd_surface_create(void *board, void *container, const char *surface, const char *const *scripts, int count)`, `void fd_surface_load(void *s, const char *url)`, `void fd_surface_eval(void *s, const char *js)`, `void fd_surface_focus(void *s)`, `void fd_surface_destroy(void *s)`, `int fd_surface_live_handlers(void)`; экспортируемые Go-вызовы `fleetdeckSurfaceMessage(surface, message *C.char)` и `fleetdeckSurfaceNavigation(surface, target *C.char) C.int` (1 — разрешить, 0 — отменить);
  - Go: `surfaceScripts(kind, panelURL string, glass glassMode, b *bridge) []string` — скрипты поверхности по порядку: `hostScript(kind, glass, b.names())`, `pageLoadScript(panelURL)`; задача 14 добавляет к ним `noticeScriptFor(kind)`;
  - Go: `newSurface(board, container unsafe.Pointer, kind, panelURL string, glass glassMode, b *bridge) *surface`, `(*surface).load(url string)`, `(*surface).send(message map[string]any)`, `(*surface).reload()`, `(*surface).focus()`, `(*surface).close()` — для задачи 12.

- [ ] **Step 1: Write the failing hostscript test**

```go
//go:build darwin

package main

import (
	"strings"
	"testing"
)

func TestTheHostScriptNamesTheSurfaceAndGlassAtVersionOne(t *testing.T) {
	js := hostScript("sessions", glassModeGlass, nil)
	for _, want := range []string{`version:1`, `surface:"sessions"`, `glass:"glass"`, `receive:function`} {
		if !strings.Contains(js, want) {
			t.Fatalf("script lacks %s:\n%s", want, js)
		}
	}
}

func TestTheHostScriptDefinesEveryBindingAsAPromiseOverTheMessageHandler(t *testing.T) {
	js := hostScript("orchestrator", glassModeOpaque, []string{"fleetdeckOpen", "fleetdeckReload"})
	for _, want := range []string{`"fleetdeckOpen"`, `"fleetdeckReload"`, `messageHandlers.fleetdeck.postMessage`, `new Promise`} {
		if !strings.Contains(js, want) {
			t.Fatalf("script lacks %s", want)
		}
	}
}

func TestTheHostScriptLeavesAnExistingHostAlone(t *testing.T) {
	if !strings.HasPrefix(hostScript("board", glassModeGlass, nil), "(function(){if(window.fleetdeckHost)return;") {
		t.Fatal("a second injection must not replace the host a page already wired")
	}
}

func TestTheHostScriptRefusesAnUnknownSurface(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("an unknown surface must panic at build time, not load a page that mounts nothing")
		}
	}()
	hostScript("header", glassModeGlass, nil)
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./cmd/fleetdeck-window -run 'HostScript' -count=1`

Expected: FAIL — `undefined: hostScript`.

- [ ] **Step 3: Implement `hostscript.go`**

```go
//go:build darwin

package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// hostScript is the user script each web view gets before its page loads: the
// host object web/js/host.js reads, and, for a surface, the bindings the board
// gets from webview_go, carried over the surface's own message handler.
func hostScript(surface string, glass glassMode, bindings []string) string {
	switch surface {
	case "board", "orchestrator", "sessions":
	default:
		panic(fmt.Sprintf("hostScript: unknown surface %q", surface))
	}
	if bindings == nil {
		bindings = []string{}
	}
	names, _ := json.Marshal(bindings)
	var b strings.Builder
	b.WriteString("(function(){if(window.fleetdeckHost)return;")
	fmt.Fprintf(&b, `var host={version:1,surface:%q,glass:%q,receive:function(){}};`, surface, string(glass))
	b.WriteString("var pending={},next=0;")
	b.WriteString("host._reply=function(id,ok,value){var p=pending[id];if(!p)return;delete pending[id];if(ok)p.resolve(value);else p.reject(new Error(value));};")
	fmt.Fprintf(&b, "%s.forEach(function(name){window[name]=function(args){return new Promise(function(resolve,reject){var id=++next;pending[id]={resolve:resolve,reject:reject};window.webkit.messageHandlers.fleetdeck.postMessage(JSON.stringify({id:id,name:name,args:args===undefined?null:args}));});};});", names)
	b.WriteString("window.fleetdeckHost=host;})();")
	return b.String()
}
```

- [ ] **Step 4: Run hostscript tests**

Run: `go test ./cmd/fleetdeck-window -run 'HostScript' -count=1`

Expected: PASS.

- [ ] **Step 5: Write the failing navigation test**

```go
//go:build darwin

package main

import "testing"

func TestASurfaceStaysOnItsOwnPage(t *testing.T) {
	const panel = "http://127.0.0.1:7777/"
	const page = "http://127.0.0.1:7777/?fleet=work"
	cases := []struct {
		target string
		want   navDecision
	}{
		{"http://127.0.0.1:7777/?fleet=work", navDecision{Allow: true}},
		{"http://127.0.0.1:7777/?fleet=work#top", navDecision{Allow: true}},
		{"http://127.0.0.1:7777/setup.html", navDecision{Board: "http://127.0.0.1:7777/setup.html"}},
		{"http://127.0.0.1:7777/?fleet=home", navDecision{Board: "http://127.0.0.1:7777/?fleet=home"}},
		{"http://127.0.0.1:7777/", navDecision{Board: "http://127.0.0.1:7777/"}},
		{"https://github.com/kroticw/fleetdeck", navDecision{External: "https://github.com/kroticw/fleetdeck"}},
		{"http://127.0.0.1:9999/?fleet=work", navDecision{External: "http://127.0.0.1:9999/?fleet=work"}},
		{"file:///etc/hosts", navDecision{}},
		{"data:text/html,hi", navDecision{}},
		{"javascript:void(0)", navDecision{}},
	}
	for _, c := range cases {
		if got := navigationDecision(panel, page, c.target); got != c.want {
			t.Errorf("%s: got %+v, want %+v", c.target, got, c.want)
		}
	}
}
```

- [ ] **Step 6: Run to verify it fails**

Run: `go test ./cmd/fleetdeck-window -run 'StaysOnItsOwnPage' -count=1`

Expected: FAIL — `undefined: navigationDecision`.

- [ ] **Step 7: Implement `navigation.go`**

```go
//go:build darwin

package main

import "net/url"

// navDecision is what a surface's web view does with a navigation (spec 6.7):
// load it, hand it to the board, or open it in the system browser. The zero
// value cancels it.
type navDecision struct {
	Allow    bool
	Board    string
	External string
}

func navigationDecision(panelURL, pageURL, target string) navDecision {
	t, err := url.Parse(target)
	if err != nil || (t.Scheme != "http" && t.Scheme != "https") {
		return navDecision{}
	}
	panel, err := url.Parse(panelURL)
	if err != nil {
		return navDecision{}
	}
	if t.Scheme != panel.Scheme || t.Host != panel.Host {
		return navDecision{External: target}
	}
	own, err := url.Parse(pageURL)
	if err == nil && t.Path == own.Path && t.RawQuery == own.RawQuery {
		return navDecision{Allow: true}
	}
	return navDecision{Board: target}
}
```

- [ ] **Step 8: Run navigation tests**

Run: `go test ./cmd/fleetdeck-window -run 'StaysOnItsOwnPage' -count=1`

Expected: PASS.

- [ ] **Step 9: Write the failing surface tests**

Помощники в `testsupport_darwin.go` над скрытым окном и рамой задачи 9 собирают в `TestMain` для поверхности `sessions`: `drawsBackground` веб-вида поверхности (`valueForKey:@"drawsBackground"`); совпадают ли `processPool` и `websiteDataStore` с доской; число `userScripts`; число регистраций обработчика `fleetdeck` (счётчик ведёт класс обработчика); затем 20 кругов `fd_surface_create` и `fd_surface_destroy` на обеих панелях, после которых — `fd_surface_live_handlers()`, число подвидов в контейнерах панелей и сколько раз зарегистрирован класс обработчика.

```go
func TestASurfaceIsATransparentWebViewSharingTheBoardsProcess(t *testing.T) {
	r := surfaceResult
	if r.drawsBackground {
		t.Fatal("a surface web view must not paint a background over the glass")
	}
	if !r.sharesPool || !r.sharesStore {
		t.Fatalf("pool shared = %v, store shared = %v: a surface on its own store splits localStorage", r.sharesPool, r.sharesStore)
	}
	if r.userScripts != 2 || r.handlers != 1 {
		t.Fatalf("user scripts = %d (want the host script and the page load script), handlers = %d", r.userScripts, r.handlers)
	}
}

func TestSurfacesCreatedAndDestroyedManyTimesLeaveNothingBehind(t *testing.T) {
	r := surfaceChurnResult
	if r.liveHandlers != 0 {
		t.Fatalf("live message handlers after 20 rounds = %d, want 0", r.liveHandlers)
	}
	if r.panelSubviews != 0 {
		t.Fatalf("subviews left in the panels = %d, want 0", r.panelSubviews)
	}
	if r.handlerClassRegistrations != 1 {
		t.Fatalf("handler class registered %d times, want once per process", r.handlerClassRegistrations)
	}
}
```

- [ ] **Step 10: Run to verify they fail**

Run: `go test ./cmd/fleetdeck-window -run 'ASurfaceIs|CreatedAndDestroyed' -count=1`

Expected: FAIL.

- [ ] **Step 11: Implement `surface_darwin.c`**

Создание (`fd_surface_create`):

1. `config = [WKWebViewConfiguration new]`; `[config setProcessPool:[[board configuration] processPool]]`; `[config setWebsiteDataStore:[[board configuration] websiteDataStore]]`.
2. Скрипты `scripts[0..count)` из `surfaceScripts` — в `[config userContentController]` по порядку, каждый `[[WKUserScript alloc] initWithSource:injectionTime:0 forMainFrameOnly:YES]` (0 — начало документа).
3. Класс `FleetdeckSurfaceHandler` (`NSObject`, протокол `WKScriptMessageHandler`, метод `userContentController:didReceiveScriptMessage:` с типом `"v@:@@"`) регистрируется один раз за процесс под статическим флагом. Экземпляр хранит имя поверхности ассоциированным объектом; метод берёт `[[message body] UTF8String]` и зовёт `fleetdeckSurfaceMessage`. Счётчик живых экземпляров растёт при создании и падает в собственной реализации `dealloc`, которая после уменьшения зовёт `dealloc` суперкласса через `objc_msgSendSuper`. Регистрация — `addScriptMessageHandler:handler name:@"fleetdeck"`.
4. Класс `FleetdeckSurfaceNavigation` (протоколы `WKNavigationDelegate` и `WKUIDelegate`), тоже один раз за процесс:
   - `webView:decidePolicyForNavigationAction:decisionHandler:` берёт `[[[action request] URL] absoluteString]`, зовёт `fleetdeckSurfaceNavigation` и отвечает блоку `decisionHandler` значением 1 (allow) или 0 (cancel). Блок вызывается из C по ABI блоков: поле `invoke` структуры блока — `((struct { void *isa; int flags; int reserved; void (*invoke)(void *, long); } *)handler)->invoke(handler, policy)`;
   - `webView:createWebViewWithConfiguration:forNavigationAction:windowFeatures:` передаёт адрес действия в `fleetdeckSurfaceNavigation` и возвращает `NULL`.
5. Веб-вид: `initWithFrame:configuration:`, `setValue:@NO forKey:@"drawsBackground"`, `setUnderPageBackgroundColor:[NSColor clearColor]`, `setNavigationDelegate:` и `setUIDelegate:` — экземпляр навигации, `setAutoresizingMask:18`, добавить в `container`.

Уничтожение (`fd_surface_destroy`), строго в этом порядке (спека 6.8): `stopLoading`; `setNavigationDelegate:NULL`, `setUIDelegate:NULL`; `[[config userContentController] removeScriptMessageHandlerForName:@"fleetdeck"]`; `removeAllUserScripts`; `removeFromSuperview`; `release` веб-вида, конфигурации, экземпляра обработчика и экземпляра навигации.

Остальное: `fd_surface_eval` — `evaluateJavaScript:completionHandler:` с `NULL`; `fd_surface_focus` — `[[webview window] makeFirstResponder:webview]`; `fd_surface_live_handlers` — счётчик из п. 3. Go: `fleetdeckSurfaceMessage` разбирает `{id, name, args}`, в горутине зовёт `bridge.call(surface, name, args)` и через `w.Dispatch` отвечает `window.fleetdeckHost._reply(id, ok, value)` в тот же веб-вид.

- [ ] **Step 12: Run tests**

Run: `go test ./cmd/fleetdeck-window -count=1 -race`

Expected: PASS.

- [ ] **Step 13: Commit**

```bash
git add cmd/fleetdeck-window/hostscript.go cmd/fleetdeck-window/hostscript_test.go cmd/fleetdeck-window/navigation.go cmd/fleetdeck-window/navigation_test.go cmd/fleetdeck-window/surface_darwin.c cmd/fleetdeck-window/surface_darwin.h cmd/fleetdeck-window/surface_darwin.go cmd/fleetdeck-window/surface_darwin_test.go cmd/fleetdeck-window/testsupport_darwin.go
git commit --signoff --message "feat(window): give the orchestrator and sessions columns web views of their own"
```

---

### Task 11: Контроллер окна

**Files:**

- Create: `cmd/fleetdeck-window/controller.go`
- Test: `cmd/fleetdeck-window/controller_test.go`

**Interfaces:**

- Consumes: `layoutFor`, `geometry`, `panelWidths`, `glassMode` (задача 9); `navigationDecision` (задача 10); `pageLoadTries` (T-057, имя подтверждено задачей 0); вызовы страниц из задач 2, 5, 6, 7.
- Produces для задач 12 (исполнитель) и 13 (капсулы):
  - `type effect interface{}` и эффекты `createSurfaces{Fleet, URL string; Glass glassMode}`, `destroySurfaces{}`, `sendTo{Surface string; Message map[string]any}`, `focusSurface{Surface string}`, `navigateBoard{URL string}`, `openExternal{URL string}`, `reloadSurface{Surface string}`, `showFailedPage{}`, `setAppearance{Choice string}`, `applyGeometry{G geometry}`, `saveWidths{W panelWidths}`, `setCapsules{Model json.RawMessage}`, `setFrameMode{Mode glassMode}`, `reloadAll{}`;
  - `newController(baseURL string, widths panelWidths, glass glassMode) *controller`;
  - методы, возвращающие `[]effect`: `layout(version int, mode, fleet string)`, `pageLoaded(surface, state string)`, `open(kind, path, short string)`, `switchFleet(name string)`, `panel(side string, folded bool)`, `theme(choice string)`, `capsules(model json.RawMessage)`, `capsuleAction(action string)` (`"tab:board"`, `"tab:docs"`, `"newCard"`, `"theme"`), `boardShowsOwnPage()`, `resized(width, height float64, fullscreen bool)`, `glassChanged(mode glassMode)`, `reload()`;
  - `navigate(surface, target string) (allow bool, effects []effect)` — для делегата навигации задачи 12.

Правила, которые закрепляют тесты:

- рама — только после `layout(1, "panel", fleet)`; повторный отчёт с тем же флотом поверхности не пересоздаёт, а отдаёт новой странице доски отступы, тему и режим стекла; другой флот — пересоздание (спека 8);
- до `pageLoaded(surface, "panel")` поверхности не уходит ни одного `sendTo`; после — её полное текущее состояние (спека 6.6); `leaving` снимает готовность; `broken` — `reloadSurface`, после `pageLoadTries` попыток — `destroySurfaces` и `showFailedPage`;
- переход из поверхности — по `navigationDecision`: на другую страницу панели — `destroySurfaces` и `navigateBoard`; на чужой адрес — `openExternal` (спека 6.7).

- [ ] **Step 1: Write the failing test**

```go
//go:build darwin

package main

import (
	"reflect"
	"testing"
)

func started() *controller {
	c := newController("http://127.0.0.1:7777/", panelWidths{Orchestrator: 368, Sessions: 348}, glassModeGlass)
	c.resized(1512, 982, false)
	return c
}

// shown is a window whose board reported the panel and whose surfaces both loaded.
func shown() *controller {
	c := started()
	c.layout(1, "panel", "work")
	c.pageLoaded("orchestrator", "panel")
	c.pageLoaded("sessions", "panel")
	return c
}

func boardInsets() sendTo {
	return sendTo{Surface: "board", Message: map[string]any{"type": "insets", "top": 64.0, "left": 394.0, "right": 0.0}}
}

func TestAPageThatReportsTheCurrentVersionGetsItsSurfaces(t *testing.T) {
	got := started().layout(1, "panel", "work")
	want := []effect{
		createSurfaces{Fleet: "work", URL: "http://127.0.0.1:7777/?fleet=work", Glass: glassModeGlass},
		applyGeometry{G: layoutFor(1512, 982, panelWidths{Orchestrator: 368, Sessions: 348})},
		boardInsets(),
		sendTo{Surface: "board", Message: map[string]any{"type": "glass", "glass": "glass"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("effects = %#v\nwant      %#v", got, want)
	}
}

func TestAPageOfAnUnknownVersionStaysOnePlainWebView(t *testing.T) {
	if got := started().layout(2, "panel", "work"); len(got) != 0 {
		t.Fatalf("effects = %#v, want none", got)
	}
}

func TestNothingIsSentToASurfaceBeforeItSaysItLoaded(t *testing.T) {
	c := started()
	c.layout(1, "panel", "work")
	if got := c.theme("dark"); !reflect.DeepEqual(got, []effect{setAppearance{Choice: "dark"}}) {
		t.Fatalf("before the surfaces loaded: %#v, want only the window's appearance", got)
	}
	got := c.pageLoaded("sessions", "panel")
	want := []effect{
		sendTo{Surface: "sessions", Message: map[string]any{"type": "theme", "choice": "dark"}},
		sendTo{Surface: "sessions", Message: map[string]any{"type": "glass", "glass": "glass"}},
		sendTo{Surface: "sessions", Message: map[string]any{"type": "folded", "folded": false}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("on load: %#v", got)
	}
}

func TestALeavingSurfaceGetsNothingUntilItLoadsAgain(t *testing.T) {
	c := shown()
	c.pageLoaded("sessions", "leaving")
	for _, e := range c.panel("sessions", true) {
		if msg, ok := e.(sendTo); ok && msg.Surface == "sessions" {
			t.Fatalf("sent to a leaving surface: %#v", msg)
		}
	}
}

func TestABrokenSurfaceIsLoadedAgainAndThenTheWindowSaysSo(t *testing.T) {
	c := started()
	c.layout(1, "panel", "work")
	for try := 1; try < pageLoadTries; try++ {
		if got := c.pageLoaded("orchestrator", "broken"); !reflect.DeepEqual(got, []effect{reloadSurface{Surface: "orchestrator"}}) {
			t.Fatalf("try %d: %#v", try, got)
		}
	}
	if got := c.pageLoaded("orchestrator", "broken"); !reflect.DeepEqual(got, []effect{destroySurfaces{}, showFailedPage{}}) {
		t.Fatalf("last try: %#v", got)
	}
}

func TestTheTakeoverSequenceCreatesSurfacesOnce(t *testing.T) {
	c := started()
	creates := 0
	count := func(effects []effect) []effect {
		for _, e := range effects {
			if _, ok := e.(createSurfaces); ok {
				creates++
			}
		}
		return effects
	}
	// The new window's "taking over" page, then the staged panel answering while
	// the handover is not done: the window's own page both times.
	count(c.boardShowsOwnPage())
	count(c.boardShowsOwnPage())
	// Handed over: the board loads and reports, then loads again once the panel
	// is restarted from the canonical bundle.
	count(c.layout(1, "panel", "work"))
	again := count(c.layout(1, "panel", "work"))
	if creates != 1 {
		t.Fatalf("surfaces created %d times, want once", creates)
	}
	if len(again) == 0 || !reflect.DeepEqual(again[0], boardInsets()) {
		t.Fatalf("a repeated report must give the new board page its insets again: %#v", again)
	}
	count(c.layout(1, "panel", "home"))
	if creates != 2 {
		t.Fatalf("another fleet must bring the surfaces back on it: created %d times", creates)
	}
}

func TestTheWindowsOwnPageTakesTheSurfacesDown(t *testing.T) {
	c := shown()
	if got := c.boardShowsOwnPage(); !reflect.DeepEqual(got, []effect{destroySurfaces{}}) {
		t.Fatalf("effects = %#v", got)
	}
	if got := c.boardShowsOwnPage(); len(got) != 0 {
		t.Fatalf("second time: %#v, want none", got)
	}
}

func TestOpeningASessionFromASideSurfaceGoesToTheBoardAndFocusesIt(t *testing.T) {
	got := shown().open("session", "", "abc12345")
	want := []effect{
		sendTo{Surface: "board", Message: map[string]any{"type": "open", "kind": "session", "short": "abc12345"}},
		focusSurface{Surface: "board"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("effects = %#v", got)
	}
}

func TestThePinnedOrchestratorIsFocusedNotOpenedTwice(t *testing.T) {
	got := shown().open("orchestrator", "", "")
	want := []effect{
		focusSurface{Surface: "orchestrator"},
		sendTo{Surface: "orchestrator", Message: map[string]any{"type": "focusTerminal"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("effects = %#v", got)
	}
}

func TestSwitchingFleetNavigatesTheBoardAndDropsTheSurfaces(t *testing.T) {
	got := shown().switchFleet("home life")
	want := []effect{destroySurfaces{}, navigateBoard{URL: "http://127.0.0.1:7777/?fleet=home+life"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("effects = %#v", got)
	}
}

func TestASurfaceLinkElsewhereOnThePanelOpensInTheBoard(t *testing.T) {
	allow, got := shown().navigate("orchestrator", "http://127.0.0.1:7777/setup.html")
	want := []effect{destroySurfaces{}, navigateBoard{URL: "http://127.0.0.1:7777/setup.html"}}
	if allow || !reflect.DeepEqual(got, want) {
		t.Fatalf("allow = %v, effects = %#v", allow, got)
	}
}

func TestASurfaceLinkToAnotherSiteOpensInTheBrowser(t *testing.T) {
	allow, got := shown().navigate("sessions", "https://github.com/kroticw/fleetdeck/pull/1")
	if allow || !reflect.DeepEqual(got, []effect{openExternal{URL: "https://github.com/kroticw/fleetdeck/pull/1"}}) {
		t.Fatalf("allow = %v, effects = %#v", allow, got)
	}
}

func TestFoldingSavesTheWidthsAndTellsTheSurface(t *testing.T) {
	got := shown().panel("sessions", true)
	w := panelWidths{Orchestrator: 368, Sessions: 348, SessionsFolded: true}
	want := []effect{
		saveWidths{W: w},
		applyGeometry{G: layoutFor(1512, 982, w)},
		boardInsets(),
		sendTo{Surface: "sessions", Message: map[string]any{"type": "folded", "folded": true}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("effects = %#v", got)
	}
}

func TestAThemeReportedByTheBoardReachesTheOthersAndTheWindow(t *testing.T) {
	got := shown().theme("dark")
	want := []effect{
		setAppearance{Choice: "dark"},
		sendTo{Surface: "orchestrator", Message: map[string]any{"type": "theme", "choice": "dark"}},
		sendTo{Surface: "sessions", Message: map[string]any{"type": "theme", "choice": "dark"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("effects = %#v", got)
	}
}

func TestReducedTransparencyRebuildsTheFrameAndTellsEverySurface(t *testing.T) {
	got := shown().glassChanged(glassModeOpaque)
	want := []effect{
		setFrameMode{Mode: glassModeOpaque},
		sendTo{Surface: "board", Message: map[string]any{"type": "glass", "glass": "opaque"}},
		sendTo{Surface: "orchestrator", Message: map[string]any{"type": "glass", "glass": "opaque"}},
		sendTo{Surface: "sessions", Message: map[string]any{"type": "glass", "glass": "opaque"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("effects = %#v", got)
	}
}

func TestACapsulePressBecomesAMessageToTheBoard(t *testing.T) {
	c := shown()
	cases := map[string]map[string]any{
		"tab:docs": {"type": "show", "section": "docs"},
		"newCard":  {"type": "newCard"},
		"theme":    {"type": "cycleTheme"},
	}
	for action, msg := range cases {
		if got := c.capsuleAction(action); !reflect.DeepEqual(got, []effect{sendTo{Surface: "board", Message: msg}}) {
			t.Fatalf("%s: effects = %#v", action, got)
		}
	}
}

func TestFullScreenTellsTheOrchestratorSurfaceToDropTheButtonsRoom(t *testing.T) {
	got := shown().resized(1512, 982, true)
	last := got[len(got)-1]
	if !reflect.DeepEqual(last, sendTo{Surface: "orchestrator", Message: map[string]any{"type": "fullscreen", "on": true}}) {
		t.Fatalf("effects = %#v", got)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./cmd/fleetdeck-window -run 'Page|Surface|Takeover|OwnPage|Opening|Pinned|Switching|Link|Folding|Theme|Reduced|CapsulePress|FullScreen' -count=1`

Expected: FAIL — `undefined: newController`.

- [ ] **Step 3: Implement `controller.go`**

Контроллер хранит: `baseURL`; `panel bool`; `fleet`; `widths`; `width`, `height`, `fullscreen`; `glass`; `theme` (пустая строка, пока доска не сообщила); `ready map[string]bool` и `tries map[string]int` для поверхностей. Поведение — ровно то, что закрепляют тесты:

- `layout`: `version != 1` или `mode != "panel"` — пустой список, если рамы нет, иначе `destroySurfaces{}`; первый отчёт или другой флот — `createSurfaces`, `applyGeometry`, `insets` и `glass` доске, готовность поверхностей сбрасывается; тот же флот — только `insets`, `glass` и тема (если известна) доске;
- адрес поверхностей и доски при смене флота — `baseURL + "?fleet=" + url.QueryEscape(fleet)`;
- `sendTo` для `orchestrator` и `sessions` добавляется в список только при `ready[surface]`; `pageLoaded(surface, "panel")` ставит готовность, обнуляет попытки и отдаёт тему (если известна), `glass`, `folded` и, для оркестратора, `fullscreen`;
- `pageLoaded(surface, "loading")` ничего не отдаёт: готовность приходит только с `panel`;
- `pageLoaded(surface, "leaving")` снимает готовность и ничего не отдаёт; `pageLoaded(surface, "broken")` увеличивает попытки: меньше `pageLoadTries` — `reloadSurface`, иначе `destroySurfaces` и `showFailedPage`, рама снимается;
- `navigate` вызывает `navigationDecision(baseURL, адрес поверхностей, target)`: `Allow` — `true` без эффектов; `Board` — `false`, `destroySurfaces` и `navigateBoard`, рама снимается; `External` — `false`, `openExternal`; иначе — `false` без эффектов;
- `resized` при раме отдаёт `applyGeometry`, `insets` доске и, если `fullscreen` изменился, последним — `fullscreen` поверхности оркестратора;
- `glassChanged` отдаёт `setFrameMode` всегда, сообщения `glass` — только при раме и готовности;
- `capsules(model)` отдаёт `setCapsules{Model}` без разбора; `reload` — `reloadAll{}`.

- [ ] **Step 4: Run tests**

Run: `go test ./cmd/fleetdeck-window -count=1 -race`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/fleetdeck-window/controller.go cmd/fleetdeck-window/controller_test.go
git commit --signoff --message "feat(window): decide the frame from what the pages report"
```

---

### Task 12: Исполнение эффектов и проводка в `main`

**Files:**

- Create: `cmd/fleetdeck-window/effects.go`
- Modify: `cmd/fleetdeck-window/main.go`, `cmd/fleetdeck-window/owner.go` (`screen`: вызов контроллера при своей странице окна), `cmd/fleetdeck-window/frame_darwin.c`, `cmd/fleetdeck-window/frame_darwin.go` (наблюдатели), `cmd/fleetdeck-window/surface_darwin.go` (делегат навигации → контроллер)
- Test: `cmd/fleetdeck-window/effects_test.go`

**Interfaces:**

- Consumes: `bridge` (8); `installFrame`, `currentGlassMode` (9); `newSurface`, `hostScript`, `fleetdeckSurfaceNavigation` (10); `controller` и эффекты (11); из T-057 (`owner.go`, `main.go` ветки `fix/update-staged-bundle-relaunch`, подтверждаются задачей 0): `pageLoadScript(panelURL string) string`, `pageLoadedBindingName = "fleetdeckPageLoaded"` с аргументами `(state, href)`, `pageLoadWait`, `pageLoadingWait`, `pageLoadTries = 3`, `pageFailedPage(pageURL string, wait time.Duration) string`, `screen.target()`.
- Produces:
  - `type natives interface { createSurface(kind, url string, glass glassMode); destroySurfaces(); send(surface string, msg map[string]any); focus(surface string); navigateBoard(url string); openExternal(url string); reloadSurface(surface string); showFailedPage(); setAppearance(choice string); applyGeometry(g geometry); saveWidths(w panelWidths); setCapsules(model json.RawMessage); setFrameMode(m glassMode); reloadAll() }` и `runEffects(n natives, effects []effect)` — для задач 13 (`setCapsules`) и 14 (`reloadAll`);
  - привязки в реестре: `fleetdeckLayout`, `fleetdeckOpen`, `fleetdeckSwitchFleet`, `fleetdeckCapsules`, `fleetdeckTheme`, `fleetdeckPanel`; `fleetdeckPageLoaded` из поверхности уходит в `controller.pageLoaded(surface, state)`, из доски — в обработчик T-057 без изменений; восемь существующих привязок переносятся в реестр — для задачи 16 (стенд).

- [ ] **Step 1: Write the failing test**

```go
//go:build darwin

package main

import (
	"encoding/json"
	"reflect"
	"testing"
)

type fakeNatives struct{ calls []string }

func (f *fakeNatives) createSurface(kind, url string, glass glassMode) {
	f.calls = append(f.calls, "create "+kind+" "+url+" "+string(glass))
}
func (f *fakeNatives) destroySurfaces()                        { f.calls = append(f.calls, "destroy") }
func (f *fakeNatives) send(surface string, msg map[string]any) { f.calls = append(f.calls, "send "+surface+" "+msg["type"].(string)) }
func (f *fakeNatives) focus(surface string)                    { f.calls = append(f.calls, "focus "+surface) }
func (f *fakeNatives) navigateBoard(url string)                { f.calls = append(f.calls, "navigate "+url) }
func (f *fakeNatives) openExternal(url string)                 { f.calls = append(f.calls, "external "+url) }
func (f *fakeNatives) reloadSurface(surface string)            { f.calls = append(f.calls, "reload "+surface) }
func (f *fakeNatives) showFailedPage()                         { f.calls = append(f.calls, "failed page") }
func (f *fakeNatives) setAppearance(choice string)             { f.calls = append(f.calls, "appearance "+choice) }
func (f *fakeNatives) applyGeometry(geometry)                  { f.calls = append(f.calls, "geometry") }
func (f *fakeNatives) saveWidths(panelWidths)                  { f.calls = append(f.calls, "save") }
func (f *fakeNatives) setCapsules(json.RawMessage)             { f.calls = append(f.calls, "capsules") }
func (f *fakeNatives) setFrameMode(m glassMode)                { f.calls = append(f.calls, "frame "+string(m)) }
func (f *fakeNatives) reloadAll()                              { f.calls = append(f.calls, "reload all") }

func TestCreatingSurfacesMakesBothColumnsOnTheSameAddress(t *testing.T) {
	f := &fakeNatives{}
	runEffects(f, []effect{createSurfaces{Fleet: "work", URL: "http://127.0.0.1:7777/?fleet=work", Glass: glassModeGlass}})
	want := []string{
		"create orchestrator http://127.0.0.1:7777/?fleet=work glass",
		"create sessions http://127.0.0.1:7777/?fleet=work glass",
	}
	if !reflect.DeepEqual(f.calls, want) {
		t.Fatalf("calls = %v", f.calls)
	}
}

func TestEffectsRunInTheOrderTheControllerGaveThem(t *testing.T) {
	f := &fakeNatives{}
	runEffects(f, []effect{destroySurfaces{}, navigateBoard{URL: "u"}, openExternal{URL: "https://x"}, reloadSurface{Surface: "sessions"}, showFailedPage{}, reloadAll{}})
	want := []string{"destroy", "navigate u", "external https://x", "reload sessions", "failed page", "reload all"}
	if !reflect.DeepEqual(f.calls, want) {
		t.Fatalf("calls = %v", f.calls)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./cmd/fleetdeck-window -run 'CreatingSurfaces|EffectsRun' -count=1`

Expected: FAIL — `undefined: runEffects`.

- [ ] **Step 3: Implement**

- `effects.go`: `runEffects` — `switch` по типу эффекта с вызовом метода `natives`. Реальная реализация `natives` держит `*frame`, две `*surface` и `webview.WebView`; нативные вызовы — через `w.Dispatch`. `openExternal` — `[[NSWorkspace sharedWorkspace] openURL:[NSURL URLWithString:]]`; `showFailedPage` — `w.SetHtml(pageFailedPage(url, pageLoadingWait))` в доску; `navigateBoard` — `w.Navigate(url)`: `screen` пойдёт за доской по её `leaving` и `loading` с новым `href`; `reloadSurface` — `(*surface).reload()`.
- `main.go`, сразу после `webview.New`: `installFrame(w.Window())` — до первой навигации; `w.Init(hostScript("board", currentGlassMode(), nil))` рядом с `w.Init(noticeScript)` и `w.Init(pageLoadScript(*url))`; реестр привязок; каждая привязка регистрируется в реестре и через `w.Bind` для доски с адаптером `func(args json.RawMessage) (any, error) { return reg.call("board", name, args) }`; существующие обработчики заворачиваются в `bridgeHandler` без изменения поведения.
- На каждую загрузку поверхности `natives` заводит таймер `pageLoadWait`, а после `loading` — `pageLoadingWait`, как `screen.tick`; `panel`, `broken` или `leaving` до срока его снимают, а сработавший таймер отдаёт контроллеру `pageLoaded(surface, "broken")` — так «нет ответа» идёт тем же путём повторов, что и у доски в T-057.
- `fleetdeckSurfaceNavigation` (задача 10) зовёт `controller.navigate` синхронно — делегату нужен ответ сразу; эффекты уходят в `runEffects` через `w.Dispatch`. Контроллер вызывают главный поток (делегаты, наблюдатели) и горутины привязок, поэтому все его методы берут один `sync.Mutex`; эффекты исполняются после того, как мьютекс отпущен.
- `frame_darwin.c`: наблюдатели `NSWorkspaceAccessibilityDisplayOptionsDidChangeNotification` (центр уведомлений `NSWorkspace`), `NSWindowDidResizeNotification`, `NSWindowDidEnterFullScreenNotification`, `NSWindowDidExitFullScreenNotification`; наблюдатель зовёт экспортируемый Go `fleetdeckFrameChanged(kind *C.char)`, который отдаёт контроллеру `glassChanged(currentGlassMode())` или `resized(...)`.
- `main.go`: в `show` ветка `page != ""` (своя страница окна через `w.SetHtml`) вызывает `controller.boardShowsOwnPage()` и исполняет эффекты; обработчик `fleetdeckPageLoaded` доски остаётся `scr.pageSays(state, href)`. Навигацию доски и подтверждение её загрузки `screen` ведёт как в T-057, без изменений.
- Ширины панелей читаются и пишутся в `NSUserDefaults` приложения под ключами `glassOrchestratorWidth`, `glassSessionsWidth`, `glassOrchestratorFolded`, `glassSessionsFolded`.

- [ ] **Step 4: Run tests**

Run: `make test`

Expected: PASS, включая `reload_test.go`, `pageload_test.go` и соседние тесты контрактов имён.

- [ ] **Step 5: Commit**

```bash
git add cmd/fleetdeck-window/effects.go cmd/fleetdeck-window/effects_test.go cmd/fleetdeck-window/main.go cmd/fleetdeck-window/owner.go cmd/fleetdeck-window/frame_darwin.c cmd/fleetdeck-window/frame_darwin.go cmd/fleetdeck-window/surface_darwin.go
git commit --signoff --message "feat(window): run the frame from the board's report and the window's own pages"
```

---

### Task 13: Капсулы

**Files:**

- Create: `cmd/fleetdeck-window/capsulemodel.go`, `cmd/fleetdeck-window/capsules_darwin.c`, `cmd/fleetdeck-window/capsules_darwin.h`, `cmd/fleetdeck-window/capsules_darwin.go`
- Modify: `cmd/fleetdeck-window/testsupport_darwin.go`
- Test: `cmd/fleetdeck-window/capsulemodel_test.go`, `cmd/fleetdeck-window/capsules_darwin_test.go`

**Interfaces:**

- Consumes: модель `fleetdeckCapsules` (задача 7), эффект `setCapsules` (задачи 11, 12), `(*frame).capsules()` и `glassMode` (задача 9), `controller.capsuleAction` (задача 11).
- Produces: `type capsuleModel struct` с полями модели задачи 7; `parseCapsuleModel(raw json.RawMessage) (capsuleModel, error)` с отказом при `version != 1`; `drawCapsules(container unsafe.Pointer, m capsuleModel, mode glassMode)`; экспортируемый Go `fleetdeckCapsulePressed(action *C.char)` — проверяется на стенде задачи 16.

- [ ] **Step 1: Write the failing model test**

```go
//go:build darwin

package main

import (
	"encoding/json"
	"testing"
)

func TestACapsuleModelOfAnotherVersionIsRefused(t *testing.T) {
	if _, err := parseCapsuleModel(json.RawMessage(`{"version":2,"tabs":[]}`)); err == nil {
		t.Fatal("a model of another version must not be drawn")
	}
}

func TestACapsuleModelKeepsThePagesWordsAndColours(t *testing.T) {
	m, err := parseCapsuleModel(json.RawMessage(`{"version":1,"tabs":[{"id":"board","label":"Доска","selected":true},{"id":"docs","label":"Доки","selected":false}],"newCard":{"label":"+ карточка"},"theme":{"label":"тема: авто"},"limits":[{"label":"5ч","text":"37%","level":"cool","color":"#2f9e44"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if m.Tabs[0].Label != "Доска" || !m.Tabs[0].Selected || m.Limits[0].Color != "#2f9e44" || m.Theme.Label != "тема: авто" {
		t.Fatalf("model = %+v", m)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./cmd/fleetdeck-window -run 'CapsuleModel' -count=1`

Expected: FAIL — `undefined: parseCapsuleModel`.

- [ ] **Step 3: Implement**

- `capsulemodel.go`: структуры с тегами JSON по модели задачи 7 и `parseCapsuleModel`.
- `capsules_darwin.c`: в контейнере капсул — ряд обёрток той же природы, что панели: `glass` — `NSGlassEffectView` Regular, радиус 16; `vibrancy` — `NSVisualEffectView` с материалом `popover` (6), withinWindow; `opaque` — `NSView` со слоем цвета `--surface-raised` (страница добавляет его в модель полем `surface`). Внутри: `NSSegmentedControl` (`segmentedControlWithLabels:trackingMode:target:action:`, `trackingMode` 0 — selectOne); `NSButton` для «+ карточка» и темы (`buttonWithTitle:target:action:`, `setBordered:NO`); для каждого лимита — `NSTextField` подписи, `NSLevelIndicator` (`setLevelIndicatorStyle:1`, `setMaxValue:100`, `setFillColor:` из `color`) и `NSTextField` текста. Цель действий — объект класса `FleetdeckCapsuleTarget` с методом `pressed:`, который по `tag` контрола зовёт `fleetdeckCapsulePressed`.
- Порядок ряда слева направо: вкладки, «+ карточка», зазор, тема, лимиты по одному в капсуле — по макету «Выбрано».

- [ ] **Step 4: Write the native test**

```go
func TestCapsulesDrawThePagesModel(t *testing.T) {
	r := capsulesResult // built in TestMain from the model of TestACapsuleModelKeepsThePagesWordsAndColours
	if r.segmentLabels[0] != "Доска" || r.segmentLabels[1] != "Доки" || r.selectedSegment != 0 {
		t.Fatalf("segments = %v selected %d", r.segmentLabels, r.selectedSegment)
	}
	if r.newCardTitle != "+ карточка" || r.themeTitle != "тема: авто" {
		t.Fatalf("buttons = %q %q", r.newCardTitle, r.themeTitle)
	}
	if len(r.levelValues) != 1 || r.levelValues[0] != 37 {
		t.Fatalf("levels = %v", r.levelValues)
	}
	if r.capsuleCount != 4 {
		t.Fatalf("capsules = %d, want tabs, new card, theme, one limit", r.capsuleCount)
	}
}
```

- [ ] **Step 5: Run tests**

Run: `make test`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/fleetdeck-window/capsulemodel.go cmd/fleetdeck-window/capsulemodel_test.go cmd/fleetdeck-window/capsules_darwin.c cmd/fleetdeck-window/capsules_darwin.h cmd/fleetdeck-window/capsules_darwin.go cmd/fleetdeck-window/capsules_darwin_test.go cmd/fleetdeck-window/testsupport_darwin.go
git commit --signoff --message "feat(window): draw tabs, theme and limits as native capsules in glass"
```

---

### Task 14: Меню и метка сборки

**Files:**

- Modify: `cmd/fleetdeck-window/menu_darwin.c`, `cmd/fleetdeck-window/menu_darwin.go`, `cmd/fleetdeck-window/foreign.go`, `cmd/fleetdeck-window/surface_darwin.go` (`surfaceScripts`), `cmd/fleetdeck-window/testsupport_darwin.go`
- Test: `cmd/fleetdeck-window/menu_darwin_test.go`, `cmd/fleetdeck-window/foreign_test.go`

**Interfaces:**

- Consumes: `reloadAll` (задача 12); строка бренда внутри `<header id="header">` в поверхности оркестратора (задача 7).
- Produces: пункт меню «Reload» с действием `fleetdeckReloadAll:` и целью класса `FleetdeckMenuTarget`, вызывающей экспортируемый Go `fleetdeckMenuReload()`; `noticeScriptFor(surface string) string` — `surfaceScripts` (задача 10) добавляет его третьим скриптом, когда он не пустой; доска получает нынешний `noticeScript` через `w.Init`, как сейчас. Тест поверхности задачи 10 строит `sessions`, у которой скрипта метки нет, и остаётся с двумя скриптами.

- [ ] **Step 1: Write the failing tests**

```go
// menu_darwin_test.go — добавить; значения собираются в TestMain
func TestReloadReloadsEveryWebViewNotOnlyTheFocusedOne(t *testing.T) {
	if viewMenuReloadAction != "fleetdeckReloadAll:" || !viewMenuReloadHasTarget {
		t.Fatalf("Reload = %q, target set = %v", viewMenuReloadAction, viewMenuReloadHasTarget)
	}
	if viewMenuReloadKey != "r" {
		t.Fatalf("Reload key = %q", viewMenuReloadKey)
	}
}
```

```go
// foreign_test.go — добавить
func TestTheBuildMarkFollowsTheBrandIntoTheOrchestratorSurface(t *testing.T) {
	if !strings.Contains(noticeScriptFor("orchestrator"), ".build-rev::before") {
		t.Fatal("the orchestrator surface shows the brand, so it needs the build mark")
	}
	if noticeScriptFor("sessions") != "" {
		t.Fatal("the sessions surface shows no brand and needs no script")
	}
	if noticeScriptFor("board") != noticeScript {
		t.Fatal("the board keeps the notice as it is")
	}
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./cmd/fleetdeck-window -run 'ReloadReloads|BuildMark' -count=1`

Expected: FAIL.

- [ ] **Step 3: Implement**

- `menu_darwin.c`: пункт «Reload» получает `setAction:sel_registerName("fleetdeckReloadAll:")` и `setTarget:` объекта рантайм-класса `FleetdeckMenuTarget` с этим методом; метод зовёт `fleetdeckMenuReload`. Пункты правки не меняются — они идут по цепочке респондеров.
- `foreign.go`: `noticeScriptFor("board")` возвращает нынешний `noticeScript`; для `"orchestrator"` — скрипт, который ставит в `<head>` элемент `<style>` только с правилом `#header .build-rev::before`; для `"sessions"` — пустую строку.

- [ ] **Step 4: Run tests**

Run: `make test`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/fleetdeck-window/menu_darwin.c cmd/fleetdeck-window/menu_darwin.go cmd/fleetdeck-window/menu_darwin_test.go cmd/fleetdeck-window/foreign.go cmd/fleetdeck-window/foreign_test.go cmd/fleetdeck-window/surface_darwin.go cmd/fleetdeck-window/testsupport_darwin.go
git commit --signoff --message "fix(window): reload every web view and mark the build where the brand now is"
```

---

### Task 15: Документация

**Files:**

- Modify: `docs/engineering/window-and-panel.md`, `docs/engineering/live-terminal.md`; пользовательские страницы в `docs/en` и `docs/ru`, где описана раскладка окна (найти: `grep -rlE "column|колонк" docs/en docs/ru`)
- Test: команда задачи `docs-parity` из `.github/workflows/ci.yaml`, запущенная локально

**Interfaces:**

- Consumes: всё, что сделано в задачах 1–14.
- Produces: раздел «The glass frame» в `window-and-panel.md` и раздел о трёх веб-видах в `live-terminal.md` — задача 16 проверяет стенд по ним.

- [ ] **Step 1:** В `window-and-panel.md` добавить раздел «The glass frame»: слои (спека 5.1), кто что создаёт, режимы `glass`, `vibrancy`, `opaque` и когда какой, когда рамы нет (спека 5.5), мост и новые привязки (спека 6.2), что измерено на стенде задачи 16. В `live-terminal.md` §7 — что `WKWebView` теперь три, как они делят процесс и хранилище, и почему лист S1 не открывается для закреплённой сессии оркестратора (спека 6.3).
- [ ] **Step 2:** Обновить пользовательские страницы `docs/en` и `docs/ru` одним коммитом, если они описывают раскладку окна.
- [ ] **Step 3:** Запустить команду задачи `docs-parity` из `.github/workflows/ci.yaml` локально.

Expected: без расхождений.

- [ ] **Step 4: Commit**

```bash
git add docs/engineering/window-and-panel.md docs/engineering/live-terminal.md docs/en docs/ru
git commit --signoff --message "docs(window): describe the glass frame and the window's web views"
```

---

### Task 16: Стенд, замеры и приёмка

**Files:**

- Create: `scripts/stand-glass-window.sh`
- Test: проверка глазами оператора по спеке 10.1, замеры по спеке 10.3; снимки, числа и итог — в карточку T-056

**Interfaces:**

- Consumes: всё из задач 1–15; правила стенда T-057.
- Produces: снимки и числа в карточке T-056 — для оператора и оркестратора.

- [ ] **Step 1: Скрипт стенда по правилам T-057** (спека 10.1):
  - `STAND=$(mktemp -d)`; `HOME="$STAND/home"`; свободный порт, не 7777; конфигурация флота стенда с пустой доской внутри `STAND`;
  - `FLEETDECK_STAND_SOCKET="$STAND/daemon.sock"`; окно передаёт его каждой панели, которую запускает, флагом `--stand-socket` (`owner.go`, T-057);
  - бандл — `make window-app BINDIR="$STAND/bin" BUNDLE_ID=dev.fleetdeck.stand`, `STAND_APP="$STAND/bin/fleetdeck.app"`; окно запускается как `"$STAND_APP/Contents/MacOS/fleetdeck-window" --url "$STAND_URL"`; после стенда — `lsregister -u "$STAND_APP"`;
  - вывод панелей окно пишет в `$HOME/Library/Logs/fleetdeck.log` (`main.go`, T-057), это `STAND_PANEL_LOG`; перед тем как показать окно, скрипт ждёт в нём строку `daemon discovery disabled` и без неё останавливает стенд с ошибкой; число таких строк сверяется с числом запусков панели после каждого перезапуска и обмена при обновлении;
  - скрипт печатает PID окна, PID панели и порт и не трогает ничего вне `STAND`;
- [ ] **Step 2: База замеров.** На том же стенде окно сборки до части Б (master после T-057 и T-058), 60 с с открытым окном:
  - память: `footprint <pid>` для каждого процесса `com.apple.WebKit.WebContent`, появившегося после запуска окна (`pgrep -f com.apple.WebKit.WebContent` до и после), сумма и каждый;
  - CPU панели: `ps -o %cpu= -p <pid панели>` раз в секунду, среднее;
  - `/ws`: `nettop -P -L 60 -s 1 -p <pid панели> -J bytes_out`, сумма.
- [ ] **Step 3: Замеры окна части Б** — те же три числа, тем же способом. Сравнить с порогами спеки 10.3: сумма памяти WebContent не больше базы + 250 МБ и каждый процесс не больше 200 МБ; CPU панели не больше базы × 1,5 и не больше 3 % ядра; `/ws` не больше базы × 3,2. Порог превышен — стенд останавливается, числа уходят оркестратору; окно не оптимизируется наугад.
- [ ] **Step 4: Проверка глазами оператора**, с его согласия, по списку спеки 10.1: обе темы и `auto` при светлой и тёмной системе; «Уменьшить прозрачность»; «Увеличить контраст» — с записью, включает ли он сам «Уменьшить прозрачность»; полный экран; K1, S1, Д1; свёрнутая колонка сессий; перетаскивание ширины за край панели — курсор изменения размера, пределы 220 pt и 60 % окна, ширина после перезапуска приложения, скачок доски при отпускании не мешает; печать в терминал S1 сразу после открытия из колонки сессий; «мастер…» из колонки оркестратора открывается в доске, внешняя ссылка — в браузере; смена флота из строки бренда и из колонки сессий; Cmd+R.
- [ ] **Step 5: Стекло обновляется.** Два снимка окна с интервалом 0,9 с при движущемся содержимом доски под панелью и сравнение области под стеклом с открытой доской, как в спайке (спека 3).
- [ ] **Step 5a: Стекло без общего контейнера** (спека 5.1). Глазами и на снимках в обеих темах: капсулы в ряду сливаются между собой; граница панели оркестратора и первой капсулы, панели сессий и последней капсулы читается как две отдельные формы и не выглядит ошибкой; клик по доске в полосе между капсулами и над верхним отступом доски доходит до доски. Итог — строкой в карточку T-056.
- [ ] **Step 6: Обновление кнопкой — обязательно** (спека 8, 10.1). По процедуре стенда обновления T-057 (`cmd/fleetdeck-window/stand_test.go`: подписанный `make dist-app` с `BUNDLE_ID=dev.fleetdeck.stand`, свой `HOME`, `FLEETDECK_STAND_SOCKET`):
  - приёмочный тест `stand_test.go` проходит на бандле этой ветки;
  - оператор нажимает «Обновить» в окне стенда сборки до части Б; передача доходит до `done`; новое окно показывает доску с панелями;
  - в логе нового окна поверхности созданы один раз; в `STAND_PANEL_LOG` строка `daemon discovery disabled` есть у каждого запуска панели.

  Не прошло — часть Б не сдаётся.
- [ ] **Step 7:** Остановить стенд; проверить, что порт свободен, окно закрыто, регистрация LaunchServices снята; записать в лог карточки T-056 числа замеров, итог каждого пункта и ссылки на снимки.
- [ ] **Step 8: Commit**

```bash
git add scripts/stand-glass-window.sh
git commit --signoff --message "test(window): add a stand for the glass window"
```

---

### Task 17: Окно со стеклом в проверке T-058 на macOS 15

Выполняется после мержа T-058 (PR #163) и rebase на него. Отдельной задачи CI и отдельного помощника стенда нет: T-058 уже запускает окно на `macos-15` (задачи `oldest-macos-app` и `window-on-oldest-macos`, скрипт `scripts/ci-window-stand.sh`), и эта задача расширяет её проверку на раму.

**Files:**

- Modify: `scripts/ci-window-stand.sh` (пятый аргумент `frame`); `.github/workflows/ci.yaml` (шаги окна этой сборки передают `frame`)
- Modify: `cmd/fleetdeck-window/glasswindow.go` (`surfacePageSays`), `cmd/fleetdeck-window/controller.go` (базовый адрес без query)
- Test: `cmd/fleetdeck-window/glasswindow_test.go`, `controller_test.go`; задача `window-on-oldest-macos` на pull request

Сверено при исполнении: `/` панели — всегда стартовая страница, раскладку `panel` сообщает только страница флота. Поэтому в режиме `frame` стенд пишет в конфиг флот `stand` с пустой доской (`cards/`) и открывает окно на `/?fleet=stand`; контроллер строит адрес поверхностей на адресе окна без query (`TestAWindowOpenedOnAFleetsPageGivesItsSurfacesTheirOwnAddress`). Проверено без окна: панель стенда с этим конфигом отвечает на `/?fleet=stand` 200 и пишет `daemon discovery disabled`. Окно v0.9.1 скрипт по-прежнему запускает без `frame`.

**Interfaces:**

- Consumes: скрипт стенда T-058 и то, чего он ждёт в логе окна (подтверждение страницы `panel` доски); `pageLoaded` поверхностей (задачи 11, 12).
- Produces: строка лога `surfacePageSays(surface, "panel")` — `the <surface> surface's page says "panel"` — для каждой поверхности, для скрипта T-058 и для задачи 16; `ci-window-stand.sh <app> <port> <out-dir> <exec|open> [page|frame]`; снимок окна с рамой (`window.png`) в артефакте `window-on-oldest-macos`.

- [ ] **Step 1:** После rebase на master с T-058 прочитать `scripts/ci-window-stand.sh`: какую строку лога он ждёт, сколько ждёт, как называет артефакт.
- [ ] **Step 2: Write the failing test** — `TestTheWindowStandWaitsForEachSurfacesLineAsTheWindowLogsIt`: скрипт стенда содержит строку `surfacePageSays(surface, "panel")` для `orchestrator` и `sessions` в том виде, в каком она стоит в двойных кавычках shell.
- [ ] **Step 3:** `glasswindow.go` пишет `surfacePageSays(surface, state)` в `pageLoaded` на каждое слово поверхности.
- [ ] **Step 4:** `scripts/ci-window-stand.sh` после строки доски ждёт строки обеих поверхностей тем же способом и тем же сроком; снимок делается после них. Нет строки поверхности — скрипт падает с именем поверхности, которой нет.
- [ ] **Step 5: Run**

Run: `go test ./cmd/fleetdeck-window -run 'TheWindowStandWaits|WindowOpenedOnAFleetsPage' -count=1`, `sh -n scripts/ci-window-stand.sh`, `shellcheck scripts/ci-window-stand.sh`, затем pull request с веткой.

Expected: тест PASS; `window-on-oldest-macos` зелёная, в её логе есть строки доски и обеих поверхностей, на снимке — окно с рамой на `NSVisualEffectView`. Красная задача — находка для оркестратора, а не повод её выключить.

- [ ] **Step 6: Commit**

```bash
git add scripts/ci-window-stand.sh cmd/fleetdeck-window/glasswindow.go cmd/fleetdeck-window/glasswindow_test.go
git commit --signoff --message "ci(window): wait for the glass surfaces in the macOS 15 window check"
```
