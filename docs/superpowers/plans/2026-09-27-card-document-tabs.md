# fleetdeck: вкладки документов в карточке и сессия-автор рядом — план реализации

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** лист карточки показывает вкладки «Карточка» и все документы карточки, а рядом с активной вкладкой — живой терминал её сессии-автора, снизу или справа.

**Architecture:** автор документа — поле `session` во frontmatter документа (правило доски, валидатор, скиллы, `/api/docs`). В странице лист карточки получает постоянный каркас: шапка, вкладки, сцена с панелью содержимого и местом сессии; место сессии — отдельный модуль, который держит терминал через перерисовки снимка. Окно и демон не меняются, кроме стендовой обвязки.

**Tech Stack:** Go 1.27 (`gopkg.in/yaml.v3`), ES-модули без сборки, `node --test` с `web/tests/fake-dom.js`, Python 3 `unittest`, стенд `scripts/ci-window-stand.sh` + `scripts/standcheck` в CI-джобе `window-on-macos-26`.

**Spec:** `docs/superpowers/specs/2026-09-27-card-document-tabs-design.md`

## Global Constraints

- Ветка `feat/v0.13.0-card-document-tabs`, PR черновиком в `release/v0.13.0`. Мерж и выпуск — оркестратор.
- Коммиты `git commit --signoff`, подпись GPG не отключать; без строк атрибуции Claude.
- Полные имена флагов в командах; shell — zsh.
- SDK только через `scripts/darwin-sdkroot.sh` (Makefile сам); не передавать несуществующие `SDKROOT`/`DEVELOPER_DIR`.
- Окна fleetdeck локально не открывать: стенд окна гоняется только в CI (`window-on-macos-26`). Порт 7777 и `~/.config/fleetdeck` не трогать.
- Полоса перетаскивания при открытом листе (T-079, `#sheet-scrim[data-window-ground]`) сохраняется.
- Порог «справа»: `DOCK_RIGHT_MIN = 640` CSS px ширины `.card-stage`.
- Ключи `localStorage`: `fleetdeck-card-session-dock` (`bottom`|`right`, по умолчанию `bottom`), `fleetdeck-card-session-height-pct` (25–80, по умолчанию 55), `fleetdeck-card-session-width-pct` (30–60, по умолчанию 45).
- Формат id сессии документа — `^[0-9a-fA-F]{6,12}$`, как у карточки.
- Подписи интерфейса — через `web/js/i18n.js`, en и ru.
- Каждый тест — на свойство и красный до правки. Мутации вручную по `docs/engineering/live-terminal.md` §10: две цифры.
- Финальные прогоны: `make test` (с `-race`), `make test-web`, `golangci-lint run ./...`, `GOOS=linux go build ./...`, зелёный CI.

## Review Focus

1. **Перерисовка снимком под открытым терминалом.** Снимок приходит раз в секунду; терминал в месте сессии не должен пересоздаваться, а фокус и набранное — теряться. Тест в задаче 8 (узел `.card-dock` и объект терминала те же после `store.push`).
2. **Документ без frontmatter и карточка без `session`.** Места под сессию нет, вкладки работают. Тест в задаче 10.
3. **Автор сменился, пока место раскрыто** (документ другого автора, или `session` карточки переназначен). Старый терминал остановлен, новый подключён к новой сессии. Тест в задаче 10.
4. **Ширина прошла порог в обе стороны при выбранном «справа».** Место уходит вниз и возвращается, выбор в хранилище не меняется. Тест в задаче 10.
5. **Хранилище недоступно** (приватный режим, `localStorage` бросает). Настройки по умолчанию, ничего не падает. Тест в задаче 6.

---

## Структура файлов

| Файл | Что | Задача |
| --- | --- | --- |
| `plugin/templates/board/scripts/validate_cards.py` | `validate_doc_frontmatter`, вызов в `main` | 1 |
| `plugin/templates/board/scripts/test_validate_cards.py` | `DocFrontmatterTest` | 1 |
| `docs/en/board-convention.md`, `docs/ru/board-convention.md` | правило `session` документа | 2 |
| `plugin/skills/card-keeping/SKILL.md`, `plugin/skills/fleet-orchestrator/SKILL.md` | строка о `session` в документе | 2 |
| `plugin/.claude-plugin/plugin.json` | 0.4.0 → 0.5.0 | 2 |
| `internal/board/docmeta.go`, `internal/board/docmeta_test.go` | `DocSession` | 3 |
| `internal/server/docs.go`, `internal/server/docs_test.go` | `Doc.Session` | 3 |
| `scripts/standdaemon/main.go` (+ его фикстура доски) | документы, карточка, экран AskUserQuestion | 4 |
| `web/js/host.js`, `cmd/fleetdeck-window/standsettings.go` | `carddoc-bottom`, `carddoc-right` | 4 |
| `web/js/standreport.js`, `web/js/main.js` | строка отчёта `card sheet`, стендовое открытие | 4, 13 |
| `scripts/standcheck/main.go`, `scripts/standcheck/cardsheet.go`, `scripts/standcheck/cardsheet_test.go` | гейты листа | 4 |
| `scripts/ci-window-stand.sh`, `.github/workflows/ci.yaml` | ожидание листа, шаги стенда | 4 |
| `web/js/docauthor.js`, `web/tests/docauthor.test.js` | `authorOf`, `authorState` | 5 |
| `web/js/carddock.js`, `web/tests/carddock.test.js` | настройки, порог, место сессии | 6, 10 |
| `web/js/cardtabs.js`, `web/tests/cardtabs.test.js` | вкладки | 7 |
| `web/js/card.js`, `web/tests/card.test.js` | каркас, вкладка документа | 8, 9 |
| `web/js/main.js` | `onOpenDoc`, `open(path, {doc})` | 9 |
| `web/app.css`, `web/tests/app-css.test.js` | каркас, вкладки, место сессии | 11 |
| `web/js/i18n.js` | подписи | 7, 9, 10 |
| `docs/engineering/window-and-panel.md` | раздел о листе карточки | 14 |

---

### Task 0: Сверка с веткой выпуска

**Files:** —

- [ ] **Step 1:** `git fetch origin` и `git log --oneline HEAD..origin/release/v0.13.0`. Если в ветке выпуска появились коммиты — `git rebase origin/release/v0.13.0` и перечитать изменённые из списка «Структура файлов».
- [ ] **Step 2:** `make test-web` и `make test` на чистой ветке — зелёные. Иначе — строка в лог карточки и письмо оркестратору: чужой красный тест не чиню.

---

### Task 1: Валидатор знает `session` документа

**Files:**
- Modify: `plugin/templates/board/scripts/validate_cards.py` (новая функция после `validate_doc_paths`; вызов в `main`)
- Test: `plugin/templates/board/scripts/test_validate_cards.py` (класс `DocFrontmatterTest` перед `if __name__`)

**Interfaces:**
- Produces: `validate_doc_frontmatter(root: Path) -> list[str]`; сообщения начинаются с пути документа относительно его каталога `docs`.

- [ ] **Step 1: Тесты**

Импорт в начале файла дополнить `validate_doc_frontmatter`. Класс:

