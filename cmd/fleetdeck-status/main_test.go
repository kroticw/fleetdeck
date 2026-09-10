package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kroticw/fleetdeck/internal/usage"
)

const sampleInput = `{"session_id":"abc-123","model":{"display_name":"Opus 5"},"cost":{"total_cost_usd":1.234},"context_window":{"used_percentage":41.7}}`

const sampleInputWithRateLimits = `{"session_id":"abc-123","model":{"display_name":"Opus 5"},"cost":{"total_cost_usd":1.234},"context_window":{"used_percentage":41.7},"rate_limits":{"five_hour":{"used_percentage":13,"resets_at":1789068600},"seven_day":{"used_percentage":40,"resets_at":1789232400}}}`

// sampleInputWithPartialRateLimits carries only one of the two windows
// writeRateLimitsTo requires -- the shape a schema drift (a field renamed,
// moved, or dropped) would produce, as opposed to sampleInput's total
// absence of rate_limits, which is the ordinary non-subscriber case.
const sampleInputWithPartialRateLimits = `{"session_id":"abc-123","model":{"display_name":"Opus 5"},"cost":{"total_cost_usd":1.234},"context_window":{"used_percentage":41.7},"rate_limits":{"five_hour":{"used_percentage":13,"resets_at":1789068600}}}`

func TestRenderShowsModelCostAndContext(t *testing.T) {
	in, err := parse([]byte(sampleInput))
	if err != nil {
		t.Fatal(err)
	}
	got := render(in)
	for _, want := range []string{"Opus 5", "1.23", "42%"} {
		if !strings.Contains(got, want) {
			t.Fatalf("status line missing %q: %s", want, got)
		}
	}
}

func TestReportPostsToThePanel(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, r.ContentLength)
		r.Body.Read(b)
		body = string(b)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	in, _ := parse([]byte(sampleInput))
	if err := report(srv.URL, in); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "abc-123") || !strings.Contains(body, "41.7") {
		t.Fatalf("report body lost data: %s", body)
	}
}

func TestReportFailureDoesNotBreakRendering(t *testing.T) {
	in, _ := parse([]byte(sampleInput))
	if err := report("http://127.0.0.1:1", in); err == nil {
		t.Fatal("an unreachable panel must be reported to the caller")
	}
	if got := render(in); got == "" {
		t.Fatal("the status line must still render when the panel is down")
	}
}

func TestEmptyStdinIsNotSuccess(t *testing.T) {
	if _, err := parse([]byte("")); err == nil {
		t.Fatal("empty input must fail: nothing to read is not a healthy status line")
	}
}

// --- resolveWrapCmd: the flag-then-config precedence ---------------------
//
// The flag wins whenever it is given, full stop -- it is the only source
// guaranteed to reach a process Claude Code launched however it likes
// (spec: not every launcher passes environment variables through, which is
// the whole reason this is a flag and not one). config.yaml is read only
// when the flag is empty.

func TestResolveWrapCmdPrefersTheFlagOverConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("statusline:\n  wrap: from-config\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := resolveWrapCmd("from-flag", path)
	if got != "from-flag" {
		t.Fatalf("got %q, want the flag value even though config.yaml has one too", got)
	}
}

func TestResolveWrapCmdFallsBackToConfigWhenFlagIsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("statusline:\n  wrap: from-config\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := resolveWrapCmd("", path)
	if got != "from-config" {
		t.Fatalf("got %q, want config.yaml's value when the flag was not given", got)
	}
}

func TestResolveWrapCmdIsEmptyWithNeitherFlagNorConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.yaml")
	got := resolveWrapCmd("", path)
	if got != "" {
		t.Fatalf("got %q, want empty -- no flag, no config file at all", got)
	}
}

// TestParseReadsRateLimits is the fix this command exists for: Claude Code's
// own statusline schema, captured rather than silently dropped the way an
// unmarshal into a struct with no matching field already would.
func TestParseReadsRateLimits(t *testing.T) {
	in, err := parse([]byte(sampleInputWithRateLimits))
	if err != nil {
		t.Fatal(err)
	}
	if in.RateLimits.FiveHour == nil || in.RateLimits.SevenDay == nil {
		t.Fatalf("rate_limits not parsed: %+v", in.RateLimits)
	}
	if in.RateLimits.FiveHour.UsedPercentage != 13 || in.RateLimits.SevenDay.UsedPercentage != 40 {
		t.Fatalf("rate_limits values wrong: %+v", in.RateLimits)
	}
	if in.RateLimits.SpendLimit != nil {
		t.Fatal("spend_limit was not in the input; must stay absent, not zero-valued")
	}
}

