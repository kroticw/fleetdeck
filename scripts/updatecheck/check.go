// Command updatecheck is an update from v0.10.0 to this branch, the way the
// operator's installed v0.10.0 does it when its Update button is pressed, run
// on a CI runner and nowhere else.
//
// It is built inside a checkout of the v0.10.0 tag, against that tag's
// internal/supervisor, so that the half of the update the installed app does
// is v0.10.0's own code: supervisor.Update.Run, with v0.10.0's handover
// deadline, stopping and starting v0.10.0's panel through v0.10.0's Keeper.
// The new window is this branch's, started from this branch's app the way
// v0.10.0 starts it, as a child of this program.
//
// Three things are not the installed app's, and a pass says nothing about
// them:
//
//   - the source: this branch's app is unpacked from a local archive, where
//     v0.10.0 downloads a release from GitHub;
//   - the seal: Seal.Verify is not asked, because this branch's app is sealed
//     ad hoc on a runner with no certificate;
//   - the press: Run is called directly, because the button is in the page.
//
// What the window adds around Update lives in its package main, which cannot
// be imported, so it is copied here. copiedFrom holds each copy to the tag's
// own source before anything runs.
//
// Usage: updatecheck -old-archive <v0.10.0 zip> -new-archive <branch zip>
//
//	-url <stand panel URL> -want-revision <branch commit> -tag-window <tag's cmd/fleetdeck-window>
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kroticw/fleetdeck/internal/supervisor"
)

// lsPathLine is a bundle's path in `lsregister -dump`: "path:", any run of
// spaces, the path, and the record's id in brackets, as read on macOS 26.6.2:
// "path:                       /Applications/fleetdeck.app (0x3168)".
var lsPathLine = regexp.MustCompile(`(?m)^path:[ \t]+(.+?)(?:[ \t]+\(0x[0-9a-fA-F]+\))?[ \t]*$`)

// registeredOnce says whether a LaunchServices dump knows the installed app at
// canonical exactly once and nothing in its staging directory. A path left
// there is the version just replaced, under the app's name, which the next
// "open fleetdeck" may start.
func registeredOnce(dump, canonical string) error {
	matches := lsPathLine.FindAllStringSubmatch(dump, -1)
	if len(matches) == 0 {
		return errors.New("the LaunchServices dump names no path at all: its format may have changed")
	}
	staging := supervisor.StagingDir(canonical) + string(filepath.Separator)
	installed := 0
	var swappedOut []string
	for _, m := range matches {
		switch path := m[1]; {
		case path == canonical:
			installed++
		case strings.HasPrefix(path, staging):
			swappedOut = append(swappedOut, path)
		}
	}
	if len(swappedOut) > 0 {
		return fmt.Errorf("LaunchServices still knows what the update swapped out: %s", strings.Join(swappedOut, ", "))
	}
	if installed != 1 {
		return fmt.Errorf("LaunchServices knows %s %d times, want once", canonical, installed)
	}
	return nil
}

// windowLogLayout is how the window stamps its log lines
// (log.LstdFlags | log.Lmicroseconds).
const windowLogLayout = "2006/01/02 15:04:05.000000"

// startStepLine is a step of the window's own start as it logs it
// (cmd/fleetdeck-window/startupsteps.go): what was done, and how long after its
// process started by the kernel's clock.
var startStepLine = regexp.MustCompile(`fleetdeck-window: (.+), (\d+) ms after the process started\s*$`)

// stepAt is an update's step and when this program heard of it.
type stepAt struct {
	step string
	at   time.Time
}

// timeline is the handover counted from the new window's process start: its
// first log line, each step of its own start it logs, the moment it starts
// taking the panel over, and each handover step as the old window read it. It
// is what says where the old window's deadline went.
func timeline(started time.Time, windowLog string, steps []stepAt) []string {
	var events []stepAt
	first := true
	for _, line := range strings.Split(windowLog, "\n") {
		if len(line) < len(windowLogLayout) {
			continue
		}
		at, err := time.ParseInLocation(windowLogLayout, line[:len(windowLogLayout)], time.Local)
		if err != nil {
			continue
		}
		if first {
			events = append(events, stepAt{"the new window's first log line", at})
			first = false
		}
		if m := startStepLine.FindStringSubmatch(line); m != nil {
			events = append(events, stepAt{fmt.Sprintf("the new window: %s (%s ms after its process started)", m[1], m[2]), at})
		}
		if strings.Contains(line, "taking the panel over by") {
			events = append(events, stepAt{"the new window starts taking the panel over", at})
		}
	}
	for _, s := range steps {
		if strings.HasPrefix(s.step, "handover:") {
			events = append(events, s)
		}
	}
	if len(events) == 0 {
		return []string{"nothing: the new window wrote no log line and reported no handover step"}
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].at.Before(events[j].at) })
	lines := make([]string, 0, len(events))
	for _, e := range events {
		lines = append(lines, fmt.Sprintf("+%dms %s", e.at.Sub(started).Milliseconds(), e.step))
	}
	return lines
}