```python
class DocFrontmatterTest(unittest.TestCase):
    """Документ может назвать сессию-автора во frontmatter; панель откроет её рядом."""

    def write(self, root: Path, text: str, name: str = "2026-09-12-report.md") -> None:
        (root / "docs" / "reports" / name).write_text(text, encoding="utf-8")

    def test_document_without_frontmatter_is_valid(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = board_with_report(tmp)
            self.assertEqual(validate_doc_frontmatter(root), [])

    def test_session_in_the_short_id_format_is_valid(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = board_with_report(tmp)
            self.write(root, "---\ndate: 2026-09-12\nsession: e62e1d58\ncards: [T-090]\n---\n# Разбор\n")
            self.assertEqual(validate_doc_frontmatter(root), [])

    def test_session_not_like_a_short_id_is_error_naming_the_document(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = board_with_report(tmp)
            self.write(root, "---\nsession: оркестр\n---\n# Разбор\n")
            errors = validate_doc_frontmatter(root)
            self.assertEqual(len(errors), 1)
            self.assertIn("reports/2026-09-12-report.md", errors[0])
            self.assertIn("оркестр", errors[0])

    def test_empty_session_is_error(self):
        # Пустое поле панель не отличит от отсутствующего, а автор думал, что назвал себя.
        with tempfile.TemporaryDirectory() as tmp:
            root = board_with_report(tmp)
            self.write(root, "---\nsession:\n---\n# Разбор\n")
            self.assertEqual(len(validate_doc_frontmatter(root)), 1)

    def test_unclosed_frontmatter_is_error(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = board_with_report(tmp)
            self.write(root, "---\nsession: e62e1d58\n# Разбор\n")
            self.assertEqual(len(validate_doc_frontmatter(root)), 1)

    def test_other_fields_are_free(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = board_with_report(tmp)
            self.write(root, "---\nprs: [143, 146]\nrepo: opensource/fleetdeck\n---\n# Разбор\n")
            self.assertEqual(validate_doc_frontmatter(root), [])

    def test_documents_next_to_the_board_are_checked(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp) / "board"
            (root / "cards").mkdir(parents=True)
            (root / ".git").mkdir()
            (Path(tmp) / "docs").mkdir()
            (Path(tmp) / "docs" / "design.md").write_text("---\nsession: x\n---\n", encoding="utf-8")
            self.assertEqual(len(validate_doc_frontmatter(root)), 1)

    def test_main_on_the_cards_directory_reports_the_document(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = board_with_report(tmp)
            (root / "cards" / "T-001-card.md").write_text(card(id="T-001"), encoding="utf-8")
            self.write(root, "---\nsession: nope\n---\n")
            out = io.StringIO()
            with contextlib.redirect_stdout(out):
                code = main(["validate_cards.py", str(root / "cards")])
            self.assertEqual(code, 1)
            self.assertIn("session документа", out.getvalue())

    def test_main_on_one_card_does_not_check_documents(self):
        # Сессия проверяет свою карточку; чужой документ ей чинить нельзя.
        with tempfile.TemporaryDirectory() as tmp:
            root = board_with_report(tmp)
            path = root / "cards" / "T-001-card.md"
            path.write_text(card(id="T-001"), encoding="utf-8")
            self.write(root, "---\nsession: nope\n---\n")
            with contextlib.redirect_stdout(io.StringIO()):
                self.assertEqual(main(["validate_cards.py", str(path)]), 0)
```

`board_with_report` и `card` уже есть в файле; если `card` не принимает `id=` — посмотреть его сигнатуру и передать так, как делает `test_main_fails_on_a_card_with_a_document_path`.

- [ ] **Step 2:** `cd plugin/templates/board/scripts && python3 -m unittest test_validate_cards.DocFrontmatterTest -v` — ошибка импорта `validate_doc_frontmatter`.

- [ ] **Step 3: Реализация** — после `validate_doc_paths`:

```python
def validate_doc_frontmatter(root: Path) -> list[str]:
    """Frontmatter документа доски: session, если есть, — short id автора.

    Панель открывает эту сессию рядом с документом. Поле необязательное; но
    названное неверно отправит ответ не туда или никуда, поэтому ошибка.
    """
    errors: list[str] = []
    for docs in docs_dirs(root):
        for path in sorted(docs.rglob("*.md")):
            name = path.relative_to(docs).as_posix()
            try:
                text = path.read_text(encoding="utf-8")
            except (OSError, UnicodeDecodeError) as exc:
                errors.append(f"{name}: документ не прочитан: {exc}")
                continue
            if not text.startswith("---"):
                continue
            fields = parse_frontmatter(text)
            if fields is None:
                errors.append(f"{name}: frontmatter документа не закрыт строкой ---")
                continue
            if "session" in fields and not SESSION_RE.match(fields["session"]):
                errors.append(f"{name}: session документа не похож на short id: {fields['session']!r}")
    return errors
```

В `main`, после цикла `validate_doc_paths`:

```python
    if target.is_dir():
        errors.extend(validate_doc_frontmatter(root))
```

- [ ] **Step 4:** весь набор: `python3 -m unittest discover --pattern 'test_*.py'` — зелёный; `go test ./plugin/templates/ -run TestTheBoardsPythonTestsAllRunAndPass -count=1` — зелёный (счётчик тестов сходится).

- [ ] **Step 5:** проверить на копии живой доски, что правило ничего не ломает: `cp -R ~/obsidian/board "$CLAUDE_JOB_DIR/tmp/board-copy"` (только чтение живой доски), `python3 plugin/templates/board/scripts/validate_cards.py "$CLAUDE_JOB_DIR/tmp/board-copy/cards"`. Ожидание: новых ошибок `session документа` нет (три документа с frontmatter — верные id). Карточные ошибки чужих карточек не чинить.

- [ ] **Step 6: Commit**

```bash
git add plugin/templates/board/scripts/validate_cards.py plugin/templates/board/scripts/test_validate_cards.py
git commit --signoff --message "feat(board): let a document name the session that wrote it"
```

---

### Task 2: Конвенция и скиллы

**Files:**
- Modify: `docs/en/board-convention.md:80-88`, `docs/ru/board-convention.md:80-88`
- Modify: `plugin/skills/card-keeping/SKILL.md:120-124`, `plugin/skills/fleet-orchestrator/SKILL.md:96-98`
- Modify: `plugin/.claude-plugin/plugin.json`

- [ ] **Step 1:** EN — абзац после предложения о `link_docs.py`:

```markdown
A document may name the session that wrote it, in a frontmatter of its own:

    ---
    session: e62e1d58
    ---

The value is the session's short id, in the same form as a card's `session`.
The panel opens that session next to the document, so an answer to a question
the document asks goes to the one who asked it. The field is optional; a
document without it falls back to the `session` of the card it was opened
from. Other frontmatter fields (`date`, `cards`, `prs`) are free. The validator
rejects a `session` that is not a short id and a frontmatter that is not closed.
```

RU — тот же смысл в разделе «Документы карточки», тем же местом и примером.

- [ ] **Step 2:** card-keeping, в абзац про `[[имя-файла]]`:

