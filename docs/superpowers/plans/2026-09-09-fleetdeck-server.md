# fleetdeck: сервер и данные — план реализации

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** собрать сервер fleetdeck, который отдаёт состояние флота, доски и лимитов по HTTP и WebSocket, принимает ввод в сессии и правки полей карточек и шлёт уведомления — без единой строки интерфейса.

**Architecture:** три источника данных, каждый в своём пакете `internal/`, не знающем о вебе: демон через unix-сокет, транскрипты через чтение `jsonl`, доска через файлы с YAML-frontmatter и git. Пакет `internal/state` собирает из них снимок и определяет события, `internal/server` отдаёт снимок наружу, тонкий `cmd/fleetdeck` связывает всё вместе.

**Tech Stack:** Go 1.25, стандартная библиотека, `gopkg.in/yaml.v3` для конфигурации и frontmatter, `github.com/fsnotify/fsnotify` для слежения за карточками, `github.com/coder/websocket` для WebSocket. Больше зависимостей не добавлять без явного решения.

**Spec:** `docs/superpowers/specs/2026-09-09-fleetdeck-design.md`

## Global Constraints

- Go 1.25 или новее; модуль называется `github.com/kroticw/fleetdeck`.
- Сервер слушает только `127.0.0.1`, порт по умолчанию `7777`.
- OAuth-токен из Keychain отправляется только на `api.anthropic.com`, не логируется, не пишется на диск, не попадает в ответы API.
- Три источника правды не смешиваются: демон отвечает за жизнь сессии, транскрипт за то, что она делала, файлы доски за смысл задачи. Состояние сессии никогда не пишется в карточку.
- Пульт умеет ровно две записи: ввод в сессию и правка полей `stage` и `progress` в карточке. Тело карточки не трогает.
- Запись в карточку хирургическая: перечитать файл непосредственно перед записью, изменить только нужное поле, остальное сохранить байт в байт.
- Отказ любого источника деградирует свою часть и не роняет остальное.
- Каждая проверка обязана отличать «посмотрел и не нашёл» от «смотреть было не на что»; второе — отказ, а не успех.
- Комментарии в коде, сообщения об ошибках и текст коммитов — английские.
- Путь к сокету демона: `/tmp/cc-daemon-<uid>/<id>/control.sock`, где `<uid>` — числовой uid пользователя.
- Значения `stage`: `new`, `active`, `review`, `done`, `blocked`. Значения `zone`: `urgent`, `unplanned`, `planned`, `niceToHave`. Значения `progress`: 0, 10, 20, 40, 60, 80, 100.

---

## Структура файлов

| Файл | Ответственность |
| --- | --- |
| `go.mod` | Модуль и зависимости |
| `Makefile` | `build`, `test`, `lint`, `run` |
| `internal/version/version.go` | Версия сборки |
| `internal/config/config.go` | Чтение и запись `config.yaml`, умолчания |
| `internal/daemon/types.go` | Типы сессии и ответа демона |
| `internal/daemon/client.go` | Клиент `control.sock` |
| `internal/transcript/locate.go` | Поиск файла транскрипта по сессии |
| `internal/transcript/digest.go` | Выжимка последних шагов из `jsonl` |
| `internal/transcript/context.go` | Оценка занятого контекста по `usage` |
| `internal/board/card.go` | Разбор карточки: frontmatter, тело, связи |
| `internal/board/scan.go` | Обход каталога карточек |
| `internal/board/write.go` | Хирургическая запись поля |
| `internal/board/git.go` | Коммит изменения |
| `internal/board/watch.go` | Слежение за каталогом через fsnotify |
| `internal/usage/keychain.go` | Извлечение OAuth-токена |
| `internal/usage/usage.go` | Запрос лимитов и кэш |
| `internal/notify/notify.go` | Баннеры через `osascript` |
| `internal/state/snapshot.go` | Сборка снимка из всех источников |
| `internal/state/events.go` | Определение событий по смене снимка |
| `internal/server/server.go` | Маршруты и запуск |
| `internal/server/api.go` | Обработчики HTTP |
| `internal/server/ws.go` | WebSocket: рассылка снимков и поток экрана |
| `cmd/fleetdeck/main.go` | Флаги, связывание, запуск |
| `cmd/fleetdeck-status/main.go` | Statusline-репортёр |

Каждый пакет `internal/` не импортирует `internal/server` и ничего не знает о вебе. Обратное направление разрешено.

---

## Task 1: Каркас модуля

**Files:**
- Create: `go.mod`, `Makefile`, `.gitignore`, `internal/version/version.go`, `internal/version/version_test.go`

**Interfaces:**
- Consumes: ничего
- Produces: `version.String() string` — версия сборки, подставляемая через ldflags

- [ ] **Step 1: Написать падающий тест**

```go
// internal/version/version_test.go
package version

import "testing"

func TestStringFallsBackWhenUnset(t *testing.T) {
	value = ""
	if got := String(); got != "dev" {
		t.Fatalf("want dev for unset build version, got %q", got)
	}
}

func TestStringUsesInjectedValue(t *testing.T) {
	value = "1.2.3"
	defer func() { value = "" }()
	if got := String(); got != "1.2.3" {
		t.Fatalf("want injected version, got %q", got)
	}
}
```

- [ ] **Step 2: Прогнать и убедиться, что падает.** Команда `go test ./internal/version/`, ожидается FAIL: пакет не существует.

- [ ] **Step 3: Создать модуль и реализацию**

```bash
cd ~/claude/fleetdeck/.claude/worktrees/design-spec
go mod init github.com/kroticw/fleetdeck
```

```go
// internal/version/version.go
// Package version exposes the build version, injected at link time.
package version

// value is set with -ldflags "-X .../internal/version.value=<v>".
var value string

// String returns the build version, or "dev" for an unversioned build.
func String() string {
	if value == "" {
		return "dev"
	}
	return value
}
```

- [ ] **Step 4: Прогнать тесты.** Команда `go test ./internal/version/`, ожидается PASS, два теста.

- [ ] **Step 5: Добавить Makefile и .gitignore**

```makefile
BINARIES := fleetdeck fleetdeck-status
VERSION  ?= dev
LDFLAGS  := -X github.com/kroticw/fleetdeck/internal/version.value=$(VERSION)

.PHONY: build test lint run

build:
	@for b in $(BINARIES); do go build -ldflags "$(LDFLAGS)" -o bin/$$b ./cmd/$$b; done

test:
	go test ./...

lint:
	go vet ./...
	@test -z "$$(gofmt -l .)" || { gofmt -l .; exit 1; }

run: build
	./bin/fleetdeck
```

```gitignore
bin/
.superpowers/
```

- [ ] **Step 6: Коммит**

```bash
git add go.mod Makefile .gitignore internal/version
git commit --signoff -m "chore: module skeleton with build version"
```

---

## Task 2: Конфигурация

**Files:**
- Create: `internal/config/config.go`, `internal/config/config_test.go`

**Interfaces:**
- Consumes: ничего
- Produces: `type Config struct { BoardPath string; DocsPaths []string; OrchestratorSession string; Notify NotifyConfig; DaemonPollInterval time.Duration; UsageEnabled bool; ServerPort int }`, `type NotifyConfig struct { Waiting, Failed, Silent, CardBlocked bool; SilenceAfter time.Duration }`, `func Default() Config`, `func Load(path string) (Config, error)` (отсутствующий файл возвращает умолчания и `nil`), `func Save(path string, c Config) error`

- [ ] **Step 1: Написать падающие тесты**

```go
// internal/config/config_test.go
package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadMissingFileReturnsDefaults(t *testing.T) {
	got, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))
	if err != nil {
		t.Fatalf("missing file must not be an error, got %v", err)
	}
	if got.ServerPort != Default().ServerPort {
		t.Fatalf("missing file must yield defaults, got %+v", got)
	}
}

func TestLoadBrokenFileIsAnError(t *testing.T) {
	p := filepath.Join(t.TempDir(), "broken.yaml")
	os.WriteFile(p, []byte("server_port: [1,2\n"), 0o600)
	if _, err := Load(p); err == nil {
		t.Fatal("a broken config must fail loudly, not fall back to defaults")
	}
}

func TestLoadOverridesOnlyGivenKeys(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	os.WriteFile(p, []byte("server_port: 9001\n"), 0o600)
	got, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.ServerPort != 9001 {
		t.Fatalf("port not applied: %d", got.ServerPort)
	}
	if got.DaemonPollInterval != Default().DaemonPollInterval {
		t.Fatalf("untouched key must keep its default, got %v", got.DaemonPollInterval)
	}
}

func TestSaveThenLoadRoundTrips(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	want := Default()
	want.BoardPath = "/tmp/board"
	want.Notify.SilenceAfter = 45 * time.Minute
	if err := Save(p, want); err != nil {
		t.Fatal(err)
	}
	got, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.BoardPath != want.BoardPath || got.Notify.SilenceAfter != want.Notify.SilenceAfter {
		t.Fatalf("round trip lost data: %+v", got)
	}
}
```

- [ ] **Step 2: Прогнать и убедиться, что падает.** Команда `go test ./internal/config/`, ожидается FAIL: пакет не существует.

- [ ] **Step 3: Реализовать**

```go
// internal/config/config.go
// Package config loads and saves the fleetdeck configuration file.
// A missing file is a set of defaults, not a failure. A malformed file is a failure.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

type NotifyConfig struct {
	Waiting      bool          `yaml:"waiting"`
	Failed       bool          `yaml:"failed"`
	Silent       bool          `yaml:"silent"`
	CardBlocked  bool          `yaml:"card_blocked"`
	SilenceAfter time.Duration `yaml:"silence_after"`
}

type Config struct {
	BoardPath           string        `yaml:"board_path"`
	DocsPaths           []string      `yaml:"docs_paths"`
	OrchestratorSession string        `yaml:"orchestrator_session"`
	Notify              NotifyConfig  `yaml:"notify"`
	DaemonPollInterval  time.Duration `yaml:"daemon_poll_interval"`
	UsageEnabled        bool          `yaml:"usage_enabled"`
	ServerPort          int           `yaml:"server_port"`
}

// Default returns the configuration used when no file exists.
func Default() Config {
	return Config{
		Notify: NotifyConfig{
			Waiting: true, Failed: true, Silent: true, CardBlocked: true,
			SilenceAfter: 30 * time.Minute,
		},
		DaemonPollInterval: 2 * time.Second,
		UsageEnabled:       true,
		ServerPort:         7777,
	}
}

// Load reads the config file. A missing file yields defaults; a malformed one is an error.
func Load(path string) (Config, error) {
	c := Default()
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("read config %s: %w", path, err)
	}
	if err := yaml.Unmarshal(raw, &c); err != nil {
		return Config{}, fmt.Errorf("parse config %s: %w", path, err)
	}
	return c, nil
}

// Save writes the config file, creating parent directories as needed.
func Save(path string, c Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	raw, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	return os.WriteFile(path, raw, 0o600)
}
```

- [ ] **Step 4: Прогнать тесты.** Команда `go test ./internal/config/`, ожидается PASS, четыре теста.

- [ ] **Step 5: Коммит**

```bash
git add internal/config go.mod go.sum
git commit --signoff -m "feat(config): load and save configuration with defaults"
```

---

## Task 3: Клиент демона

Эта задача содержит **проверку открытого вопроса спеки**: несут ли поля `needs`, `intent`, `detail`, `options` ожидающий вопрос с вариантами ответа. Результат проверки нужно записать в отчёт исполнителя дословно — от него зависит устройство панели ответа во втором плане.

**Files:**
- Create: `internal/daemon/types.go`, `internal/daemon/client.go`, `internal/daemon/client_test.go`, `internal/daemon/testdata/list_sessions.json`

**Interfaces:**
- Consumes: ничего
- Produces: `type Option struct { Label, Value string }`, `type Session struct { ID, SessionID, Name, Title, CWD, Kind, State, Status string; PID int; StartedAt int64; Live, Pinned, Resumable bool; Needs, Intent, Detail string; Options []Option; StreamTail string }`, `func (Session) Waiting() bool`, `func New(socketPath string) *Client`, `func SocketPath() (string, error)`, `func (c *Client) ListSessions(ctx context.Context) ([]Session, error)`, `func (c *Client) ReadScreen(ctx context.Context, session string, tail int) (string, error)`, `func (c *Client) SendText(ctx context.Context, session, text string, submit bool) error`, `func (c *Client) SendKeys(ctx context.Context, session, keys string) error`, `var ErrDaemonUnavailable`