// TestWriteRateLimitsRoundTripsThroughReadLocal is what makes the local
// file usable by the panel at all: writeRateLimits' output must be exactly
// what usage.ReadLocal expects, not merely valid JSON.
func TestWriteRateLimitsRoundTripsThroughReadLocal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rate_limits.json")
	tracePath := filepath.Join(t.TempDir(), "rate_limits_trace.log")

	in, err := parse([]byte(sampleInputWithRateLimits))
	if err != nil {
		t.Fatal(err)
	}
	if err := writeRateLimitsTo(path, tracePath, in); err != nil {
		t.Fatal(err)
	}
	got, err := usage.ReadLocal(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.FiveHour.Utilization != 13 || got.SevenDay.Utilization != 40 {
		t.Fatalf("round trip lost the values: %+v", got)
	}
	if got.FetchedAt.IsZero() {
		t.Fatal("writeRateLimits must record when it wrote, not leave it zero")
	}
}

// TestWriteRateLimitsSkipsWhenEitherWindowIsMissing covers the invariant
// the rest of this codebase already assumes (usage.Fetcher's own network
// path enforces the same rule): five_hour and seven_day arrive together or
// not at all, never one without the other in the shared local file. The
// ordinary non-subscriber case (no rate_limits at all) is not a schema
// drift and must not be traced either.
func TestWriteRateLimitsSkipsWhenEitherWindowIsMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rate_limits.json")
	tracePath := filepath.Join(t.TempDir(), "rate_limits_trace.log")
	in, err := parse([]byte(sampleInput)) // no rate_limits at all
	if err != nil {
		t.Fatal(err)
	}
	if err := writeRateLimitsTo(path, tracePath, in); err != nil {
		t.Fatal(err)
	}
	if _, err := usage.ReadLocal(path); !errors.Is(err, usage.ErrNoLocalFile) {
		t.Fatalf("no file should have been written, got err=%v", err)
	}
	if _, err := os.Stat(tracePath); !os.IsNotExist(err) {
		t.Fatalf("no rate_limits at all is the ordinary case, must not be traced: stat err=%v", err)
	}
}

// TestWriteRateLimitsTracesAPartialWindow is the fix the orchestrator
// required: a payload that carries only one of the two required windows is
// exactly what a schema drift (a field renamed, moved, or dropped) would
// look like, and silently doing nothing here is the same shape of failure
// this whole task exists to stop -- a value that quietly stops updating
// with nothing on record to say why. The file must still not be written
// (the pair invariant holds), but the incomplete arrival must leave a
// trace a person can find.
func TestWriteRateLimitsTracesAPartialWindow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rate_limits.json")
	tracePath := filepath.Join(t.TempDir(), "rate_limits_trace.log")
	in, err := parse([]byte(sampleInputWithPartialRateLimits))
	if err != nil {
		t.Fatal(err)
	}
	if err := writeRateLimitsTo(path, tracePath, in); err != nil {
		t.Fatal(err)
	}
	if _, err := usage.ReadLocal(path); !errors.Is(err, usage.ErrNoLocalFile) {
		t.Fatalf("an incomplete pair must still not produce a rate_limits.json, got err=%v", err)
	}
	body, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatalf("expected a trace file to exist, got: %v", err)
	}
	if !strings.Contains(string(body), "five_hour=true") || !strings.Contains(string(body), "seven_day=false") {
		t.Fatalf("trace does not name which windows arrived: %q", body)
	}
}

// --- statusLineOutput: the pass-through contract -------------------------

func fakeRun(output string, err error) func(string, []byte) ([]byte, error) {
	return func(string, []byte) ([]byte, error) {
		return []byte(output), err
	}
}

func TestStatusLineOutputPrintsTheWrappedToolVerbatimWhenItSucceeds(t *testing.T) {
	in, _ := parse([]byte(sampleInput))
	got := statusLineOutput([]byte(sampleInput), in, nil, "claudeline", fakeRun("wrapped tool's own text\n", nil))
	if string(got) != "wrapped tool's own text\n" {
		t.Fatalf("got %q, want the wrapped tool's output unchanged", got)
	}
}

// TestStatusLineOutputFallsBackWhenTheWrappedToolFails is the fix the
// pass-through requirement's other half exists for: the wrapped tool being
// missing, unexecutable, or exiting non-zero must never leave the status
// line blank.
func TestStatusLineOutputFallsBackWhenTheWrappedToolFails(t *testing.T) {
	in, _ := parse([]byte(sampleInput))
	got := statusLineOutput([]byte(sampleInput), in, nil, "claudeline", fakeRun("", errors.New("not found")))
	if !strings.Contains(string(got), "Opus 5") {
		t.Fatalf("wrap failed but no fallback rendered: %q", got)
	}
}