```markdown
Документ, который пишешь ты, начинай frontmatter с `session: <твой короткий id>`
(его показывает `whoami`): панель откроет твою сессию рядом с документом, и
ответ на вопрос из документа придёт тебе. Переписываешь чужой документ — ставь
свой id: отвечать будут тому, кто документ сейчас ведёт.
```

fleet-orchestrator, в абзац про разбор под `docs`: одна фраза «и попроси начать его frontmatter строкой `session: <её короткий id>`».

- [ ] **Step 3:** `plugin.json`: `"version": "0.5.0"`. Проверить, что в репозитории нет другого места с версией плагина: `grep --recursive --line-number '"0.4.0"' plugin .claude-plugin`.

- [ ] **Step 4:** markdownlint не гоняется в CI; проверить глазами: пустые строки вокруг списков и блоков, язык у блоков кода.

- [ ] **Step 5: Commit**

```bash
git add docs/en/board-convention.md docs/ru/board-convention.md plugin/skills plugin/.claude-plugin/plugin.json
git commit --signoff --message "docs(board): ask sessions to sign the documents they write"
```

---

### Task 3: `/api/docs` отдаёт автора

**Files:**
- Create: `internal/board/docmeta.go`, `internal/board/docmeta_test.go`
- Modify: `internal/server/docs.go:31-35` (`Doc`), `:92` (заполнение)
- Test: `internal/server/docs_test.go`

**Interfaces:**
- Produces: `board.DocSession(path string) string` — id или `""`; JSON-поле `session` у элемента `/api/docs`, опущено, когда пусто.

- [ ] **Step 1: Тесты `docmeta_test.go`**

```go
package board

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeDoc(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "doc.md")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func TestDocSessionReadsTheShortIDFromTheFrontmatter(t *testing.T) {
	path := writeDoc(t, "---\ndate: 2026-09-27\nsession: e62e1d58\ncards: [T-090]\n---\n# Doc\n")
	if got := DocSession(path); got != "e62e1d58" {
		t.Fatalf("DocSession = %q, want e62e1d58", got)
	}
}

func TestDocSessionIsEmptyWithoutAFrontmatter(t *testing.T) {
	if got := DocSession(writeDoc(t, "# Doc\n\nsession: e62e1d58\n")); got != "" {
		t.Fatalf("a session line in the body is not the author: %q", got)
	}
}

func TestDocSessionRefusesAValueThatIsNotAShortID(t *testing.T) {
	for _, value := range []string{"orchestrator", "e62e1", "e62e1d58e62e1d58", "\"\"", "[a, b]"} {
		if got := DocSession(writeDoc(t, "---\nsession: "+value+"\n---\n")); got != "" {
			t.Errorf("session %s: DocSession = %q, want empty", value, got)
		}
	}
}

func TestDocSessionIgnoresAFrontmatterPastTheHead(t *testing.T) {
	// Only the head is read: a document is listed on every panel open, and a
	// frontmatter that far down is not one.
	body := "---\n" + strings.Repeat("x: y\n", 2000) + "session: e62e1d58\n---\n"
	if got := DocSession(writeDoc(t, body)); got != "" {
		t.Fatalf("DocSession = %q past the head", got)
	}
}

func TestDocSessionOfAMissingFileIsEmpty(t *testing.T) {
	if got := DocSession(filepath.Join(t.TempDir(), "none.md")); got != "" {
		t.Fatalf("DocSession = %q", got)
	}
}
```

В `docs_test.go`:

```go
func TestDocsListNamesTheSessionThatWroteADocument(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "signed.md"), "---\nsession: e62e1d58\n---\n# a")
	writeFile(t, filepath.Join(dir, "plain.md"), "# b")
	writeFile(t, filepath.Join(dir, "wrong.md"), "---\nsession: nobody\n---\n# c")

	rec := getDocs(docsDeps(t, dir), "/api/docs")
	var docs []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &docs); err != nil {
		t.Fatalf("decode: %v", err)
	}
	got := map[string]any{}
	for _, d := range docs {
		got[filepath.Base(d["path"].(string))] = d["session"]
	}
	if got["signed.md"] != "e62e1d58" || got["plain.md"] != nil || got["wrong.md"] != nil {
		t.Fatalf("sessions by document: %v", got)
	}
}
```

- [ ] **Step 2:** `go test ./internal/board/ ./internal/server/ -run 'DocSession|NamesTheSession' -count=1` — не компилируется / красный.

- [ ] **Step 3: `docmeta.go`**

```go
package board

import (
	"io"
	"os"
	"regexp"

	"gopkg.in/yaml.v3"
)

// docHead is how much of a document is read for its frontmatter. The list of
// documents is built on every card the panel opens, so the whole file is not.
const docHead = 8 << 10

// shortIDRe is the form of a session's short id, the same the board validator
// holds a card's and a document's session to.
var shortIDRe = regexp.MustCompile(`^[0-9a-fA-F]{6,12}$`)

// DocSession is the short id of the session that wrote the document at path,
// from the document's own frontmatter (docs/en/board-convention.md, "Documents
// a card links to"), or "" when the document names none or names it wrongly.
func DocSession(path string) string {
	f, err := os.Open(path) //nolint:gosec // path comes from the docs listing, already confined to a root
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	head, err := io.ReadAll(io.LimitReader(f, docHead))
	if err != nil {
		return ""
	}
	m := frontmatterRe.FindSubmatch(head)
	if m == nil {
		return ""
	}
	var fm struct {
		Session string `yaml:"session"`
	}
	if yaml.Unmarshal(m[1], &fm) != nil || !shortIDRe.MatchString(fm.Session) {
		return ""
	}
	return fm.Session
}
```

Если в `internal/board` уже есть регулярка формата id — использовать её, не заводить вторую (`grep --line-number 'a-fA-F' internal/board/*.go`). `nolint` оставить, только если golangci-lint действительно ругается.

В `docs.go`: поле `Session string \`json:"session,omitempty"\`` с комментарием «the short id of the session that wrote it, from the document's frontmatter; empty when it names none»; в `WalkDir`: `out = append(out, Doc{Path: resolved, Title: rel, Root: realRoot, Session: board.DocSession(resolved)})`.

- [ ] **Step 4:** `go test ./internal/... -race -count=1` — зелёный. `golangci-lint run ./internal/...` — чисто.

- [ ] **Step 5: Commit**

```bash
git add internal/board/docmeta.go internal/board/docmeta_test.go internal/server/docs.go internal/server/docs_test.go
git commit --signoff --message "feat(server): name the session that wrote each document"
```

---

### Task 4: Стенд видит лист карточки — и красный без фичи

**Files:**
- Modify: `scripts/standdaemon/main.go` и то место, где он раскладывает доску фикстуры (`layout(home, boardDir)`)
- Modify: `web/js/host.js:27` (`STAND_OPENS`), `cmd/fleetdeck-window/standsettings.go:108-111` (+ его тест)
- Modify: `web/js/standreport.js`, `web/js/main.js:361-379`
- Create: `scripts/standcheck/cardsheet.go`, `scripts/standcheck/cardsheet_test.go`
- Modify: `scripts/standcheck/main.go`, `scripts/ci-window-stand.sh`, `.github/workflows/ci.yaml` (джоба `window-on-macos-26`)

