package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kroticw/fleetdeck/internal/config"
	"github.com/kroticw/fleetdeck/internal/daemon"
	"github.com/kroticw/fleetdeck/internal/state"
	"github.com/kroticw/fleetdeck/internal/usage"
)

// sampleUUID is a well-formed session UUID: transcript.Locate refuses anything that
// is not one, so a test fixture cannot use a short id here.
const sampleUUID = "11111111-2222-3333-4444-555555555555"

func writeSampleCard(t *testing.T, dir string) {
	t.Helper()
	body := "---\nzone: planned\nstage: active\nprogress: 20\nsession: abc12345\nrepo: x/y\ncreated: 2026-09-09\n---\n\n# card\n"
	if err := os.WriteFile(filepath.Join(dir, "c.md"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// deadDaemon is a client bound to a socket that does not exist, so every call fails
// the way an unreachable daemon does.
func deadDaemon(t *testing.T) *daemon.Client {
	t.Helper()
	return daemon.New(filepath.Join(t.TempDir(), "absent.sock"), nil)
}

// assistantLine is one transcript line carrying a usage block, which is what
// transcript.ContextUsage reads its estimate from.
func assistantLine(tokens int) string {
	return fmt.Sprintf(`{"type":"assistant","message":{"model":"claude-opus-5","usage":{"input_tokens":0,"cache_read_input_tokens":%d}}}`, tokens)
}

// sampleTokens is the context estimate a fixture transcript carries, distinct enough
// from any reported percentage that a test can tell which of the two it is looking at.
const sampleTokens = 100

// writeTranscript lays out a projects directory the way Claude Code does —
// <projects>/<project>/<sampleUUID>.jsonl — and returns the transcript's path.
func writeTranscript(t *testing.T, projectsDir string) string {
	t.Helper()
	project := filepath.Join(projectsDir, "some-project")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(project, sampleUUID+".jsonl")
	if err := os.WriteFile(path, []byte(assistantLine(sampleTokens)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCollectReportsDaemonFailureAndKeepsBoard(t *testing.T) {
	dir := t.TempDir()
	cardsDir := filepath.Join(dir, "cards")
	if err := os.MkdirAll(cardsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeSampleCard(t, cardsDir)

	cfg := config.Default()
	cfg.BoardPath = dir
	cfg.UsageEnabled = false

	snap := NewCollector(cfg, deadDaemon(t), nil, t.TempDir()).Collect(context.Background())

	if snap.DaemonError == "" {
		t.Fatal("an unreachable daemon must be named in the snapshot")
	}
	if len(snap.Cards) != 1 {
		t.Fatalf("the board must survive a dead daemon, got %d cards", len(snap.Cards))
	}
}

func TestCollectReportsEmptyBoardAndKeepsGoing(t *testing.T) {
	cfg := config.Default()
	cfg.BoardPath = t.TempDir()
	cfg.UsageEnabled = false

	snap := NewCollector(cfg, deadDaemon(t), nil, t.TempDir()).Collect(context.Background())

	if snap.BoardError == "" {
		t.Fatal("an empty board must be reported, not pass as a board with no cards")
	}
	if snap.DaemonError == "" {
		t.Fatal("a failing board must not swallow the daemon's own failure")
	}
}

func TestCollectSkipsUsageWhenDisabled(t *testing.T) {
	cfg := config.Default()
	cfg.UsageEnabled = false
	cfg.BoardPath = ""

	uf := usage.NewFetcher(func() (string, error) {
		t.Error("usage must not be fetched when disabled")
		return "", nil
	}, "", time.Minute)

	snap := NewCollector(cfg, deadDaemon(t), uf, t.TempDir()).Collect(context.Background())

	if snap.Limits != nil {
		t.Fatal("limits must be absent when usage is disabled")
	}
	if snap.UsageError != "" {
		t.Fatalf("a disabled source has not failed, got %q", snap.UsageError)
	}
}

func TestCollectStampsTheSnapshot(t *testing.T) {
	cfg := config.Default()
	cfg.UsageEnabled = false
	cfg.BoardPath = ""

	snap := NewCollector(cfg, deadDaemon(t), nil, t.TempDir()).Collect(context.Background())
	if snap.At.IsZero() {
		t.Fatal("a collected snapshot must carry the moment it was collected: a zero At is what state.Diff reads as 'never collected'")
	}
}

func TestContextEstimateIsNotRecomputedForUnchangedFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "s.jsonl")
	if err := os.WriteFile(p, []byte(assistantLine(100)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	c := NewCollector(config.Default(), nil, nil, dir)
	first, ok, _ := c.transcriptState(p)
	if !ok || first.Tokens != 100 {
		t.Fatalf("first read must compute 100 tokens, got %d ok=%v", first.Tokens, ok)
	}

	// Rewrite the content but restore size and mtime: a cache keyed on file state
	// must not notice, and must not recompute.
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(assistantLine(999)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, fi.ModTime(), fi.ModTime()); err != nil {
		t.Fatal(err)
	}

	second, ok, _ := c.transcriptState(p)
	if !ok {
		t.Fatal("second read must still succeed")
	}
	if second.Tokens != 100 {
		t.Fatalf("unchanged size and mtime must serve the cached value, got %d", second.Tokens)
	}
}

func TestContextEstimateIsRecomputedWhenFileGrows(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "s.jsonl")
	base := assistantLine(100)
	if err := os.WriteFile(p, []byte(base+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	c := NewCollector(config.Default(), nil, nil, dir)
	if u, _, _ := c.transcriptState(p); u.Tokens != 100 {
		t.Fatalf("want 100, got %d", u.Tokens)
	}

	grown := base + "\n" + assistantLine(250)
	if err := os.WriteFile(p, []byte(grown+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if u, _, _ := c.transcriptState(p); u.Tokens != 250 {
		t.Fatalf("a grown transcript must be re-read, got %d", u.Tokens)
	}
}

// TestSilentForIsTheAgeOfTheTranscript pins spec section 6: silence is how long ago
// the session's transcript was last written, and it comes from the same stat that
// serves the context cache.
func TestSilentForIsTheAgeOfTheTranscript(t *testing.T) {
	projects := t.TempDir()
	path := writeTranscript(t, projects)
	hourAgo := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, hourAgo, hourAgo); err != nil {
		t.Fatal(err)
	}

	c := NewCollector(config.Default(), nil, nil, projects)
	views := []state.SessionView{{Session: daemon.Session{Short: "abc12345", SessionID: sampleUUID}}}
	c.enrich(views, nil)

	got := views[0].SilentFor
	if got < 55*time.Minute || got > 65*time.Minute {
		t.Fatalf("a transcript last written an hour ago must read as roughly an hour of silence, got %s", got)
	}
	if views[0].Context == nil || views[0].Context.Tokens != 100 {
		t.Fatal("the same stat must still serve the context estimate")
	}
}

// TestSilentForIsZeroWithoutATranscript pins the other half of spec section 6: no
// transcript means silence was not measured, which is not the same as a long silence.
// state.Diff never fires on a zero, so substituting a start time or "a very long
// time" here would banner every session that has just started.
func TestSilentForIsZeroWithoutATranscript(t *testing.T) {
	c := NewCollector(config.Default(), nil, nil, t.TempDir())
	views := []state.SessionView{{Session: daemon.Session{Short: "abc12345", SessionID: sampleUUID}}}
	c.enrich(views, nil)

	if views[0].SilentFor != 0 {
		t.Fatalf("a session with no transcript must read as not measured (zero), got %s", views[0].SilentFor)
	}
	if views[0].Context != nil {
		t.Fatal("a session with no transcript has no context estimate either")
	}
}

// TestEnrichCopiesTheOperatorsLabelBySessionID pins the lookup key: a label
// is matched by the session's transcript UUID (SessionID), never its short
// id, the same distinction every other UUID-keyed lookup in this package
// makes.
func TestEnrichCopiesTheOperatorsLabelBySessionID(t *testing.T) {
	c := NewCollector(config.Default(), nil, nil, t.TempDir())
	views := []state.SessionView{{Session: daemon.Session{Short: "abc12345", SessionID: sampleUUID}}}
	labels := map[string]string{sampleUUID: "orchestrator"}

	c.enrich(views, labels)

	if views[0].Label != "orchestrator" {
		t.Fatalf("Label = %q, want orchestrator", views[0].Label)
	}
}

// TestEnrichLeavesLabelEmptyWithoutInventingAnything is this package's own
// instance of the project's core rule: no label recorded for a session
// means the view shows no label, never a fabricated one and never another
// session's label borrowed by mistake.
func TestEnrichLeavesLabelEmptyWithoutInventingAnything(t *testing.T) {
	c := NewCollector(config.Default(), nil, nil, t.TempDir())
	views := []state.SessionView{{Session: daemon.Session{Short: "abc12345", SessionID: sampleUUID}}}
	labels := map[string]string{"22222222-2222-2222-2222-222222222222": "someone else's label"}

	c.enrich(views, labels)

	if views[0].Label != "" {
		t.Fatalf("a session with no recorded label must show none, got %q", views[0].Label)
	}
}

// TestSilenceIsUnmeasuredForATranscriptThatCannotBeStatted covers the other way a
// session ends up with no measurement: the transcript was located and then could not
// be read. Zero is the answer there too — substituting "a very long time" would hand
// state.Diff a silence it never observed.
func TestSilenceIsUnmeasuredForATranscriptThatCannotBeStatted(t *testing.T) {
	c := NewCollector(config.Default(), nil, nil, t.TempDir())

	_, ok, silentFor := c.transcriptState(filepath.Join(t.TempDir(), "gone.jsonl"))

	if ok {
		t.Fatal("a transcript that is not there has no context estimate")
	}
	if silentFor != 0 {
		t.Fatalf("an unstattable transcript must read as not measured (zero), got %s", silentFor)
	}
}

// TestReportedContextBeatsTheTranscriptEstimate pins spec section 3.2: the statusline
// reporter's number comes from Claude Code itself and is exact, the transcript
// estimate is the fallback. A session that has both must show the report, unmarked as
// an estimate — otherwise the reporter posts into a route whose value nothing reads.
func TestReportedContextBeatsTheTranscriptEstimate(t *testing.T) {
	projects := t.TempDir()
	writeTranscript(t, projects)

	c := NewCollector(config.Default(), nil, nil, projects)
	c.PutStatus(sampleUUID, "Opus", 42, 1.25)

	views := []state.SessionView{{Session: daemon.Session{Short: "abc12345", SessionID: sampleUUID}}}
	c.enrich(views, nil)

	got := views[0].Context
	if got == nil {
		t.Fatal("a reported session must carry a context reading")
	}
	if got.Estimated {
		t.Fatal("a reported context comes from Claude Code itself and must not be marked as an estimate")
	}
	if got.Window == 0 || float64(got.Tokens)/float64(got.Window)*100 != 42 {
		t.Fatalf("the reported occupancy must be 42%%, got %d/%d", got.Tokens, got.Window)
	}
}

// TestReportedModelAndCostReachTheView pins the two values that exist in the panel
// only because the reporter sends them: Claude Code hands the model name and the
// running cost to its statusline command and to nothing else (spec section 3.2). They
// have no transcript fallback, so a report is the only way either can ever be shown.
func TestReportedModelAndCostReachTheView(t *testing.T) {
	projects := t.TempDir()
	writeTranscript(t, projects)

	c := NewCollector(config.Default(), nil, nil, projects)
	c.PutStatus(sampleUUID, "Opus", 42, 1.25)

	views := []state.SessionView{{Session: daemon.Session{Short: "abc12345", SessionID: sampleUUID}}}
	c.enrich(views, nil)

	if views[0].Model != "Opus" {
		t.Fatalf("the reported model name must reach the view, got %q", views[0].Model)
	}
	if views[0].CostUSD == nil {
		t.Fatal("a session with a report has a cost, even when that cost is nothing")
	}
	if *views[0].CostUSD != 1.25 {
		t.Fatalf("want the reported cost of 1.25, got %v", *views[0].CostUSD)
	}
}

// TestAZeroCostIsStillAReport is the reason CostUSD is a pointer: a session that has
// so far cost nothing is not a session nobody reported on.
func TestAZeroCostIsStillAReport(t *testing.T) {
	c := NewCollector(config.Default(), nil, nil, t.TempDir())
	c.PutStatus(sampleUUID, "Opus", 0, 0)

	views := []state.SessionView{{Session: daemon.Session{Short: "abc12345", SessionID: sampleUUID}}}
	c.enrich(views, nil)

	if views[0].CostUSD == nil {
		t.Fatal("a reported cost of zero must be a cost, not an absence")
	}
	if *views[0].CostUSD != 0 {
		t.Fatalf("want 0, got %v", *views[0].CostUSD)
	}
}

// TestAnUnreportedSessionCarriesNoModelOrCost: neither field has a fallback, so a
// session whose reporter is not installed must show nothing rather than something
// derived from elsewhere.
func TestAnUnreportedSessionCarriesNoModelOrCost(t *testing.T) {
	projects := t.TempDir()
	writeTranscript(t, projects)

	c := NewCollector(config.Default(), nil, nil, projects)
	views := []state.SessionView{{Session: daemon.Session{Short: "abc12345", SessionID: sampleUUID}}}
	c.enrich(views, nil)

	if views[0].Model != "" || views[0].CostUSD != nil {
		t.Fatalf("an unreported session must carry neither field, got model=%q cost=%v", views[0].Model, views[0].CostUSD)
	}
}

// TestAReportReachesTheSnapshotJSON follows one report the whole way it travels
// inside this process — PutStatus, the store, enrich, the snapshot, the wire — since
// every field of it exists only to be read by a browser. The daemon's own contribution
// (producing the session in the first place) is covered by the degrade-in-parts tests
// above and by the live check in the task report.
func TestAReportReachesTheSnapshotJSON(t *testing.T) {
	projects := t.TempDir()
	writeTranscript(t, projects)

	c := NewCollector(config.Default(), nil, nil, projects)
	c.PutStatus(sampleUUID, "Opus", 42, 1.25)

	snap := state.Snapshot{
		At:       time.Now(),
		Sessions: []state.SessionView{{Session: daemon.Session{Short: "abc12345", SessionID: sampleUUID}}},
	}
	c.enrich(snap.Sessions, nil)

	raw, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"model":"Opus"`, `"costUSD":1.25`, `"estimated":false`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("the snapshot on the wire must carry %s: %s", want, raw)
		}
	}
}

// TestReportIsKeyedByTranscriptUUIDNotShortID pins which id the store is keyed by.
// The reporter learns its session id from Claude Code, which knows nothing of the
// daemon's short ids, so a store keyed on Short would never match a single report.
func TestReportIsKeyedByTranscriptUUIDNotShortID(t *testing.T) {
	c := NewCollector(config.Default(), nil, nil, t.TempDir())
	c.PutStatus("abc12345", "Opus", 42, 1.25)

	views := []state.SessionView{{Session: daemon.Session{Short: "abc12345", SessionID: sampleUUID}}}
	c.enrich(views, nil)

	if views[0].Context != nil {
		t.Fatal("a report filed under a short id must not be attached to the session whose transcript UUID it isn't")
	}
}

// TestStaleReportFallsBackToTheEstimate pins the answer to "what happens to a report
// whose reporter stopped running": it expires, and the transcript estimate — which is
// still moving — takes over. A frozen exact number shown forever is worse than an
// honest estimate.
func TestStaleReportFallsBackToTheEstimate(t *testing.T) {
	projects := t.TempDir()
	writeTranscript(t, projects)

	c := NewCollector(config.Default(), nil, nil, projects)
	c.PutStatus(sampleUUID, "Opus", 42, 1.25)
	c.ageReport(sampleUUID, reportTTL+time.Minute)

	views := []state.SessionView{{Session: daemon.Session{Short: "abc12345", SessionID: sampleUUID}}}
	c.enrich(views, nil)

	got := views[0].Context
	if got == nil {
		t.Fatal("an expired report must leave the estimate showing, not nothing")
	}
	if !got.Estimated {
		t.Fatal("once the report has expired the reading is the transcript estimate and must say so")
	}
	if got.Tokens != 100 {
		t.Fatalf("want the transcript estimate of 100 tokens, got %d", got.Tokens)
	}
	if views[0].Model != "" || views[0].CostUSD != nil {
		t.Fatalf("an expired report takes its model and cost with it, got model=%q cost=%v", views[0].Model, views[0].CostUSD)
	}
}

// TestExpiredReportsAreForgotten keeps the store from growing for the lifetime of the
// process: a session that ended still has its last report sitting in the map.
func TestExpiredReportsAreForgotten(t *testing.T) {
	c := NewCollector(config.Default(), nil, nil, t.TempDir())
	c.PutStatus(sampleUUID, "Opus", 42, 1.25)
	c.ageReport(sampleUUID, reportTTL+time.Minute)

	c.enrich([]state.SessionView{{Session: daemon.Session{Short: "abc12345", SessionID: sampleUUID}}}, nil)

	if n := c.reportCount(); n != 0 {
		t.Fatalf("an expired report must be dropped from the store, %d left", n)
	}
}

// TestCollectDegradesWithoutADaemonClient covers the panel built with no daemon at
// all: the snapshot says so rather than the process dying on a nil pointer.
func TestCollectDegradesWithoutADaemonClient(t *testing.T) {
	cfg := config.Default()
	cfg.UsageEnabled = false
	cfg.BoardPath = ""

	snap := NewCollector(cfg, nil, nil, t.TempDir()).Collect(context.Background())
	if snap.DaemonError == "" {
		t.Fatal("a panel wired without a daemon must say so in the snapshot")
	}
}

// ageReport backdates a stored report so a test can cross reportTTL without waiting
// it out. It lives here rather than in collect.go because nothing in the panel ever
// needs to move a report backwards in time.
func (c *Collector) ageReport(sessionID string, by time.Duration) {
	c.reportMu.Lock()
	defer c.reportMu.Unlock()
	r, ok := c.reports[sessionID]
	if !ok {
		panic("ageReport: no report for " + sessionID)
	}
	r.at = r.at.Add(-by)
	c.reports[sessionID] = r
}

// reportCount is how many reports the store is holding.
func (c *Collector) reportCount() int {
	c.reportMu.Lock()
	defer c.reportMu.Unlock()
	return len(c.reports)
}

// TestCollectReportsThePinnedOrchestratorSession pins the copy Collect performs
// from configuration into the snapshot: the orchestrator column reads the pin
// from the snapshot alone and never reaches into config itself.
func TestCollectReportsThePinnedOrchestratorSession(t *testing.T) {
	cfg := config.Default()
	cfg.BoardPath = ""
	cfg.UsageEnabled = false
	cfg.OrchestratorSession = "abc12345"

	snap := NewCollector(cfg, deadDaemon(t), nil, t.TempDir()).Collect(context.Background())

	if snap.OrchestratorSession != "abc12345" {
		t.Fatalf("want the configured pin abc12345, got %q", snap.OrchestratorSession)
	}
}

// TestSetOrchestratorSessionChangesTheNextCollect pins the write path a
// concurrent HTTP handler uses: SetOrchestratorSession updates what the next
// Collect() reports, without needing a fresh Collector.
func TestSetOrchestratorSessionChangesTheNextCollect(t *testing.T) {
	cfg := config.Default()
	cfg.BoardPath = ""
	cfg.UsageEnabled = false

	c := NewCollector(cfg, deadDaemon(t), nil, t.TempDir())
	if snap := c.Collect(context.Background()); snap.OrchestratorSession != "" {
		t.Fatalf("want no pin before SetOrchestratorSession, got %q", snap.OrchestratorSession)
	}

	c.SetOrchestratorSession("xyz98765")

	snap := c.Collect(context.Background())
	if snap.OrchestratorSession != "xyz98765" {
		t.Fatalf("want the newly pinned session xyz98765, got %q", snap.OrchestratorSession)
	}
}

// TestConcurrentCollectAndSetOrchestratorSessionDoNotRace exercises exactly the
// scenario this task introduces: Collect() runs on the poll goroutine while
// SetOrchestratorSession is called from an HTTP handler goroutine. Without
// Collector.cfgMu this is a data race on cfg.OrchestratorSession that only
// `go test -race` reliably surfaces — a plain `go test` run can pass by luck.
func TestConcurrentCollectAndSetOrchestratorSessionDoNotRace(t *testing.T) {
	cfg := config.Default()
	cfg.BoardPath = ""
	cfg.UsageEnabled = false

	c := NewCollector(cfg, deadDaemon(t), nil, t.TempDir())

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			c.Collect(context.Background())
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			c.SetOrchestratorSession("abc12345")
		}
	}()
	wg.Wait()
}

// TestConcurrentEnrichAndSetSessionLabelDoNotRace is
// TestConcurrentCollectAndSetOrchestratorSessionDoNotRace's own sibling for
// the map this task added. SessionLabels is a reference type, unlike every
// other field on config.Config, so a copy of the struct alone does not make
// an independent snapshot of it — enrich() reading cfg.SessionLabels[id]
// while SetSessionLabel mutates the live map concurrently is a "concurrent
// map read and map write" fatal error outside of -race. This caught a real
// bug: Config() used to return the map by reference before this test (and
// the fix in Config() itself) existed.
//
// This exercises enrich() directly rather than going through Collect(),
// deliberately: Collect() only ever reaches this map through a session the
// daemon actually listed, and deadDaemon (every other concurrency test in
// this file's daemon of choice) always returns zero sessions — enrich's
// loop body, where the map read lives, would never run at all, and the
// test would pass whether or not the race existed. Calling enrich with the
// exact view/labels shapes Collect() itself builds keeps the coverage real
// without needing a live daemon socket.
func TestConcurrentEnrichAndSetSessionLabelDoNotRace(t *testing.T) {
	c := NewCollector(config.Default(), nil, nil, t.TempDir())
	c.SetSessionLabel(sampleUUID, "seed")
	views := []state.SessionView{{Session: daemon.Session{Short: "abc12345", SessionID: sampleUUID}}}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			c.enrich(views, c.Config().SessionLabels)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			c.SetSessionLabel(sampleUUID, "x")
		}
	}()
	wg.Wait()
}

// TestConfigReturnsAnIndependentCopyOfSessionLabels pins the fix directly,
// without needing -race to observe it: mutating the map SetSessionLabel
// owns after Config() has already handed one out must never be visible
// through the earlier snapshot, the same guarantee every other field of
// config.Config already gives for free by being a value type.
func TestConfigReturnsAnIndependentCopyOfSessionLabels(t *testing.T) {
	c := NewCollector(config.Default(), nil, nil, t.TempDir())
	c.SetSessionLabel(sampleUUID, "first")

	snapshot := c.Config()
	c.SetSessionLabel(sampleUUID, "second")
	c.SetSessionLabel("22222222-2222-2222-2222-222222222222", "unrelated")

	if snapshot.SessionLabels[sampleUUID] != "first" {
		t.Fatalf("a Config() snapshot must not see a later SetSessionLabel, got %+v", snapshot.SessionLabels)
	}
	if len(snapshot.SessionLabels) != 1 {
		t.Fatalf("a Config() snapshot must not grow when a later, unrelated label is set, got %+v", snapshot.SessionLabels)
	}
}

// TestASlowUsageEndpointDoesNotStallTheCycle covers the one source that reaches off
// the machine. Every other source is local and bounded by its own package; a usage
// request that never answers would otherwise freeze the whole collect cycle, and the
// panel would stop showing sessions because a rate-limit gauge is slow.
func TestASlowUsageEndpointDoesNotStallTheCycle(t *testing.T) {
	original := usageTimeout
	usageTimeout = 50 * time.Millisecond
	t.Cleanup(func() { usageTimeout = original })

	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	cfg := config.Default()
	cfg.BoardPath = ""
	uf := usage.NewFetcher(func() (string, error) { return "token", nil }, srv.URL, time.Minute)

	done := make(chan state.Snapshot, 1)
	go func() {
		done <- NewCollector(cfg, nil, uf, t.TempDir()).Collect(context.Background())
	}()

	select {
	case snap := <-done:
		if snap.UsageError == "" {
			t.Fatal("a usage request that timed out must light its own error field")
		}
		if snap.Limits != nil {
			t.Fatal("a timed-out request produced no limits")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Collect must not wait on the usage endpoint indefinitely")
	}
}