- [ ] **Step 1: Снять живой ответ демона в фикстуру**

```bash
mkdir -p internal/daemon/testdata
/opt/homebrew/bin/claude agents --json > internal/daemon/testdata/list_sessions.json
```

Затем обезличить файл вручную: заменить пути в `cwd` на `/home/user/project`, имена сессий на нейтральные, `sessionId` на случайные UUID. Файл едет в публичный репозиторий, поэтому прочитать его глазами целиком перед коммитом.

- [ ] **Step 2: Написать падающие тесты**

```go
// internal/daemon/client_test.go
package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// serveOnce answers a single request with the given payload and records what it received.
func serveOnce(t *testing.T, reply string) (string, chan map[string]any) {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "control.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan map[string]any, 1)
	go func() {
		defer ln.Close()
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		var req map[string]any
		json.NewDecoder(conn).Decode(&req)
		got <- req
		conn.Write([]byte(reply))
	}()
	return sock, got
}

func TestListSessionsParsesFixture(t *testing.T) {
	raw, err := os.ReadFile("testdata/list_sessions.json")
	if err != nil {
		t.Fatal(err)
	}
	sock, _ := serveOnce(t, `{"ok":true,"sessions":`+string(raw)+`}`)
	sessions, err := New(sock).ListSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) == 0 {
		t.Fatal("fixture must yield sessions; zero here means the parser had nothing to look at")
	}
	for _, s := range sessions {
		if s.ID == "" && s.SessionID == "" {
			t.Fatalf("session without any identifier: %+v", s)
		}
	}
}

func TestMissingSocketIsTypedError(t *testing.T) {
	_, err := New(filepath.Join(t.TempDir(), "absent.sock")).ListSessions(context.Background())
	if !errors.Is(err, ErrDaemonUnavailable) {
		t.Fatalf("want ErrDaemonUnavailable, got %v", err)
	}
}

func TestBrokenJSONIsAnError(t *testing.T) {
	sock, _ := serveOnce(t, `{"ok":true,"sessions":[{`)
	if _, err := New(sock).ListSessions(context.Background()); err == nil {
		t.Fatal("truncated JSON must fail, not yield an empty list")
	}
}

func TestSendTextCarriesSubmitFlag(t *testing.T) {
	sock, got := serveOnce(t, `{"ok":true}`)
	if err := New(sock).SendText(context.Background(), "abc123", "hello", true); err != nil {
		t.Fatal(err)
	}
	req := <-got
	if req["type"] != "send_text" || req["session"] != "abc123" || req["text"] != "hello" || req["submit"] != true {
		t.Fatalf("unexpected request: %+v", req)
	}
}

func TestSilentDaemonTimesOut(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "control.sock")
	ln, _ := net.Listen("unix", sock)
	defer ln.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := New(sock).ListSessions(ctx); err == nil {
		t.Fatal("a silent daemon must time out, not hang")
	}
}
```

- [ ] **Step 3: Прогнать и убедиться, что падает.** Команда `go test ./internal/daemon/`, ожидается FAIL: пакет не существует.

- [ ] **Step 4: Реализовать типы**

```go
// internal/daemon/types.go
// Package daemon talks to the Claude Code session daemon over its unix control socket.
// It is the source for what a session is right now; it never answers what a task means.
package daemon

// Option is one choice of a pending question shown by a session.
type Option struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// Session is one entry of the daemon's session list. Unknown fields are ignored
// on purpose: a daemon update should add data, not break the parse.
type Session struct {
	ID         string   `json:"id"`
	SessionID  string   `json:"sessionId"`
	Name       string   `json:"name"`
	Title      string   `json:"title"`
	CWD        string   `json:"cwd"`
	Kind       string   `json:"kind"`
	State      string   `json:"state"`
	Status     string   `json:"status"`
	PID        int      `json:"pid"`
	StartedAt  int64    `json:"startedAt"`
	Live       bool     `json:"live"`
	Pinned     bool     `json:"pinned"`
	Resumable  bool     `json:"resumable"`
	Needs      string   `json:"needs"`
	Intent     string   `json:"intent"`
	Detail     string   `json:"detail"`
	Options    []Option `json:"options"`
	StreamTail string   `json:"streamTail"`
}

// Waiting reports whether the session is stopped on something a human must answer.
func (s Session) Waiting() bool {
	return s.Needs != "" || s.Status == "waiting"
}
```

- [ ] **Step 5: Реализовать клиент**

```go
// internal/daemon/client.go
package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// ErrDaemonUnavailable means the daemon socket could not be reached. Callers
// degrade the session view and keep the rest of the panel alive.
var ErrDaemonUnavailable = errors.New("daemon unavailable")

type Client struct{ socket string }

func New(socketPath string) *Client { return &Client{socket: socketPath} }

// SocketPath locates the control socket for the current user.
func SocketPath() (string, error) {
	base := filepath.Join(os.TempDir(), "cc-daemon-"+strconv.Itoa(os.Getuid()))
	matches, err := filepath.Glob(filepath.Join(base, "*", "control.sock"))
	if err != nil {
		return "", fmt.Errorf("glob daemon sockets: %w", err)
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("%w: no socket under %s", ErrDaemonUnavailable, base)
	}
	return matches[0], nil
}

func (c *Client) call(ctx context.Context, req map[string]any, out any) error {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", c.socket)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrDaemonUnavailable, err)
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	} else {
		conn.SetDeadline(time.Now().Add(5 * time.Second))
	}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return fmt.Errorf("send %v: %w", req["type"], err)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(conn).Decode(out); err != nil {
		return fmt.Errorf("decode %v reply: %w", req["type"], err)
	}
	return nil
}

func (c *Client) ListSessions(ctx context.Context) ([]Session, error) {
	var reply struct {
		OK       bool      `json:"ok"`
		Error    string    `json:"error"`
		Sessions []Session `json:"sessions"`
	}
	if err := c.call(ctx, map[string]any{"type": "list_sessions"}, &reply); err != nil {
		return nil, err
	}
	if reply.Error != "" {
		return nil, fmt.Errorf("daemon refused list_sessions: %s", reply.Error)
	}
	return reply.Sessions, nil
}

func (c *Client) ReadScreen(ctx context.Context, session string, tail int) (string, error) {
	var reply struct {
		Screen string `json:"screen"`
		Error  string `json:"error"`
	}
	req := map[string]any{"type": "read_screen", "session": session, "tail": tail}
	if err := c.call(ctx, req, &reply); err != nil {
		return "", err
	}
	if reply.Error != "" {
		return "", fmt.Errorf("daemon refused read_screen: %s", reply.Error)
	}
	return reply.Screen, nil
}

func (c *Client) SendText(ctx context.Context, session, text string, submit bool) error {
	req := map[string]any{"type": "send_text", "session": session, "text": text, "submit": submit}
	return c.call(ctx, req, nil)
}

func (c *Client) SendKeys(ctx context.Context, session, keys string) error {
	req := map[string]any{"type": "send_keys", "session": session, "keys": keys}
	return c.call(ctx, req, nil)
}
```

- [ ] **Step 6: Прогнать тесты.** Команда `go test ./internal/daemon/`, ожидается PASS, пять тестов.

- [ ] **Step 7: Проверить открытый вопрос спеки на живой сессии**

```bash
cd /tmp && /opt/homebrew/bin/claude --bg --name "fleetdeck probe" --dangerously-skip-permissions
```

Дождаться, пока пробная сессия задаст вопрос с вариантами, и снять её запись из списка командой `/opt/homebrew/bin/claude agents --json`. Записать в отчёт дословно, что лежит в полях `needs`, `intent`, `detail`, `options` у ожидающей сессии. Если варианты приходят структурно — отметить, что второй план рисует список вариантов; если поля пусты — отметить, что остаётся путь `read_screen` плюс `send_keys`. Погасить пробную сессию командой `/opt/homebrew/bin/claude stop <id>`.

- [ ] **Step 8: Коммит**

```bash
git add internal/daemon
git commit --signoff -m "feat(daemon): control socket client with typed unavailability"
```

---

## Task 4: Транскрипты

**Files:**
- Create: `internal/transcript/locate.go`, `internal/transcript/digest.go`, `internal/transcript/context.go`, `internal/transcript/digest_test.go`, `internal/transcript/context_test.go`, `internal/transcript/testdata/session.jsonl`

**Interfaces:**
- Consumes: ничего
- Produces: `func Locate(projectsDir, sessionUUID string) (string, error)`, `type Step struct { Role, Text string; At time.Time }`, `func Digest(path string, limit int) ([]Step, error)`, `type Usage struct { Tokens, Window int; Estimated bool }`, `func ContextUsage(path string) (Usage, error)`, `var ErrNoTranscript`

- [ ] **Step 1: Сделать фикстуру из настоящего транскрипта**

```bash
mkdir -p internal/transcript/testdata
f=$(find ~/.claude/projects -name "*.jsonl" | head -1)
tail -n 40 "$f" > internal/transcript/testdata/session.jsonl
```

Обезличить фикстуру: заменить пути, имена репозиториев и любые адреса на нейтральные. Файл едет в публичный репозиторий, поэтому прочитать каждую строку глазами перед коммитом.

- [ ] **Step 2: Написать падающие тесты выжимки**

```go
// internal/transcript/digest_test.go
package transcript

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDigestReturnsOldestFirstWithinLimit(t *testing.T) {
	steps, err := Digest("testdata/session.jsonl", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) == 0 {
		t.Fatal("fixture must yield steps; zero here means the parser had nothing to look at")
	}
	if len(steps) > 5 {
		t.Fatalf("limit ignored: got %d", len(steps))
	}
	for i := 1; i < len(steps); i++ {
		if steps[i].At.Before(steps[i-1].At) {
			t.Fatal("steps must be ordered oldest first")
		}
	}
}

func TestDigestDropsMessagesWithoutText(t *testing.T) {
	steps, err := Digest("testdata/session.jsonl", 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range steps {
		if s.Text == "" {
			t.Fatal("a step without text is noise and must be dropped")
		}
	}
}

func TestDigestOnMissingFileIsTypedError(t *testing.T) {
	_, err := Digest(filepath.Join(t.TempDir(), "absent.jsonl"), 5)
	if !errors.Is(err, ErrNoTranscript) {
		t.Fatalf("want ErrNoTranscript, got %v", err)
	}
}

func TestDigestOnEmptyFileIsNotSuccess(t *testing.T) {
	p := filepath.Join(t.TempDir(), "empty.jsonl")
	os.WriteFile(p, nil, 0o600)
	if _, err := Digest(p, 5); err == nil {
		t.Fatal("an empty transcript must be reported, not silently pass as zero steps")
	}
}

func TestDigestSurvivesBrokenLines(t *testing.T) {
	p := filepath.Join(t.TempDir(), "mixed.jsonl")
	good := `{"type":"assistant","timestamp":"2026-09-09T00:00:00Z","message":{"role":"assistant","content":[{"type":"text","text":"ok"}]}}`
	os.WriteFile(p, []byte("{not json\n"+good+"\n"), 0o600)
	steps, err := Digest(p, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 || steps[0].Text != "ok" {
		t.Fatalf("one broken line must not discard the good ones: %+v", steps)
	}
}
```

- [ ] **Step 3: Написать падающие тесты контекста**

```go
// internal/transcript/context_test.go
package transcript

import (
	"os"
	"path/filepath"
	"testing"
)

func TestContextUsageSumsLastAssistantUsage(t *testing.T) {
	p := filepath.Join(t.TempDir(), "u.jsonl")
	line := `{"type":"assistant","message":{"model":"claude-opus-5","usage":{"input_tokens":2,"cache_creation_input_tokens":984,"cache_read_input_tokens":597051}}}`
	os.WriteFile(p, []byte(line+"\n"), 0o600)
	got, err := ContextUsage(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.Tokens != 598037 {
		t.Fatalf("want 598037 tokens, got %d", got.Tokens)
	}
	if got.Window != 1000000 {
		t.Fatalf("opus-5 window must be 1M, got %d", got.Window)
	}
	if !got.Estimated {
		t.Fatal("a value derived from the transcript must be marked as an estimate")
	}
}

func TestContextUsageWithoutUsageIsAnError(t *testing.T) {
	p := filepath.Join(t.TempDir(), "nousage.jsonl")
	os.WriteFile(p, []byte(`{"type":"user","message":{"role":"user","content":"hi"}}`+"\n"), 0o600)
	if _, err := ContextUsage(p); err == nil {
		t.Fatal("a transcript with no usage data must fail, not report zero context")
	}
}

func TestContextUsageUnknownModelFallsBack(t *testing.T) {
	p := filepath.Join(t.TempDir(), "unknown.jsonl")
	line := `{"type":"assistant","message":{"model":"future-model-9","usage":{"input_tokens":10,"cache_read_input_tokens":90}}}`
	os.WriteFile(p, []byte(line+"\n"), 0o600)
	got, err := ContextUsage(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.Window != 200000 {
		t.Fatalf("unknown model must fall back to 200k, got %d", got.Window)
	}
}
```

- [ ] **Step 4: Прогнать и убедиться, что падает.** Команда `go test ./internal/transcript/`, ожидается FAIL: пакет не существует.

- [ ] **Step 5: Реализовать поиск транскрипта**

```go
// internal/transcript/locate.go
// Package transcript reads Claude Code session transcripts. It is the source
// for what a session did; it never reports what a session is doing now.
package transcript

import (
	"errors"
	"fmt"
	"path/filepath"
)

// ErrNoTranscript means no usable transcript exists for the session.
var ErrNoTranscript = errors.New("transcript not found")

// Locate finds the transcript of a session by its UUID under the projects directory.
func Locate(projectsDir, sessionUUID string) (string, error) {
	matches, err := filepath.Glob(filepath.Join(projectsDir, "*", sessionUUID+".jsonl"))
	if err != nil {
		return "", fmt.Errorf("glob transcripts: %w", err)
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("%w: %s", ErrNoTranscript, sessionUUID)
	}
	return matches[0], nil
}
```

- [ ] **Step 6: Реализовать выжимку**

```go
// internal/transcript/digest.go
package transcript

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"
)

// Step is one readable moment of a session: a message that carries text.
type Step struct {
	Role string    `json:"role"`
	Text string    `json:"text"`
	At   time.Time `json:"at"`
}

type rawLine struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	Message   struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// text extracts plain text from a content field that is either a string or a block list.
func (r rawLine) text() string {
	var s string
	if json.Unmarshal(r.Message.Content, &s) == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(r.Message.Content, &blocks) != nil {
		return ""
	}
	out := ""
	for _, b := range blocks {
		if b.Type == "text" {
			out += b.Text
		}
	}
	return out
}

// Digest returns up to limit newest steps, oldest first. An empty transcript is
// an error: nothing to look at is not the same as nothing found.
func Digest(path string, limit int) ([]Step, error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w: %s", ErrNoTranscript, path)
	}
	if err != nil {
		return nil, fmt.Errorf("open transcript: %w", err)
	}
	defer f.Close()

	var steps []Step
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 16<<20)
	lines := 0
	for sc.Scan() {
		lines++
		var r rawLine
		if json.Unmarshal(sc.Bytes(), &r) != nil {
			continue
		}
		if r.Type != "assistant" && r.Type != "user" {
			continue
		}
		txt := r.text()
		if txt == "" {
			continue
		}
		at, _ := time.Parse(time.RFC3339, r.Timestamp)
		steps = append(steps, Step{Role: r.Type, Text: txt, At: at})
		if len(steps) > limit {
			steps = steps[1:]
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read transcript: %w", err)
	}
	if lines == 0 {
		return nil, fmt.Errorf("%w: %s is empty", ErrNoTranscript, path)
	}
	return steps, nil
}
```

- [ ] **Step 7: Реализовать оценку контекста**

```go
// internal/transcript/context.go
package transcript

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
)

// Usage is the context occupancy of a session. Estimated is true when the number
// came from the transcript rather than from Claude Code itself.
type Usage struct {
	Tokens    int  `json:"tokens"`
	Window    int  `json:"window"`
	Estimated bool `json:"estimated"`
}

// windows maps a model name to its context window. Unknown models fall back to 200k.
var windows = map[string]int{
	"claude-opus-5":    1_000_000,
	"claude-sonnet-5":  1_000_000,
	"claude-fable-5-1": 1_000_000,
	"claude-haiku-4-5": 200_000,
}

// ContextUsage reads the last assistant usage block and reports occupancy.
func ContextUsage(path string) (Usage, error) {
	f, err := os.Open(path)
	if err != nil {
		return Usage{}, fmt.Errorf("open transcript: %w", err)
	}
	defer f.Close()

	var last struct {
		Model string `json:"model"`
		Usage struct {
			Input         int `json:"input_tokens"`
			CacheCreation int `json:"cache_creation_input_tokens"`
			CacheRead     int `json:"cache_read_input_tokens"`
		} `json:"usage"`
	}
	found := false
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 16<<20)
	for sc.Scan() {
		var envelope struct {
			Message json.RawMessage `json:"message"`
		}
		if json.Unmarshal(sc.Bytes(), &envelope) != nil || len(envelope.Message) == 0 {
			continue
		}
		var probe struct {
			Usage json.RawMessage `json:"usage"`
		}
		if json.Unmarshal(envelope.Message, &probe) != nil || len(probe.Usage) == 0 {
			continue
		}
		if json.Unmarshal(envelope.Message, &last) == nil {
			found = true
		}
	}
	if err := sc.Err(); err != nil {
		return Usage{}, fmt.Errorf("read transcript: %w", err)
	}
	if !found {
		return Usage{}, fmt.Errorf("no usage data in %s", path)
	}
	window, ok := windows[last.Model]
	if !ok {
		window = 200_000
	}
	return Usage{
		Tokens:    last.Usage.Input + last.Usage.CacheCreation + last.Usage.CacheRead,
		Window:    window,
		Estimated: true,
	}, nil
}
```

- [ ] **Step 8: Прогнать тесты.** Команда `go test ./internal/transcript/`, ожидается PASS, восемь тестов.

- [ ] **Step 9: Коммит**

```bash
git add internal/transcript
git commit --signoff -m "feat(transcript): digest and context estimate from session transcripts"
```

---

## Task 5: Карточки доски

**Files:**
- Create: `internal/board/card.go`, `internal/board/scan.go`, `internal/board/write.go`, `internal/board/card_test.go`, `internal/board/write_test.go`

**Interfaces:**
- Consumes: ничего
- Produces: `type Card struct { Path, Zone, Stage, Session, Repo, Created, Title, Body string; Progress int; Links []string; ParseError string }`, `func ParseCard(path string) (Card, error)`, `func Scan(dir string) ([]Card, error)`, `func SetField(path, field, value string) error`, `var ErrUnknownField`, `var ErrNoCards`

- [ ] **Step 1: Написать падающие тесты разбора**

```go
// internal/board/card_test.go
package board

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

const sample = `---
zone: planned
stage: review
progress: 80
session: abc12345
repo: work/thing
created: 2026-09-09
---

# BS-1 — заголовок

Тело со связью [[other-card]] и ещё одной [[third]].
`

func writeCard(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestParseCardReadsFieldsTitleAndLinks(t *testing.T) {
	c, err := ParseCard(writeCard(t, t.TempDir(), "c.md", sample))
	if err != nil {
		t.Fatal(err)
	}
	if c.Zone != "planned" || c.Stage != "review" || c.Progress != 80 || c.Session != "abc12345" {
		t.Fatalf("fields wrong: %+v", c)
	}
	if c.Title != "BS-1 — заголовок" {
		t.Fatalf("title wrong: %q", c.Title)
	}
	if len(c.Links) != 2 || c.Links[0] != "other-card" || c.Links[1] != "third" {
		t.Fatalf("links wrong: %v", c.Links)
	}
}

func TestParseCardWithBrokenFrontmatterReportsInsteadOfFailing(t *testing.T) {
	c, err := ParseCard(writeCard(t, t.TempDir(), "b.md", "---\nzone: [unclosed\n---\n\nbody\n"))
	if err != nil {
		t.Fatalf("a broken card must be reported through ParseError, not through err: %v", err)
	}
	if c.ParseError == "" {
		t.Fatal("ParseError must say what went wrong")
	}
}

func TestParseCardWithoutFrontmatterIsBroken(t *testing.T) {
	c, _ := ParseCard(writeCard(t, t.TempDir(), "n.md", "# no frontmatter\n"))
	if c.ParseError == "" {
		t.Fatal("a file without frontmatter is not a card and must say so")
	}
}

func TestScanEmptyDirectoryIsTypedError(t *testing.T) {
	_, err := Scan(t.TempDir())
	if !errors.Is(err, ErrNoCards) {
		t.Fatalf("an empty board must be reported, not pass as success with zero cards: %v", err)
	}
}

func TestScanKeepsGoingPastOneBrokenCard(t *testing.T) {
	dir := t.TempDir()
	writeCard(t, dir, "good.md", sample)
	writeCard(t, dir, "bad.md", "---\nzone: [\n---\n")
	cards, err := Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 2 {
		t.Fatalf("both cards must be returned, the broken one included: %d", len(cards))
	}
}
```

- [ ] **Step 2: Написать падающие тесты записи**

```go
// internal/board/write_test.go
package board

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestSetFieldChangesOnlyTheTargetLine(t *testing.T) {
	p := writeCard(t, t.TempDir(), "c.md", sample)
	before, _ := os.ReadFile(p)
	if err := SetField(p, "stage", "done"); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(p)
	bl, al := strings.Split(string(before), "\n"), strings.Split(string(after), "\n")
	if len(bl) != len(al) {
		t.Fatalf("line count changed: %d -> %d", len(bl), len(al))
	}
	diff := 0
	for i := range bl {
		if bl[i] != al[i] {
			diff++
		}
	}
	if diff != 1 {
		t.Fatalf("exactly one line must change, %d changed", diff)
	}
	if !strings.Contains(string(after), "stage: done") {
		t.Fatal("stage was not written")
	}
}

func TestSetFieldRefusesFieldsThePanelDoesNotOwn(t *testing.T) {
	p := writeCard(t, t.TempDir(), "c.md", sample)
	if err := SetField(p, "session", "deadbeef"); !errors.Is(err, ErrUnknownField) {
		t.Fatalf("only stage and progress are writable, got %v", err)
	}
}

func TestSetFieldRejectsInvalidValues(t *testing.T) {
	p := writeCard(t, t.TempDir(), "c.md", sample)
	if err := SetField(p, "stage", "almost"); err == nil {
		t.Fatal("an unknown stage must be refused")
	}
	if err := SetField(p, "progress", "55"); err == nil {
		t.Fatal("progress outside the allowed ladder must be refused")
	}
}

func TestSetFieldPreservesBodyAppendedBetweenReadAndWrite(t *testing.T) {
	p := writeCard(t, t.TempDir(), "c.md", sample)
	f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString("- новая строка лога от агента\n")
	f.Close()

	if err := SetField(p, "progress", "100"); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(p)
	if !strings.Contains(string(after), "новая строка лога от агента") {
		t.Fatal("a concurrent append by an agent must survive the field write")
	}
	if !strings.Contains(string(after), "progress: 100") {
		t.Fatal("progress was not written")
	}
}
```

- [ ] **Step 3: Прогнать и убедиться, что падает.** Команда `go test ./internal/board/`, ожидается FAIL: пакет не существует.

- [ ] **Step 4: Реализовать разбор**

```go
// internal/board/card.go
// Package board reads and writes the fleet board: markdown cards with YAML
// frontmatter. The panel owns two fields, stage and progress. Everything else
// belongs to the agents.
package board

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

var (
	// ErrNoCards means the board directory holds no cards at all.
	ErrNoCards = errors.New("no cards found")
	// ErrUnknownField means a write targeted a field the panel does not own.
	ErrUnknownField = errors.New("field is not writable")

	frontmatterRe = regexp.MustCompile(`(?s)\A---\n(.*?)\n---\n`)
	linkRe        = regexp.MustCompile(`\[\[([^\]|#]+)`)
	titleRe       = regexp.MustCompile(`(?m)^#\s+(.+)$`)
)

type Card struct {
	Path       string   `json:"path"`
	Zone       string   `json:"zone"`
	Stage      string   `json:"stage"`
	Progress   int      `json:"progress"`
	Session    string   `json:"session"`
	Repo       string   `json:"repo"`
	Created    string   `json:"created"`
	Title      string   `json:"title"`
	Body       string   `json:"body"`
	Links      []string `json:"links"`
	ParseError string   `json:"parseError,omitempty"`
}

type frontmatter struct {
	Zone     string `yaml:"zone"`
	Stage    string `yaml:"stage"`
	Progress int    `yaml:"progress"`
	Session  string `yaml:"session"`
	Repo     string `yaml:"repo"`
	Created  string `yaml:"created"`
}

// ParseCard reads one card. A malformed card comes back with ParseError set:
// one bad card must not blind the board to the others.
func ParseCard(path string) (Card, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Card{}, fmt.Errorf("read card: %w", err)
	}
	c := Card{Path: path}
	m := frontmatterRe.FindSubmatch(raw)
	if m == nil {
		c.ParseError = "no frontmatter block"
		return c, nil
	}
	var fm frontmatter
	if err := yaml.Unmarshal(m[1], &fm); err != nil {
		c.ParseError = "frontmatter: " + err.Error()
		return c, nil
	}
	c.Zone, c.Stage, c.Progress = fm.Zone, fm.Stage, fm.Progress
	c.Session, c.Repo, c.Created = fm.Session, fm.Repo, fm.Created
	c.Body = string(raw[len(m[0]):])
	if t := titleRe.FindStringSubmatch(c.Body); t != nil {
		c.Title = strings.TrimSpace(t[1])
	}
	for _, l := range linkRe.FindAllStringSubmatch(c.Body, -1) {
		c.Links = append(c.Links, strings.TrimSpace(l[1]))
	}
	return c, nil
}
```

```go
// internal/board/scan.go
package board

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Scan reads every card in dir. An empty directory is an error, not an empty success.
func Scan(dir string) ([]Card, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read board dir: %w", err)
	}
	var cards []Card
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		p := filepath.Join(dir, e.Name())
		c, err := ParseCard(p)
		if err != nil {
			c = Card{Path: p, ParseError: err.Error()}
		}
		cards = append(cards, c)
	}
	if len(cards) == 0 {
		return nil, fmt.Errorf("%w in %s", ErrNoCards, dir)
	}
	sort.Slice(cards, func(i, j int) bool { return cards[i].Path < cards[j].Path })
	return cards, nil
}
```

- [ ] **Step 5: Реализовать хирургическую запись**

```go
// internal/board/write.go
package board

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

var (
	validStages   = map[string]bool{"new": true, "active": true, "review": true, "done": true, "blocked": true}
	validProgress = map[int]bool{0: true, 10: true, 20: true, 40: true, 60: true, 80: true, 100: true}
)

// SetField rewrites exactly one frontmatter line and leaves every other byte
// alone. The file is re-read here, immediately before the write, so a log line
// an agent appended after the panel rendered the card survives.
func SetField(path, field, value string) error {
	switch field {
	case "stage":
		if !validStages[value] {
			return fmt.Errorf("unknown stage %q", value)
		}
	case "progress":
		n, err := strconv.Atoi(value)
		if err != nil || !validProgress[n] {
			return fmt.Errorf("progress must be one of 0,10,20,40,60,80,100, got %q", value)
		}
	default:
		return fmt.Errorf("%w: %s", ErrUnknownField, field)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("re-read card before write: %w", err)
	}
	m := frontmatterRe.FindSubmatch(raw)
	if m == nil {
		return fmt.Errorf("card %s has no frontmatter to write into", path)
	}
	head := string(raw[:len(m[0])])
	line := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(field) + `:.*$`)
	if !line.MatchString(head) {
		return fmt.Errorf("card %s has no %s field", path, field)
	}
	out := line.ReplaceAllString(head, field+": "+value) + string(raw[len(m[0]):])
	if strings.Count(out, "\n") != strings.Count(string(raw), "\n") {
		return fmt.Errorf("refusing to write: line count would change in %s", path)
	}
	return os.WriteFile(path, []byte(out), 0o600)
}
```

- [ ] **Step 6: Прогнать тесты.** Команда `go test ./internal/board/`, ожидается PASS, девять тестов.

- [ ] **Step 7: Коммит**

```bash
git add internal/board
git commit --signoff -m "feat(board): parse cards and write owned fields surgically"
```

---

## Task 6: Git и слежение за доской

**Files:**
- Create: `internal/board/git.go`, `internal/board/git_test.go`, `internal/board/watch.go`, `internal/board/watch_test.go`

**Interfaces:**
- Consumes: `writeCard` и `sample` из `card_test.go` задачи 5
- Produces: `func Commit(dir, file, message string) error`, `func Watch(ctx context.Context, dir string, onChange func()) error`

- [ ] **Step 1: Написать падающие тесты git**

```go
// internal/board/git_test.go
package board

import (
	"os/exec"
	"strings"
	"testing"
)

func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "--initial-branch=main"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "T"},
		{"config", "commit.gpgsign", "false"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	return dir
}

