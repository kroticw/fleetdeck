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

func TestTheTimelineCountsFromTheNewWindowsStart(t *testing.T) {
	at := func(s string) time.Time {
		v, err := time.ParseInLocation(windowLogLayout, s, time.Local)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	started := at("2026/09/14 14:53:27.350000")
	log := "2026/09/14 14:53:27.910000 fleetdeck-window: on this stand: the panel has 3s to answer\n" +
		"not a line of the log\n" +
		"2026/09/14 14:53:28.100000 fleetdeck-window: taking the panel over by 14:53:29.786, the old window's deadline\n" +
		"2026/09/14 14:53:28.300000 fleetdeck-window: panel starting (pid 7, started by this window)\n"
	steps := []stepAt{
		{"handover", at("2026/09/14 14:53:27.349000")},
		{"handover:alive", at("2026/09/14 14:53:28.150000")},
		{"handover:done", at("2026/09/14 14:53:28.900000")},
		{"done", at("2026/09/14 14:53:28.901000")},
	}
	got := strings.Join(timeline(started, log, steps), "\n")
	want := strings.Join([]string{
		"+560ms the new window's first log line",
		"+750ms the new window starts taking the panel over",
		"+800ms handover:alive",
		"+1550ms handover:done",
	}, "\n")
	if got != want {
		t.Errorf("timeline:\n%s\nwant:\n%s", got, want)
	}
}

func TestTheTimelineHoldsTheWindowsOwnStartSteps(t *testing.T) {
	started, err := time.ParseInLocation(windowLogLayout, "2026/09/14 15:00:56.234000", time.Local)
	if err != nil {
		t.Fatal(err)
	}
	log := "2026/09/14 15:00:56.260000 fleetdeck-window: started, 24 ms after the process started\n" +
		"2026/09/14 15:00:57.400000 fleetdeck-window: the web view is made, 1164 ms after the process started\n" +
		"2026/09/14 15:00:58.100000 fleetdeck-window: the glass frame is made, 1864 ms after the process started\n" +
		"2026/09/14 15:00:58.560000 fleetdeck-window: worked out how this build updates, 2324 ms after the process started\n"
	got := strings.Join(timeline(started, log, nil), "\n")
	want := strings.Join([]string{
		"+26ms the new window's first log line",
		"+26ms the new window: started (24 ms after its process started)",
		"+1166ms the new window: the web view is made (1164 ms after its process started)",
		"+1866ms the new window: the glass frame is made (1864 ms after its process started)",
		"+2326ms the new window: worked out how this build updates (2324 ms after its process started)",
	}, "\n")
	if got != want {
		t.Errorf("timeline:\n%s\nwant:\n%s", got, want)
	}
}

func TestATimelineWithNothingInItSaysSo(t *testing.T) {
	started := time.Now()
	got := timeline(started, "", []stepAt{{"handover", started.Add(-time.Millisecond)}})
	want := "nothing: the new window wrote no log line and reported no handover step"
	if len(got) != 1 || got[0] != want {
		t.Errorf("timeline %q, want [%q]", got, want)
	}
}

func TestLaunchServicesMustKnowTheInstalledAppOnceAndNothingSwappedOut(t *testing.T) {
	const canonical = "/Applications/fleetdeck.app"
	entry := func(path string) string {
		return "--------------------------------------------------------------------------------\n" +
			"bundle id:                  dev.fleetdeck.stand\n" +
			"path:                       " + path + " (0x1f2c)\n" +
			"name:                       fleetdeck\n"
	}
	other := entry("/Applications/Safari.app")
	cases := map[string]struct {
		dump string
		ok   bool
	}{
		"the installed app, once":            {other + entry(canonical), true},
		"a copy elsewhere is not this check": {other + entry(canonical) + entry("/Users/runner/work/new/fleetdeck.app"), true},
		"the installed app, twice":           {entry(canonical) + entry(canonical), false},
		"not the installed app":              {other, false},
		"the bundle swapped out":             {entry(canonical) + entry("/Applications/.fleetdeck-update/fleetdeck.app"), false},
		"no path lines at all":               {"bundle id: x\nname: y\n", false},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			err := registeredOnce(c.dump, canonical)
			if (err == nil) != c.ok {
				t.Fatalf("registeredOnce: %v, want ok %v", err, c.ok)
			}
		})
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
