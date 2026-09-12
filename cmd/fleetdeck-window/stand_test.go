//go:build darwin

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	neturl "net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kroticw/fleetdeck/internal/supervisor"
)

// The acceptance for updating from a release, as far as it can be taken
// without two published releases carrying this code.
//
// What is real here: a signed app bundle at a canonical path, the real
// releases page, the real download, every check on it, a new window started
// from the downloaded bundle as a separate process, the handover between the
// two, and the swap. What is not: the press. The button calls
// supervisor.Update through runUpdate, and this calls supervisor.Update
// directly, because a click into a WKWebView cannot be made from a test.
//
// It needs a stand built for it, and it is skipped without one. Build it with
// a version older than whatever is published, so there is something to update
// to, and sign it -- an unsigned stand is refused by updateWay before any of
// this is reached, which is itself the behaviour way_test.go pins:
//
//	make dist-app DISTDIR=/tmp/stand-dist VERSION=v0.3.9 \
//	    SIGN_IDENTITY="Developer ID Application: … (TEAMID)" EXPECT_SEAL=developer-id
//	ditto -x -k /tmp/stand-dist/fleetdeck-v0.3.9-macos.zip /tmp/stand
//	FLEETDECK_STAND_APP=/tmp/stand/fleetdeck.app FLEETDECK_NETWORK_TEST=1 \
//	    go test ./cmd/fleetdeck-window/ -run TestAStandInstalledFromAReleaseUpdatesItself -v
//
// The stand needs no notarization ticket of its own: what is checked is what
// comes down, not what is running.
func TestAStandInstalledFromAReleaseUpdatesItself(t *testing.T) {
	stand := os.Getenv("FLEETDECK_STAND_APP")
	if stand == "" || os.Getenv("FLEETDECK_NETWORK_TEST") != "1" {
		t.Skip("set FLEETDECK_STAND_APP and FLEETDECK_NETWORK_TEST=1 to run the acceptance")
	}

	// A HOME and a port of this test's own. The operator's panel answers on
	// 7777 and their fleet is live: nothing here may take that port.
	//
	// The port goes in a configuration file rather than on a command line,
	// because the panel the *new* window starts is started by that window
	// with its own arguments -- the stand cannot pass anything to it. HOME is
	// set on this process so both windows and both panels inherit it, which
	// is also how they find that file.
	home := t.TempDir()
	t.Setenv("HOME", home)
	url := freeURL(t)
	writeStandConfig(t, home, portOf(t, url))
	canonical := filepath.Join(t.TempDir(), supervisor.BundleName)
	if out, err := exec.Command("/usr/bin/ditto", stand, canonical).CombinedOutput(); err != nil {
		t.Fatalf("put the stand at a canonical path: %v\n%s", err, out)
	}
	running := standVersion(t, canonical)
	t.Logf("the stand at %s is %s", canonical, running)

	// The window's own way of deciding how it updates, on the stand: this is
	// the decision the whole change turns on, and it is not stubbed.
	exe := filepath.Join(canonical, "Contents", "MacOS", "fleetdeck-window")
	how := updateWay(config{tree: "", exe: exe, version: running, teamID: ownTeamID(exe)})
	if how.Refusal != "" {
		t.Fatalf("the stand refuses to update itself: %s", how.Refusal)
	}
	if _, ok := how.Source.(*supervisor.ReleaseSource); !ok {
		t.Fatalf("the stand updates from %T, want a *supervisor.ReleaseSource", how.Source)
	}

	// The keeper of the window that is being replaced, on the stand's own
	// panel. The update pauses it and the new window takes the panel over.
	env := os.Environ() // HOME is already the stand’s, set above
	keeper := &supervisor.Keeper{
		URL:          url,
		Bin:          supervisor.PanelIn(canonical),
		Args:         panelArgs(os.Getpid()),
		Owner:        os.Getpid(),
		Env:          env,
		LogPath:      filepath.Join(home, "panel.log"),
		StartTimeout: panelStartTimeout,
		MinUptime:    launchdThrottle,
		Poll:         takenPanelPoll,
	}
	kept := &keeperRun{k: keeper}
	kept.start()
	defer kept.stop()
	if !answers(url, 20*time.Second) {
		t.Fatalf("the stand's panel never answered at %s; log:\n%s", url, tail(home))
	}

	// Everything below here is what the button does, minus the press.
	lockPath, err := supervisor.LockPath(canonical)
	if err != nil {
		t.Fatal(err)
	}
	var steps []string
	var mu sync.Mutex
	u := &supervisor.Update{
		Source:          how.Source,
		Canonical:       canonical,
		LockPath:        lockPath,
		HandoverTimeout: 60 * time.Second, // a download and a launch, not just a launch
		Launch:          launchNewWindow(url),
		Pause:           kept.stop,
		Resume:          kept.start,
		Progress: func(p supervisor.Progress) {
			mu.Lock()
			steps = append(steps, p.Step)
			mu.Unlock()
			t.Logf("update: %s %s", p.Step, p.Detail)
		},
	}
	if err := u.Run(context.Background()); err != nil {
		t.Fatalf("the update did not finish: %v (steps %v)\nnew window's log:\n%s",
			err, steps, newWindowLog(canonical))
	}

	// The app at the canonical path is the release now, and it says so itself.
	updated := standVersion(t, canonical)
	if updated == running {
		t.Fatalf("the app at the canonical path is still %s", running)
	}
	t.Logf("the app at %s is %s now", canonical, updated)

	// The one the update replaced is kept beside it, to go back to.
	kept0 := filepath.Join(supervisor.StagingDir(canonical), supervisor.BundleName)
	if was := standVersion(t, kept0); was != running {
		t.Errorf("the bundle swapped out is %s, want the previous %s", was, running)
	}

	// Nothing gave the new app a quarantine attribute. It is the attribute
	// that arms App Translocation, and a translocated app records paths that
	// disappear (docs/engineering/release-app.md section 3).
	if out, _ := exec.Command("/usr/bin/xattr", "-p", "com.apple.quarantine", canonical).CombinedOutput(); len(out) > 0 &&
		!strings.Contains(string(out), "No such xattr") {
		t.Errorf("the updated app carries a quarantine attribute: %s", out)
	}

	// And the panel answering at the end is the new one, running out of the
	// canonical path rather than out of a staging directory or a translocated
	// copy of one.
	//
	// The two paths are compared with their symlinks resolved: the panel
	// reports os.Executable() through EvalSymlinks (internal/buildinfo), and
	// on macOS /var is a symlink to /private/var, so the same directory is
	// spelled two ways. Measured here the first time this ran, as a failure
	// that was the test's and not the app's.
	where, version := panelSelfReport(t, url)
	if strings.Contains(where, "AppTranslocation") {
		t.Errorf("the panel runs translocated, from %s", where)
	}
	if !strings.HasPrefix(where, resolve(t, canonical)) {
		t.Errorf("the panel runs from %s, want it inside %s", where, resolve(t, canonical))
	}
	if version != updated {
		t.Errorf("the panel answering reports %s, want %s", version, updated)
	}
	t.Logf("the panel at %s is %s, running from %s", url, version, where)
}

