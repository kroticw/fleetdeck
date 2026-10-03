// Command lsprobe measures when LaunchServices registers the path of a
// fleetdeck window started by exec, as an update starts its new window from
// /Applications/.fleetdeck-update (T-117), and whether a bundle copied into
// /Applications is registered without being started at all (T-118).
//
// It starts windows and writes to /Applications, so it runs on a CI runner
// only (the lsprobe workflow) and refuses anywhere else. It is given a window
// built with -tags lsprobe (cmd/fleetdeck-window/lsprobe_darwin.go), which
// looks at LaunchServices at each mark of its own start. Beside it this
// program watches the same path from outside: a quick look every 100 ms and
// lsregister -dump back to back. Each scenario builds its bundle under a bundle
// identifier of its own, so a quick look is about that path alone.
//
// It fails only when a scenario could not be run; what it measures is written
// to -out and to the job's summary, not judged.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/kroticw/fleetdeck/internal/supervisor"
)

const (
	dittoPath = "/usr/bin/ditto"
	plutil    = "/usr/bin/plutil"
	pkill     = "/usr/bin/pkill"

	// lookEvery is how often the quick look is taken.
	lookEvery = 100 * time.Millisecond
	// copyWatch is how long a copy that is never started is watched, and
	// copyWatchBeforeStart how long a copy is watched before it is forgotten
	// and started.
	copyWatch            = 60 * time.Second
	copyWatchBeforeStart = 10 * time.Second
	// forgottenFor is how long a forgotten path must stay unregistered before
	// it is started, and forgetWithin how long that is waited for.
	forgottenFor = 2 * time.Second
	forgetWithin = 20 * time.Second
	// activationWithin is how long a started window has to reach its first
	// activation -- its probe activates it itself ten seconds into w.Run --
	// and watchAfter how long it is watched after that.
	activationWithin = 60 * time.Second
	watchAfter       = 20 * time.Second
)

type scenario struct {
	name  string
	dir   string
	start bool
	// move: the bundle is renamed into place from a directory on the same
	// volume, as the swap moves the installed bundle into the staging
	// directory, rather than copied there.
	move bool
}

func main() {
	app := flag.String("app", "", "a fleetdeck.app whose window is built with -tags lsprobe")
	out := flag.String("out", "", "where to write what is measured")
	flag.Parse()
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	if os.Getenv("GITHUB_ACTIONS") != "true" {
		log.Fatal("lsprobe: starts windows and writes to /Applications; it runs on a CI runner only")
	}
	temp := os.Getenv("RUNNER_TEMP")
	if *app == "" || *out == "" || temp == "" {
		log.Fatal("lsprobe: -app, -out and RUNNER_TEMP are needed")
	}
	canonical := "/Applications/" + supervisor.BundleName
	// With no installed app, a window started from the staging directory
	// says so and runs on, rather than opening the installed app and going
	// (cmd/fleetdeck-window/staged.go): it reaches every mark.
	if _, err := os.Stat(canonical); err == nil {
		log.Fatalf("lsprobe: %s is installed; the window started from the staging directory would open it and go", canonical)
	}
	// The bundles never started are placed twice over, before the started ones
	// and after them: in the first run (37114670018) a copy into a hidden
	// directory in /Applications was registered about 5 s after it was made in
	// two scenarios of three, and one alone says too little.
	unstarted := func(round string) []scenario {
		return []scenario{
			{name: "copied into a hidden directory in /Applications, not started" + round, dir: "/Applications/.lsprobe-copy" + round},
			{name: "copied outside /Applications, not started" + round, dir: filepath.Join(temp, "lsprobe-copy"+round)},
			{name: "moved into the update's staging directory, as the swap moves a bundle, not started" + round, dir: supervisor.StagingDir(canonical), move: true},
		}
	}
	scenarios := unstarted("")
	scenarios = append(scenarios,
		scenario{name: "started from the update's staging directory", dir: supervisor.StagingDir(canonical), start: true},
		scenario{name: "started from another hidden directory in /Applications", dir: "/Applications/.lsprobe-run", start: true},
		scenario{name: "started outside /Applications", dir: filepath.Join(temp, "lsprobe-run"), start: true},
	)
	scenarios = append(scenarios, unstarted(", again")...)
	runID := os.Getenv("GITHUB_RUN_ID") + "." + os.Getenv("GITHUB_RUN_ATTEMPT")
	var sections []string
	failed := false
	for i, s := range scenarios {
		res, err := run(s, i, *app, *out, temp, runID)
		if err != nil {
			log.Printf("lsprobe: %s: %v", s.name, err)
			res.Note = "Not measured: " + err.Error()
			failed = true
		}
		sections = append(sections, res.markdown())
	}
	summary := "## When LaunchServices registers a bundle's path\n\n" + strings.Join(sections, "\n")
	if err := os.WriteFile(filepath.Join(*out, "summary.md"), []byte(summary), 0o644); err != nil {
		log.Fatal(err)
	}
	if path := os.Getenv("GITHUB_STEP_SUMMARY"); path != "" {
		if f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644); err == nil {
			_, _ = f.WriteString(summary)
			_ = f.Close()
		}
	}
	fmt.Print(summary)
	if failed {
		os.Exit(1)
	}
}