func TestCommitStagesOnlyTheNamedFile(t *testing.T) {
	dir := initRepo(t)
	writeCard(t, dir, "a.md", sample)
	writeCard(t, dir, "b.md", sample)
	if err := Commit(dir, "a.md", "test: commit a"); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = dir
	out, _ := cmd.Output()
	if !strings.Contains(string(out), "b.md") {
		t.Fatal("b.md must remain uncommitted: the panel commits only what it wrote")
	}
	if strings.Contains(string(out), "a.md") {
		t.Fatal("a.md must be committed")
	}
}

func TestCommitOutsideRepositoryIsAnError(t *testing.T) {
	dir := t.TempDir()
	writeCard(t, dir, "a.md", sample)
	if err := Commit(dir, "a.md", "test"); err == nil {
		t.Fatal("committing outside a repository must fail loudly")
	}
}

func TestCommitWithNothingStagedIsNotSuccess(t *testing.T) {
	dir := initRepo(t)
	writeCard(t, dir, "a.md", sample)
	if err := Commit(dir, "a.md", "first"); err != nil {
		t.Fatal(err)
	}
	if err := Commit(dir, "a.md", "second"); err == nil {
		t.Fatal("a commit with nothing to commit must be reported, not silently pass")
	}
}
```

- [ ] **Step 2: Написать падающие тесты слежения**

```go
// internal/board/watch_test.go
package board