**Interfaces:**
- Produces (страница → лог окна): строка `the board reports its card sheet: <json>`, где json:

```json
{"open":true,"stage":{"w":820,"h":560},"pane":{"x":0,"y":0,"w":450,"h":560},
 "dock":{"x":458,"y":0,"w":362,"h":560},"place":"right","chosen":"right",
 "terminal":{"open":true,"cols":48,"rows":30}}
```

`pane`, `dock`, `terminal` — `null`, когда таких узлов нет. Координаты — `getBoundingClientRect()` в CSS px.
- Produces (стенд): `FLEETDECK_STAND_OPEN=carddoc-bottom|carddoc-right`.

- [ ] **Step 1: Фикстура.** Прочитать `scripts/standdaemon/main.go` целиком (субагентом, если длинный) и найти, где пишется доска. Добавить:
  - каталог `docs` рядом с доской фикстуры (так, как рабочий каталог fleetdeck раскладывает `<корень>/board` и `<корень>/docs`), и чтобы `Deps.DocsRoots` панели на стенде его видел — проверить, откуда панель стенда берёт корни документов;
  - `docs/reports/2026-09-26-sessions-fold-design.md`: frontmatter `session: 5e55a002`, заголовок, раздел «Вопросы оператору» с двумя пунктами, несколько абзацев;
  - `docs/reports/2026-09-26-column-widths.md`: `session: 5e55a003`;
  - карточку `T-085` стадии `review` с `session: 5e55a002` и ссылками на оба документа;
  - экран для `5e55a002`: выбор AskUserQuestion так, как его рисует Claude Code (заголовок, вопрос, «❯ 1. …» — «4. Type something.», строка подсказки). Если сейчас `screen()` отдаётся всем сессиям одинаково — сделать экран зависимым от short.
  - Тест `standdaemon` (если у него есть тесты) — что документы и карточка на месте и ссылки резолвятся.

- [ ] **Step 2: Имена открытия.** `STAND_OPENS` += `"carddoc-bottom"`, `"carddoc-right"`; `standsettings.go` — те же имена в проверке и в тексте ошибки; тест `standsettings` на новые имена — сначала красный.

- [ ] **Step 3: Стендовое открытие без фичи.** В `main.js` рядом с открытием `session`: для `carddoc-*` — по первому снимку с карточкой фикстуры (`T-085`) вызвать `cardPanel.open(path, { doc: "2026-09-26-sessions-fold-design", dock: "bottom"|"right", expand: true })`. На этом шаге `open` второй аргумент игнорирует — лист откроется обычный. Хранилище не трогать: выбор места передаётся параметром, а не пишется в `localStorage` стенда.

- [ ] **Step 4: Отчёт.** В `standreport.js` — функция `cardSheetReport(document)`, собирающая json выше по селекторам `#card-panel`, `.card-stage`, `.card-pane`, `.card-dock`, `.card-dock .xterm`; строка уходит тем же путём, что `the board reports its top band`. Тест в `web/js/_tests/standreport.test.js`: на фейковом DOM без `.card-stage` — `pane:null`, `dock:null`; с узлами — прямоугольники.

- [ ] **Step 5: Гейты.** `cardsheet.go`:

```go
// cardSheetPrefix is the line the board writes about an open card sheet
// (web/js/standreport.js, cardSheetReport).
const cardSheetPrefix = "fleetdeck-window: the board reports its card sheet: "

type rect struct{ X, Y, W, H float64 }

type cardSheet struct {
	Open     bool   `json:"open"`
	Stage    *rect  `json:"stage"`
	Pane     *rect  `json:"pane"`
	Dock     *rect  `json:"dock"`
	Place    string `json:"place"`
	Chosen   string `json:"chosen"`
	Terminal *struct {
		Open       bool `json:"open"`
		Cols, Rows int
	} `json:"terminal"`
}

// dockRightMin is web/js/carddock.js DOCK_RIGHT_MIN.
const dockRightMin = 640

// cardSheetVerdicts holds the last card-sheet report to the spec's gates
// (docs/superpowers/specs/2026-09-27-card-document-tabs-design.md, 8.1).
func cardSheetVerdicts(s cardSheet, chosen string) []string
```

Возвращает список нарушений словами (пустой — прошло):
1. нет `Dock` или нет `Terminal` или `!Terminal.Open` или терминал меньше 200 × 120 → «the session is not open next to the document»;
2. `Pane` меньше 200 × 120, или `Pane` и `Dock` пересекаются (площадь пересечения > 0) → «the session covers the document»;
3. `chosen == "right"`: `Stage.W < 640` и `Place != "bottom"` → «a narrow sheet keeps the session on the right»; `Stage.W >= 640` и `Place != "right"` → «a wide sheet puts the session below though right was chosen»; `chosen == "bottom"` и `Place != "bottom"` → то же слово.

Гейт 4 — существующий гейт полосы перетаскивания: в `ci-window-stand.sh` ожидание листа (`sheet_open`) сейчас ждёт `"sheetOpen":["session-panel"]`; расширить на `card-panel` для `carddoc-*` и включить для них тот же гейт полосы.

Таблица тестов `cardsheet_test.go`: каждое из трёх правил по отдельности красное, граница 639/640, полностью верный отчёт — пусто. `main.go`: при `-open` с `carddoc-*` — взять последнюю строку `cardSheetPrefix`, прогнать `cardSheetVerdicts`; нет строки — вердикт «the board never reported a card sheet».

- [ ] **Step 6: CI.** В `window-on-macos-26` после шага «session open» — пять шагов, каждый с `if: ${{ !cancelled() }}` и комментарием, что он держит:

| Шаг | `FLEETDECK_STAND_WINDOW_SIZE` | `APPEARANCE` | `CAPSULES` | `OPEN` | каталог |
| --- | --- | --- | --- | --- | --- |
| document tab, session below, glass, dark | 1000x700 | dark | glass | carddoc-bottom | out/dark-carddoc-bottom |
| document tab, session below, opaque, light | 1000x700 | light | opaque | carddoc-bottom | out/light-opaque-carddoc-bottom |
| session asked right on a narrow sheet, glass, dark | 1000x700 | dark | glass | carddoc-right | out/dark-carddoc-right-narrow |
| session right, glass, light | 1600x900 | light | glass | carddoc-right | out/light-carddoc-right |
| session right, opaque, dark | 1600x900 | dark | opaque | carddoc-right | out/dark-opaque-carddoc-right |

Перед этим проверить: как `CAPSULES=opaque` выражен в существующих шагах (значение может называться иначе) и помещается ли 1600x900 на дисплей раннера (джоба печатает его). Не помещается — наибольший размер, при котором `.card-stage` шире 640; записать в лог карточки.

- [ ] **Step 7:** локально: `go test ./scripts/... ./cmd/... -count=1`, `make test-web`. Коммиты по частям (фикстура; имена; отчёт; гейты; CI), каждый `--signoff`.