func run(s scenario, i int, app, out, temp, runID string) (result, error) {
	bundle := filepath.Join(s.dir, supervisor.BundleName)
	res := result{Name: s.name, Bundle: bundle, Started: s.start}
	dir := filepath.Join(out, fmt.Sprintf("%d-%s", i+1, slug(s.name)))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return res, err
	}
	if _, err := os.Stat(s.dir); err == nil {
		return res, fmt.Errorf("%s is there already", s.dir)
	}
	ls := supervisor.LaunchServices{Lsregister: supervisor.LsregisterPath}
	defer func() {
		if err := ls.Forget(bundle); err != nil {
			log.Printf("lsprobe: %v", err)
		}
		if err := os.RemoveAll(s.dir); err != nil {
			log.Printf("lsprobe: %v", err)
		}
	}()

	// The bundle, under an identifier of its own, made outside the place it
	// is measured in and then copied there whole, as an update extracts it.
	src := filepath.Join(temp, fmt.Sprintf("lsprobe-src-%d", i+1), supervisor.BundleName)
	if err := command(dittoPath, app, src); err != nil {
		return res, err
	}
	ident := fmt.Sprintf("dev.fleetdeck.lsprobe.%s.%d", runID, i+1)
	if err := command(plutil, "-replace", "CFBundleIdentifier", "-string", ident, filepath.Join(src, "Contents", "Info.plist")); err != nil {
		return res, err
	}
	if was, err := quickLook(bundle); err == nil && was {
		return res, fmt.Errorf("%s is registered before it is copied", bundle)
	}
	if s.move {
		if err := os.MkdirAll(s.dir, 0o755); err != nil {
			return res, err
		}
		if err := os.Rename(src, bundle); err != nil {
			return res, err
		}
	} else if err := command(dittoPath, src, bundle); err != nil {
		return res, err
	}
	res.CopyEnd = time.Now().UnixMilli()

	if !s.start {
		res.WatchMs = copyWatch.Milliseconds()
		res.Observed = watchFor(bundle, copyWatch)
		return res, writeRecords(filepath.Join(dir, "observed.jsonl"), res.Observed)
	}

	copied := watchFor(bundle, copyWatchBeforeStart)
	_, res.CopyRegistered = firstRegistered(copied, res.CopyEnd)
	if err := writeRecords(filepath.Join(dir, "observed-after-copy.jsonl"), copied); err != nil {
		return res, err
	}
	if err := ls.Forget(bundle); err != nil {
		return res, err
	}
	if err := stayForgotten(bundle); err != nil {
		return res, err
	}

	home := filepath.Join(temp, fmt.Sprintf("lsprobe-home-%d", i+1))
	port := 7830 + i
	if err := os.MkdirAll(filepath.Join(home, ".config", "fleetdeck"), 0o755); err != nil {
		return res, err
	}
	if err := os.WriteFile(filepath.Join(home, ".config", "fleetdeck", "config.yaml"), []byte("server:\n  port: "+strconv.Itoa(port)+"\n"), 0o644); err != nil {
		return res, err
	}
	marks := filepath.Join(dir, "marks.jsonl")
	windowLog, err := os.Create(filepath.Join(dir, "window.log"))
	if err != nil {
		return res, err
	}
	defer func() { _ = windowLog.Close() }()
	cmd := exec.Command(filepath.Join(bundle, "Contents", "MacOS", "fleetdeck-window"), "--url", fmt.Sprintf("http://127.0.0.1:%d/", port))
	cmd.Env = append(os.Environ(),
		"HOME="+home,
		"FLEETDECK_STAND_SOCKET="+filepath.Join(temp, fmt.Sprintf("lsprobe-no-daemon-%d.sock", i+1)),
		"FLEETDECK_LSPROBE_OUT="+marks,
	)
	cmd.Stdout, cmd.Stderr = windowLog, windowLog

	stop := make(chan struct{})
	observed := make(chan []record)
	go func() { observed <- watch(bundle, stop) }()
	res.Exec = time.Now().UnixMilli()
	if err := cmd.Start(); err != nil {
		close(stop)
		<-observed
		return res, err
	}
	activated := waitForActivation(marks, activationWithin)
	time.Sleep(watchAfter)
	close(stop)
	res.Observed = <-observed
	stopWindow(cmd, bundle)
	_ = command("/bin/cp", "-R", filepath.Join(home, "Library", "Logs"), filepath.Join(dir, "logs"))

	if err := writeRecords(filepath.Join(dir, "observed.jsonl"), res.Observed); err != nil {
		return res, err
	}
	f, err := os.Open(marks)
	if err != nil {
		return res, fmt.Errorf("the window wrote no marks: %w", err)
	}
	defer func() { _ = f.Close() }()
	if res.Marks, err = readRecords(f); err != nil {
		return res, err
	}
	if !activated {
		res.Note = fmt.Sprintf("The window reached no activation within %s.", activationWithin)
	}
	return res, nil
}

