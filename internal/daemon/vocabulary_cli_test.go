package daemon

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The stall vocabulary in types.go is copied out of a closed-source binary, and that
// binary changes under it without telling anyone. Between 2.1.263 (where the list was
// first read) and 2.1.269 the daemon grew two renderings nobody here knew about. They
// happened to land correctly, because an unrecognised value falls to Waiting by design,
// but nothing would have said so either way, and the same silence would have covered a
// value that landed wrong. Breaking that silence is what this file is for: it reads the
// renderings back out of whatever CLI is installed and fails the moment they differ
// from what is recorded below.
//
// It deliberately records *every* needs rendering, questions included, not only the
// stall-shaped ones: a new question form is the same kind of news as a new stall form,
// and the cheapest moment to notice either is while the diff is still one line long.

// needsLiteral matches the daemon's `needs:"..."` constructions in the bundled
// JavaScript. Escapes are kept raw here and decoded afterwards, since the bundle writes
// its em dashes as —.
var needsLiteral = regexp.MustCompile(`needs:"((?:[^"\\]|\\.){0,300})"`)

// needsIndirect matches the other way the bundle assigns a rendering: through a
// variable, as `needs:eIn`. One of the two renderings new in 2.1.269 is written this
// way, and a detector that only read literals would have missed it -- the same blind
// spot, one level down, as the defect this file exists to catch.
var needsIndirect = regexp.MustCompile(`needs:([A-Za-z_$][A-Za-z0-9_$]*)`)

// switchAnchor locates the function that renders these values. The function's own name
// is minified and changes between builds, so it cannot be the anchor; one of its case
// labels can, because the labels are the daemon's API-error taxonomy and not generated.
var switchAnchor = regexp.MustCompile(`case"billing_error"`)

// caseLabel matches the labels of the switch the anchor sits in. These are the backstop:
// a new stall category has to add a case here no matter how its text is spelled, so the
// labels catch an addition even when its rendering hides behind an indirection this
// file cannot resolve.
var caseLabel = regexp.MustCompile(`case"([a-z_]+)"`)

// renderingWindow is how much of the bundle around the anchor counts as the rendering
// function. The whole switch is about 1.2 kB; this is wide enough to also take in the
// variable declarations immediately before it, which is where the indirect rendering
// lives, and narrow enough not to drag in neighbouring code.
const renderingWindow = 4096

// needsOverlap is how much of each chunk is carried into the next one so a literal
// straddling a read boundary is still matched whole. Any value above the longest
// literal the regexp can match works; this is comfortably above it.
const needsOverlap = 4096

// extractNeedsRenderings streams r and returns every distinct `needs:"..."` literal in
// it, unescaped, sorted. Streaming rather than reading it all in because the CLI binary
// is around 200 MB.
func extractNeedsRenderings(r io.Reader) ([]string, error) {
	found := map[string]struct{}{}
	err := eachWindow(r, func(window []byte) {
		for _, m := range needsLiteral.FindAllSubmatch(window, -1) {
			found[unescapeJS(string(m[1]))] = struct{}{}
		}
	})
	if err != nil {
		return nil, err
	}
	return sortedKeys(found), nil
}

