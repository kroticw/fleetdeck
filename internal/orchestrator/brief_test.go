package orchestrator

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The embedded working order is the repository's file byte for byte, in both
// languages. A copy that drifted from docs/ would hand every new orchestrator
// rules nobody reviewed.
func TestKnowledgeIsTheRepositoryFile(t *testing.T) {
	for _, lang := range []string{"en", "ru"} {
		want, err := os.ReadFile(filepath.Join("..", "..", "docs", lang, "orchestrator.md"))
		if err != nil {
			t.Fatal(err)
		}
		got, err := Knowledge(lang)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("Knowledge(%q) is not docs/%s/orchestrator.md", lang, lang)
		}
	}
}

func TestLangIsRussianOrEnglish(t *testing.T) {
	for in, want := range map[string]string{
		"ru": "ru", "ru-RU": "ru", "RU": "ru",
		"en": "en", "en-US": "en", "de": "en", "": "en", "rus": "en",
	} {
		if got := Lang(in); got != want {
			t.Errorf("Lang(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBriefNamesWhereThingsAreThenCarriesTheKnowledge(t *testing.T) {
	board := t.TempDir()
	p := Paths{Board: board, Docs: []string{"/w/docs", "/w/more docs"}, Config: "/h/.config/fleetdeck/config.yaml"}
	for _, lang := range []string{"en", "ru"} {
		got, err := Brief(lang, p)
		if err != nil {
			t.Fatal(err)
		}
		knowledge, _ := Knowledge(lang)
		if !bytes.HasPrefix(got, []byte(marker+"\n")) {
			t.Errorf("%s: the brief does not open with the marker line:\n%s", lang, got)
		}
		if !bytes.HasSuffix(got, knowledge) {
			t.Errorf("%s: the brief does not end with the whole working order", lang)
		}
		head := string(got[:len(got)-len(knowledge)])
		for _, want := range []string{
			"`" + board + "`",
			"`" + filepath.Join(board, "cards") + "`",
			"`/w/docs`",
			"`/w/more docs`",
			"`/h/.config/fleetdeck/config.yaml`",
		} {
			if !strings.Contains(head, want) {
				t.Errorf("%s: the brief's head does not name %s:\n%s", lang, want, head)
			}
		}
	}
}

// The board's own rules are pointed at only where they exist: a brief naming a
// file that is not there sends the orchestrator looking for it.
func TestBriefPointsAtTheBoardReadmeOnlyWhenItExists(t *testing.T) {
	board := t.TempDir()
	readme := "`" + filepath.Join(board, "README.md") + "`"
	for _, lang := range []string{"en", "ru"} {
		got, err := Brief(lang, Paths{Board: board, Docs: []string{"/d"}})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(got), readme) {
			t.Errorf("%s: the brief names %s, which does not exist", lang, readme)
		}
	}
	if err := os.WriteFile(filepath.Join(board, "README.md"), []byte("# board\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, lang := range []string{"en", "ru"} {
		got, err := Brief(lang, Paths{Board: board, Docs: []string{"/d"}})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(got), readme) {
			t.Errorf("%s: the brief does not name the board's README.md:\n%s", lang, got)
		}
	}
}

func TestBriefPathIsTheFirstDocsDirectoryElseTheBoard(t *testing.T) {
	if got := BriefPath(Paths{Board: "/w/board", Docs: []string{"/w/docs", "/x"}}); got != "/w/docs/orchestrator.md" {
		t.Errorf("BriefPath = %q", got)
	}
	if got := BriefPath(Paths{Board: "/w/board"}); got != "/w/board/orchestrator.md" {
		t.Errorf("BriefPath without docs = %q", got)
	}
}

func TestWriteBriefCreatesAndReplacesItsOwnFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orchestrator.md")
	first := []byte(marker + "\nfirst\n")
	if err := WriteBrief(path, first); err != nil {
		t.Fatal(err)
	}
	second := []byte(marker + "\nsecond\n")
	if err := WriteBrief(path, second); err != nil {
		t.Fatalf("rewriting its own brief: %v", err)
	}
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, second) {
		t.Errorf("the file holds %q, want %q", got, second)
	}
}

// A person's own orchestrator.md in their documentation is theirs: the wizard
// refuses rather than replacing it, and says what to do.
func TestWriteBriefRefusesAFileItDidNotWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orchestrator.md")
	theirs := []byte("# my own notes on orchestration\n")
	if err := os.WriteFile(path, theirs, 0o600); err != nil {
		t.Fatal(err)
	}
	err := WriteBrief(path, []byte(marker+"\nbrief\n"))
	if !errors.Is(err, ErrNotOurs) {
		t.Fatalf("WriteBrief over a person's file: err = %v, want ErrNotOurs", err)
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("the refusal does not name the file: %v", err)
	}
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, theirs) {
		t.Errorf("the person's file was changed to %q", got)
	}
}

// A file that cannot be read cannot be shown to be fleetdeck's, so it is not
// replaced either.
func TestWriteBriefRefusesAFileItCannotRead(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a file whatever its mode")
	}
	path := filepath.Join(t.TempDir(), "orchestrator.md")
	if err := os.WriteFile(path, []byte("# mine\n"), 0o000); err != nil {
		t.Fatal(err)
	}
	if err := WriteBrief(path, []byte(marker+"\nbrief\n")); err == nil {
		t.Fatal("replaced a file it could not read")
	}
	_ = os.Chmod(path, 0o600)
	got, _ := os.ReadFile(path)
	if string(got) != "# mine\n" {
		t.Errorf("the unreadable file was changed to %q", got)
	}
}

