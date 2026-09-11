// Package orchestrator appoints the session a fleet is led from: it writes the
// orchestrator's working order where the session can read it, tells the
// session to read it, and pins the session to the panel's orchestrator column.
//
// A new session and an existing one are appointed the same way — the same
// file, the same one-line message, the same pin — and differ only in that a
// new one is started first. Two paths that loaded the knowledge differently
// would drift apart, and the difference would surface only as two
// orchestrators that behave differently.
//
// Why a file and a line rather than the working order as the message: a reply
// is submitted to the session's prompt, and a long, multi-line one can be left
// there as an unsent paste instead of starting a turn. The claude-agents
// fleet tooling found this and has to press Enter after such replies itself.
// One line has nothing to collapse, and the file stays on disk for the
// orchestrator to read again once its context has been compacted.
package orchestrator

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/kroticw/fleetdeck/docs"
)

// marker is the brief's first line. A file that does not start with it was not
// written by fleetdeck, and is never replaced.
const marker = "<!-- fleetdeck: the orchestrator's brief, written by the orchestrator wizard and rewritten each time it runs -->"

// briefName is the brief's file name inside the directory BriefPath picks.
const briefName = "orchestrator.md"

// ErrNotOurs is WriteBrief refusing a file fleetdeck did not write.
var ErrNotOurs = errors.New("not written by fleetdeck")

// Lang is the language the working order is given in: Russian for any "ru"
// tag, English for everything else — the two languages it is written in.
func Lang(tag string) string {
	tag = strings.ToLower(tag)
	if tag == "ru" || strings.HasPrefix(tag, "ru-") {
		return "ru"
	}
	return "en"
}

// Knowledge is the orchestrator's working order, docs/<lang>/orchestrator.md.
func Knowledge(lang string) ([]byte, error) {
	return fs.ReadFile(docs.Orchestrator, Lang(lang)+"/orchestrator.md")
}

// Paths are the places the orchestrator is told about: the panel's board, its
// documentation directories and its configuration file.
type Paths struct {
	Board  string
	Docs   []string
	Config string
}

// BriefPath is where the brief is written: the first documentation directory,
// which the panel's documentation section shows, or the board when the panel
// has no documentation directory.
func BriefPath(p Paths) string {
	if len(p.Docs) > 0 {
		return filepath.Join(p.Docs[0], briefName)
	}
	return filepath.Join(p.Board, briefName)
}

// Brief is the file an orchestrator reads: the marker, where this panel keeps
// its board, documentation and configuration, then the working order itself.
func Brief(lang string, p Paths) ([]byte, error) {
	knowledge, err := Knowledge(lang)
	if err != nil {
		return nil, err
	}
	w := words[Lang(lang)]
	quote := func(paths []string) string {
		q := make([]string, len(paths))
		for i, s := range paths {
			q[i] = "`" + s + "`"
		}
		return strings.Join(q, ", ")
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, "%s\n\n> %s\n>\n", marker, w.written)
	board := fmt.Sprintf(w.board, "`"+p.Board+"`", "`"+filepath.Join(p.Board, "cards")+"`")
	if _, err := os.Stat(filepath.Join(p.Board, "README.md")); err == nil {
		board += fmt.Sprintf(w.boardRules, "`"+filepath.Join(p.Board, "README.md")+"`")
	}
	fmt.Fprintf(&b, "> - %s\n", board)
	if len(p.Docs) > 0 {
		fmt.Fprintf(&b, "> - %s\n", fmt.Sprintf(w.docs, quote(p.Docs)))
	}
	if p.Config != "" {
		fmt.Fprintf(&b, "> - %s\n", fmt.Sprintf(w.config, "`"+p.Config+"`"))
	}
	b.WriteString("\n")
	b.Write(knowledge)
	return b.Bytes(), nil
}

// Message is what the appointed session is sent: one line, the same whichever
// session is appointed.
func Message(lang, briefPath string) string {
	return fmt.Sprintf(words[Lang(lang)].message, "`"+briefPath+"`")
}

// WriteBrief puts content at path, replacing a brief fleetdeck wrote before
// and refusing anything else there. The directory must exist: it is one the
// configuration names, and making it is setup's business, not this one's.
//
// The file is written beside its place and renamed in, so a session reading it
// never sees half of it.
func WriteBrief(path string, content []byte) error {
	existing, err := os.ReadFile(path)
	switch {
	case err == nil:
		if !bytes.HasPrefix(existing, []byte(marker)) {
			return fmt.Errorf("%s is %w: move it aside or rename it, and the wizard will write its own", path, ErrNotOurs)
		}
	case !errors.Is(err, fs.ErrNotExist):
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+briefName+".*")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(content)
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return nil
}

// words are the brief's head, the message and a new session's name, in each language the working
// order is written in.
var words = map[string]struct {
	written, board, boardRules, docs, config, message, sessionName string
}{
	"en": {
		written:    "fleetdeck's orchestrator wizard wrote this file when it appointed this panel's orchestrator, and will write it again the next time it runs.",
		board:      "The board: %s, its cards in %s",
		boardRules: "; the board's own rules are in %s",
		docs:       "Documentation: %s",
		config:     "The panel's configuration: %s",
		message:    "fleetdeck: this session has been appointed the fleet's orchestrator. Read the whole of %s and work by it from now on. If you had a task of your own before this message, do not drop it silently: tell the operator where you stopped.",
		// What a new session is called in the daemon's list and the panel's.
		sessionName: "orchestrator",
	},
	"ru": {
		written:     "Этот файл записал мастер fleetdeck, когда назначал оркестратора этой панели, и перепишет при следующем прохождении.",
		board:       "Доска: %s, карточки — в %s",
		boardRules:  "; правила самой доски — в %s",
		docs:        "Документация: %s",
		config:      "Настройки панели: %s",
		message:     "fleetdeck: эта сессия назначена оркестратором флота. Прочитай целиком файл %s и дальше работай по нему. Если до этого сообщения у тебя была своя задача, не бросай её молча: скажи оператору, в каком она состоянии.",
		sessionName: "оркестратор",
	},
}
