// Command updatecheck is an update from a released version to this branch,
// the way that version, installed, does it when its Update button is pressed,
// run on a CI runner and nowhere else.
//
// It is built inside a checkout of the release's tag, against that tag's
// internal/supervisor, so that the half of the update the installed app does
// is the release's own code: supervisor.Update.Run, with its handover
// deadline, stopping and starting its panel through its Keeper. The new
// window is this branch's, started from this branch's app the way the release
// starts it, as a child of this program.
//
// Three things are not the installed app's, and a pass says nothing about
// them:
//
//   - the source: this branch's app is unpacked from a local archive, where
//     the release downloads one from GitHub;
//   - the seal: Seal.Verify is not asked, because this branch's app is sealed
//     ad hoc on a runner with no certificate;
//   - the press: Run is called directly, because the button is in the page.
//
// What the window adds around Update lives in its package main, which cannot
// be imported, so it is copied here, once per way an old window starts the
// new window and its panel: a profile. profileFor picks the one whose pins
// the tag's own source holds, before anything runs, and refuses a tag no
// profile matches.
//
// Usage: updatecheck -old-archive <release zip> -new-archive <branch zip>
//
//	-url <stand panel URL> -want-revision <branch commit> -want-old-version <tag>
//	-tag-window <tag's cmd/fleetdeck-window>
//
// updatecheck -previous-release prints the release a person updates from to
// HEAD, from the git repository it is run in.
package main

import (
	"fmt"
	"io/fs"
	neturl "net/url"
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

// appsIn is every app bundle on disk under dir, not looking inside a bundle for
// more: what LaunchServices could open from there at the moment it is asked.
func appsIn(dir string) map[string]bool {
	apps := map[string]bool{}
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && path != dir && strings.HasSuffix(path, ".app") {
			apps[path] = true
			return filepath.SkipDir
		}
		return nil
	})
	return apps
}

// stagedRecords are the paths a LaunchServices dump holds in canonical's
// staging directory, told apart by whether exists finds them on disk. A record
// of a bundle still there is one LaunchServices can open -- the version just
// replaced, under the app's name; a record of a bundle gone is its own
// bookkeeping.
func stagedRecords(dump, canonical string, exists func(string) bool) (existing, gone []string) {
	staging := supervisor.StagingDir(canonical) + string(filepath.Separator)
	for _, m := range lsPathLine.FindAllStringSubmatch(dump, -1) {
		path := m[1]
		if !strings.HasPrefix(path, staging) {
			continue
		}
		if exists(path) {
			existing = append(existing, path)
		} else {
			gone = append(gone, path)
		}
	}
	return existing, gone
}

// resolvesTo says whether what a bundle identifier opens is right. The
// identifier of the build installed at canonical must open exactly canonical.
// Any other -- the identifier of the version replaced -- may open anything but
// a path in the staging directory, whether its bundle is still there or not:
// that is opening the version replaced. A path elsewhere that is gone, such as
// the directory this program unpacked the release into, is LaunchServices'
// bookkeeping and not a way back to that version.
func resolvesTo(got, canonical string, installed bool) error {
	if installed {
		if got != canonical {
			return fmt.Errorf("the installed build's identifier opens %q, want %s", got, canonical)
		}
		return nil
	}
	if strings.HasPrefix(got, supervisor.StagingDir(canonical)+string(filepath.Separator)) {
		return fmt.Errorf("the replaced version's identifier opens %s, in the staging directory", got)
	}
	return nil
}

// fleetdeckPaths are the bundle paths in a LaunchServices dump that name
// fleetdeck, as the dump spells them: what is printed of each look.
func fleetdeckPaths(dump string) []string {
	var lines []string
	for _, line := range strings.Split(dump, "\n") {
		if lsPathLine.MatchString(line) && strings.Contains(line, "fleetdeck") {
			lines = append(lines, line)
		}
	}
	return lines
}