// A documentation directory that is not there is reported, not made: the
// wizard writes one file into a directory the configuration names, and
// creating directories is setup's business.
func TestWriteBriefDoesNotMakeTheDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "missing")
	err := WriteBrief(filepath.Join(dir, "orchestrator.md"), []byte(marker+"\n"))
	if err == nil {
		t.Fatal("WriteBrief into a missing directory succeeded")
	}
	if _, statErr := os.Stat(dir); !errors.Is(statErr, fs.ErrNotExist) {
		t.Errorf("the missing directory was created: %v", statErr)
	}
}

// The message is one line. A long or multi-line reply can be left in the
// session's input box as an unsent paste instead of being submitted — which is
// why the working order travels as a file and only this line as the message.
func TestMessageIsOneLineNamingTheBrief(t *testing.T) {
	for _, lang := range []string{"en", "ru"} {
		got := Message(lang, "/w/docs/orchestrator.md")
		if strings.ContainsAny(got, "\r\n") {
			t.Errorf("%s: the message has more than one line: %q", lang, got)
		}
		if !strings.Contains(got, "`/w/docs/orchestrator.md`") {
			t.Errorf("%s: the message does not name the brief: %q", lang, got)
		}
	}
	if Message("ru", "/p") == Message("en", "/p") {
		t.Error("the Russian and English messages are the same text")
	}
}

// The preview is what the wizard shows before anything is done, and it is
// built from the same functions the appointment uses: the page cannot promise
// one message and the panel send another.
func TestPreviewIsWhatAnAppointmentWillDo(t *testing.T) {
	p := workspace(t)
	a := &Appointer{Paths: p, Start: func(context.Context, string, string) (string, error) { return "", nil }}
	for _, lang := range []string{"en", "ru"} {
		got, err := a.Preview(lang)
		if err != nil {
			t.Fatal(err)
		}
		brief, _ := Brief(lang, p)
		want := Preview{
			Path:      BriefPath(p),
			Message:   Message(lang, BriefPath(p)),
			Brief:     string(brief),
			Workspace: filepath.Dir(p.Board),
			Name:      words[lang].sessionName,
			CanStart:  true,
		}
		if got != want {
			t.Errorf("%s: Preview = %+v\nwant %+v", lang, got, want)
		}
	}
	a.Start = nil
	if got, _ := a.Preview("en"); got.CanStart {
		t.Error("a panel that cannot start sessions previews that it can")
	}
	if _, err := os.Stat(BriefPath(p)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a preview wrote the brief: %v", err)
	}
}

// The panel and the wizard answer "is this fleetdeck's brief?" with one rule.
// If they drifted, the panel could call a file ours that the wizard then
// refuses to replace, or call foreign a file the wizard would overwrite
// without a word -- and the operator would be told to move aside something
// that was never in the way. So ReadBriefState says foreign exactly when
// WriteBrief refuses with ErrNotOurs, for every shape of content the rule can
// see.
func TestReadBriefStateAgreesWithWriteBrief(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
		want    BriefState
	}{
		{"the wizard's own brief", marker + "\n\n> written\n", BriefOurs},
		{"the marker and nothing else", marker, BriefOurs},
		{"a person's file", "# my orchestrator notes\n", BriefForeign},
		{"an empty file", "", BriefForeign},
		{"the marker cut short", marker[:20], BriefForeign},
		{"the marker below a first line", "# notes\n" + marker + "\n", BriefForeign},
		{"the marker after a space", " " + marker + "\n", BriefForeign},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "orchestrator.md")
			if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}
			if got := ReadBriefState(path); got != tc.want {
				t.Errorf("ReadBriefState = %v, want %v", got, tc.want)
			}
			refused := errors.Is(WriteBrief(path, []byte(marker+"\n")), ErrNotOurs)
			if refused != (tc.want == BriefForeign) {
				t.Errorf("the panel says %v and WriteBrief refused = %v: the two rules have drifted", tc.want, refused)
			}
		})
	}
}

// Nothing at the path, a directory of that name, and a file the panel may not
// read are none of them a working order a session can read -- and none of
// them is something WriteBrief refuses as not ours. Calling any of them
// foreign would tell the operator to move aside a file that is not in the way.
func TestReadBriefStateCountsWhatCannotBeReadAsNone(t *testing.T) {
	if got := ReadBriefState(filepath.Join(t.TempDir(), "orchestrator.md")); got != BriefNone {
		t.Errorf("nothing there: ReadBriefState = %v, want BriefNone", got)
	}

	asDir := filepath.Join(t.TempDir(), "orchestrator.md")
	if err := os.Mkdir(asDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if got := ReadBriefState(asDir); got != BriefNone {
		t.Errorf("a directory: ReadBriefState = %v, want BriefNone", got)
	}

	if os.Geteuid() == 0 {
		t.Skip("root reads a file whatever its mode")
	}
	locked := filepath.Join(t.TempDir(), "orchestrator.md")
	if err := os.WriteFile(locked, []byte("# mine\n"), 0o000); err != nil {
		t.Fatal(err)
	}
	if got := ReadBriefState(locked); got != BriefNone {
		t.Errorf("an unreadable file: ReadBriefState = %v, want BriefNone", got)
	}
}