// writeStandConfig puts a configuration in the stand's home naming the port
// its panels are to listen on, and nothing else: every other setting is left
// to the panel's own defaults.
func writeStandConfig(t *testing.T, home string, port int) {
	t.Helper()
	dir := filepath.Join(home, ".config", "fleetdeck")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf("server:\n  port: %d\n", port)
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func resolve(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func portOf(t *testing.T, rawURL string) int {
	t.Helper()
	u, err := neturl.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	return port
}

func freeURL(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("http://127.0.0.1:%d/", port)
}

func standVersion(t *testing.T, bundle string) string {
	t.Helper()
	out, err := exec.Command(supervisor.PanelIn(bundle), "version").CombinedOutput()
	if err != nil {
		t.Fatalf("ask %s its version: %v\n%s", bundle, err, out)
	}
	return strings.TrimSpace(string(out))
}

func answers(url string, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		resp, err := (&http.Client{Timeout: time.Second}).Get(url)
		if err == nil {
			_ = resp.Body.Close()
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

// panelSelfReport asks the panel where its binary is and what version it is.
func panelSelfReport(t *testing.T, url string) (executable, version string) {
	t.Helper()
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Get(strings.TrimSuffix(url, "/") + "/api/snapshot")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var snap struct {
		Build struct {
			Executable string `json:"executable"`
			Version    string `json:"version"`
		} `json:"build"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&snap); err != nil {
		t.Fatal(err)
	}
	return snap.Build.Executable, snap.Build.Version
}

func tail(home string) string {
	data, _ := os.ReadFile(filepath.Join(home, "panel.log"))
	return string(data)
}

func newWindowLog(canonical string) string {
	data, _ := os.ReadFile(filepath.Join(supervisor.StagingDir(canonical), "new-window.log"))
	return string(data)
}