// waitFor asks cond every poll until it says yes. Past within it says what did
// not happen, and why the last look said no when cond gave a reason.
func waitFor(what string, within, poll time.Duration, cond func() (bool, error)) error {
	deadline := time.Now().Add(within)
	for {
		ok, err := cond()
		if ok {
			return nil
		}
		if time.Now().After(deadline) {
			if err != nil {
				return fmt.Errorf("%s did not happen within %s: %w", what, within, err)
			}
			return fmt.Errorf("%s did not happen within %s", what, within)
		}
		time.Sleep(poll)
	}
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

// Every old window's handover deadline (cmd/fleetdeck-window/update.go of
// each tag a profile below is held to): the old window gives the new one this
// long from its start to done, and kills it when it runs out. Nothing a new
// version does can change it.
const (
	measuredWorstHandover = 812 * time.Millisecond
	handoverMargin        = 3
	handoverTimeout       = measuredWorstHandover * handoverMargin
)

// Every old window's keeper settings for the panel it starts
// (cmd/fleetdeck-window/owner.go of the tag).
const (
	launchdThrottle = 10 * time.Second
	takenPanelPoll  = 2 * time.Second
)

// tagWindowFiles are the files of the tag's cmd/fleetdeck-window the copies
// here come from.
var tagWindowFiles = []string{"update.go", "main.go", "owner.go"}

// pin is one thing a copy here says about the tag, as the tag's source says it.
type pin struct {
	file, what string
	re         *regexp.Regexp
}

// windowProfile is one way an old window starts the new window and its own
// panel. Its pins are the tag's source that says so; its functions are this
// program's copies of what that source does.
type windowProfile struct {
	// name is the first release whose window starts them this way.
	name string
	pins []pin
	// newWindowArgs is what the old window starts the new window with.
	newWindowArgs func(url, handover, canonical string) []string
	// panelArgs is what the old window's keeper starts its panel with, on a
	// stand and at the port of the URL the window looks at.
	panelArgs func(window int, standSocket string, port int) []string
}

// The pins every old window holds: the deadline, how its button's update is
// made, and its keeper's settings.
var sharedPins = []pin{
	{"update.go", "the measured handover is 812 ms", regexp.MustCompile(`measuredWorstHandover\s*=\s*812 \* time\.Millisecond\n`)},
	{"update.go", "the handover margin is 3", regexp.MustCompile(`handoverMargin\s*=\s*3\n`)},
	{"update.go", "the handover deadline is the two multiplied", regexp.MustCompile(`handoverTimeout\s*=\s*measuredWorstHandover \* handoverMargin\n`)},
	{"main.go", "the button's update waits the handover deadline", regexp.MustCompile(`HandoverTimeout:\s*handoverTimeout,`)},
	{"main.go", "the button's update starts the new window with launchNewWindow", regexp.MustCompile(`Launch:\s*launchNewWindow\(url\),`)},
	{"main.go", "the button's update pauses the window's keeper", regexp.MustCompile(`Pause:\s*kept\.stop,`)},
	{"main.go", "the button's update resumes the window's keeper", regexp.MustCompile(`Resume:\s*kept\.start,`)},
	{"owner.go", "the panel is started with its window's pid", regexp.MustCompile(`args := \[\]string\{"--owner-pid", strconv\.Itoa\(window\)\}`)},
	{"owner.go", "the panel is started with the stand's socket", regexp.MustCompile(`args = append\(args, "--stand-socket", standSocket\)`)},
	{"owner.go", "the keeper's throttle is 10 s", regexp.MustCompile(`launchdThrottle\s*=\s*10 \* time\.Second\n`)},
	{"owner.go", "the keeper polls a panel it did not start every 2 s", regexp.MustCompile(`takenPanelPoll\s*=\s*2 \* time\.Second\n`)},
}

// How an old window starts the new window, told nothing of its deadline: a
// window that knows no such flag, so the new window must manage on its own
// default.
var (
	windowWithoutDeadlinePins = []pin{
		{"update.go", "the new window is started with --url, --handover and --canonical and nothing else",
			regexp.MustCompile(`exec\.Command\(filepath\.Join\(staged, "Contents", "MacOS", "fleetdeck-window"\),\s*"--url", url, "--handover", handover, "--canonical", canonical\)`)},
	}
	windowWithoutDeadline = func(url, handover, canonical string) []string {
		return []string{"--url", url, "--handover", handover, "--canonical", canonical}
	}
)

// How an old window starts the new window, told its deadline with
// --handover-timeout.
var (
	windowWithDeadlinePins = []pin{
		{"update.go", "the deadline flag is --handover-timeout", regexp.MustCompile(`handoverTimeoutFlag\s*=\s*"handover-timeout"\n`)},
		{"update.go", "newWindowArgs takes the URL, the canonical path and the handover, in that order",
			regexp.MustCompile(`func newWindowArgs\(url, canonical, handover string\) \[\]string \{`)},
		{"update.go", "the new window is started with --url, --handover, --canonical and --handover-timeout",
			regexp.MustCompile(`return \[\]string\{\s*"--url", url,\s*"--handover", handover,\s*"--canonical", canonical,\s*"--" \+ handoverTimeoutFlag, handoverTimeout\.String\(\),\s*\}`)},
		{"update.go", "the new window is started with newWindowArgs",
			regexp.MustCompile(`exec\.Command\(filepath\.Join\(staged, "Contents", "MacOS", "fleetdeck-window"\),\s*newWindowArgs\(url, canonical, handover\)\.\.\.\)`)},
	}
	windowWithDeadline = func(url, handover, canonical string) []string {
		return []string{"--url", url, "--handover", handover, "--canonical", canonical, "--handover-timeout", handoverTimeout.String()}
	}
)

// How an old window's keeper starts its panel, told nothing of its port: the
// panel listens on server.port.
var (
	panelWithoutPortPins = []pin{
		{"owner.go", "the panel is started with its window's pid and the stand's socket only",
			regexp.MustCompile(`func panelArgs\(window int, standSocket string\) \[\]string \{`)},
	}
	panelWithoutPort = func(window int, standSocket string, _ int) []string {
		args := []string{"--owner-pid", strconv.Itoa(window)}
		if standSocket != "" {
			args = append(args, "--stand-socket", standSocket)
		}
		return args
	}
)

// How an old window's keeper starts its panel, told the port of the window's
// URL with --port. --config is a dev app's alone, never the app's.
var (
	panelWithPortPins = []pin{
		{"owner.go", "the panel is started with its port, when the window knows it",
			regexp.MustCompile(`if port != 0 \{\s*args = append\(args, "--port", strconv\.Itoa\(port\)\)\s*\}`)},
	}
	panelWithPort = func(window int, standSocket string, port int) []string {
		args := []string{"--owner-pid", strconv.Itoa(window)}
		if port != 0 {
			args = append(args, "--port", strconv.Itoa(port))
		}
		if standSocket != "" {
			args = append(args, "--stand-socket", standSocket)
		}
		return args
	}
)

// pinsOf is a profile's pins: what its window and its panel are started with,
// and what every old window holds.
func pinsOf(groups ...[]pin) []pin {
	all := append([]pin(nil), sharedPins...)
	for _, g := range groups {
		all = append(all, g...)
	}
	return all
}

// profiles are the old windows this program can play. A release that changes
// how its window starts the new window or its panel needs one of its own,
// added with that change: TestThisBranchsWindowHasAProfile holds that.
var profiles = []windowProfile{
	{name: "v0.10.0", pins: pinsOf(windowWithoutDeadlinePins, panelWithoutPortPins),
		newWindowArgs: windowWithoutDeadline, panelArgs: panelWithoutPort},
	{name: "v0.10.1", pins: pinsOf(windowWithDeadlinePins, panelWithoutPortPins),
		newWindowArgs: windowWithDeadline, panelArgs: panelWithoutPort},
	{name: "v0.11.0", pins: pinsOf(windowWithDeadlinePins, panelWithPortPins),
		newWindowArgs: windowWithDeadline, panelArgs: panelWithPort},
}

// drifted is what of p's pins the tag's source no longer says.
func (p windowProfile) drifted(sources map[string]string) []string {
	var off []string
	for _, pn := range p.pins {
		if !pn.re.MatchString(sources[pn.file]) {
			off = append(off, pn.file+": "+pn.what)
		}
	}
	return off
}

// profileFor is the one profile whose every pin the tag's source holds. None
// is the tag's window starting things in a way this program cannot play yet,
// and it says so rather than playing it some other way.
func profileFor(sources map[string]string) (windowProfile, error) {
	var matched []windowProfile
	var why []string
	for _, p := range profiles {
		off := p.drifted(sources)
		if len(off) == 0 {
			matched = append(matched, p)
			continue
		}
		why = append(why, fmt.Sprintf("%s's: %s", p.name, strings.Join(off, "; ")))
	}
	switch len(matched) {
	case 1:
		return matched[0], nil
	case 0:
		return windowProfile{}, fmt.Errorf("the old window matches none of this program's profiles, so its copies would not be its own:\n  %s\n"+
			"if that release changed how its window starts the new window or its panel, add a profile for it to scripts/updatecheck (docs/engineering/update-from-release.md)",
			strings.Join(why, "\n  "))
	}
	names := make([]string, 0, len(matched))
	for _, p := range matched {
		names = append(names, p.name)
	}
	return windowProfile{}, fmt.Errorf("the old window matches profiles %s at once: make their pins tell them apart", strings.Join(names, ", "))
}

// urlPort is the port the URL names, the one the old window's panel is told.
func urlPort(raw string) (int, error) {
	u, err := neturl.Parse(raw)
	if err != nil {
		return 0, err
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		return 0, fmt.Errorf("%s names no port", raw)
	}
	return port, nil
}

// stableTag is a release's tag: vX.Y.Z and nothing after it.
var stableTag = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)$`)

// previousRelease is the release a person updates from to HEAD: the newest
// stable tag among merged, the tags HEAD contains, other than one on HEAD
// itself, which is this release and not the one before it.
func previousRelease(merged, atHead []string) (string, error) {
	onHead := map[string]bool{}
	for _, t := range atHead {
		onHead[t] = true
	}
	best, bestKey := "", [3]int{-1, -1, -1}
	for _, t := range merged {
		m := stableTag.FindStringSubmatch(t)
		if m == nil || onHead[t] {
			continue
		}
		var key [3]int
		for i := range key {
			key[i], _ = strconv.Atoi(m[i+1])
		}
		if best == "" || key[0] > bestKey[0] ||
			key[0] == bestKey[0] && (key[1] > bestKey[1] || key[1] == bestKey[1] && key[2] > bestKey[2]) {
			best, bestKey = t, key
		}
	}
	if best == "" {
		return "", fmt.Errorf("no stable release tag vX.Y.Z is behind HEAD (tags behind it: %v; on it: %v): a fork or a clone without its history and tags has no release to update from", merged, atHead)
	}
	return best, nil
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
