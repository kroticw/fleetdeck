package codex

import (
	"reflect"
	"strings"
	"testing"

	"github.com/kroticw/fleetdeck/internal/config"
)

const launchThread = "01a1a6d4-90a4-70f3-9130-a59ec873d0ba"

func TestLaunchUsesOrdinaryResumeAndExplicitPolicies(t *testing.T) {
	c := config.CodexConfig{Enabled: true, Command: []string{"/opt/wrapper with spaces", "codex"}, Profile: "work", Model: "model-id", Sandbox: "read-only", ApprovalPolicy: "on-request"}
	l, err := BuildLaunch(c, "", launchThread, []string{"PATH=/bin", "TERM=dumb", "CODEX_THREAD_ID=parent", "CODEX_INTERNAL_ORIGINATOR_OVERRIDE=parent"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/opt/wrapper with spaces", "codex", "--profile", "work", "--model", "model-id", "--sandbox", "read-only", "--ask-for-approval", "on-request", "resume", launchThread}
	if !reflect.DeepEqual(l.Argv, want) {
		t.Fatalf("argv=%q", l.Argv)
	}
	if !reflect.DeepEqual(l.Env, []string{"PATH=/bin", "TERM=xterm-256color"}) {
		t.Fatalf("env=%q", l.Env)
	}
	for _, arg := range l.Argv {
		if arg == "--remote" || strings.Contains(arg, "dangerously-bypass") {
			t.Fatalf("unexpected launch grant: %q", l.Argv)
		}
	}
}

func TestLaunchPreservesNativeDefaultsAndSeparatesHomes(t *testing.T) {
	c := config.CodexConfig{Enabled: true}
	l, err := BuildLaunch(c, "/opt/homebrew/bin/codex", launchThread, []string{"CODEX_HOME=/user/codex", "PATH=/bin"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(l.Argv, []string{"/opt/homebrew/bin/codex", "resume", launchThread}) {
		t.Fatalf("default model or policy overridden: %q", l.Argv)
	}
	if !strings.Contains(strings.Join(l.Env, "\n"), "CODEX_HOME=/user/codex") {
		t.Fatal("existing CODEX_HOME discarded")
	}
	for _, home := range []string{"/first/codex home", "/second/codex"} {
		c.Home = home
		l, err := BuildLaunch(c, "/bin/codex", launchThread, []string{"CODEX_HOME=/parent", "PATH=/bin"})
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		for _, entry := range l.Env {
			if strings.HasPrefix(entry, "CODEX_HOME=") {
				count++
				if entry != "CODEX_HOME="+home {
					t.Fatalf("wrong home %q", entry)
				}
			}
		}
		if count != 1 {
			t.Fatalf("home entries=%d", count)
		}
	}
}

func TestLaunchRejectsUnregisteredThreadAndRelativeExecutable(t *testing.T) {
	c := config.CodexConfig{Enabled: true}
	for _, pair := range [][2]string{{"codex", launchThread}, {"/bin/codex", ""}, {"/bin/codex", "../../thread"}} {
		if _, err := BuildLaunch(c, pair[0], pair[1], nil); err == nil {
			t.Fatalf("invalid launch accepted: %q", pair)
		}
	}
}