- [ ] **Step 8: Красный стенд.** `git push --set-upstream origin feat/v0.13.0-card-document-tabs`, открыть черновой PR в `release/v0.13.0` (текст — по шаблону `.github`, если есть; показать оркестратору не требуется, PR черновой), `gh pr checks --watch` в фоне. Ожидание: в пяти новых шагах гейты 1–3 красные («the board never reported a card sheet» или «the session is not open next to the document»), гейт полосы — зелёный; прочие шаги — зелёные. Логи и кадры — субагентом (`gh run download`), он возвращает вердикты и одну строку о каждом кадре. Итог — строкой в лог карточки.

---

### Task 5: Автор и его состояние

**Files:**
- Create: `web/js/docauthor.js`, `web/tests/docauthor.test.js`

**Interfaces:**
- Produces:
  - `authorOf(doc: {session?: string} | null, card: {session?: string}) -> {short: string, from: "document"|"card"} | null`
  - `authorState(short: string, snap) -> "orchestrator"|"unknown"|"dead"|"stopped"|"stalled"|"waiting"|"working"`
  - `sessionOf(short, snap) -> object | null`

- [ ] **Step 1: Тесты**

```js
import { test } from "node:test";
import assert from "node:assert/strict";
import { authorOf, authorState } from "../js/docauthor.js";

test("a document that names its session is authored by it, whatever the card says", () => {
  assert.deepEqual(authorOf({ session: "a41c09d2" }, { session: "909bf9b2" }), { short: "a41c09d2", from: "document" });
});

test("a document without a session falls back to the card it is opened from", () => {
  assert.deepEqual(authorOf({}, { session: "909bf9b2" }), { short: "909bf9b2", from: "card" });
  assert.deepEqual(authorOf({ session: "" }, { session: "909bf9b2" }), { short: "909bf9b2", from: "card" });
});

test("the card tab is authored by the card's session", () => {
  assert.deepEqual(authorOf(null, { session: "909bf9b2" }), { short: "909bf9b2", from: "card" });
});

test("nobody to answer when neither names a session", () => {
  assert.equal(authorOf({}, {}), null);
  assert.equal(authorOf(null, { session: "" }), null);
});

const snap = (sessions, orchestratorSession = "") => ({ sessions, orchestratorSession });

test("the orchestrator's pinned session is the orchestrator, even while it waits", () => {
  assert.equal(authorState("0c7e1a2b", snap([{ short: "0c7e1a2b", needs: "answer: x?" }], "0c7e1a2b")), "orchestrator");
});

test("a session the snapshot does not list is unknown", () => {
  assert.equal(authorState("a41c09d2", snap([])), "unknown");
});

test("lifecycle comes before what the session last said", () => {
  assert.equal(authorState("a", snap([{ short: "a", lifecycle: "dead", needs: "answer: x?" }])), "dead");
  assert.equal(authorState("a", snap([{ short: "a", lifecycle: "stopped", needs: "answer: x?" }])), "stopped");
});

test("stalled, waiting and working follow needs.js", () => {
  assert.equal(authorState("a", snap([{ short: "a", needs: "usage limit reached" }])), "stalled");
  assert.equal(authorState("a", snap([{ short: "a", needs: "answer: first or after?" }])), "waiting");
  assert.equal(authorState("a", snap([{ short: "a", needs: "" }])), "working");
});
```

Точные значения `needs` для «stalled» и «waiting» сверить с `web/js/needs.js` (`STALLED_NEEDS_PREFIXES`, `waiting`) и с `web/tests/needs.test.js`, если он есть.

- [ ] **Step 2:** `node --test web/tests/docauthor.test.js` — модуля нет.

- [ ] **Step 3: Реализация**

```js
// web/js/docauthor.js — who wrote a card's document, and what that session is doing.
//
// The author is the session a document names in its own frontmatter
// (docs/en/board-convention.md, "Documents a card links to"); a document that
// names none falls back to the card it was opened from, and says so.
import { DEAD, STOPPED } from "./lifecycle.js";
import { isStalled, waiting, WAITING_YES } from "./needs.js";

export function authorOf(doc, card) {
  if (doc?.session) return { short: doc.session, from: "document" };
  if (card?.session) return { short: card.session, from: "card" };
  return null;
}

export function sessionOf(short, snap) {
  return (snap?.sessions ?? []).find((s) => s.short === short) ?? null;
}

export function authorState(short, snap) {
  if (short && short === snap?.orchestratorSession) return "orchestrator";
  const s = sessionOf(short, snap);
  if (!s) return "unknown";
  if (s.lifecycle === DEAD) return "dead";
  if (s.lifecycle === STOPPED) return "stopped";
  if (isStalled(s)) return "stalled";
  if (waiting(s) === WAITING_YES) return "waiting";
  return "working";
}
```

- [ ] **Step 4:** `make test-web` — зелёный.
- [ ] **Step 5:** `git commit --signoff --message "feat(panel): tell who wrote a card's document and what it is doing"` (с `git add` двух файлов).

---

### Task 6: Настройки места сессии

**Files:**
- Create: `web/js/carddock.js` (чистая часть), `web/tests/carddock.test.js`

**Interfaces:**
- Produces:
  - `DOCK_RIGHT_MIN = 640`, `DOCK_KEYS = { place: "fleetdeck-card-session-dock", height: "fleetdeck-card-session-height-pct", width: "fleetdeck-card-session-width-pct" }`
  - `effectiveDock(chosen: "bottom"|"right", stageWidth: number) -> "bottom"|"right"`
  - `clampDockSize(place, pct) -> number` (bottom 25–80, right 30–60; не число → значение по умолчанию 55/45)
  - `readDockPrefs(storage = globalThis.localStorage, keys = DOCK_KEYS) -> {place, height, width}`
  - `writeDockPref(name: "place"|"height"|"width", value, storage, keys) -> void`

- [ ] **Step 1: Тесты**

```js
import { test } from "node:test";
import assert from "node:assert/strict";
import { DOCK_RIGHT_MIN, clampDockSize, effectiveDock, readDockPrefs, writeDockPref } from "../js/carddock.js";

function memoryStorage(initial = {}) {
  const data = new Map(Object.entries(initial));
  return {
    getItem: (k) => (data.has(k) ? data.get(k) : null),
    setItem: (k, v) => data.set(k, String(v)),
    removeItem: (k) => data.delete(k),
    data,
  };
}
const throwing = { getItem() { throw new Error("denied"); }, setItem() { throw new Error("denied"); }, removeItem() { throw new Error("denied"); } };

test("right is taken only from the threshold up", () => {
  assert.equal(DOCK_RIGHT_MIN, 640);
  assert.equal(effectiveDock("right", 639), "bottom");
  assert.equal(effectiveDock("right", 640), "right");
  assert.equal(effectiveDock("bottom", 2000), "bottom");
});

test("anything but right reads as bottom", () => {
  assert.equal(effectiveDock("left", 2000), "bottom");
  assert.equal(effectiveDock(undefined, 2000), "bottom");
});

test("sizes are held inside their range, and garbage gives the default", () => {
  assert.equal(clampDockSize("bottom", 10), 25);
  assert.equal(clampDockSize("bottom", 95), 80);
  assert.equal(clampDockSize("right", 10), 30);
  assert.equal(clampDockSize("right", 95), 60);
  assert.equal(clampDockSize("bottom", Number.NaN), 55);
  assert.equal(clampDockSize("right", "abc"), 45);
});

test("defaults without anything stored", () => {
  assert.deepEqual(readDockPrefs(memoryStorage()), { place: "bottom", height: 55, width: 45 });
});

test("stored choices come back, clamped", () => {
  const s = memoryStorage({ "fleetdeck-card-session-dock": "right", "fleetdeck-card-session-height-pct": "90", "fleetdeck-card-session-width-pct": "40" });
  assert.deepEqual(readDockPrefs(s), { place: "right", height: 80, width: 40 });
});

test("a storage that refuses is not an error", () => {
  assert.deepEqual(readDockPrefs(throwing), { place: "bottom", height: 55, width: 45 });
  assert.doesNotThrow(() => writeDockPref("place", "right", throwing));
});

test("writing the place stores exactly that key", () => {
  const s = memoryStorage();
  writeDockPref("place", "right", s);
  assert.deepEqual([...s.data], [["fleetdeck-card-session-dock", "right"]]);
});
```