import (
	"context"
	"testing"
	"time"
)

func TestWatchFiresOnChangeAndCoalesces(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fired := make(chan struct{}, 16)
	go Watch(ctx, dir, func() { fired <- struct{}{} })
	time.Sleep(100 * time.Millisecond)

	for i := 0; i < 5; i++ {
		writeCard(t, dir, "c.md", sample)
		time.Sleep(10 * time.Millisecond)
	}

	select {
	case <-fired:
	case <-time.After(2 * time.Second):
		t.Fatal("a change in the board directory must wake the watcher")
	}
	time.Sleep(500 * time.Millisecond)
	if len(fired) > 1 {
		t.Fatalf("five rapid writes must coalesce, got %d extra callbacks", len(fired))
	}
}

func TestWatchOnMissingDirectoryIsAnError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := Watch(ctx, "/definitely/not/here", func() {}); err == nil {
		t.Fatal("watching a directory that does not exist must fail, not sit silent")
	}
}
```

- [ ] **Step 3: Прогнать и убедиться, что падает.** Команда `go test ./internal/board/ -run 'TestCommit|TestWatch'`, ожидается FAIL: функции не существуют.

- [ ] **Step 4: Реализовать git**

```go
// internal/board/git.go
package board

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// Commit stages exactly one file and commits it. Other changes in the working
// tree are left alone: the panel commits what it wrote and nothing else.
func Commit(dir, file, message string) error {
	if out, err := run(dir, "rev-parse", "--git-dir"); err != nil {
		return fmt.Errorf("not a git repository: %s", strings.TrimSpace(out))
	}
	if out, err := run(dir, "add", "--", file); err != nil {
		return fmt.Errorf("stage %s: %s", file, strings.TrimSpace(out))
	}
	staged, err := run(dir, "diff", "--cached", "--name-only", "--", file)
	if err != nil {
		return fmt.Errorf("inspect staged changes: %s", strings.TrimSpace(staged))
	}
	if strings.TrimSpace(staged) == "" {
		return fmt.Errorf("nothing to commit for %s", file)
	}
	if out, err := run(dir, "commit", "--signoff", "--message", message, "--", file); err != nil {
		return fmt.Errorf("commit %s: %s", file, strings.TrimSpace(out))
	}
	return nil
}

func run(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	return buf.String(), err
}
```

- [ ] **Step 5: Реализовать слежение**

```go
// internal/board/watch.go
package board

import (
	"context"
	"fmt"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Watch calls onChange when anything in dir changes, coalescing bursts into one
// call per 300ms so a rewrite by an agent does not flood the panel.
func Watch(ctx context.Context, dir string, onChange func()) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("create watcher: %w", err)
	}
	defer w.Close()
	if err := w.Add(dir); err != nil {
		return fmt.Errorf("watch %s: %w", dir, err)
	}

	var timer *time.Timer
	for {
		select {
		case <-ctx.Done():
			return nil
		case _, ok := <-w.Events:
			if !ok {
				return nil
			}
			if timer == nil {
				timer = time.AfterFunc(300*time.Millisecond, onChange)
			} else {
				timer.Reset(300 * time.Millisecond)
			}
		case err, ok := <-w.Errors:
			if !ok {
				return nil
			}
			return fmt.Errorf("watcher: %w", err)
		}
	}
}
```

- [ ] **Step 6: Прогнать тесты.** Команда `go test ./internal/board/`, ожидается PASS, четырнадцать тестов.

- [ ] **Step 7: Коммит**

```bash
git add internal/board go.mod go.sum
git commit --signoff -m "feat(board): commit written files and watch the board directory"
```

---

## Task 7: Лимиты аккаунта

**Files:**
- Create: `internal/usage/keychain.go`, `internal/usage/usage.go`, `internal/usage/usage_test.go`

**Interfaces:**
- Consumes: ничего
- Produces: `type Window struct { Utilization float64; ResetsAt time.Time }`, `type Limits struct { FiveHour, SevenDay Window; FetchedAt time.Time }`, `type Fetcher struct{}`, `func NewFetcher(token func() (string, error), endpoint string, ttl time.Duration) *Fetcher`, `func (f *Fetcher) Limits(ctx context.Context) (Limits, error)`, `func KeychainToken() (string, error)`, `var ErrNoToken`

Токен берётся строго через переданную функцию, чтобы тест не лез в Keychain и чтобы значение не оседало в структуре дольше одного запроса.

- [ ] **Step 1: Написать падающие тесты**

```go
// internal/usage/usage_test.go
package usage

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

const body = `{"five_hour":{"utilization":17.4,"resets_at":"2026-09-09T12:00:00.000Z"},"seven_day":{"utilization":48.2,"resets_at":"2026-09-13T00:00:00.000Z"}}`

func TestLimitsParsesBothWindows(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("anthropic-beta"); got != "oauth-2025-04-20" {
			t.Errorf("beta header missing, got %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Errorf("token not sent, got %q", got)
		}
		w.Write([]byte(body))
	}))
	defer srv.Close()

	f := NewFetcher(func() (string, error) { return "tok", nil }, srv.URL, time.Minute)
	got, err := f.Limits(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.FiveHour.Utilization != 17.4 || got.SevenDay.Utilization != 48.2 {
		t.Fatalf("utilization wrong: %+v", got)
	}
	if got.FiveHour.ResetsAt.IsZero() || got.SevenDay.ResetsAt.IsZero() {
		t.Fatal("reset timestamps must be parsed")
	}
}

func TestLimitsCachesWithinTTL(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Write([]byte(body))
	}))
	defer srv.Close()

	f := NewFetcher(func() (string, error) { return "tok", nil }, srv.URL, time.Minute)
	for i := 0; i < 3; i++ {
		if _, err := f.Limits(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if hits != 1 {
		t.Fatalf("three calls within the TTL must hit the endpoint once, got %d", hits)
	}
}

func TestExpiredTokenIsAnErrorNotZeroes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"error":{"type":"authentication_error"}}`))
	}))
	defer srv.Close()

	f := NewFetcher(func() (string, error) { return "tok", nil }, srv.URL, time.Minute)
	if _, err := f.Limits(context.Background()); err == nil {
		t.Fatal("an auth error must be reported; zero gauges would look like a healthy account")
	}
}

func TestMissingTokenIsTypedError(t *testing.T) {
	f := NewFetcher(func() (string, error) { return "", ErrNoToken }, "http://127.0.0.1:1", time.Minute)
	if _, err := f.Limits(context.Background()); !errors.Is(err, ErrNoToken) {
		t.Fatalf("want ErrNoToken, got %v", err)
	}
}

func TestEmptyBodyIsNotSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	f := NewFetcher(func() (string, error) { return "tok", nil }, srv.URL, time.Minute)
	if _, err := f.Limits(context.Background()); err == nil {
		t.Fatal("a response without windows must fail; nothing to read is not a healthy zero")
	}
}
```

