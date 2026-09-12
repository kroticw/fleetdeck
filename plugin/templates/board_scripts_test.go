package templates

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// boardScripts holds the board's Python scripts and their tests. They ship in
// the template, so this package is where they are run from: `make test` and CI
// reach them through go test, with no second language in the CI matrix.
const boardScripts = "board/scripts"

var (
	// A test method as unittest collects it: a def named test* inside a class.
	// A def at column 0 is a module-level function, which unittest does not run.
	declaredTestRE = regexp.MustCompile(`(?m)^[ \t]+def (test\w*)\(`)

	// A passing result line of `unittest --verbose`. A test with a docstring has
	// the docstring's first line printed on a line of its own between its name
	// and its verdict, so the verdict may sit one line below the name.
	passedTestRE = regexp.MustCompile(`(?m)^(test\w*) \((\w+)\.[\w.]+\)(?:\n[^\n]*)? \.\.\. ok$`)

	ranRE = regexp.MustCompile(`(?m)^Ran (\d+) tests? in `)
)

// The board's Python tests are run the way bootstrap-board.sh and
// upgrade-board.sh run them, and then checked against their own source.
//
// A zero exit from unittest is not enough on its own. A test that is skipped,
// never collected, or shadowed by a second method of the same name leaves
// nothing standing to fail, and the run exits zero while the check it was
// written to be never happens. So every test method the source declares must be
// reported as passed by name, and the number unittest says it ran must be the
// number the source declares.
//
// python3 missing fails the test instead of skipping it: a skipped run reads as
// a green one, and this is an unchecked board, not a passing one.
func TestTheBoardsPythonTestsAllRunAndPass(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(boardScripts, "test_*.py"))
	if err != nil {
		t.Fatal(err)
	}
	declared := map[string]int{}
	total := 0
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		module := strings.TrimSuffix(filepath.Base(file), ".py")
		for _, m := range declaredTestRE.FindAllStringSubmatch(string(data), -1) {
			declared[module+"."+m[1]]++
			total++
		}
	}
	if total == 0 {
		t.Fatalf("no test methods found in %s/test_*.py: this check would pass vacuously", boardScripts)
	}

	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatalf("python3 is not on PATH, so the board's %d Python tests cannot run: "+
			"this is an unchecked board, not a passing one — install python3", total)
	}

	cmd := exec.Command(python, "-m", "unittest", "discover",
		"--start-directory", boardScripts, "--pattern", "test_*.py", "--verbose")
	// No __pycache__ left inside the template, and no colour codes in the
	// output this test reads.
	cmd.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1", "PYTHON_COLORS=0", "NO_COLOR=1")
	raw, err := cmd.CombinedOutput()
	out := string(raw)
	if err != nil {
		t.Fatalf("the board's Python tests failed: %v\n%s", err, out)
	}

	passed := map[string]int{}
	for _, m := range passedTestRE.FindAllStringSubmatch(out, -1) {
		passed[m[2]+"."+m[1]]++
	}
	var missing []string
	for name, n := range declared {
		if passed[name] < n {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("declared in the source but never reported as passed by unittest "+
			"(skipped, not collected, or shadowed by a method of the same name):\n  %s\n\n%s",
			strings.Join(missing, "\n  "), out)
	}

	m := ranRE.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("unittest did not say how many tests it ran:\n%s", out)
	}
	if ran, _ := strconv.Atoi(m[1]); ran != total {
		t.Fatalf("unittest ran %d tests, the source declares %d: "+
			"a test is defined in a way this check does not read, so update declaredTestRE\n%s",
			ran, total, out)
	}
	t.Logf("%d declared Python tests, all reported as passed by unittest", total)
}