- [ ] **Step 2:** `node --test web/tests/carddock.test.js` — модуля нет.

- [ ] **Step 3: Реализация** (шапка модуля — комментарий о назначении, как у соседей):

```js
export const DOCK_RIGHT_MIN = 640;
export const DOCK_KEYS = {
  place: "fleetdeck-card-session-dock",
  height: "fleetdeck-card-session-height-pct",
  width: "fleetdeck-card-session-width-pct",
};
const RANGE = { bottom: [25, 80, 55], right: [30, 60, 45] };

export function effectiveDock(chosen, stageWidth) {
  return chosen === "right" && stageWidth >= DOCK_RIGHT_MIN ? "right" : "bottom";
}

export function clampDockSize(place, pct) {
  const [min, max, fallback] = RANGE[place === "right" ? "right" : "bottom"];
  const n = typeof pct === "number" ? pct : Number.parseFloat(pct);
  if (!Number.isFinite(n)) return fallback;
  return Math.min(max, Math.max(min, n));
}

function read(storage, key) {
  try { return storage?.getItem(key) ?? null; } catch { return null; }
}

export function readDockPrefs(storage = globalThis.localStorage, keys = DOCK_KEYS) {
  const place = read(storage, keys.place) === "right" ? "right" : "bottom";
  return {
    place,
    height: clampDockSize("bottom", read(storage, keys.height)),
    width: clampDockSize("right", read(storage, keys.width)),
  };
}

export function writeDockPref(name, value, storage = globalThis.localStorage, keys = DOCK_KEYS) {
  try { storage?.setItem(keys[name], String(value)); } catch { /* a refusing storage keeps the choice for this page only */ }
}
```

- [ ] **Step 4:** `make test-web` — зелёный.
- [ ] **Step 5:** `git commit --signoff --message "feat(panel): remember where a card keeps its session"`.

---

### Task 7: Вкладки

**Files:**
- Create: `web/js/cardtabs.js`, `web/tests/cardtabs.test.js`
- Modify: `web/js/i18n.js` (en и ru)

**Interfaces:**
- Consumes: `documentsOf`, `docTitle` из `docnames.js`; `authorOf`, `authorState` из задачи 5.
- Produces:
  - `tabsOf(card, cards, docs) -> Array<{key: "card"} | {key: string /* doc.path */, doc}>` — первая всегда `{key: "card"}`
  - `renderTabs(root, tabs, { active, stateOf, onPick })` — рисует в `root` (`.card-tabs`, `role="tablist"`) кнопки `.card-tab` (`role="tab"`, `aria-selected`), у документов — `.card-tab-dot[data-state]` с `aria-label` состояния; кнопку `.card-tabs-more` «ещё N ▾», если вкладки не помещаются; возвращает `{ scrollActiveIntoView() }`.
  - i18n: `card_tab_card`, `card_tabs_more` (`"{n} more"` / `"ещё {n}"`), `author_state_orchestrator|unknown|dead|stopped|stalled|waiting|working`.

- [ ] **Step 1: Тесты** — на фейковом DOM (`installDOM`), фикстура `snapshot.json` + два фейковых документа:
  - первая вкладка «Карточка», потом документы в порядке ссылок тела, без повторов и без битых ссылок;
  - `stateOf(tab)` вызывается для каждой вкладки документа, точка несёт `data-state` и `aria-label` из i18n;
  - щелчок по вкладке зовёт `onPick(key)`; ←/→ на `tablist` зовут `onPick` соседней, по кругу не ходят;
  - активная вкладка одна, `aria-selected="true"`;
  - «ещё N»: фейковый DOM не считает ширины — передать `measure` (функция, возвращающая `{row, tabs:[{right}]}`) параметром; при ширине строки меньше суммы — кнопка с N = число вкладок целиком за правым краем; щелчок по ней открывает список `.card-tabs-menu` всех вкладок, выбор зовёт `onPick`.

Каждый тест — сначала красный (модуля нет), потом зелёный.

- [ ] **Step 2–4:** реализация по интерфейсу; DOM через `createElement`/`textContent`, как в `card.js` (никакого `innerHTML` для подписей). `make test-web`.
- [ ] **Step 5:** `git commit --signoff --message "feat(panel): lay a card's documents out as tabs"`.

---

### Task 8: Каркас листа карточки

**Files:**
- Modify: `web/js/card.js` (`renderCard`, `draw`, `build`)
- Test: `web/tests/card.test.js`

**Interfaces:**
- Produces: DOM внутри `root` (`#card-panel`), строится один раз в `renderCard`:
  - `.card-sheet` → `.card-head`, `.card-tabs`, `.card-stage[data-dock]` → `.card-pane`, `.card-dock-grip`, `.card-dock`.
  - `draw(snap)` меняет только детей `.card-head`, `.card-tabs` и `.card-pane` (по той же подписи `signature`/`painted`, но раздельно для шапки/вкладок и для панели), `.card-dock` не трогает.

- [ ] **Step 1: Тесты (красные на текущем коде)**

```js
test("a snapshot redraws the card without replacing where its session lives", async () => {
  const snap = snapshot();
  const store = fakeStore(snap);
  const root = open(snap, FLEET_UI, { subscribe: store.subscribe });
  const dock = root.querySelector(".card-dock");
  assert.ok(dock, "the sheet has a place for the session");
  const changed = snapshot();
  changed.cards.find((c) => c.path === FLEET_UI).progress = 60;
  store.push(changed);
  await settle();
  assert.equal(root.querySelector(".card-dock"), dock);
  assert.match(root.querySelector(".card-pane").textContent, /60/);
});

test("the card's fields, meta, documents, body and backlinks sit in the pane", () => {
  const root = open(snapshot());
  const pane = root.querySelector(".card-pane");
  for (const sel of [".card-fields", ".card-meta", ".card-body"]) assert.ok(pane.querySelector(sel), sel);
  assert.ok(root.querySelector(".card-head"));
  assert.ok(!pane.querySelector(".card-head"), "the head stays above the tabs");
});
```

`open` и `options.subscribe` — как в существующих тестах файла; если `renderCard` принимает стор другим именем — взять его.