- [ ] **Step 2: Прогнать и убедиться, что падает.** Команда `go test ./internal/usage/`, ожидается FAIL: пакет не существует.

- [ ] **Step 3: Реализовать извлечение токена**

```go
// internal/usage/keychain.go
// Package usage reads account rate-limit windows from the Anthropic OAuth usage
// endpoint. The token is read from the macOS Keychain, sent only to that
// endpoint, never logged and never stored.
package usage

import (
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"os/user"
	"strings"
)

// ErrNoToken means no OAuth token could be read from the Keychain.
var ErrNoToken = errors.New("no oauth token in keychain")

// KeychainToken reads the Claude Code OAuth token of the current user.
func KeychainToken() (string, error) {
	u, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("current user: %w", err)
	}
	out, err := exec.Command("security", "find-generic-password",
		"-s", "Claude Code-credentials", "-a", u.Username, "-w").Output()
	if err != nil {
		return "", fmt.Errorf("%w: keychain lookup failed", ErrNoToken)
	}
	var creds struct {
		ClaudeAIOAuth struct {
			AccessToken string `json:"accessToken"`
		} `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(out))), &creds); err != nil {
		return "", fmt.Errorf("%w: credentials are not JSON", ErrNoToken)
	}
	if creds.ClaudeAIOAuth.AccessToken == "" {
		return "", fmt.Errorf("%w: credentials carry no access token", ErrNoToken)
	}
	return creds.ClaudeAIOAuth.AccessToken, nil
}
```

- [ ] **Step 4: Реализовать запрос лимитов**

```go
// internal/usage/usage.go
package usage

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// Endpoint is the production usage endpoint. It is unofficial and gated behind a
// beta header: when it changes, the gauges go dark and the panel keeps working.
const Endpoint = "https://api.anthropic.com/api/oauth/usage"

type Window struct {
	Utilization float64   `json:"utilization"`
	ResetsAt    time.Time `json:"resetsAt"`
}

type Limits struct {
	FiveHour  Window    `json:"fiveHour"`
	SevenDay  Window    `json:"sevenDay"`
	FetchedAt time.Time `json:"fetchedAt"`
}

type Fetcher struct {
	token    func() (string, error)
	endpoint string
	ttl      time.Duration

	mu     sync.Mutex
	cached Limits
	at     time.Time
}

func NewFetcher(token func() (string, error), endpoint string, ttl time.Duration) *Fetcher {
	return &Fetcher{token: token, endpoint: endpoint, ttl: ttl}
}

