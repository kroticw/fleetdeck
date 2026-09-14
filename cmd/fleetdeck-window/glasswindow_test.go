//go:build darwin

package main

import (
	"os"
	"strings"
	"testing"
)

// The CI stand (scripts/ci-window-stand.sh) knows the frame came up by the
// window's log: the board's page and each surface's page saying "panel". The
// line is written in Go and waited for in shell, so the two are held together
// here, as pageload_test.go holds the page's marker.
func TestTheWindowStandWaitsForEachSurfacesLineAsTheWindowLogsIt(t *testing.T) {
	script, err := os.ReadFile("../../scripts/ci-window-stand.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, surface := range sideSurfaces {
		// In the script the line sits in a double-quoted shell string.
		want := strings.ReplaceAll(surfacePageSays(surface, pagePanel), `"`, `\"`)
		if !strings.Contains(string(script), want) {
			t.Errorf("scripts/ci-window-stand.sh does not wait for %s", want)
		}
	}
}