- [ ] **Step 2:** `node --test web/tests/card.test.js` — два новых красные, прочие зелёные.
- [ ] **Step 3:** переделать `renderCard`: каркас при создании; `build()` возвращает детей панели, `head()` — шапку; `draw` кладёт их в свои контейнеры. Обработчики Escape и щелчка мимо — без изменений (Escape из `[data-terminal]` по-прежнему не закрывает). Прокрутка — у `.card-pane`, не у `#card-panel`: `markScrollablesWithin`/`watchScrollables` перевести на `.card-pane`.
- [ ] **Step 4:** весь `make test-web` — зелёный, включая все старые тесты `card.test.js` без правок их ожиданий. Если старый тест опирался на «лист прокручивается целиком» — это изменение поведения; не переписывать тест молча, а записать в лог карточки и поправить с объяснением в сообщении коммита.
- [ ] **Step 5:** `git commit --signoff --message "refactor(panel): give the card sheet a frame that outlives its redraws"`.

---

### Task 9: Документ во вкладке

**Files:**
- Modify: `web/js/card.js`, `web/js/main.js:98-112`, `web/js/i18n.js`
- Test: `web/tests/card.test.js`

**Interfaces:**
- Consumes: `tabsOf`, `renderTabs` (задача 7), `authorOf` (задача 5), `fetchDoc`, `renderMarkdown`, `docCardsRow`.
- Produces:
  - `createCardPanel(...).open(path, { doc?: string /* wiki-имя или путь */, dock?: "bottom"|"right", expand?: boolean })`
  - вкладка документа в `.card-pane`: `.card-doc-head` (заголовок `h3`, `docCardsRow`, `.card-doc-author` — «написала <id> · из документа|из карточки»), `article.card-doc-body`
  - `options.onOpenDoc(path)` зовётся только для документа, которого у карточки нет.
  - i18n: `card_doc_author` (`"written by {short}"`/`"написала {short}"`), `card_doc_author_from_document`, `card_doc_author_from_card`, `card_doc_loading`, `card_doc_failed`.

- [ ] **Step 1: Тесты (красные)**
  - щелчок по `.card-doc` в блоке документов карточки — активна вкладка документа, `onOpenDoc` не вызван, `fetch` вызван с `/api/docs/content?path=…` один раз; повторный выбор этой вкладки — без второго `fetch`;
  - ссылка `[[имя]]` в теле карточки на её документ — вкладка; на чужой документ — `onOpenDoc(path)`;
  - подпись автора: `doc.session` → «из документа»; без → `card.session` и «из карточки»; ни того ни другого — подписи нет;
  - `open(path, {doc})` — лист открыт сразу на этом документе;
  - ошибка `fetchDoc` — текст `card_doc_failed` с причиной в панели, вкладки работают;
  - перерисовка снимком не перезапрашивает тело.
- [ ] **Step 2–3:** реализация; `main.js`: у `createCardPanel` `onOpenDoc` по-прежнему закрывает лист и открывает читалку — но теперь зовётся только для чужих документов. `routes`/`fleetdeckOpen({kind:"doc"})` не меняются.
- [ ] **Step 4:** `make test-web` — зелёный.
- [ ] **Step 5:** `git commit --signoff --message "feat(panel): open a card's documents inside the card"`.

---

### Task 10: Место сессии

**Files:**
- Modify: `web/js/carddock.js` (UI-часть), `web/js/card.js` (подключение), `web/js/i18n.js`
- Test: `web/tests/carddock.test.js`, `web/tests/card.test.js`

**Interfaces:**
- Consumes: `effectiveDock`, `clampDockSize`, `readDockPrefs`, `writeDockPref` (задача 6); `authorState`, `sessionOf` (задача 5); `createLiveTerminal` (`liveterminal.js`); `KEYS` (`session.js`); `resumeSession` (`api.js`); `isResumable` (`lifecycle.js`).
- Produces:

```js
// createCardDock mounts the place a card keeps its session in.
// options: {
//   stage,                      // .card-stage, whose width decides the place
//   storage, keys,              // DOCK_KEYS by default
//   observe(el, fn) -> stop,    // ResizeObserver by default; a test passes its own
//   terminal(host, short, o),   // createLiveTerminal by default
//   resume(short) -> Promise,   // resumeSession by default
//   toOrchestrator(),           // routes.openOrchestrator or focusing its terminal
//   links, fontKey,             // passed to the terminal as session.js does
//   place?, expand?             // the stand's forced choice, not stored
// }
// returns { show(author: {short, from} | null, snap), dispose() }
export function createCardDock(host, options)
```

  - `show` зовётся из `card.js` на каждой перерисовке и при смене вкладки; сам решает, пересоздавать ли терминал (только при смене `short` или при переходе в/из состояния, где терминала нет).
  - DOM: `.card-dock[data-open][data-place][data-state]`; свёрнутая ручка `.card-dock-handle` (снизу) или рейка `.card-dock-rail` (справа); раскрытая шапка `.card-dock-head` c `.card-dock-toggle` (две кнопки `aria-pressed`, у «справа» при узком листе `disabled` и `title` «справа не помещается»), `.card-dock-term` (`data-terminal`), `.s-keys`-подобный ряд `.card-dock-keys`; плашки `.card-dock-stopped`, `.card-dock-gone`, `.card-dock-orchestrator`.
  - `.card-stage[data-dock]` ставит `createCardDock`; размер — CSS-переменная `--card-dock-size` в процентах на `.card-stage`.
  - Разделитель `.card-dock-grip`: pointer-события, во время перетаскивания — только переменная, по отпусканию — `writeDockPref("height"|"width", …)`.
  - i18n: `dock_open`, `dock_close`, `dock_bottom`, `dock_right`, `dock_right_no_room`, `dock_resume`, `dock_resuming`, `dock_stopped_note`, `dock_gone`, `dock_write_orchestrator`, `dock_to_orchestrator`.

- [ ] **Step 1: Тесты `carddock.test.js` (красные)** — с фейковыми `observe`, `terminal` (запоминает вызовы `open/stop/type` и `short`), `resume`, `toOrchestrator`, `memoryStorage`:
  1. открыт свёрнутым: терминал не создан; ручка показывает id, состояние и `needs` для `waiting`;
  2. «Открыть» — создан терминал для `short`, `.open()` вызван; «Свернуть» — `.stop()`;
  3. `show` с тем же `short` десять раз — один терминал;
  4. `show` с другим `short` при раскрытом — старый `.stop()`, новый создан с новым `short`;
  5. `show(null)` — место скрыто (`hidden`), терминал остановлен;
  6. `orchestrator` — терминала нет, кнопка зовёт `toOrchestrator`; `dead`/`unknown` — «Написать оркестратору» зовёт `toOrchestrator`;
  7. `stopped` — «Возобновить» зовёт `resume(short)`, кнопка неактивна до ответа; ошибка — текст; следующий `show` с `lifecycle: live` при раскрытом — терминал создан;
  8. выбран «справа», `observe` сообщает 639 → `data-dock="bottom"`, кнопка «справа» `disabled`; 640 → `right`; снова 639 → `bottom`; в хранилище всё время `right`;
  9. щелчок ⬓/◨ пишет `place`; стендовый `place` из опций не пишет;
  10. перетаскивание разделителя меняет `--card-dock-size` и пишет размер один раз, по отпусканию, в границах `clampDockSize`;
  11. `dispose` останавливает терминал и снимает наблюдатель.

  В `card.test.js`:
  12. документ без `session` в карточке без `session` — `.card-dock` скрыт, вкладки работают;
  13. переключение вкладки на документ другого автора при раскрытом месте — терминал переподключён к новому автору (через фейковый `terminal` в опциях `renderCard`);
  14. `store.push` под раскрытым терминалом — тот же объект терминала, `.open()` один раз.