// watch looks at bundle until stop is closed: a quick look every lookEvery,
// and dumps back to back beside them.
func watch(bundle string, stop <-chan struct{}) []record {
	var mu sync.Mutex
	var recs []record
	add := func(r record) {
		mu.Lock()
		recs = append(recs, r)
		mu.Unlock()
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		tick := time.NewTicker(lookEvery)
		defer tick.Stop()
		for {
			at := time.Now()
			ok, err := quickLook(bundle)
			r := record{Kind: "look", UnixMs: at.UnixMilli(), EndUnixMs: time.Now().UnixMilli(), Bundle: bundle, Registered: ok}
			if err != nil {
				r.Err = err.Error()
			}
			add(r)
			select {
			case <-stop:
				return
			case <-tick.C:
			}
		}
	}()
	go func() {
		defer wg.Done()
		ls := supervisor.LaunchServices{Lsregister: supervisor.LsregisterPath}
		for {
			select {
			case <-stop:
				return
			default:
			}
			at := time.Now()
			ok, err := ls.Registered(bundle)
			r := record{Kind: "dump", UnixMs: at.UnixMilli(), EndUnixMs: time.Now().UnixMilli(), Bundle: bundle, Registered: ok}
			if err != nil {
				r.Err = err.Error()
			}
			add(r)
		}
	}()
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	return recs
}

func watchFor(bundle string, d time.Duration) []record {
	stop := make(chan struct{})
	time.AfterFunc(d, func() { close(stop) })
	return watch(bundle, stop)
}

// stayForgotten waits for bundle to stay unregistered for forgottenFor.
func stayForgotten(bundle string) error {
	deadline := time.Now().Add(forgetWithin)
	var since time.Time
	for time.Now().Before(deadline) {
		ok, err := quickLook(bundle)
		if err != nil {
			return err
		}
		switch {
		case ok:
			since = time.Time{}
		case since.IsZero():
			since = time.Now()
		case time.Since(since) >= forgottenFor:
			return nil
		}
		time.Sleep(lookEvery)
	}
	return fmt.Errorf("%s did not stay forgotten for %s within %s", bundle, forgottenFor, forgetWithin)
}

// waitForActivation waits for the window to write its first activation.
func waitForActivation(marks string, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(marks); err == nil && strings.Contains(string(data), `"mark":"first activation`) {
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false
}

// stopWindow ends the window, and any panel it started from bundle.
func stopWindow(cmd *exec.Cmd, bundle string) {
	_ = cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		<-done
	}
	_ = exec.Command(pkill, "-f", bundle+"/Contents/MacOS/").Run()
}

func writeRecords(path string, recs []record) error {
	var b strings.Builder
	for _, r := range recs {
		line, err := json.Marshal(r)
		if err != nil {
			return err
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func command(name string, args ...string) error {
	if out, err := exec.Command(name, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func slug(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case b.Len() > 0 && !strings.HasSuffix(b.String(), "-"):
			b.WriteByte('-')
		}
	}
	return strings.TrimSuffix(b.String(), "-")
}
