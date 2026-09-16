// -stand-socket exists so a test stand run near a live fleet cannot reach
// the real daemon — see daemonClient's own doc for the reasoning: the
// override path shares no code with daemon.Discover, so there is nowhere
// for a "not found here, try the default" fallback to live. These tests
// pin the two halves of that guarantee: checkStandSocket refuses the one
// way a script could pass the flag and still end up unconfigured, and
// daemonClient's override branch genuinely reaches the socket it is given
// rather than merely constructing something that looks right.
package main

import (
	"context"
	"os"
	"testing"

	"github.com/kroticw/fleetdeck/internal/daemon/daemontest"
)

func TestCheckStandSocket(t *testing.T) {
	cases := []struct {
		name    string
		given   bool
		value   string
		wantErr bool
	}{
		{"not given at all: every real panel, unconditionally", false, "", false},
		// Not a real case flag.Parse can produce (given=false implies value is
		// the flag's zero value), but checked anyway: the function's contract
		// is "given decides", not "value decides", and this proves it does not
		// quietly fall back to inspecting value on its own.
		{"not given, value happens to be non-empty", false, "/some/path", false},
		{"given with a path: an isolated stand", true, "/tmp/some/control.sock", false},
		{"given empty: the exact bug this guards against", true, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkStandSocket(tc.given, tc.value)
			if tc.wantErr && err == nil {
				t.Fatal("want an error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("want no error, got %v", err)
			}
		})
	}
}

// fakeDaemonSocket starts a minimal daemon that answers "ping" well enough
// for Client.Ping to succeed, and returns the path of the unix socket it
// listens on -- the one fact daemonClient's override branch is handed.
func fakeDaemonSocket(t *testing.T) string {
	t.Helper()
	return daemontest.StartTest(t, nil).Socket
}

// The functional half of the guarantee: given a path, daemonClient's
// override branch genuinely dials it -- not a client that merely looks
// correctly constructed. daemon.New shares no code with Discover (see
// daemonClient's own doc), so this is also indirect proof that reaching this
// fake daemon did not go through the real uid-scoped glob at all: there was
// never a real daemon for it to find there in the first place, in a
// directory this test made up itself.
func TestDaemonClientOverrideReachesTheGivenSocket(t *testing.T) {
	sockPath := fakeDaemonSocket(t)

	client := daemonClient(sockPath, t.TempDir())
	info, err := client.Ping(context.Background())
	if err != nil {
		t.Fatalf("daemonClient(%q).Ping: %v", sockPath, err)
	}
	if info.Version != "test" {
		t.Fatalf("want the fake daemon's own version %q, got %q -- this did not reach the socket it was given", "test", info.Version)
	}
}

// The other half: a path that no daemon is listening on stays refused, not
// silently answered by something else -- there is nowhere else for
// daemonClient's override branch to go looking.
func TestDaemonClientOverrideToNothingListeningFailsCleanly(t *testing.T) {
	client := daemonClient(os.TempDir()+"/fleetdeck-test-nothing-here.sock", t.TempDir())
	if _, err := client.Ping(context.Background()); err == nil {
		t.Fatal("want an error dialing a socket nothing listens on, got nil")
	}
}