// v0.10.0's handover deadline (cmd/fleetdeck-window/update.go of the tag):
// the old window gives the new one this long from its start to done, and
// kills it when it runs out. Nothing a new version does can change it.
const (
	measuredWorstHandover = 812 * time.Millisecond
	handoverMargin        = 3
	handoverTimeout       = measuredWorstHandover * handoverMargin
)

// v0.10.0's keeper settings for the panel its window starts
// (cmd/fleetdeck-window/owner.go of the tag).
const (
	launchdThrottle = 10 * time.Second
	takenPanelPoll  = 2 * time.Second
)

// newWindowArgs is what v0.10.0 starts the new window with. It tells the new
// window nothing of the old window's deadline: v0.10.0 knows no such flag, so
// a new window started by it must manage on its own default.
func newWindowArgs(url, handover, canonical string) []string {
	return []string{"--url", url, "--handover", handover, "--canonical", canonical}
}

// panelArgs is what v0.10.0's window starts its panel with.
func panelArgs(window int, standSocket string) []string {
	args := []string{"--owner-pid", strconv.Itoa(window)}
	if standSocket != "" {
		args = append(args, "--stand-socket", standSocket)
	}
	return args
}

// tagWindowFiles are the files of the tag's cmd/fleetdeck-window the copies
// above come from.
var tagWindowFiles = []string{"update.go", "main.go", "owner.go"}

// pin is one thing a copy here says about the tag, as the tag's source says it.
type pin struct {
	file, what string
	re         *regexp.Regexp
}

var pins = []pin{
	{"update.go", "the measured handover is 812 ms", regexp.MustCompile(`measuredWorstHandover\s*=\s*812 \* time\.Millisecond\n`)},
	{"update.go", "the handover margin is 3", regexp.MustCompile(`handoverMargin\s*=\s*3\n`)},
	{"update.go", "the handover deadline is the two multiplied", regexp.MustCompile(`handoverTimeout\s*=\s*measuredWorstHandover \* handoverMargin\n`)},
	{"update.go", "the new window is started with --url, --handover and --canonical and nothing else",
		regexp.MustCompile(`exec\.Command\(filepath\.Join\(staged, "Contents", "MacOS", "fleetdeck-window"\),\s*"--url", url, "--handover", handover, "--canonical", canonical\)`)},
	{"main.go", "the button's update waits the handover deadline", regexp.MustCompile(`HandoverTimeout:\s*handoverTimeout,`)},
	{"main.go", "the button's update starts the new window with launchNewWindow", regexp.MustCompile(`Launch:\s*launchNewWindow\(url\),`)},
	{"main.go", "the button's update pauses the window's keeper", regexp.MustCompile(`Pause:\s*kept\.stop,`)},
	{"main.go", "the button's update resumes the window's keeper", regexp.MustCompile(`Resume:\s*kept\.start,`)},
	{"owner.go", "the panel is started with its window's pid", regexp.MustCompile(`args := \[\]string\{"--owner-pid", strconv\.Itoa\(window\)\}`)},
	{"owner.go", "the panel is started with the stand's socket", regexp.MustCompile(`args = append\(args, "--stand-socket", standSocket\)`)},
	{"owner.go", "the keeper's throttle is 10 s", regexp.MustCompile(`launchdThrottle\s*=\s*10 \* time\.Second\n`)},
	{"owner.go", "the keeper polls a panel it did not start every 2 s", regexp.MustCompile(`takenPanelPoll\s*=\s*2 \* time\.Second\n`)},
}

// copiedFrom says which of the copies in this program no longer match the
// tag's source, by file name.
func copiedFrom(sources map[string]string) error {
	var drifted []string
	for _, p := range pins {
		if !p.re.MatchString(sources[p.file]) {
			drifted = append(drifted, p.file+": "+p.what)
		}
	}
	if len(drifted) > 0 {
		return fmt.Errorf("this program's copies of v0.10.0's window no longer match its source:\n  %s", strings.Join(drifted, "\n  "))
	}
	return nil
}

// readTagWindow reads the files copiedFrom compares with, from the tag's
// cmd/fleetdeck-window.
func readTagWindow(dir string) (map[string]string, error) {
	sources := map[string]string{}
	for _, name := range tagWindowFiles {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		sources[name] = string(data)
	}
	return sources, nil
}

// handoverSteps are the steps a new window reports on its way to done, in the
// order Takeover takes them.
var handoverSteps = []string{"handover:alive", "handover:panel", "handover:swapped", "handover:done"}

// handoverInOrder says whether an update's progress holds every handover step,
// in order, with no failure, and ends done.
func handoverInOrder(steps []string) error {
	next := 0
	for _, s := range steps {
		if s == "handover:failed" {
			return fmt.Errorf("the new window reported failed (steps %v)", steps)
		}
		if next < len(handoverSteps) && s == handoverSteps[next] {
			next++
			continue
		}
		for _, h := range handoverSteps {
			if s == h {
				return fmt.Errorf("%s came out of order (steps %v)", s, steps)
			}
		}
	}
	if next < len(handoverSteps) {
		return fmt.Errorf("the new window never reported %s (steps %v)", handoverSteps[next], steps)
	}
	if steps[len(steps)-1] != "done" {
		return fmt.Errorf("the update did not end done (steps %v)", steps)
	}
	return nil
}