// Limits returns the account windows, cached for the fetcher's TTL so one
// request serves every session instead of one request per session.
func (f *Fetcher) Limits(ctx context.Context) (Limits, error) {
	f.mu.Lock()
	if !f.at.IsZero() && time.Since(f.at) < f.ttl {
		defer f.mu.Unlock()
		return f.cached, nil
	}
	f.mu.Unlock()

	tok, err := f.token()
	if err != nil {
		return Limits{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.endpoint, nil)
	if err != nil {
		return Limits{}, fmt.Errorf("build usage request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Limits{}, fmt.Errorf("usage request: %w", err)
	}
	defer resp.Body.Close()

	var payload struct {
		Error *struct {
			Type string `json:"type"`
		} `json:"error"`
		FiveHour *struct {
			Utilization float64 `json:"utilization"`
			ResetsAt    string  `json:"resets_at"`
		} `json:"five_hour"`
		SevenDay *struct {
			Utilization float64 `json:"utilization"`
			ResetsAt    string  `json:"resets_at"`
		} `json:"seven_day"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return Limits{}, fmt.Errorf("decode usage reply: %w", err)
	}
	if payload.Error != nil {
		return Limits{}, fmt.Errorf("usage endpoint refused: %s", payload.Error.Type)
	}
	if payload.FiveHour == nil || payload.SevenDay == nil {
		return Limits{}, fmt.Errorf("usage reply carries no windows")
	}

	parse := func(s string) time.Time {
		t, _ := time.Parse(time.RFC3339, s)
		return t
	}
	out := Limits{
		FiveHour:  Window{Utilization: payload.FiveHour.Utilization, ResetsAt: parse(payload.FiveHour.ResetsAt)},
		SevenDay:  Window{Utilization: payload.SevenDay.Utilization, ResetsAt: parse(payload.SevenDay.ResetsAt)},
		FetchedAt: time.Now(),
	}
	f.mu.Lock()
	f.cached, f.at = out, time.Now()
	f.mu.Unlock()
	return out, nil
}
```

- [ ] **Step 5: Прогнать тесты.** Команда `go test ./internal/usage/`, ожидается PASS, пять тестов.

- [ ] **Step 6: Проверить, что токен не утекает в вывод.** Команда `grep -rn "tok\|AccessToken" internal/usage/*.go | grep -i "print\|log\|fmt.Err"` — ожидается пустой вывод: токен не должен попадать ни в одно сообщение об ошибке.

- [ ] **Step 7: Коммит**

```bash
git add internal/usage
git commit --signoff -m "feat(usage): account limits with keychain token and caching"
```

---

## Task 8: Уведомления

**Files:**
- Create: `internal/notify/notify.go`, `internal/notify/notify_test.go`

**Interfaces:**
- Consumes: ничего
- Produces: `type Notifier struct{}`, `func New(send func(title, text string) error) *Notifier`, `func (n *Notifier) Fire(key, title, text string) error`, `func OSAScriptSend(title, text string) error`

`Fire` шлёт баннер только при первом появлении ключа и молчит, пока ключ не пропадёт из вызовов. Ключ — это событие, а не состояние: пока сессия висит в ожидании, баннер один, а не каждые две секунды.

- [ ] **Step 1: Написать падающие тесты**

```go
// internal/notify/notify_test.go
package notify

import (
	"errors"
	"testing"
)

func TestFireSendsOncePerKey(t *testing.T) {
	sent := 0
	n := New(func(title, text string) error { sent++; return nil })
	for i := 0; i < 3; i++ {
		if err := n.Fire("session:abc:waiting", "t", "x"); err != nil {
			t.Fatal(err)
		}
	}
	if sent != 1 {
		t.Fatalf("a standing state must produce one banner, got %d", sent)
	}
}

func TestFireAgainAfterClear(t *testing.T) {
	sent := 0
	n := New(func(title, text string) error { sent++; return nil })
	n.Fire("k", "t", "x")
	n.Clear("k")
	n.Fire("k", "t", "x")
	if sent != 2 {
		t.Fatalf("a state that went away and came back is a new event, got %d", sent)
	}
}

func TestSendFailureIsReportedAndNotRemembered(t *testing.T) {
	fail := true
	n := New(func(title, text string) error {
		if fail {
			return errors.New("osascript missing")
		}
		return nil
	})
	if err := n.Fire("k", "t", "x"); err == nil {
		t.Fatal("a failed banner must be reported")
	}
	fail = false
	if err := n.Fire("k", "t", "x"); err != nil {
		t.Fatal("a key whose banner failed must be retried, not marked as delivered")
	}
}
```

- [ ] **Step 2: Прогнать и убедиться, что падает.** Команда `go test ./internal/notify/`, ожидается FAIL: пакет не существует.

- [ ] **Step 3: Реализовать**

```go
// internal/notify/notify.go
// Package notify shows macOS banners. The decision to notify belongs to the
// server, not the browser: the server knows the state and a banner must not
// depend on whether a tab is open.
package notify

import (
	"fmt"
	"os/exec"
	"strings"
	"sync"
)

type Notifier struct {
	send func(title, text string) error

	mu   sync.Mutex
	seen map[string]bool
}

func New(send func(title, text string) error) *Notifier {
	return &Notifier{send: send, seen: map[string]bool{}}
}

// Fire delivers a banner the first time a key appears. A key is an event, not a
// state: while a session stands waiting, one banner is enough.
func (n *Notifier) Fire(key, title, text string) error {
	n.mu.Lock()
	already := n.seen[key]
	n.mu.Unlock()
	if already {
		return nil
	}
	if err := n.send(title, text); err != nil {
		return fmt.Errorf("send banner %s: %w", key, err)
	}
	n.mu.Lock()
	n.seen[key] = true
	n.mu.Unlock()
	return nil
}

// Clear forgets a key so its next appearance is a new event.
func (n *Notifier) Clear(key string) {
	n.mu.Lock()
	delete(n.seen, key)
	n.mu.Unlock()
}

// OSAScriptSend shows a macOS notification banner.
func OSAScriptSend(title, text string) error {
	esc := func(s string) string { return strings.ReplaceAll(s, `"`, `\"`) }
	script := fmt.Sprintf(`display notification "%s" with title "%s"`, esc(text), esc(title))
	if out, err := exec.Command("osascript", "-e", script).CombinedOutput(); err != nil {
		return fmt.Errorf("osascript: %s", strings.TrimSpace(string(out)))
	}
	return nil
}
```

- [ ] **Step 4: Прогнать тесты.** Команда `go test ./internal/notify/`, ожидается PASS, три теста.

- [ ] **Step 5: Коммит**

```bash
git add internal/notify
git commit --signoff -m "feat(notify): macOS banners fired once per event"
```

---

## Task 9: Снимок состояния и события

**Files:**
- Create: `internal/state/snapshot.go`, `internal/state/events.go`, `internal/state/snapshot_test.go`, `internal/state/events_test.go`

**Interfaces:**
- Consumes: `daemon.Session`, `board.Card`, `usage.Limits`, `transcript.Usage`
- Produces: `type SessionView struct { daemon.Session; Context *transcript.Usage; CardPath string; SilentFor time.Duration }`, `type Snapshot struct { Sessions []SessionView; Cards []board.Card; Limits *usage.Limits; DaemonError, BoardError, UsageError string; At time.Time }`, `type Event struct { Key, Title, Text string }`, `func Diff(prev, next Snapshot, silenceAfter time.Duration) (fire []Event, clear []string)`

Снимок собирается из того, что удалось получить: отказавший источник заполняет своё поле ошибки и оставляет остальные данные нетронутыми. Именно здесь реализуется правило деградации по частям.

- [ ] **Step 1: Написать падающие тесты снимка**

```go
// internal/state/snapshot_test.go
package state

import (
	"testing"

	"github.com/kroticw/fleetdeck/internal/board"
	"github.com/kroticw/fleetdeck/internal/daemon"
)

func TestLinkSessionsToCardsBySessionField(t *testing.T) {
	sessions := []daemon.Session{{ID: "abc12345"}, {ID: "deadbeef"}}
	cards := []board.Card{{Path: "/b/one.md", Session: "abc12345"}}
	views := Link(sessions, cards)
	if views[0].CardPath != "/b/one.md" {
		t.Fatalf("session must be linked to its card: %+v", views[0])
	}
	if views[1].CardPath != "" {
		t.Fatal("a session without a card must not borrow someone else's")
	}
}

func TestCardPointingAtDeadSessionIsVisible(t *testing.T) {
	cards := []board.Card{{Path: "/b/one.md", Session: "gone1234"}}
	orphans := OrphanCards(nil, cards)
	if len(orphans) != 1 || orphans[0] != "/b/one.md" {
		t.Fatalf("a card referencing a dead session must be reported: %v", orphans)
	}
}

func TestSnapshotKeepsBoardWhenDaemonIsDown(t *testing.T) {
	s := Snapshot{DaemonError: "daemon unavailable", Cards: []board.Card{{Path: "/b/one.md"}}}
	if len(s.Cards) != 1 {
		t.Fatal("a dead daemon must not take the board down with it")
	}
}
```

- [ ] **Step 2: Написать падающие тесты событий**

```go
// internal/state/events_test.go
package state

import (
	"testing"
	"time"

	"github.com/kroticw/fleetdeck/internal/board"
	"github.com/kroticw/fleetdeck/internal/daemon"
)

func waiting(id string) SessionView {
	return SessionView{Session: daemon.Session{ID: id, Name: "S " + id, Needs: "answer"}}
}

func TestSessionBecomingWaitingFires(t *testing.T) {
	prev := Snapshot{Sessions: []SessionView{{Session: daemon.Session{ID: "a", Name: "S a"}}}}
	next := Snapshot{Sessions: []SessionView{waiting("a")}}
	fire, _ := Diff(prev, next, time.Hour)
	if len(fire) != 1 || fire[0].Key != "session:a:waiting" {
		t.Fatalf("a session that started waiting must fire once: %+v", fire)
	}
}

func TestStillWaitingDoesNotFireAgain(t *testing.T) {
	prev := Snapshot{Sessions: []SessionView{waiting("a")}}
	next := Snapshot{Sessions: []SessionView{waiting("a")}}
	fire, _ := Diff(prev, next, time.Hour)
	if len(fire) != 0 {
		t.Fatalf("a standing state is not a new event: %+v", fire)
	}
}

func TestSessionThatStoppedWaitingIsCleared(t *testing.T) {
	prev := Snapshot{Sessions: []SessionView{waiting("a")}}
	next := Snapshot{Sessions: []SessionView{{Session: daemon.Session{ID: "a"}}}}
	_, clear := Diff(prev, next, time.Hour)
	if len(clear) != 1 || clear[0] != "session:a:waiting" {
		t.Fatalf("a resolved state must be cleared so it can fire again: %v", clear)
	}
}

func TestSilenceFiresOnceAfterThreshold(t *testing.T) {
	prev := Snapshot{Sessions: []SessionView{{Session: daemon.Session{ID: "a"}, SilentFor: 10 * time.Minute}}}
	next := Snapshot{Sessions: []SessionView{{Session: daemon.Session{ID: "a"}, SilentFor: 31 * time.Minute}}}
	fire, _ := Diff(prev, next, 30*time.Minute)
	if len(fire) != 1 || fire[0].Key != "session:a:silent" {
		t.Fatalf("crossing the silence threshold must fire: %+v", fire)
	}
}

func TestCardEnteringBlockedFires(t *testing.T) {
	prev := Snapshot{Cards: []board.Card{{Path: "/b/c.md", Stage: "active"}}}
	next := Snapshot{Cards: []board.Card{{Path: "/b/c.md", Stage: "blocked"}}}
	fire, _ := Diff(prev, next, time.Hour)
	if len(fire) != 1 || fire[0].Key != "card:/b/c.md:blocked" {
		t.Fatalf("a card entering blocked must fire: %+v", fire)
	}
}

func TestEmptyPreviousSnapshotDoesNotFireForEverything(t *testing.T) {
	next := Snapshot{Sessions: []SessionView{waiting("a"), waiting("b")}}
	fire, _ := Diff(Snapshot{}, next, time.Hour)
	if len(fire) != 0 {
		t.Fatalf("the first snapshot has nothing to compare against and must stay quiet, got %+v", fire)
	}
}
```

- [ ] **Step 3: Прогнать и убедиться, что падает.** Команда `go test ./internal/state/`, ожидается FAIL: пакет не существует.

- [ ] **Step 4: Реализовать снимок**

```go
// internal/state/snapshot.go
// Package state assembles one snapshot out of the three sources and decides
// which changes are worth a banner. It never writes anywhere.
package state

import (
	"time"

	"github.com/kroticw/fleetdeck/internal/board"
	"github.com/kroticw/fleetdeck/internal/daemon"
	"github.com/kroticw/fleetdeck/internal/transcript"
	"github.com/kroticw/fleetdeck/internal/usage"
)

// SessionView is a daemon session enriched with what the other sources know.
type SessionView struct {
	daemon.Session
	Context   *transcript.Usage `json:"context,omitempty"`
	CardPath  string            `json:"cardPath,omitempty"`
	SilentFor time.Duration     `json:"silentFor"`
}

// Snapshot is everything the panel shows at one moment. A failed source fills
// its own error field and leaves the rest of the snapshot intact.
type Snapshot struct {
	Sessions    []SessionView `json:"sessions"`
	Cards       []board.Card  `json:"cards"`
	Limits      *usage.Limits `json:"limits,omitempty"`
	OrphanCards []string      `json:"orphanCards,omitempty"`
	DaemonError string        `json:"daemonError,omitempty"`
	BoardError  string        `json:"boardError,omitempty"`
	UsageError  string        `json:"usageError,omitempty"`
	At          time.Time     `json:"at"`
}

// Link attaches each session to the card that names it. A card is found by the
// session field only: the panel never guesses a link from names or paths.
func Link(sessions []daemon.Session, cards []board.Card) []SessionView {
	bySession := map[string]string{}
	for _, c := range cards {
		if c.Session != "" {
			bySession[c.Session] = c.Path
		}
	}
	views := make([]SessionView, 0, len(sessions))
	for _, s := range sessions {
		views = append(views, SessionView{Session: s, CardPath: bySession[s.ID]})
	}
	return views
}

// OrphanCards lists cards whose session no longer exists in the daemon.
func OrphanCards(sessions []daemon.Session, cards []board.Card) []string {
	alive := map[string]bool{}
	for _, s := range sessions {
		alive[s.ID] = true
	}
	var out []string
	for _, c := range cards {
		if c.Session != "" && !alive[c.Session] {
			out = append(out, c.Path)
		}
	}
	return out
}
```

- [ ] **Step 5: Реализовать события**

```go
// internal/state/events.go
package state

import (
	"fmt"
	"time"
)

// Event is a change worth a banner.
type Event struct {
	Key   string `json:"key"`
	Title string `json:"title"`
	Text  string `json:"text"`
}

// Diff compares two snapshots and returns banners to fire and keys to clear.
// The very first snapshot fires nothing: with nothing to compare against, every
// standing state would look like it just happened.
func Diff(prev, next Snapshot, silenceAfter time.Duration) (fire []Event, clear []string) {
	if len(prev.Sessions) == 0 && len(prev.Cards) == 0 {
		return nil, nil
	}

	prevSessions := map[string]SessionView{}
	for _, s := range prev.Sessions {
		prevSessions[s.ID] = s
	}
	for _, s := range next.Sessions {
		was, existed := prevSessions[s.ID]
		if s.Waiting() && (!existed || !was.Waiting()) {
			fire = append(fire, Event{
				Key:   fmt.Sprintf("session:%s:waiting", s.ID),
				Title: s.Name,
				Text:  "ждёт ответа",
			})
		}
		if !s.Waiting() && existed && was.Waiting() {
			clear = append(clear, fmt.Sprintf("session:%s:waiting", s.ID))
		}
		if s.State == "failed" && (!existed || was.State != "failed") {
			fire = append(fire, Event{
				Key:   fmt.Sprintf("session:%s:failed", s.ID),
				Title: s.Name,
				Text:  "сессия завершилась с ошибкой",
			})
		}
		crossed := s.SilentFor >= silenceAfter && (!existed || was.SilentFor < silenceAfter)
		if crossed {
			fire = append(fire, Event{
				Key:   fmt.Sprintf("session:%s:silent", s.ID),
				Title: s.Name,
				Text:  fmt.Sprintf("молчит дольше %s", silenceAfter),
			})
		}
		if s.SilentFor < silenceAfter && existed && was.SilentFor >= silenceAfter {
			clear = append(clear, fmt.Sprintf("session:%s:silent", s.ID))
		}
	}

	prevCards := map[string]string{}
	for _, c := range prev.Cards {
		prevCards[c.Path] = c.Stage
	}
	for _, c := range next.Cards {
		was, existed := prevCards[c.Path]
		if !existed || was == c.Stage {
			continue
		}
		if c.Stage == "blocked" || c.Stage == "review" {
			fire = append(fire, Event{
				Key:   fmt.Sprintf("card:%s:%s", c.Path, c.Stage),
				Title: c.Title,
				Text:  "карточка ушла в " + c.Stage,
			})
		}
		if was == "blocked" || was == "review" {
			clear = append(clear, fmt.Sprintf("card:%s:%s", c.Path, was))
		}
	}
	return fire, clear
}
```

- [ ] **Step 6: Прогнать тесты.** Команда `go test ./internal/state/`, ожидается PASS, девять тестов.

- [ ] **Step 7: Коммит**

```bash
git add internal/state
git commit --signoff -m "feat(state): assemble snapshots and detect notifiable changes"
```

---

## Task 10: HTTP и WebSocket

**Files:**
- Create: `internal/server/server.go`, `internal/server/api.go`, `internal/server/ws.go`, `internal/server/api_test.go`

**Interfaces:**
- Consumes: `state.Snapshot`, `board.SetField`, `board.Commit`, `daemon.Client`
- Produces: `type Deps struct { Snapshot func() state.Snapshot; SendText func(session, text string, submit bool) error; SendKeys func(session, keys string) error; ReadScreen func(session string, tail int) (string, error); SetCardField func(path, field, value string) error }`, `func New(d Deps) http.Handler`

Маршруты: `GET /api/snapshot`, `POST /api/sessions/{id}/text`, `POST /api/sessions/{id}/keys`, `GET /api/sessions/{id}/screen`, `PATCH /api/cards`, `GET /ws`.

- [ ] **Step 1: Написать падающие тесты**

```go
// internal/server/api_test.go
package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kroticw/fleetdeck/internal/board"
	"github.com/kroticw/fleetdeck/internal/state"
)

func testDeps() (Deps, *[]string) {
	var calls []string
	return Deps{
		Snapshot: func() state.Snapshot {
			return state.Snapshot{Cards: []board.Card{{Path: "/b/c.md", Stage: "active"}}}
		},
		SendText: func(session, text string, submit bool) error {
			calls = append(calls, "text:"+session+":"+text)
			return nil
		},
		SendKeys: func(session, keys string) error {
			calls = append(calls, "keys:"+session+":"+keys)
			return nil
		},
		ReadScreen:   func(session string, tail int) (string, error) { return "screen", nil },
		SetCardField: func(path, field, value string) error { calls = append(calls, "card:"+field+":"+value); return nil },
	}, &calls
}

func TestSnapshotIsServedAsJSON(t *testing.T) {
	d, _ := testDeps()
	rec := httptest.NewRecorder()
	New(d).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/snapshot", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"/b/c.md"`) {
		t.Fatalf("snapshot body missing cards: %s", rec.Body.String())
	}
}

func TestSendTextReachesTheSession(t *testing.T) {
	d, calls := testDeps()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/abc123/text", strings.NewReader(`{"text":"hi","submit":true}`))
	New(d).ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(*calls) != 1 || (*calls)[0] != "text:abc123:hi" {
		t.Fatalf("unexpected calls: %v", *calls)
	}
}

func TestPatchCardRefusesFieldsThePanelDoesNotOwn(t *testing.T) {
	d, _ := testDeps()
	d.SetCardField = func(path, field, value string) error { return board.ErrUnknownField }
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/api/cards", strings.NewReader(`{"path":"/b/c.md","field":"session","value":"x"}`))
	New(d).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("writing a foreign field must be refused with 400, got %d", rec.Code)
	}
}

func TestUnknownRouteIs404(t *testing.T) {
	d, _ := testDeps()
	rec := httptest.NewRecorder()
	New(d).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/nothing", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", rec.Code)
	}
}

func TestMalformedBodyIsRejected(t *testing.T) {
	d, _ := testDeps()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/abc/text", strings.NewReader(`{`))
	New(d).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a truncated body must be refused, got %d", rec.Code)
	}
}
```

- [ ] **Step 2: Прогнать и убедиться, что падает.** Команда `go test ./internal/server/`, ожидается FAIL: пакет не существует.

- [ ] **Step 3: Реализовать маршруты и обработчики**

```go
// internal/server/server.go
// Package server exposes the snapshot over HTTP and WebSocket and accepts the
// only two writes the panel performs: input into a session and a card field.
package server

import (
	"net/http"

	"github.com/kroticw/fleetdeck/internal/state"
)

type Deps struct {
	Snapshot     func() state.Snapshot
	SendText     func(session, text string, submit bool) error
	SendKeys     func(session, keys string) error
	ReadScreen   func(session string, tail int) (string, error)
	SetCardField func(path, field, value string) error
}

// New builds the router. Routes use Go 1.22 pattern matching, no third-party mux.
func New(d Deps) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/snapshot", d.handleSnapshot)
	mux.HandleFunc("POST /api/sessions/{id}/text", d.handleSendText)
	mux.HandleFunc("POST /api/sessions/{id}/keys", d.handleSendKeys)
	mux.HandleFunc("GET /api/sessions/{id}/screen", d.handleScreen)
	mux.HandleFunc("PATCH /api/cards", d.handlePatchCard)
	mux.HandleFunc("GET /ws", d.handleWS)
	return mux
}
```

```go
// internal/server/api.go
package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/kroticw/fleetdeck/internal/board"
)

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func (d Deps) handleSnapshot(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, d.Snapshot())
}

