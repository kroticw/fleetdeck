package board

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const codexSession = "codex:01a1a6d4-90a4-70f3-9130-a59ec873d0ba"

func TestCardExecutorSelection(t *testing.T) {
	for _, tt := range []struct {
		name, fields, session, want string
		invalid                     bool
	}{
		{"legacy", "", "abc12345", "claude", false},
		{"explicit Claude", "executor: claude\n", "abc12345", "claude", false},
		{"Codex", "executor: codex\n", codexSession, "codex", false},
		{"unbound Codex", "executor: codex\n", "", "codex", false},
		{"unknown", "executor: other\n", "", "", true},
		{"Codex with Claude session", "executor: codex\n", "abc12345", "", true},
		{"implicit Claude with Codex session", "", codexSession, "", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			body := strings.Replace(sample, "session: abc12345\n", "session: \""+tt.session+"\"\n"+tt.fields, 1)
			path := writeCard(t, t.TempDir(), "card.md", body)
			card, err := ParseCard(path)
			if err != nil {
				t.Fatal(err)
			}
			if tt.invalid {
				if card.ParseError == "" {
					t.Fatal("invalid executor/session pair accepted")
				}
				return
			}
			if card.ParseError != "" || card.Executor != tt.want || card.Session != tt.session {
				t.Fatalf("got executor=%q session=%q error=%q", card.Executor, card.Session, card.ParseError)
			}
		})
	}
}

func TestCreateCardRecordsExecutorAndRejectsUnknownBeforeClaim(t *testing.T) {
	dir := emptyBoard(t)
	if _, err := CreateCard(dir, NewCard{Title: "Task", Zone: "planned", Executor: "other"}, createDay); !errors.Is(err, ErrInvalidCard) {
		t.Fatalf("unknown executor: %v", err)
	}
	path, err := CreateCard(dir, NewCard{Title: "Task", Zone: "planned", Executor: "codex"}, createDay)
	if err != nil {
		t.Fatal(err)
	}
	card, err := ParseCard(path)
	if err != nil || card.Executor != "codex" || card.ID != "T-001" {
		t.Fatalf("card=%+v err=%v", card, err)
	}
}

func TestSessionWriteChecksExecutor(t *testing.T) {
	for _, provider := range []string{"claude", "codex"} {
		t.Run(provider, func(t *testing.T) {
			body := strings.Replace(sample, "session: abc12345", "session: \"\"\nexecutor: "+provider, 1)
			path := writeCard(t, t.TempDir(), "card.md", body)
			correct, wrong := "abc12345", codexSession
			if provider == "codex" {
				correct, wrong = wrong, correct
			}
			if err := SetField(path, "session", wrong, nil); err == nil {
				t.Fatal("cross-executor session accepted")
			}
			raw, err := os.ReadFile(path)
			if err != nil || string(raw) != body {
				t.Fatalf("refused write changed card: %v", err)
			}
			empty := ""
			if err := SetField(path, "session", correct, &empty); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCodexDocumentSession(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.md")
	if err := os.WriteFile(path, []byte("---\nsession: "+codexSession+"\n---\nReport\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := DocSession(path); got != codexSession {
		t.Fatalf("got %q", got)
	}
}
