//go:build ldflagscheck

package version

import "testing"

// TestLdflagsSymbolPathActuallyLands proves that the Makefile's -ldflags -X symbol path
// actually reaches this package's `value` variable. The linker does not fail when an
// -X target names a symbol that does not exist — a module rename, a package move, or a
// renamed variable silently leaves `value` empty, and String() then reports "dev" in
// every release binary with nothing anywhere failing to say so.
//
// This test is guarded by the "ldflagscheck" build tag specifically so that an ordinary
// `go test ./...` (which never passes -tags ldflagscheck or a matching -ldflags) does
// not run it — it is meaningless without both. `make verify-ldflags` builds and runs it
// with VERSION=9.9.9 and the real LDFLAGS symbol path; see the Makefile.
func TestLdflagsSymbolPathActuallyLands(t *testing.T) {
	if value != "9.9.9" {
		t.Fatalf("ldflags -X did not reach this variable; got %q", value)
	}
}