func TestStatusLineOutputFallsBackToStatusUnavailableWhenParseAlsoFailed(t *testing.T) {
	got := statusLineOutput([]byte("not json"), statusInput{}, errors.New("parse error"), "claudeline", fakeRun("", errors.New("not found")))
	if string(got) != "Claude | status unavailable\n" {
		t.Fatalf("got %q, want the fixed unavailable line", got)
	}
}

func TestStatusLineOutputWithNoWrapConfiguredUsesItsOwnRender(t *testing.T) {
	in, _ := parse([]byte(sampleInput))
	// wrapCmd empty: run must never be called at all.
	called := false
	run := func(string, []byte) ([]byte, error) {
		called = true
		return nil, nil
	}
	got := statusLineOutput([]byte(sampleInput), in, nil, "", run)
	if called {
		t.Fatal("run must not be called when no wrap command is configured")
	}
	if !strings.Contains(string(got), "Opus 5") {
		t.Fatalf("got %q, want this command's own render", got)
	}
}

// --- runWrapped: the real subprocess path ---------------------------------

func TestRunWrappedPassesStdinThroughAndCapturesStdout(t *testing.T) {
	out, err := runWrapped("cat", []byte("hello from stdin"))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "hello from stdin" {
		t.Fatalf("got %q, want stdin echoed back unchanged", out)
	}
}

func TestRunWrappedReportsAMissingCommand(t *testing.T) {
	if _, err := runWrapped("this-command-does-not-exist-fleetdeck-status-test", nil); err == nil {
		t.Fatal("a command that cannot run at all must return an error")
	}
}

func TestRunWrappedReportsANonZeroExit(t *testing.T) {
	if _, err := runWrapped("exit 1", nil); err == nil {
		t.Fatal("a non-zero exit must be reported as an error, even with no output to distrust")
	}
}

// --- end to end: the ordering guarantee, through the real binary ---------
//
// statusLineOutput and writeRateLimits are each tested in isolation above.
// This is the property neither of those alone proves: that main's own
// statement order actually holds in the compiled binary, not just in
// functions a test calls directly. Three separate, named failures, each
// run alone against the real process: a wrapped command that does not
// exist, one that exists but exits non-zero, and a rate-limits file that
// cannot be written at all. Every one of them must still produce the
// status line.
func buildFleetdeckStatusBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "fleetdeck-status-bin")
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return bin
}

func TestEndToEndSurvivesAWrappedCommandThatDoesNotExist(t *testing.T) {
	bin := buildFleetdeckStatusBinary(t)
	cmd := exec.Command(bin, "-wrap", "this-command-does-not-exist-fleetdeck-status-test")
	cmd.Stdin = strings.NewReader(sampleInput)
	cmd.Env = append(os.Environ(), "FLEETDECK_ENDPOINT=http://127.0.0.1:1")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("exited with an error: %v", err)
	}
	if !strings.Contains(string(out), "Opus 5") {
		t.Fatalf("status line missing with a non-existent wrapped command: %q", out)
	}
}

func TestEndToEndSurvivesAWrappedCommandThatExitsNonZero(t *testing.T) {
	bin := buildFleetdeckStatusBinary(t)
	cmd := exec.Command(bin, "-wrap", "exit 1")
	cmd.Stdin = strings.NewReader(sampleInput)
	cmd.Env = append(os.Environ(), "FLEETDECK_ENDPOINT=http://127.0.0.1:1")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("exited with an error: %v", err)
	}
	if !strings.Contains(string(out), "Opus 5") {
		t.Fatalf("status line missing with a wrapped command that exited non-zero: %q", out)
	}
}

func TestEndToEndSurvivesAnUnwritableRateLimitsPath(t *testing.T) {
	bin := buildFleetdeckStatusBinary(t)
	readOnlyDir := t.TempDir()
	if err := os.Chmod(readOnlyDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(readOnlyDir, 0o700) }) // let t.TempDir() clean up after itself

	cmd := exec.Command(bin)
	cmd.Stdin = strings.NewReader(sampleInputWithRateLimits)
	cmd.Env = append(os.Environ(),
		"FLEETDECK_ENDPOINT=http://127.0.0.1:1",
		"FLEETDECK_RATE_LIMITS_PATH="+filepath.Join(readOnlyDir, "rate_limits.json"),
	)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("exited with an error: %v", err)
	}
	if !strings.Contains(string(out), "Opus 5") {
		t.Fatalf("status line missing with an unwritable rate-limits path: %q", out)
	}
}