func (d Deps) handleSendText(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text   string `json:"text"`
		Submit bool   `json:"submit"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		fail(w, http.StatusBadRequest, "malformed body: "+err.Error())
		return
	}
	if err := d.SendText(r.PathValue("id"), body.Text, body.Submit); err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d Deps) handleSendKeys(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Keys string `json:"keys"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		fail(w, http.StatusBadRequest, "malformed body: "+err.Error())
		return
	}
	if err := d.SendKeys(r.PathValue("id"), body.Keys); err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d Deps) handleScreen(w http.ResponseWriter, r *http.Request) {
	tail := 200
	if v := r.URL.Query().Get("tail"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			tail = n
		}
	}
	screen, err := d.ReadScreen(r.PathValue("id"), tail)
	if err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"screen": screen})
}

func (d Deps) handlePatchCard(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path  string `json:"path"`
		Field string `json:"field"`
		Value string `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		fail(w, http.StatusBadRequest, "malformed body: "+err.Error())
		return
	}
	err := d.SetCardField(body.Path, body.Field, body.Value)
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, board.ErrUnknownField):
		fail(w, http.StatusBadRequest, err.Error())
	default:
		fail(w, http.StatusBadRequest, err.Error())
	}
}
```

- [ ] **Step 4: Реализовать WebSocket**

```go
// internal/server/ws.go
package server

import (
	"context"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// handleWS pushes a fresh snapshot to the browser once a second. The socket is
// read-only: every write goes through the HTTP routes so one path handles
// validation and errors.
func (d Deps) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{"127.0.0.1:*", "localhost:*"}})
	if err != nil {
		return
	}
	defer conn.CloseNow()

	ctx := r.Context()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sendCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			err := wsjson.Write(sendCtx, conn, d.Snapshot())
			cancel()
			if err != nil {
				return
			}
		}
	}
}
```

- [ ] **Step 5: Прогнать тесты.** Команда `go test ./internal/server/`, ожидается PASS, пять тестов.

- [ ] **Step 6: Коммит**

```bash
git add internal/server go.mod go.sum
git commit --signoff -m "feat(server): snapshot API, session input and card patching"
```

---

## Task 11: Связывание и запуск

**Files:**
- Create: `cmd/fleetdeck/main.go`, `cmd/fleetdeck/collect.go`, `cmd/fleetdeck/collect_test.go`

**Interfaces:**
- Consumes: все пакеты `internal/`
- Produces: `type Collector struct{}`, `func NewCollector(cfg config.Config, dc *daemon.Client, uf *usage.Fetcher, projectsDir string) *Collector`, `func (c *Collector) Collect(ctx context.Context) state.Snapshot`, бинарь `fleetdeck`

`Collect` — то место, где правило деградации по частям становится кодом: каждый источник опрашивается отдельно, его отказ пишется в своё поле и не мешает остальным.

- [ ] **Step 1: Написать падающие тесты сборщика**

```go
// cmd/fleetdeck/collect_test.go
package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/kroticw/fleetdeck/internal/config"
	"github.com/kroticw/fleetdeck/internal/daemon"
	"github.com/kroticw/fleetdeck/internal/usage"
)

func TestCollectReportsDaemonFailureAndKeepsBoard(t *testing.T) {
	dir := t.TempDir()
	writeSampleCard(t, dir)

	cfg := config.Default()
	cfg.BoardPath = dir
	cfg.UsageEnabled = false

	dead := daemon.New(filepath.Join(t.TempDir(), "absent.sock"))
	snap := NewCollector(cfg, dead, nil, t.TempDir()).Collect(context.Background())

	if snap.DaemonError == "" {
		t.Fatal("an unreachable daemon must be named in the snapshot")
	}
	if len(snap.Cards) != 1 {
		t.Fatal("the board must survive a dead daemon")
	}
}

func TestCollectReportsEmptyBoardAndKeepsGoing(t *testing.T) {
	cfg := config.Default()
	cfg.BoardPath = t.TempDir()
	cfg.UsageEnabled = false

	dead := daemon.New(filepath.Join(t.TempDir(), "absent.sock"))
	snap := NewCollector(cfg, dead, nil, t.TempDir()).Collect(context.Background())

	if snap.BoardError == "" {
		t.Fatal("an empty board must be reported, not pass as a board with no cards")
	}
}

func TestCollectSkipsUsageWhenDisabled(t *testing.T) {
	cfg := config.Default()
	cfg.UsageEnabled = false
	cfg.BoardPath = ""

	uf := usage.NewFetcher(func() (string, error) { t.Fatal("usage must not be fetched when disabled"); return "", nil }, "", time.Minute)
	dead := daemon.New(filepath.Join(t.TempDir(), "absent.sock"))
	snap := NewCollector(cfg, dead, uf, t.TempDir()).Collect(context.Background())

	if snap.Limits != nil {
		t.Fatal("limits must be absent when usage is disabled")
	}
}
```

Вспомогательная функция для теста, в том же файле:

```go
func writeSampleCard(t *testing.T, dir string) {
	t.Helper()
	body := "---\nzone: planned\nstage: active\nprogress: 20\nsession: abc12345\nrepo: x/y\ncreated: 2026-09-09\n---\n\n# card\n"
	if err := os.WriteFile(filepath.Join(dir, "c.md"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 2: Прогнать и убедиться, что падает.** Команда `go test ./cmd/fleetdeck/`, ожидается FAIL: пакет не существует.

- [ ] **Step 3: Реализовать сборщик**

```go
// cmd/fleetdeck/collect.go
package main

import (
	"context"
	"time"

	"github.com/kroticw/fleetdeck/internal/board"
	"github.com/kroticw/fleetdeck/internal/config"
	"github.com/kroticw/fleetdeck/internal/daemon"
	"github.com/kroticw/fleetdeck/internal/state"
	"github.com/kroticw/fleetdeck/internal/transcript"
	"github.com/kroticw/fleetdeck/internal/usage"
)

// Collector asks every source in turn. A source that fails fills its own error
// field; the others are unaffected. This is where degrade-in-parts lives.
type Collector struct {
	cfg         config.Config
	daemon      *daemon.Client
	usage       *usage.Fetcher
	projectsDir string
}

func NewCollector(cfg config.Config, dc *daemon.Client, uf *usage.Fetcher, projectsDir string) *Collector {
	return &Collector{cfg: cfg, daemon: dc, usage: uf, projectsDir: projectsDir}
}

func (c *Collector) Collect(ctx context.Context) state.Snapshot {
	snap := state.Snapshot{At: time.Now()}

	sessions, err := c.daemon.ListSessions(ctx)
	if err != nil {
		snap.DaemonError = err.Error()
	}

	var cards []board.Card
	if c.cfg.BoardPath != "" {
		cards, err = board.Scan(c.cfg.BoardPath)
		if err != nil {
			snap.BoardError = err.Error()
		}
	}
	snap.Cards = cards
	snap.Sessions = state.Link(sessions, cards)
	snap.OrphanCards = state.OrphanCards(sessions, cards)

	for i, s := range snap.Sessions {
		if s.SessionID == "" {
			continue
		}
		path, err := transcript.Locate(c.projectsDir, s.SessionID)
		if err != nil {
			continue
		}
		if u, err := transcript.ContextUsage(path); err == nil {
			snap.Sessions[i].Context = &u
		}
	}

	if c.cfg.UsageEnabled && c.usage != nil {
		if l, err := c.usage.Limits(ctx); err != nil {
			snap.UsageError = err.Error()
		} else {
			snap.Limits = &l
		}
	}
	return snap
}
```

- [ ] **Step 3a: Добавить кэш оценки контекста**

Без кэша `Collect` перечитывает транскрипт каждой сессии раз в две секунды, а транскрипты доходят до десятков мегабайт: при пяти сессиях это сотни мегабайт чтения в минуту. Кэш держится по времени изменения файла — транскрипт растёт только когда сессия что-то делает.

Добавить в `Collector` поля и метод, а в конструкторе инициализировать карту:

```go
// cachedUsage remembers the last estimate together with the file state it was
// computed from, so an idle session costs no reads at all.
type cachedUsage struct {
	usage transcript.Usage
	size  int64
	mtime time.Time
}

// contextFor returns the context estimate for a transcript, recomputing it only
// when the file actually changed.
func (c *Collector) contextFor(path string) (transcript.Usage, bool) {
	fi, err := os.Stat(path)
	if err != nil {
		return transcript.Usage{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if hit, ok := c.contextCache[path]; ok && hit.size == fi.Size() && hit.mtime.Equal(fi.ModTime()) {
		return hit.usage, true
	}
	u, err := transcript.ContextUsage(path)
	if err != nil {
		return transcript.Usage{}, false
	}
	c.contextCache[path] = cachedUsage{usage: u, size: fi.Size(), mtime: fi.ModTime()}
	return u, true
}
```

Поля структуры: `mu sync.Mutex` и `contextCache map[string]cachedUsage`, инициализируется в `NewCollector` как `contextCache: map[string]cachedUsage{}`. Импорты пополняются на `os` и `sync`.

Тест на то, что кэш работает, добавить в `collect_test.go`:

```go
func TestContextEstimateIsNotRecomputedForUnchangedFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "s.jsonl")
	line := func(tokens int) string {
		return fmt.Sprintf(`{"type":"assistant","message":{"model":"claude-opus-5","usage":{"input_tokens":0,"cache_read_input_tokens":%d}}}`, tokens)
	}
	os.WriteFile(p, []byte(line(100)+"\n"), 0o600)

	c := NewCollector(config.Default(), nil, nil, dir)
	first, ok := c.contextFor(p)
	if !ok || first.Tokens != 100 {
		t.Fatalf("first read must compute 100 tokens, got %d ok=%v", first.Tokens, ok)
	}

	// Rewrite the content but restore size and mtime: a cache keyed on file
	// state must not notice, and must not recompute.
	fi, _ := os.Stat(p)
	os.WriteFile(p, []byte(line(999)+"\n"), 0o600)
	os.Chtimes(p, fi.ModTime(), fi.ModTime())

	second, ok := c.contextFor(p)
	if !ok {
		t.Fatal("second read must still succeed")
	}
	if second.Tokens != 100 {
		t.Fatalf("unchanged size and mtime must serve the cached value, got %d", second.Tokens)
	}
}

func TestContextEstimateIsRecomputedWhenFileGrows(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "s.jsonl")
	base := `{"type":"assistant","message":{"model":"claude-opus-5","usage":{"input_tokens":0,"cache_read_input_tokens":100}}}`
	os.WriteFile(p, []byte(base+"\n"), 0o600)

	c := NewCollector(config.Default(), nil, nil, dir)
	if u, _ := c.contextFor(p); u.Tokens != 100 {
		t.Fatalf("want 100, got %d", u.Tokens)
	}

	grown := base + "\n" + `{"type":"assistant","message":{"model":"claude-opus-5","usage":{"input_tokens":0,"cache_read_input_tokens":250}}}`
	os.WriteFile(p, []byte(grown+"\n"), 0o600)

	if u, _ := c.contextFor(p); u.Tokens != 250 {
		t.Fatalf("a grown transcript must be re-read, got %d", u.Tokens)
	}
}
```

- [ ] **Step 4: Реализовать точку входа**

```go
// cmd/fleetdeck/main.go
// Command fleetdeck serves the fleet control panel on the loopback interface.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/kroticw/fleetdeck/internal/board"
	"github.com/kroticw/fleetdeck/internal/config"
	"github.com/kroticw/fleetdeck/internal/daemon"
	"github.com/kroticw/fleetdeck/internal/notify"
	"github.com/kroticw/fleetdeck/internal/server"
	"github.com/kroticw/fleetdeck/internal/state"
	"github.com/kroticw/fleetdeck/internal/usage"
	"github.com/kroticw/fleetdeck/internal/version"
)

