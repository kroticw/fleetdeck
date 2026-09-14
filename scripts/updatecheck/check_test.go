package main

import (
	"strings"
	"testing"
	"time"
)

// What the copies in this program are held to: v0.10.0's own words, as they
// stand in cmd/fleetdeck-window of the tag.
const tagUpdateGo = `
const (
	measuredWorstHandover = 812 * time.Millisecond
	handoverMargin        = 3
	handoverTimeout       = measuredWorstHandover * handoverMargin
)

func launchNewWindow(url string) func(staged, canonical, handover string) (func(), error) {
	return func(staged, canonical, handover string) (func(), error) {
		cmd := exec.Command(filepath.Join(staged, "Contents", "MacOS", "fleetdeck-window"),
			"--url", url, "--handover", handover, "--canonical", canonical)
		cmd.Stdout, cmd.Stderr = log, log
	}
}
`

const tagMainGo = `
	u := &supervisor.Update{
		Source:          source,
		Canonical:       canonical,
		LockPath:        lockPath,
		HandoverTimeout: handoverTimeout,
		Launch:          launchNewWindow(url),
		Pause:           kept.stop,
		Resume:          kept.start,
	}
`

const tagOwnerGo = `
func panelArgs(window int, standSocket string) []string {
	args := []string{"--owner-pid", strconv.Itoa(window)}
	if standSocket != "" {
		args = append(args, "--stand-socket", standSocket)
	}
	return args
}

const launchdThrottle = 10 * time.Second

const takenPanelPoll = 2 * time.Second
`

func tagSources() map[string]string {
	return map[string]string{"update.go": tagUpdateGo, "main.go": tagMainGo, "owner.go": tagOwnerGo}
}

func TestTheCopiesMatchTheTag(t *testing.T) {
	if err := copiedFrom(tagSources()); err != nil {
		t.Fatalf("v0.10.0's own sources refused: %v", err)
	}
}

func TestACopyDriftingFromTheTagIsRefused(t *testing.T) {
	cases := map[string]struct{ file, old, new string }{
		"the new window told the old window's deadline": {"update.go",
			`"--canonical", canonical)`, `"--canonical", canonical, "--handover-deadline", "2436ms")`},
		"another measured handover": {"update.go", "812 * time.Millisecond", "900 * time.Millisecond"},
		"another margin":            {"update.go", "handoverMargin        = 3", "handoverMargin        = 4"},
		"the button's update waits on something else": {"main.go",
			"HandoverTimeout: handoverTimeout", "HandoverTimeout: 5 * time.Second"},
		"the button's update pauses nothing":     {"main.go", "Pause:           kept.stop", "Pause:           func() {}"},
		"the panel is started without its owner": {"owner.go", `"--owner-pid", strconv.Itoa(window)`, `"--owner", strconv.Itoa(window)`},
		"another throttle":                       {"owner.go", "10 * time.Second", "5 * time.Second"},
		"another poll":                           {"owner.go", "2 * time.Second", "time.Second"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			sources := tagSources()
			if !strings.Contains(sources[c.file], c.old) {
				t.Fatalf("the case does not apply: %q is not in %s", c.old, c.file)
			}
			sources[c.file] = strings.Replace(sources[c.file], c.old, c.new, 1)
			if err := copiedFrom(sources); err == nil {
				t.Fatal("a copy that no longer matches the tag was accepted")
			}
		})
	}
}

func TestTheCopiedValuesAreV0100s(t *testing.T) {
	if handoverTimeout != 2436*time.Millisecond {
		t.Errorf("handoverTimeout is %s, v0.10.0's is 2.436s", handoverTimeout)
	}
	got := strings.Join(newWindowArgs("http://127.0.0.1:7900/", "/h", "/Applications/fleetdeck.app"), " ")
	want := "--url http://127.0.0.1:7900/ --handover /h --canonical /Applications/fleetdeck.app"
	if got != want {
		t.Errorf("the new window is started with %q, v0.10.0 starts it with %q", got, want)
	}
	got = strings.Join(panelArgs(42, "/stand.sock"), " ")
	if want := "--owner-pid 42 --stand-socket /stand.sock"; got != want {
		t.Errorf("the old panel is started with %q, v0.10.0's window starts it with %q", got, want)
	}
}

func TestTheHandoverStepsAreCheckedInOrder(t *testing.T) {
	full := []string{"check", "unpack", "handover", "handover:alive", "handover:panel", "handover:swapped", "handover:done", "done"}
	if err := handoverInOrder(full); err != nil {
		t.Fatalf("a whole handover refused: %v", err)
	}
	refused := map[string][]string{
		"no swap":          {"check", "handover", "handover:alive", "handover:panel", "handover:done", "done"},
		"swapped too soon": {"check", "handover", "handover:alive", "handover:swapped", "handover:panel", "handover:done", "done"},
		"failed":           {"check", "handover", "handover:alive", "handover:failed"},
		"not done after":   {"check", "handover", "handover:alive", "handover:panel", "handover:swapped", "handover:done"},
		"nothing":          nil,
	}
	for name, steps := range refused {
		t.Run(name, func(t *testing.T) {
			if err := handoverInOrder(steps); err == nil {
				t.Fatalf("%v accepted", steps)
			}
		})
	}
}
