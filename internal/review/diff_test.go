package review

import "testing"

const stageDiff = `diff --git a/a.go b/a.go
index 8fcb02a..8439dcb 100644
--- a/a.go
+++ b/a.go
@@ -4,0 +5,3 @@ import "fmt"
+// Load reads path and reports its size.
+// It never retries.
+// Errors are wrapped.
@@ -8 +11 @@ func Load(path string) error {
-		return err
+		return fmt.Errorf("read %s: %w", path, err)
@@ -10 +12,0 @@ func Load(path string) error {
-	record(len(raw))
`

func TestHunkHeadersTakeACountOfOneWhenItIsLeftOut(t *testing.T) {
	t.Parallel()
	files, err := ParseDiff(stageDiff, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].OldPath != "a.go" || files[0].NewPath != "a.go" {
		t.Fatalf("files = %+v", files)
	}
	got := files[0].Hunks
	want := [][4]int{{4, 0, 5, 3}, {8, 1, 11, 1}, {10, 1, 12, 0}}
	if len(got) != len(want) {
		t.Fatalf("hunks = %+v", got)
	}
	for i, h := range got {
		if [4]int{h.OldStart, h.OldCount, h.NewStart, h.NewCount} != want[i] {
			t.Fatalf("hunk %d = %+v, want %v", i, h, want[i])
		}
	}
	if l := got[1].Lines[1]; l.Kind != "+" || l.New != 11 || l.Old != 0 {
		t.Fatalf("added line = %+v", l)
	}
}

func TestARenameCarriesBothPaths(t *testing.T) {
	t.Parallel()
	raw := "diff --git a/a.go b/load.go\nsimilarity index 52%\nrename from a.go\nrename to load.go\nindex 8fcb02a..cb59b65 100644\n--- a/a.go\n+++ b/load.go\n@@ -1,0 +2 @@ package a\n+// Package a loads things.\n"
	files, err := ParseDiff(raw, 0)
	if err != nil {
		t.Fatal(err)
	}
	if files[0].OldPath != "a.go" || files[0].NewPath != "load.go" {
		t.Fatalf("paths = %q -> %q", files[0].OldPath, files[0].NewPath)
	}
}

func TestAPureRenameHasNoHunksAndStillNamesBothPaths(t *testing.T) {
	t.Parallel()
	raw := "diff --git a/a.go b/b.go\nsimilarity index 100%\nrename from a.go\nrename to b.go\n"
	files, err := ParseDiff(raw, 0)
	if err != nil {
		t.Fatal(err)
	}
	if files[0].OldPath != "a.go" || files[0].NewPath != "b.go" || len(files[0].Hunks) != 0 {
		t.Fatalf("file = %+v", files[0])
	}
}

func TestABinaryFileIsAFileWithoutHunks(t *testing.T) {
	t.Parallel()
	raw := "diff --git a/i.png b/i.png\nindex 1..2 100644\nBinary files a/i.png and b/i.png differ\n"
	files, err := ParseDiff(raw, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !files[0].Binary || files[0].NewPath != "i.png" {
		t.Fatalf("file = %+v", files[0])
	}
}

func TestAddedAndDeletedFilesLeaveTheMissingSideEmpty(t *testing.T) {
	t.Parallel()
	raw := "diff --git a/n.go b/n.go\nnew file mode 100644\n--- /dev/null\n+++ b/n.go\n@@ -0,0 +1 @@\n+package a\ndiff --git a/o.go b/o.go\ndeleted file mode 100644\n--- a/o.go\n+++ /dev/null\n@@ -1 +0,0 @@\n-package a\n"
	files, err := ParseDiff(raw, 0)
	if err != nil {
		t.Fatal(err)
	}
	if files[0].OldPath != "" || files[0].NewPath != "n.go" || files[1].OldPath != "o.go" || files[1].NewPath != "" {
		t.Fatalf("files = %+v", files)
	}
}

func TestACyrillicPathWithASpaceIsReadWhole(t *testing.T) {
	t.Parallel()
	// git ends a ---/+++ name holding a space with a tab.
	raw := "diff --git a/docs/ревью файла.md b/docs/ревью файла.md\nnew file mode 100644\n--- /dev/null\n+++ b/docs/ревью файла.md\t\n@@ -0,0 +1 @@\n+строка\n"
	files, err := ParseDiff(raw, 0)
	if err != nil {
		t.Fatal(err)
	}
	if files[0].NewPath != "docs/ревью файла.md" {
		t.Fatalf("path = %q", files[0].NewPath)
	}
}

func TestAFileOverTheLineCeilingIsFoldedNotDropped(t *testing.T) {
	t.Parallel()
	files, err := ParseDiff(stageDiff, 3)
	if err != nil {
		t.Fatal(err)
	}
	if !files[0].Truncated || len(files[0].Hunks) != 0 || files[0].NewPath != "a.go" {
		t.Fatalf("file = %+v", files[0])
	}
}

func TestAnAddedBinaryFileHasOldPathEmpty(t *testing.T) {
	t.Parallel()
	raw := "diff --git a/logo.png b/logo.png\nnew file mode 100644\nindex 0000000..b653e61\nBinary files /dev/null and b/logo.png differ\n"
	files, err := ParseDiff(raw, 0)
	if err != nil {
		t.Fatal(err)
	}
	if files[0].OldPath != "" || files[0].NewPath != "logo.png" || !files[0].Binary {
		t.Fatalf("file = %+v", files[0])
	}
}

func TestADeletedBinaryFileHasNewPathEmpty(t *testing.T) {
	t.Parallel()
	raw := "diff --git a/logo.png b/logo.png\ndeleted file mode 100644\nindex b653e61..0000000\nBinary files a/logo.png and /dev/null differ\n"
	files, err := ParseDiff(raw, 0)
	if err != nil {
		t.Fatal(err)
	}
	if files[0].OldPath != "logo.png" || files[0].NewPath != "" || !files[0].Binary {
		t.Fatalf("file = %+v", files[0])
	}
}

// git with diff.suppressBlankEmpty prints a blank context line as an empty
// line instead of a single space; it is still a line of the hunk.
func TestAnEmptyLineInsideAHunkIsABlankContextLine(t *testing.T) {
	t.Parallel()
	raw := "diff --git a/y.txt b/y.txt\n--- a/y.txt\n+++ b/y.txt\n@@ -1,3 +1,3 @@\n one\n\n-two\n+TWO\n"
	files, err := ParseDiff(raw, 0)
	if err != nil {
		t.Fatal(err)
	}
	lines := files[0].Hunks[0].Lines
	if len(lines) != 4 || lines[1] != (Line{Kind: " ", Old: 2, New: 2}) || lines[2].Old != 3 || lines[3].New != 3 {
		t.Fatalf("lines = %+v", lines)
	}
}