// extractRenderingSwitch returns what the rendering function says about itself: the
// renderings it assigns through a variable, resolved, and the case labels of its
// switch. Both come back empty when the anchor is not found at all, which the caller
// must treat as the detector having broken rather than as the CLI having nothing to say.
func extractRenderingSwitch(r io.Reader) (indirect []string, cases []string, err error) {
	renderings := map[string]struct{}{}
	labels := map[string]struct{}{}
	err = eachWindow(r, func(window []byte) {
		for _, a := range switchAnchor.FindAllIndex(window, -1) {
			lo := max(0, a[0]-renderingWindow)
			hi := min(len(window), a[1]+renderingWindow)
			region := window[lo:hi]
			// Regions carrying the same label but no renderings are other switches
			// over the same taxonomy -- error classifiers elsewhere in the bundle.
			// Only the one that assigns `needs` is the rendering function.
			if !bytes.Contains(region, []byte("needs:")) {
				continue
			}
			for _, m := range caseLabel.FindAllSubmatch(region, -1) {
				labels[string(m[1])] = struct{}{}
			}
			for _, m := range needsIndirect.FindAllSubmatch(region, -1) {
				// An identifier that resolves to a string in the same region is a
				// rendering. One that does not is ordinary minified code that happens
				// to spell a property `needs`, and guessing at it would put junk in
				// the record.
				assign := regexp.MustCompile(`\b` + regexp.QuoteMeta(string(m[1])) + `\s*=\s*"((?:[^"\\]|\\.)*)"`)
				if v := assign.FindSubmatch(region); v != nil {
					renderings[unescapeJS(string(v[1]))] = struct{}{}
				}
			}
		}
	})
	if err != nil {
		return nil, nil, err
	}
	return sortedKeys(renderings), sortedKeys(labels), nil
}