- [ ] **Step 2–3:** реализация. Терминал — через `options.terminal ?? createLiveTerminal` с теми же `report`-обработчиками, что `session.js` `openTerminal` (ошибки потока — строкой в шапке места).
- [ ] **Step 4:** `make test-web` — зелёный.
- [ ] **Step 5:** коммиты по смыслу (`feat(panel): keep a card's session below or beside it`, `feat(panel): bring a stopped author back from the card`), `--signoff`.

---

### Task 11: Стили

**Files:**
- Modify: `web/app.css` (после правил `#card-panel, #reader-panel`, ~стр. 1150)
- Test: `web/tests/app-css.test.js`

- [ ] **Step 1: Тесты (красные)** в стиле существующих `app-css.test.js`:
  - `#card-panel` — `display:flex; flex-direction:column; overflow:hidden`, прокрутка у `.card-pane` (`overflow:auto; min-height:0`);
  - `.card-stage[data-dock="bottom"]` — колонка, `[data-dock="right"]` — строка; `.card-dock` использует `--card-dock-size`;
  - `.card-tab-dot[data-state=…]` для всех семи состояний берёт токены `--attn`, `--danger`, `--ok`, `--text-faint`, `--accent`, без литеральных цветов;
  - ни один новый селектор не задаёт `data-window-ground` и не меняет `#sheet-scrim`;
  - новые правила не опираются на `@media` по ширине (порог — в JS).
- [ ] **Step 2–3:** правила. Цвета — только токены; разделитель и вкладки — как на макете (холст, артборд «Вариант 4»). В `[data-glass="opaque"]` лист и так непрозрачный — проверить, что ничего нового не просвечивает.
- [ ] **Step 4:** `make test-web`.
- [ ] **Step 5:** `git commit --signoff --message "style(panel): lay out a card's tabs and its session in both themes"`.

---

### Task 12: Проводка в `main.js`

**Files:**
- Modify: `web/js/main.js`

- [ ] **Step 1:** `createCardPanel` получает `toOrchestrator`: в окне — `routes.openOrchestrator()`, в браузере — `document.querySelector("#orchestrator .xterm-helper-textarea")?.focus()` (то же, что действие `focusTerminal`), и `links: terminalLinks`, `fontKey` — как у листа сессии.
- [ ] **Step 2:** тест в `web/tests/main-*.test.js`, если проводка main.js уже тестируется (найти: `grep --recursive --files-with-matches "main.js" web/tests`); иначе проводка покрывается стендом (задача 13).
- [ ] **Step 3:** `make test-web`; `git commit --signoff --message "feat(panel): send a card's orchestrator-authored document to the orchestrator"`.

---

### Task 13: Стенд зелёный

**Files:**
- Modify: `web/js/main.js` (стендовое открытие из задачи 4 теперь передаёт `doc`, `dock`, `expand`), `web/js/standreport.js` (при необходимости)

- [ ] **Step 1:** `open(path, {doc, dock, expand})` с задачи 9–10 уже понимает параметры; убедиться, что стендовый вызов их передаёт и что строка `card sheet` пишется после того, как терминал раскрыт и подогнан (по `report.ready` терминала или по первому ненулевому `cols`).
- [ ] **Step 2:** `git push`, `gh pr checks --watch` в фоне. Ожидание: все шаги `window-on-macos-26` зелёные, включая пять новых.
- [ ] **Step 3:** субагентом (`gh run download`, модель sonnet) — пять новых кадров: одна строка на кадр, совпадает ли с артбордом «Вариант 4» холста (вкладки, точка, место сессии, терминал с выбором, обе темы, стекло/opaque). Расхождения — в задачу 11, повтор.
- [ ] **Step 4:** строка в лог карточки: стенд зелёный, где кадры (артефакт `window-on-macos-26` прогона №…).

---

### Task 14: Документация, мутации, итог

**Files:**
- Modify: `docs/engineering/window-and-panel.md` (раздел о листе карточки; если его нет — новый подраздел рядом с листом сессии), `docs/engineering/live-terminal.md` (строка: третий потребитель терминала — место сессии в карточке)

- [ ] **Step 1:** документация: вкладки, автор (ссылка на конвенцию), место сессии и порог, ключи хранилища, стендовые `carddoc-*` и гейты. По-английски, как весь `docs/engineering`.
- [ ] **Step 2: Мутации** (по `live-terminal.md` §10, одна правка за раз, весь набор, откат). Минимальный список:
  - `effectiveDock`: `>=` → `>`; всегда `chosen`; всегда `bottom`;
  - `clampDockSize`: без нижней границы; без верхней; `NaN` проходит;
  - `readDockPrefs`: без `try`;
  - `authorOf`: без запасного `card.session`; порядок document/card наоборот;
  - `authorState`: без ветки оркестратора; `stopped` после `waiting`; `dead` как `stopped`;
  - `createCardDock`: терминал создаётся свёрнутым; не останавливается при сворачивании; пересоздаётся на каждом `show`; не пересоздаётся при смене автора; выбор «справа» стирается при узком листе; размер пишется на каждом движении;
  - `card.js`: `.card-dock` внутри перерисовываемой части; тело документа перезапрашивается снимком; ссылка на свой документ уходит в читалку;
  - `tabsOf`: битые ссылки становятся вкладками; порядок не по телу;
  - валидатор: проверка документов и для одного файла; `SESSION_RE` не применяется; незакрытый frontmatter молчит;
  - `DocSession`: без `LimitReader`; без проверки формата;
  - `cardSheetVerdicts`: граница 640 → 641; без проверки пересечения.

  Итог — две цифры (убито из запущенных; новых тестов, не убивших ни одного) и список выживших с объяснением; в описание PR и лог карточки.
- [ ] **Step 3: Прогоны:** `make test`, `make test-web`, `golangci-lint run ./...`, `GOOS=linux go build ./...` — зелёные.
- [ ] **Step 4:** коммит документации; `git push`; CI зелёный (ждать пассивно).
- [ ] **Step 5:** описание PR по шаблону `.github` (если есть): что и зачем, правило автора документа и подъём плагина до 0.5.0, как обновится копия валидатора на доске (`plugin/scripts/upgrade-board.sh`), стенд, мутации. Без атрибуции.
- [ ] **Step 6:** карточка → `review`, `progress: 80`; последняя строка лога — где PR и что нужно от оркестратора (посмотреть кадры, собрать dev-сборку для оператора). Одно письмо оркестратору с итогом.
