// Package scripts holds the plugin's shell scripts. It carries no Go code of
// its own — only the tests that run those scripts, so that they are checked by
// `make test` and by CI. The board's own Python tests are run the same way,
// from plugin/templates, next to the files they test.
package scripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// templateDir is the board template the script upgrades a board towards. Tests
// use the real one rather than a fixture: what is being checked is the script's
// reading of a board against the template that ships, and a fixture would let
// the two drift apart without a word.
const templateDir = "../templates/board"

// upgrade runs upgrade-board.sh as the operator runs it and returns everything
// it printed. A non-zero exit fails the test on the spot: this script is a
// report, and a report that exits non-zero is a script somebody will switch off.
func upgrade(t *testing.T, args ...string) string {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not installed; the script's own verification cannot run")
	}
	cmd := exec.Command("bash", append([]string{"upgrade-board.sh"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("upgrade-board.sh %v failed: %v\n%s", args, err, out)
	}
	return string(out)
}

// boardFromTemplate is a board deployed from the template and untouched since:
// the state in which the script has nothing to say.
func boardFromTemplate(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if out, err := exec.Command("cp", "-R", templateDir+"/.", dir).CombinedOutput(); err != nil {
		t.Fatalf("copy the template: %v\n%s", err, out)
	}
	return dir
}

// snapshot is every file under dir with its contents, for proving that a dry
// run changed nothing.
func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		files[rel] = string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestDryRunReportsAStaleScriptAndDoesNotTouchIt(t *testing.T) {
	dir := boardFromTemplate(t)
	script := filepath.Join(dir, "scripts", "validate_cards.py")
	if err := os.WriteFile(script, []byte("# an old copy\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, dir)

	out := upgrade(t, "--dry-run", dir)
	if !strings.Contains(out, "scripts/validate_cards.py") {
		t.Fatalf("the stale script must be named:\n%s", out)
	}
	if got := snapshot(t, dir); got[filepath.Join("scripts", "validate_cards.py")] != before[filepath.Join("scripts", "validate_cards.py")] {
		t.Fatal("a dry run rewrote the script it was only asked to report")
	}
}

func TestDryRunReportsAMissingFileAndDoesNotCreateIt(t *testing.T) {
	dir := boardFromTemplate(t)
	if err := os.Remove(filepath.Join(dir, "README.md")); err != nil {
		t.Fatal(err)
	}

	out := upgrade(t, "--dry-run", dir)
	if !strings.Contains(out, "README.md") {
		t.Fatalf("the missing file must be named:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "README.md")); !os.IsNotExist(err) {
		t.Fatal("a dry run created the file it was only asked to report")
	}
}

// The case the dry run exists for on the other side: the board is ahead of the
// template. Nothing is copied back — the script cannot write to the template,
// and would not be right to — so all it can do is say the name out loud.
func TestDryRunNamesAScriptTheBoardHasAndTheTemplateDoesNot(t *testing.T) {
	dir := boardFromTemplate(t)
	if err := os.WriteFile(filepath.Join(dir, "scripts", "new_card.py"), []byte("# grown on the board\n"), 0o700); err != nil {
		t.Fatal(err)
	}

	out := upgrade(t, "--dry-run", dir)
	if !strings.Contains(out, "scripts/new_card.py") {
		t.Fatalf("a script the template does not have must be named:\n%s", out)
	}
}

// The parity report is not a dry-run feature: a script that only speaks when
// asked to speak is a script nobody asks. Running the upgrade for real says it
// too, because the upgrade cannot fix this half by itself.
func TestScriptsTheTemplateLacksAreNamedInANormalRunAsWell(t *testing.T) {
	dir := boardFromTemplate(t)
	if err := os.WriteFile(filepath.Join(dir, "scripts", "new_card.py"), []byte("# grown on the board\n"), 0o700); err != nil {
		t.Fatal(err)
	}

	out := upgrade(t, dir)
	if !strings.Contains(out, "scripts/new_card.py") {
		t.Fatalf("a normal run must name it too:\n%s", out)
	}
}

// A check that fires every time is a check that gets scrolled past, and the
// real findings get scrolled past with it. On a board that matches the
// template, the script says so and names nothing.
func TestABoardMadeFromTheTemplateIsReportedAsMatchingAndNothingIsNamed(t *testing.T) {
	dir := boardFromTemplate(t)

	out := upgrade(t, "--dry-run", dir)
	if !strings.Contains(out, "соответствует шаблону") {
		t.Fatalf("an untouched board must be reported as matching:\n%s", out)
	}
	if strings.Contains(out, "нет в шаблоне") {
		t.Fatalf("nothing must be named on an untouched board:\n%s", out)
	}
}

// Running the board's tests in place leaves a __pycache__ behind, and the
// board's own .gitignore keeps it out of the board. Reporting it would make the
// check fire on every board that ever ran its tests — which is the same as not
// having the check.
func TestAPycacheDirectoryIsNotReportedAsDrift(t *testing.T) {
	dir := boardFromTemplate(t)
	if err := os.MkdirAll(filepath.Join(dir, "scripts", "__pycache__"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "scripts", "__pycache__", "validate_cards.pyc"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	out := upgrade(t, "--dry-run", dir)
	if strings.Contains(out, "__pycache__") {
		t.Fatalf("a __pycache__ must not be reported:\n%s", out)
	}
}

// Without --dry-run the script still does what it has always done: it fixes.
func TestWithoutDryRunAStaleScriptIsUpdated(t *testing.T) {
	dir := boardFromTemplate(t)
	script := filepath.Join(dir, "scripts", "validate_cards.py")
	if err := os.WriteFile(script, []byte("# an old copy\n"), 0o700); err != nil {
		t.Fatal(err)
	}

	upgrade(t, dir)

	updated, err := os.ReadFile(script)
	if err != nil {
		t.Fatal(err)
	}
	fromTemplate, err := os.ReadFile(filepath.Join(templateDir, "scripts", "validate_cards.py"))
	if err != nil {
		t.Fatal(err)
	}
	if string(updated) != string(fromTemplate) {
		t.Fatal("a real run must bring the script back to the template's copy")
	}
}

// The order of the arguments is not something the operator should have to
// remember, and a flag that only works in one position fails silently in the
// other: the path would be read as the flag and the board's own directory
// would go unchecked.
func TestTheFlagIsAcceptedOnEitherSideOfThePath(t *testing.T) {
	dir := boardFromTemplate(t)
	if err := os.Remove(filepath.Join(dir, "README.md")); err != nil {
		t.Fatal(err)
	}

	for _, args := range [][]string{{"--dry-run", dir}, {dir, "--dry-run"}} {
		out := upgrade(t, args...)
		if !strings.Contains(out, "README.md") {
			t.Fatalf("%v: the missing file must be named:\n%s", args, out)
		}
		if _, err := os.Stat(filepath.Join(dir, "README.md")); !os.IsNotExist(err) {
			t.Fatalf("%v: a dry run created the file", args)
		}
	}
}

func TestAnUnknownFlagIsRefusedRatherThanTakenForAPath(t *testing.T) {
	dir := boardFromTemplate(t)
	cmd := exec.Command("bash", "upgrade-board.sh", "--drynrun", dir)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("a misspelled flag must be refused, not obeyed:\n%s", out)
	}
	if !strings.Contains(string(out), "--drynrun") {
		t.Fatalf("the refusal must name what it did not understand:\n%s", out)
	}
}