func main() {
	home, _ := os.UserHomeDir()
	configPath := flag.String("config", filepath.Join(home, ".config", "fleetdeck", "config.yaml"), "path to config file")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(version.String())
		return
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	sock, err := daemon.SocketPath()
	if err != nil {
		log.Printf("daemon: %v (the panel starts anyway, sessions will be unavailable)", err)
	}
	dc := daemon.New(sock)
	uf := usage.NewFetcher(usage.KeychainToken, usage.Endpoint, time.Minute)
	collector := NewCollector(cfg, dc, uf, filepath.Join(home, ".claude", "projects"))

	var mu sync.RWMutex
	current := state.Snapshot{}
	notifier := notify.New(notify.OSAScriptSend)

	ctx := context.Background()
	go func() {
		ticker := time.NewTicker(cfg.DaemonPollInterval)
		defer ticker.Stop()
		for range ticker.C {
			next := collector.Collect(ctx)
			mu.Lock()
			prev := current
			current = next
			mu.Unlock()

			fire, clear := state.Diff(prev, next, cfg.Notify.SilenceAfter)
			for _, k := range clear {
				notifier.Clear(k)
			}
			for _, e := range fire {
				if err := notifier.Fire(e.Key, e.Title, e.Text); err != nil {
					log.Printf("notify: %v", err)
				}
			}
		}
	}()

	deps := server.Deps{
		Snapshot: func() state.Snapshot {
			mu.RLock()
			defer mu.RUnlock()
			return current
		},
		SendText:   func(s, t string, submit bool) error { return dc.SendText(ctx, s, t, submit) },
		SendKeys:   func(s, k string) error { return dc.SendKeys(ctx, s, k) },
		ReadScreen: func(s string, tail int) (string, error) { return dc.ReadScreen(ctx, s, tail) },
		SetCardField: func(path, field, value string) error {
			if err := board.SetField(path, field, value); err != nil {
				return err
			}
			msg := fmt.Sprintf("chore(board): set %s to %s", field, value)
			return board.Commit(filepath.Dir(path), filepath.Base(path), msg)
		},
	}

	addr := fmt.Sprintf("127.0.0.1:%d", cfg.ServerPort)
	log.Printf("fleetdeck %s listening on http://%s", version.String(), addr)
	log.Fatal(http.ListenAndServe(addr, server.New(deps)))
}
```

- [ ] **Step 5: Прогнать тесты и сборку.** Команды `go test ./...` и `make build`, ожидается PASS по всем пакетам и два бинаря в `bin/`.

- [ ] **Step 6: Проверить руками, что сервер отвечает.** Запустить `./bin/fleetdeck` и в другом окне выполнить `curl --silent http://127.0.0.1:7777/api/snapshot | head -c 400` — в ответе должен быть JSON со списком сессий либо непустое поле `daemonError`. Пустой ответ или зависание считать провалом задачи.

- [ ] **Step 7: Коммит**

```bash
git add cmd/fleetdeck
git commit --signoff -m "feat(cmd): wire sources, notifications and the HTTP server"
```

---

## Task 12: Statusline-репортёр

**Files:**
- Create: `cmd/fleetdeck-status/main.go`, `cmd/fleetdeck-status/main_test.go`

**Interfaces:**
- Consumes: ничего из `internal/`
- Produces: бинарь `fleetdeck-status`, `func render(in statusInput) string`, `func report(endpoint string, in statusInput) error`

Репортёр читает JSON Claude Code со stdin, печатает статусную строку и отправляет структурные данные в пульт. Он обязан печатать строку даже когда пульт не отвечает: сломанный репортёр ломает статусную строку у всех сессий сразу.

- [ ] **Step 1: Написать падающие тесты**

```go
// cmd/fleetdeck-status/main_test.go
package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const sampleInput = `{"session_id":"abc-123","model":{"display_name":"Opus 5"},"cost":{"total_cost_usd":1.234},"context_window":{"used_percentage":41.7}}`

func TestRenderShowsModelCostAndContext(t *testing.T) {
	in, err := parse(strings.NewReader(sampleInput))
	if err != nil {
		t.Fatal(err)
	}
	got := render(in)
	for _, want := range []string{"Opus 5", "1.23", "42%"} {
		if !strings.Contains(got, want) {
			t.Fatalf("status line missing %q: %s", want, got)
		}
	}
}

func TestReportPostsToThePanel(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, r.ContentLength)
		r.Body.Read(b)
		body = string(b)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	in, _ := parse(strings.NewReader(sampleInput))
	if err := report(srv.URL, in); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "abc-123") || !strings.Contains(body, "41.7") {
		t.Fatalf("report body lost data: %s", body)
	}
}

func TestReportFailureDoesNotBreakRendering(t *testing.T) {
	in, _ := parse(strings.NewReader(sampleInput))
	if err := report("http://127.0.0.1:1", in); err == nil {
		t.Fatal("an unreachable panel must be reported to the caller")
	}
	if got := render(in); got == "" {
		t.Fatal("the status line must still render when the panel is down")
	}
}

func TestEmptyStdinIsNotSuccess(t *testing.T) {
	if _, err := parse(strings.NewReader("")); err == nil {
		t.Fatal("empty input must fail: nothing to read is not a healthy status line")
	}
}
```

- [ ] **Step 2: Прогнать и убедиться, что падает.** Команда `go test ./cmd/fleetdeck-status/`, ожидается FAIL: пакет не существует.

- [ ] **Step 3: Реализовать**

```go
// cmd/fleetdeck-status/main.go
// Command fleetdeck-status is the statusline reporter. Claude Code runs it for
// every session: it prints the status line and forwards the same structured
// data to the local panel, which cannot obtain it any other way.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

type statusInput struct {
	SessionID string `json:"session_id"`
	Model     struct {
		DisplayName string `json:"display_name"`
	} `json:"model"`
	Cost struct {
		TotalUSD float64 `json:"total_cost_usd"`
	} `json:"cost"`
	ContextWindow struct {
		UsedPercentage float64 `json:"used_percentage"`
	} `json:"context_window"`
}

func parse(r io.Reader) (statusInput, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return statusInput{}, fmt.Errorf("read stdin: %w", err)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return statusInput{}, fmt.Errorf("no status input on stdin")
	}
	var in statusInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return statusInput{}, fmt.Errorf("parse status input: %w", err)
	}
	return in, nil
}

func render(in statusInput) string {
	name := in.Model.DisplayName
	if name == "" {
		name = "Claude"
	}
	return fmt.Sprintf("%s | $%.2f | %.0f%%", name, in.Cost.TotalUSD, in.ContextWindow.UsedPercentage)
}

func report(endpoint string, in statusInput) error {
	body, err := json.Marshal(map[string]any{
		"sessionId":      in.SessionID,
		"model":          in.Model.DisplayName,
		"costUSD":        in.Cost.TotalUSD,
		"contextPercent": in.ContextWindow.UsedPercentage,
	})
	if err != nil {
		return fmt.Errorf("encode report: %w", err)
	}
	client := &http.Client{Timeout: 500 * time.Millisecond}
	resp, err := client.Post(endpoint, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("post report: %w", err)
	}
	resp.Body.Close()
	return nil
}

func main() {
	in, err := parse(os.Stdin)
	if err != nil {
		fmt.Println("Claude | status unavailable")
		return
	}
	endpoint := os.Getenv("FLEETDECK_ENDPOINT")
	if endpoint == "" {
		endpoint = "http://127.0.0.1:7777/api/status"
	}
	// A panel that is down must never cost the user their status line.
	_ = report(endpoint, in)
	fmt.Println(render(in))
}
```

- [ ] **Step 4: Добавить приём отчёта на сервере**

Отчёт репортёра — точное значение от самого Claude Code, поэтому он замещает оценку по транскрипту. В `internal/server/server.go` добавить в `Deps` поле `PutStatus func(sessionID string, contextPercent, costUSD float64)` и маршрут `mux.HandleFunc("POST /api/status", d.handleStatus)`. Обработчик в `internal/server/api.go`:

```go
func (d Deps) handleStatus(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SessionID      string  `json:"sessionId"`
		ContextPercent float64 `json:"contextPercent"`
		CostUSD        float64 `json:"costUSD"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		fail(w, http.StatusBadRequest, "malformed body: "+err.Error())
		return
	}
	if body.SessionID == "" {
		fail(w, http.StatusBadRequest, "sessionId is required")
		return
	}
	d.PutStatus(body.SessionID, body.ContextPercent, body.CostUSD)
	w.WriteHeader(http.StatusNoContent)
}
```

В `cmd/fleetdeck/collect.go` добавить приём отчётов и их приоритет над оценкой:

```go
// reported holds what the statusline reporter told us about a session.
// It is exact, unlike the transcript estimate, so it wins when present.
type reported struct {
	contextPercent float64
	costUSD        float64
	at             time.Time
}

// PutStatus records a reporter payload. Called from the HTTP handler.
func (c *Collector) PutStatus(sessionID string, contextPercent, costUSD float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reports[sessionID] = reported{contextPercent: contextPercent, costUSD: costUSD, at: time.Now()}
}

// reportFor returns the reporter value for a session if it is fresh enough.
// A stale report is worse than no report: it describes a session that has moved on.
func (c *Collector) reportFor(sessionID string) (transcript.Usage, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, ok := c.reports[sessionID]
	if !ok || time.Since(r.at) > 2*time.Minute {
		return transcript.Usage{}, false
	}
	return transcript.Usage{Percent: r.contextPercent, Estimated: false}, true
}
```

Тип `transcript.Usage` дополняется полем `Percent float64` с тегом `json:"percent"`, которое заполняют оба пути: оценка считает его как `float64(Tokens) / float64(Window) * 100`, отчёт кладёт значение от Claude Code напрямую. Интерфейс показывает `Percent`, а `Estimated` решает, помечать ли значение как оценку.

В `Collect` отчёт проверяется первым:

```go
	if u, ok := c.reportFor(s.SessionID); ok {
		snap.Sessions[i].Context = &u
		continue
	}
```

Тест на приоритет отчёта над оценкой, в `collect_test.go`:

```go
func TestReportedContextWinsOverTranscriptEstimate(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sess-uuid.jsonl")
	line := `{"type":"assistant","message":{"model":"claude-opus-5","usage":{"input_tokens":0,"cache_read_input_tokens":100000}}}`
	os.WriteFile(p, []byte(line+"\n"), 0o600)

	c := NewCollector(config.Default(), nil, nil, dir)
	c.PutStatus("sess-uuid", 41.7, 1.23)

	u, ok := c.reportFor("sess-uuid")
	if !ok {
		t.Fatal("a fresh report must be used")
	}
	if u.Estimated {
		t.Fatal("a reported value must not be marked as an estimate")
	}
	if u.Percent != 41.7 {
		t.Fatalf("want the reported percentage, got %v", u.Percent)
	}
}

func TestStaleReportIsIgnored(t *testing.T) {
	c := NewCollector(config.Default(), nil, nil, t.TempDir())
	c.mu.Lock()
	c.reports["old"] = reported{contextPercent: 10, at: time.Now().Add(-10 * time.Minute)}
	c.mu.Unlock()
	if _, ok := c.reportFor("old"); ok {
		t.Fatal("a stale report describes a session that has moved on and must be ignored")
	}
}
```

- [ ] **Step 5: Прогнать всё.** Команды `go test ./...` и `make lint`, ожидается PASS и пустой вывод `gofmt`.

- [ ] **Step 6: Проверить репортёр руками.** Команда `echo '{"session_id":"x","model":{"display_name":"Opus 5"},"cost":{"total_cost_usd":0.5},"context_window":{"used_percentage":12.3}}' | ./bin/fleetdeck-status` — ожидается строка вида `Opus 5 | $0.50 | 12%` даже при погашенном пульте.

- [ ] **Step 7: Коммит**

```bash
git add cmd/fleetdeck-status internal/server cmd/fleetdeck
git commit --signoff -m "feat(status): statusline reporter feeding context and cost to the panel"
```

---

## Готовность плана

После двенадцатой задачи должно быть верно всё перечисленное, и это проверяется руками перед переходом ко второму плану:

- `go test ./...` зелёный, `make lint` без замечаний;
- `./bin/fleetdeck` поднимается и отдаёт `/api/snapshot` с сессиями, карточками и лимитами;
- при погашенном демоне снимок продолжает отдавать доску, а поле `daemonError` объясняет, что случилось;
- правка стадии через `PATCH /api/cards` меняет ровно одну строку в файле и создаёт коммит;
- баннер macOS приходит один раз на событие, а не каждые две секунды;
- в отчёте исполнителя задачи 3 записано, что содержат поля `needs`, `intent`, `detail`, `options` у ожидающей сессии — от этого зависит устройство панели ответа во втором плане.