// eachWindow feeds fn overlapping windows of r, so a match spanning a read boundary is
// still seen whole by whatever fn looks for.
func eachWindow(r io.Reader, fn func(window []byte)) error {
	br := bufio.NewReaderSize(r, 1<<20)
	var carry []byte
	buf := make([]byte, 1<<20)
	for {
		n, err := br.Read(buf)
		if n > 0 {
			window := append(carry, buf[:n]...)
			fn(window)
			keep := min(len(window), needsOverlap+2*renderingWindow)
			carry = append(carry[:0], window[len(window)-keep:]...)
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for s := range m {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// unescapeJS turns the \uXXXX escapes the bundle writes back into the characters the
// daemon actually sends, so a recorded rendering can be compared to what a client sees
// on the wire rather than to its source spelling.
func unescapeJS(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == '\\' && i+5 < len(s) && s[i+1] == 'u' {
			if r, err := strconv.ParseUint(s[i+2:i+6], 16, 32); err == nil {
				b.WriteRune(rune(r))
				i += 6
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// knownNeedsRenderings is every `needs` value the installed CLI can produce, as read out
// of the 2.1.269 binary, with what this client decides about each one. true means the
// value matches stalledNeedsPrefixes and lands in the quiet counter; false means it
// lands in Waiting, the loud one.
//
// This is a record of what has been seen, never a list the production code consults --
// isStalledNeeds reads stalledNeedsPrefixes and nothing else. When this test fails
// because the CLI grew a rendering, the fix is to decide what the new value means and
// record it here. Adding it to stalledNeedsPrefixes is a separate decision, and the
// default answer is no: an unknown stop belongs in the counter a person actually
// watches.
var knownNeedsRenderings = map[string]bool{
	// Stall-shaped: matched by stalledNeedsPrefixes, no answer fixes them.
	"usage limit reached — check plan": true,
	"login required — run /login":      true,
	"rate limited — wait and retry":    true,
	"API overloaded — wait and retry":  true,
	"API unavailable — retry":          true,
	"invalid API request — see detail": true,
	"API error — see detail":           true,
	"API error":                        true,

	// Person-shaped: someone has to go and do something, so these stay out of the
	// closed list even though they are not questions either. The last two are the ones
	// 2.1.269 added; both reached Waiting on their own, by the unfamiliar-prefix rule.
	"account on hold — see detail":                          false,
	"org disabled OAuth — use API key or ask admin":         false,
	"request too large — /compact or trim":                  false,
	"cloud credentials unavailable — check or refresh them": false,
	"organization verification required — see detail":       false,

	// Questions the daemon renders for a person to answer.
	"choose: run ultraplan in the cloud":                 false,
	"choose: retry on fallback model or edit prompt":     false,
	"choose: make auto mode the default permission mode": false,
	"choose: install the iTerm2 integration or use tmux": false,
	"choose: continue on usage credits or switch models": false,
	"choose: allow or deny the computer-use action":      false,
	"acknowledge: file sync offline notice":              false,
	"MCP input: open link":                               false,

	// Not renderings at all: the empty initialiser, and a bundled test fixture. Kept so
	// the extraction does not have to special-case them, and excluded from the
	// disappeared-rendering check below, where they would mean nothing.
	"":             false,
	"simple:needs": false,
}

// knownRenderingCases is the taxonomy the rendering switch dispatches on, as read out of
// the 2.1.269 binary. It is the backstop for renderings this file cannot read directly:
// a category the daemon starts reporting has to appear here first, whatever its text
// looks like and however it is assigned.
var knownRenderingCases = map[string]struct{}{
	"account_on_hold":        {},
	"authentication_failed":  {},
	"billing_error":          {},
	"cloud_credential_error": {},
	"invalid_request":        {},
	"max_output_tokens":      {},
	"oauth_org_not_allowed":  {},
	"overloaded":             {},
	"rate_limit":             {},
	"server_error":           {},
	"unknown":                {},
	"verification_required":  {},
}

func TestExtractNeedsRenderings(t *testing.T) {
	t.Run("finds literals and decodes their escapes", func(t *testing.T) {
		src := `case"billing_error":return{state:"blocked",needs:"usage limit reached — check plan"};`
		got, err := extractNeedsRenderings(strings.NewReader(src))
		if err != nil {
			t.Fatalf("extract: %v", err)
		}
		if len(got) != 1 || got[0] != "usage limit reached — check plan" {
			t.Fatalf("got %q, want the decoded rendering", got)
		}
	})

	t.Run("finds a literal straddling a read boundary", func(t *testing.T) {
		// The reason the overlap exists at all: without it a literal split across two
		// reads is silently invisible, and an invisible miss is exactly what this file
		// is meant to prevent.
		src := append(bytes.Repeat([]byte("x"), (1<<20)-10), []byte(`needs:"login required — run /login"`)...)
		got, err := extractNeedsRenderings(bytes.NewReader(src))
		if err != nil {
			t.Fatalf("extract: %v", err)
		}
		if len(got) != 1 || got[0] != "login required — run /login" {
			t.Fatalf("got %q, want the straddling literal", got)
		}
	})

	t.Run("a rendering the record does not know is not silently accepted", func(t *testing.T) {
		// Proving the detector detects: a value absent from knownNeedsRenderings is
		// what a future CLI looks like from here.
		got, err := extractNeedsRenderings(strings.NewReader(`needs:"quantum flux collapsed — reboot"`))
		if err != nil {
			t.Fatalf("extract: %v", err)
		}
		if _, known := knownNeedsRenderings[got[0]]; known {
			t.Fatalf("%q should not be a known rendering", got[0])
		}
	})
}

func TestExtractRenderingSwitch(t *testing.T) {
	t.Run("resolves a rendering assigned through a variable", func(t *testing.T) {
		src := `var eIn="organization verification required — see detail";` +
			`function f(n){switch(n){case"verification_required":return{state:"blocked",needs:eIn};` +
			`case"billing_error":return{state:"blocked",needs:"usage limit reached — check plan"}}}`
		indirect, cases, err := extractRenderingSwitch(strings.NewReader(src))
		if err != nil {
			t.Fatalf("extract: %v", err)
		}
		if len(indirect) != 1 || indirect[0] != "organization verification required — see detail" {
			t.Fatalf("indirect = %q, want the variable's value resolved", indirect)
		}
		if len(cases) != 2 {
			t.Fatalf("cases = %q, want both labels", cases)
		}
	})

	t.Run("ignores a switch over the same labels that renders nothing", func(t *testing.T) {
		// The bundle has other switches over the same API-error taxonomy. Only the one
		// that assigns needs is the rendering function; taking labels from the others
		// would make the backstop fail on changes that have nothing to do with us.
		src := `function g(n){switch(n){case"billing_error":return 402;case"quota_error":return 429}}`
		_, cases, err := extractRenderingSwitch(strings.NewReader(src))
		if err != nil {
			t.Fatalf("extract: %v", err)
		}
		if len(cases) != 0 {
			t.Fatalf("cases = %q, want none from a switch that renders nothing", cases)
		}
	})

	t.Run("does not invent a rendering for an identifier it cannot resolve", func(t *testing.T) {
		src := `function f(n){switch(n){case"billing_error":return{needs:s}}}`
		indirect, _, err := extractRenderingSwitch(strings.NewReader(src))
		if err != nil {
			t.Fatalf("extract: %v", err)
		}
		if len(indirect) != 0 {
			t.Fatalf("indirect = %q, want nothing guessed", indirect)
		}
	})
}

// TestStalledNeedsVocabularyMatchesInstalledCLI is the drift detector itself. It reads
// the installed binary; with no CLI on PATH there is nothing to compare against and it
// skips, so a machine without Claude Code -- CI, a fresh checkout -- stays green.
func TestStalledNeedsVocabularyMatchesInstalledCLI(t *testing.T) {
	bin := os.Getenv("FLEETDECK_CLAUDE_BIN")
	if bin == "" {
		p, err := exec.LookPath("claude")
		if err != nil {
			t.Skip("no claude on PATH: nothing to check the vocabulary against")
		}
		bin = p
	}

	literal := readWith(t, bin, extractNeedsRenderings)
	var indirect, cases []string
	readWith(t, bin, func(r io.Reader) ([]string, error) {
		var err error
		indirect, cases, err = extractRenderingSwitch(r)
		return nil, err
	})
	all := append(append([]string{}, literal...), indirect...)

	if len(literal) == 0 {
		// Not a skip. Zero matches means the extraction stopped working -- the bundle
		// is minified differently, or the daemon builds needs some other way -- and a
		// detector that quietly finds nothing is worse than no detector at all.
		t.Fatalf("no needs renderings found in %s: the extraction no longer matches how the CLI builds them", bin)
	}
	if len(cases) == 0 {
		t.Fatalf("the rendering switch was not found in %s: the anchor no longer holds", bin)
	}

	for _, needs := range all {
		wantStalled, known := knownNeedsRenderings[needs]
		if !known {
			t.Errorf("%s renders a needs value this client has never seen: %q\n"+
				"decide what it means and record it in knownNeedsRenderings. It currently falls to Waiting, "+
				"which is the right default for an unknown stop -- changing that is a separate decision.",
				cliVersion(bin), needs)
			continue
		}
		if gotStalled := isStalledNeeds(needs); gotStalled != wantStalled {
			t.Errorf("isStalledNeeds(%q) = %v, recorded as %v: the closed vocabulary and the CLI have diverged",
				needs, gotStalled, wantStalled)
		}
	}

	for _, label := range cases {
		if _, known := knownRenderingCases[label]; !known {
			t.Errorf("%s dispatches a stop category this client has never seen: %q\n"+
				"read what it renders and record both it and its rendering", cliVersion(bin), label)
		}
	}

	// The other direction: something recorded that has since disappeared. Harmless to
	// the running panel, but it means the record describes a CLI nobody runs any more,
	// which is how the record came to be wrong in the first place.
	for needs := range knownNeedsRenderings {
		if needs == "" || needs == "simple:needs" {
			continue
		}
		if !contains(all, needs) {
			t.Errorf("recorded rendering %q is gone from %s: the record describes an older CLI", needs, cliVersion(bin))
		}
	}
	for label := range knownRenderingCases {
		if !contains(cases, label) {
			t.Errorf("recorded stop category %q is gone from %s: the record describes an older CLI", label, cliVersion(bin))
		}
	}
}

func readWith(t *testing.T, bin string, fn func(io.Reader) ([]string, error)) []string {
	t.Helper()
	f, err := os.Open(bin)
	if err != nil {
		t.Skipf("cannot read %s: %v", bin, err)
	}
	defer f.Close()
	out, err := fn(f)
	if err != nil {
		t.Fatalf("reading %s: %v", bin, err)
	}
	return out
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// cliVersion is for failure messages only: a drift report is useless without saying
// which build drifted.
func cliVersion(bin string) string {
	out, err := exec.Command(bin, "--version").Output()
	if err != nil {
		return bin
	}
	return "CLI " + strings.TrimSpace(string(out))
}
