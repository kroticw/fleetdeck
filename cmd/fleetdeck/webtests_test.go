package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestRunWebTestsScript proves scripts/run-web-tests.sh against the real
// script, not a description of it — the same reason TestDistBuildsVerifiedArchives
// runs the real `make dist` instead of trusting verify-dist.sh's own exit code.
// A shell script asserting its own correctness is one edit away from checking
// nothing, and the failure this script exists to catch (a test that never
// actually runs) is exactly the kind of thing that looks fine until someone
// checks.
func TestRunWebTestsScript(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not on PATH, so the frontend test runner cannot be exercised here")
	}

	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(repoRoot, "scripts", "run-web-tests.sh")

	run := func(t *testing.T, root string) (string, error) {
		t.Helper()
		cmd := exec.Command("sh", scriptPath, root)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	writeFixture := func(t *testing.T, root, name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("a matching suite passes", func(t *testing.T) {
		root := t.TempDir()
		writeFixture(t, root, "good.test.js", `
import test from "node:test";
import assert from "node:assert/strict";
test("a", () => { assert.equal(1, 1); });
test("b", () => { assert.equal(1, 1); });
`)
		out, err := run(t, root)
		if err != nil {
			t.Fatalf("a suite where every declared test actually runs must pass, got error %v, output:\n%s", err, out)
		}
		if !strings.Contains(out, "2 declared tests") {
			t.Fatalf("want the script to report the count it verified, got:\n%s", out)
		}
	})

	// This is the precise case a plain declared-vs-executed COUNT comparison
	// does not catch: node's test runner gives a file that registers zero real
	// tests one synthetic "file passed" entry of its own, which can cancel out
	// exactly one skipped test's absence from the count. Comparing test names
	// instead of a count is what closes it — see run-web-tests.sh's own
	// comment for how this was found.
	t.Run("a test sitting behind a condition that never holds is caught by name", func(t *testing.T) {
		root := t.TempDir()
		writeFixture(t, root, "conditional.test.js", `
import test from "node:test";
import assert from "node:assert/strict";
test("a", () => { assert.equal(1, 1); });
const neverTrueInCI = false;
if (neverTrueInCI) {
test("b, never runs", () => { assert.equal(1, 1); });
}
`)
		out, err := run(t, root)
		if err == nil {
			t.Fatalf("a declared test that never runs must fail the check, got a clean pass:\n%s", out)
		}
		if !strings.Contains(out, "b, never runs") {
			t.Fatalf("the failure must name the specific test that never ran, got:\n%s", out)
		}
	})

	t.Run("a file that throws on import is caught by node's own exit code", func(t *testing.T) {
		root := t.TempDir()
		writeFixture(t, root, "broken.test.js", `
import test from "node:test";
throw new Error("boom during import");
test("c", () => {});
`)
		out, err := run(t, root)
		if err == nil {
			t.Fatalf("a file that throws before registering its tests must fail, got a clean pass:\n%s", out)
		}
		if !strings.Contains(out, "node --test reported a failure") {
			t.Fatalf("want the node-failure branch, not the name-comparison branch, got:\n%s", out)
		}
	})

	// The declared-name extractor only understands a flat test() call at
	// column 0, which is every file's actual shape today. describe() would
	// nest a test one indent level deeper, silently dropping it out of the
	// "declared" set rather than out of "ran" -- the one direction this
	// script's name comparison cannot turn into a loud failure on its own.
	// Refusing describe() outright is the guard against that blind spot.
	t.Run("describe() is refused outright rather than silently under-counted", func(t *testing.T) {
		root := t.TempDir()
		writeFixture(t, root, "nested.test.js", `
import test from "node:test";
import assert from "node:assert/strict";
import { describe } from "node:test";
describe("a group", () => {
  test("a", () => { assert.equal(1, 1); });
});
`)
		out, err := run(t, root)
		if err == nil {
			t.Fatalf("describe() must be refused, not silently under-counted:\n%s", out)
		}
		if !strings.Contains(out, "describe(") {
			t.Fatalf("want the describe() guard's own message, got:\n%s", out)
		}
	})

	t.Run("an empty tree refuses to pass vacuously", func(t *testing.T) {
		root := t.TempDir()
		out, err := run(t, root)
		if err == nil {
			t.Fatalf("no test files at all must be a failure, not a vacuous pass:\n%s", out)
		}
		if !strings.Contains(out, "no *.test.js files found") {
			t.Fatalf("want the vacuous-pass guard's own message, got:\n%s", out)
		}
	})
}
