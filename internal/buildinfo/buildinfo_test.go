package buildinfo

import (
	"encoding/json"
	"os"
	"runtime/debug"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// The web hash exists to answer one question: is the page a browser is
// running the page this panel serves now? So it has to change exactly when
// what the browser would receive changes, and at no other time. Each case
// below pins one half of that.

func TestWebHashIsTheSameForTheSameFiles(t *testing.T) {
	a := fstest.MapFS{
		"index.html":  {Data: []byte("<!doctype html>")},
		"js/main.js":  {Data: []byte("connect()")},
		"js/store.js": {Data: []byte("export {}")},
	}
	// Same files, declared in a different order: a map has no order, and a
	// hash that followed iteration order would differ between two builds of
	// the same tree -- the "rebuilt on the same commit, banner appears
	// anyway" noise the banner must never make.
	b := fstest.MapFS{
		"js/store.js": {Data: []byte("export {}")},
		"index.html":  {Data: []byte("<!doctype html>")},
		"js/main.js":  {Data: []byte("connect()")},
	}

	ha, err := WebHash(a)
	if err != nil {
		t.Fatal(err)
	}
	hb, err := WebHash(b)
	if err != nil {
		t.Fatal(err)
	}
	if ha != hb {
		t.Fatalf("same files hashed differently: %s vs %s", ha, hb)
	}
}

func TestWebHashChangesWhenOneByteOfOneFileChanges(t *testing.T) {
	before := fstest.MapFS{
		"index.html": {Data: []byte("<!doctype html>")},
		"js/main.js": {Data: []byte("connect()")},
	}
	// One byte, and the length kept: the length is hashed on its own, so an
	// edit that also changed it would pass even with the bytes left out of
	// the hash entirely -- measured, the first version of this case did.
	after := fstest.MapFS{
		"index.html": {Data: []byte("<!doctype html>")},
		"js/main.js": {Data: []byte("connecT()")},
	}

	hb, _ := WebHash(before)
	ha, _ := WebHash(after)
	if hb == ha {
		t.Fatal("a changed script left the hash unchanged: the page would never learn it is out of date")
	}
}

// A file moved to another path is a different page even with the same bytes:
// the browser asks for it by path.
func TestWebHashChangesWhenAFileMoves(t *testing.T) {
	before := fstest.MapFS{"js/a.js": {Data: []byte("x")}}
	after := fstest.MapFS{"js/b.js": {Data: []byte("x")}}

	hb, _ := WebHash(before)
	ha, _ := WebHash(after)
	if hb == ha {
		t.Fatal("a renamed file left the hash unchanged")
	}
}

// Without a separator between a path and its content, "ab" + "c" and "a" +
// "bc" feed the same bytes to the hash. Two different trees must never
// collide that cheaply.
func TestWebHashKeepsPathAndContentApart(t *testing.T) {
	one := fstest.MapFS{"ab": {Data: []byte("c")}}
	two := fstest.MapFS{"a": {Data: []byte("bc")}}

	h1, _ := WebHash(one)
	h2, _ := WebHash(two)
	if h1 == h2 {
		t.Fatal("path and content run together: two different trees share a hash")
	}
}

func TestFromBuildInfoReadsTheCommit(t *testing.T) {
	info := &debug.BuildInfo{Settings: []debug.BuildSetting{
		{Key: "vcs.revision", Value: "24d0c7a657ebdf751972b6963b7039d5f4c10eb5"},
		{Key: "vcs.time", Value: "2026-09-10T19:00:15Z"},
		{Key: "vcs.modified", Value: "true"},
	}}

	var f Fingerprint
	fromBuildInfo(&f, info)

	if f.Revision != "24d0c7a657ebdf751972b6963b7039d5f4c10eb5" {
		t.Errorf("revision = %q", f.Revision)
	}
	if !f.Modified {
		t.Error("modified tree reported as clean")
	}
	want := time.Date(2026, 9, 10, 19, 0, 15, 0, time.UTC)
	if !f.CommitTime.Equal(want) {
		t.Errorf("commit time = %v, want %v", f.CommitTime, want)
	}
}

// A binary built outside a checkout -- or by `go test`, which never stamps
// vcs settings -- has no commit. It must say so by leaving the field empty,
// not by inventing one.
func TestFromBuildInfoWithoutVCSLeavesTheCommitEmpty(t *testing.T) {
	var f Fingerprint
	fromBuildInfo(&f, &debug.BuildInfo{})

	if f.Revision != "" || f.Modified || !f.CommitTime.IsZero() {
		t.Fatalf("invented a commit: %+v", f)
	}
}

// BuiltAt is when the binary was written, never when its commit was made.
// The two diverge exactly in the case this package exists for -- a binary
// built today from yesterday's commit -- and reading one for the other puts a
// second false date on the screen next to the first.
func TestBuiltAtIsTheBinaryFilesTimeNotTheCommitTime(t *testing.T) {
	dir := t.TempDir()
	exe := dir + "/fleetdeck"
	if err := os.WriteFile(exe, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	written := time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC)
	if err := os.Chtimes(exe, written, written); err != nil {
		t.Fatal(err)
	}

	f, err := read(fstest.MapFS{"index.html": {Data: []byte("x")}}, exe, &debug.BuildInfo{Settings: []debug.BuildSetting{
		{Key: "vcs.time", Value: "2026-09-10T19:00:15Z"},
	}})
	if err != nil {
		t.Fatal(err)
	}

	if !f.BuiltAt.Equal(written) {
		t.Errorf("built at = %v, want the file's own time %v", f.BuiltAt, written)
	}
	if f.BuiltAt.Equal(f.CommitTime) {
		t.Error("built-at and commit time are the same value: one was read for the other")
	}
	if f.Executable != exe {
		t.Errorf("executable = %q, want %q", f.Executable, exe)
	}
}

// The version is the one `fleetdeck version` prints -- the release tag the
// build was stamped with, or "dev" -- so the header and the command line can
// never name two different versions of one binary. A person who downloaded the
// app cannot tell a release from a commit hash; the header shows them this.
func TestTheFingerprintCarriesTheVersionTheBinaryReports(t *testing.T) {
	was := versionOf
	t.Cleanup(func() { versionOf = was })
	versionOf = func() string { return "v7.7.7" }

	f, err := Read(fstest.MapFS{"index.html": {Data: []byte("x")}})
	if err != nil {
		t.Fatal(err)
	}
	if f.Version != "v7.7.7" {
		t.Errorf("version = %q, want the binary's own %q", f.Version, "v7.7.7")
	}
}

// The header reads snapshot.build.version (web/js/buildcheck.js). The field's
// JSON name is the other half of that agreement, in another language.
func TestTheVersionReachesThePageUnderTheNameTheHeaderReads(t *testing.T) {
	out, err := json.Marshal(Fingerprint{Web: "w", Version: "v7.7.7"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"version":"v7.7.7"`) {
		t.Errorf("the fingerprint marshals as %s, without the \"version\" the header reads", out)
	}
}
