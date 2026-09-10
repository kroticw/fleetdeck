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
