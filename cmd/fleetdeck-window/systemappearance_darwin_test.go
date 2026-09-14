package main

// Read once, on the main thread, in TestMain (menu_darwin_test.go).

import "testing"

var systemAppearanceResult systemAppearanceProbe

func collectSystemAppearanceResults() { systemAppearanceResult = probeSystemAppearanceForTest() }

// A stand's log said the system was dark by what `defaults` read, while the
// screenshot could not tell (run 34869969788): the window says the system's
// appearance as AppKit draws an app with none of its own, before it gives the
// app the theme it was asked for.
func TestTheSystemsAppearanceIsSaidOnlyWhileTheAppHasNoneOfItsOwn(t *testing.T) {
	r := systemAppearanceResult
	if r.withoutOwn == "" || r.withoutOwn != r.effective {
		t.Errorf("with no appearance of the app's own: system %q, the app drawn in %q; want the same, not empty", r.withoutOwn, r.effective)
	}
	if r.withOwn != "" {
		t.Errorf("with the app dark: system %q, want nothing said", r.withOwn)
	}
	if r.appAfter != r.appBefore {
		t.Errorf("app appearance %q after the probe, %q before", r.appAfter, r.appBefore)
	}
}
