package board

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// newCardPy is the board script that claims numbers for everyone who is not the
// panel: the orchestrator and the sessions themselves. The panel must claim
// them the same way, so several tests here run it against the same board.
func newCardPy(t *testing.T) (python, script string) {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not installed; the board scripts cannot run here")
	}
	script, err = filepath.Abs(filepath.Join("..", "..", "plugin", "templates", "board", "scripts", "new_card.py"))
	if err != nil {
		t.Fatal(err)
	}
	return python, script
}

func TestCreateCardClaimsTheFirstNumberOnAnEmptyBoard(t *testing.T) {
	dir := emptyBoard(t)
	path, err := CreateCard(dir, "A task", "planned", createDay)
	if err != nil {
		t.Fatal(err)
	}
	if got := filepath.Base(path); got != "T-001-2026-09-11-a-task.md" {
		t.Fatalf("file name = %s", got)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "id: T-001\n") {
		t.Fatalf("the card carries no id:\n%s", raw)
	}
}

// The marker file is the claim. It is what makes two numbers impossible to hand
// out twice, and it is the one thing the Go side and the Python side must agree
// on byte for byte: the same directory, the same name.
func TestCreateCardLeavesItsMarkerInTheRegistry(t *testing.T) {
	dir := emptyBoard(t)
	if _, err := CreateCard(dir, "A task", "planned", createDay); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, idsDir, "T-001")); err != nil {
		t.Fatalf("no marker for the number the card was given: %v", err)
	}
}

func TestCreateCardTakesTheNextNumberAfterTheCardsOnTheBoard(t *testing.T) {
	dir := emptyBoard(t)
	write(t, filepath.Join(CardsDir(dir), "T-007-2026-09-10-older.md"), "---\nid: T-007\n---\n")
	path, err := CreateCard(dir, "A task", "planned", createDay)
	if err != nil {
		t.Fatal(err)
	}
	if got := filepath.Base(path); !strings.HasPrefix(got, "T-008-") {
		t.Fatalf("file name = %s, want the number after T-007", got)
	}
}

// A card moved to the archive keeps its number, so the archive has to be read
// too: without it the maximum drops back and a number is handed out twice.
func TestCreateCardCountsTheArchiveAsWell(t *testing.T) {
	dir := emptyBoard(t)
	if err := os.MkdirAll(filepath.Join(dir, "archive"), 0o700); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "archive", "T-012-2026-09-01-done.md"), "---\nid: T-012\n---\n")
	path, err := CreateCard(dir, "A task", "planned", createDay)
	if err != nil {
		t.Fatal(err)
	}
	if got := filepath.Base(path); !strings.HasPrefix(got, "T-013-") {
		t.Fatalf("file name = %s, want the number after the archived T-012", got)
	}
}

// A marker without a card is the trace of a creation that failed halfway. The
// number stays spent: releasing it would hand the same number to two cards, one
// of which is already on someone's screen.
func TestCreateCardDoesNotReuseANumberHeldOnlyByAMarker(t *testing.T) {
	dir := emptyBoard(t)
	if err := os.MkdirAll(filepath.Join(dir, idsDir), 0o700); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, idsDir, "T-005"), "")
	path, err := CreateCard(dir, "A task", "planned", createDay)
	if err != nil {
		t.Fatal(err)
	}
	if got := filepath.Base(path); !strings.HasPrefix(got, "T-006-") {
		t.Fatalf("file name = %s, want the number after the claimed T-005", got)
	}
}

// Cards are started in parallel — by the operator at the panel, by the
// orchestrator and by the sessions themselves. "Read the maximum, add one" hands
// one number to two cards; the claim is what makes that impossible.
func TestCreateCardGivesEveryConcurrentCardItsOwnNumber(t *testing.T) {
	dir := emptyBoard(t)
	const n = 16

	var wg sync.WaitGroup
	paths := make([]string, n)
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			paths[i], errs[i] = CreateCard(dir, "Task", "planned", createDay)
		}()
	}
	wg.Wait()

	seen := map[string]int{}
	for i, err := range errs {
		if err != nil {
			t.Fatalf("card %d: %v", i, err)
		}
		id := filepath.Base(paths[i])[:5]
		if first, ok := seen[id]; ok {
			t.Fatalf("%s was handed to cards %d and %d", id, first, i)
		}
		seen[id] = i
	}
}

// The point of the port: the panel and the board's own script must be one
// source of numbers, not two. They are checked against each other rather than
// each against its own idea of the registry.
func TestCreateCardAndNewCardPyDoNotCollide(t *testing.T) {
	python, script := newCardPy(t)
	dir := emptyBoard(t)

	first, err := CreateCard(dir, "From the panel", "planned", createDay)
	if err != nil {
		t.Fatal(err)
	}
	if got := filepath.Base(first); !strings.HasPrefix(got, "T-001-") {
		t.Fatalf("the panel's card = %s, want T-001", got)
	}

	out, err := exec.Command(python, script,
		"--board", dir, "--title", "From the script", "--slug", "from-the-script",
		"--zone", "planned", "--created", "2026-09-11",
	).CombinedOutput()
	if err != nil {
		t.Fatalf("new_card.py failed: %v\n%s", err, out)
	}
	if got := filepath.Base(strings.TrimSpace(string(out))); !strings.HasPrefix(got, "T-002-") {
		t.Fatalf("the script's card = %s, want T-002 — the two are not sharing the registry", got)
	}

	third, err := CreateCard(dir, "From the panel again", "planned", createDay)
	if err != nil {
		t.Fatal(err)
	}
	if got := filepath.Base(third); !strings.HasPrefix(got, "T-003-") {
		t.Fatalf("the panel's second card = %s, want T-003", got)
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
