package main

import (
	"strings"
	"testing"
)

// The probe window's lines are read whole, in the order written, and a line
// cut short by the window being killed mid-write is not taken for a mark.
func TestRecordsAreReadInOrderAndACutLineIsLeftOut(t *testing.T) {
	in := `{"kind":"look","mark":"started","unix_ms":1000,"since_start_ms":4,"end_unix_ms":1001,"bundle":"/a.app","registered":false}
{"kind":"dump","mark":"started","unix_ms":1000,"since_start_ms":4,"end_unix_ms":3000,"bundle":"/a.app","registered":true}
{"kind":"look","mark":"the web vi`
	got, err := readRecords(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Kind != "look" || got[1].Kind != "dump" || !got[1].Registered || got[1].EndUnixMs != 3000 {
		t.Fatalf("read %+v", got)
	}
}

// When the path was first seen registered is the start of the first quick look
// that saw it, at or after the moment asked about; dumps are not counted, since
// a dump's answer is about some moment within seconds.
func TestFirstRegisteredIsTheFirstQuickLookThatSawIt(t *testing.T) {
	observed := []record{
		{Kind: "look", UnixMs: 900, Registered: true},
		{Kind: "look", UnixMs: 1100, Registered: false},
		{Kind: "dump", UnixMs: 1150, EndUnixMs: 1200, Registered: true},
		{Kind: "look", UnixMs: 1300, Registered: true},
	}
	at, ok := firstRegistered(observed, 1000)
	if !ok || at != 1300 {
		t.Fatalf("first registered at %d (%v), want 1300", at, ok)
	}
	if _, ok := firstRegistered(observed[1:3], 1000); ok {
		t.Fatal("a dump was counted as a quick look")
	}
}

// What a started window's section says: each mark, its time in the process,
// the quick look's answer, and the dump started there with when it ended.
func TestAStartedWindowsSectionListsEachMark(t *testing.T) {
	r := result{
		Name:    "started from the staging directory",
		Bundle:  "/Applications/.fleetdeck-update/fleetdeck.app",
		Started: true,
		Exec:    10_000,
		Marks: []record{
			{Kind: "look", Mark: "started", UnixMs: 10_004, SinceStart: 4},
			{Kind: "dump", Mark: "started", UnixMs: 10_004, SinceStart: 4, EndUnixMs: 12_004},
			{Kind: "look", Mark: "the web view is made", UnixMs: 10_400, SinceStart: 400, Registered: true},
		},
		Observed: []record{{Kind: "look", UnixMs: 10_150, Registered: true}},
	}
	got := r.markdown()
	for _, want := range []string{
		"### started from the staging directory",
		"`/Applications/.fleetdeck-update/fleetdeck.app`",
		"first seen registered by the observer 150 ms after exec",
		"| started | 4 | no | no, 4–2004 ms |",
		"| the web view is made | 400 | yes | — |",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the section lacks %q:\n%s", want, got)
		}
	}
}

// A copy that is never started says whether the copy alone had it registered,
// and when.
func TestACopysSectionSaysWhetherTheCopyAloneRegisteredIt(t *testing.T) {
	r := result{Name: "copied, not started", Bundle: "/Applications/.lsprobe-copy/fleetdeck.app", CopyEnd: 5_000, WatchMs: 30_000}
	if got := r.markdown(); !strings.Contains(got, "not registered in 30000 ms of watching after the copy") {
		t.Errorf("unregistered copy:\n%s", got)
	}
	r.Observed = []record{{Kind: "look", UnixMs: 5_700, Registered: true}}
	if got := r.markdown(); !strings.Contains(got, "registered by the copy alone, first seen 700 ms after it") {
		t.Errorf("registered copy:\n%s", got)
	}
}
