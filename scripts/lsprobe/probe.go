package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// record is one answer about whether LaunchServices holds a bundle's path: a
// quick look (Kind "look"), NSWorkspace's list under the bundle's identifier,
// or a dump (Kind "dump"), lsregister -dump, whose answer is about some moment
// between UnixMs and EndUnixMs. The probe window writes them at its marks
// (cmd/fleetdeck-window/lsprobe_darwin.go), in the same form; this program
// writes its own, with no mark, as it watches.
type record struct {
	Kind       string `json:"kind"`
	Mark       string `json:"mark"`
	UnixMs     int64  `json:"unix_ms"`
	SinceStart int64  `json:"since_start_ms"`
	EndUnixMs  int64  `json:"end_unix_ms"`
	Bundle     string `json:"bundle"`
	Registered bool   `json:"registered"`
	Err        string `json:"err,omitempty"`
}

// readRecords reads one record per line. A last line with no newline is one
// the window was killed while writing, and is left out.
func readRecords(r io.Reader) ([]record, error) {
	var out []record
	br := bufio.NewReader(r)
	for {
		line, err := br.ReadString('\n')
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		var rec record
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			return nil, fmt.Errorf("a probe line %q: %w", strings.TrimSpace(line), err)
		}
		out = append(out, rec)
	}
}

// firstRegistered is when the first quick look started at or after from saw
// the path registered.
func firstRegistered(observed []record, from int64) (int64, bool) {
	for _, r := range observed {
		if r.Kind == "look" && r.UnixMs >= from && r.Registered {
			return r.UnixMs, true
		}
	}
	return 0, false
}

// result is what one scenario found.
type result struct {
	Name   string
	Bundle string
	// Started: the bundle was started by exec, at Exec; otherwise it was only
	// copied, and watched for WatchMs after the copy ended.
	Started bool
	// CopyEnd is when the copy into place ended. For a started bundle,
	// CopyRegistered says whether the copy alone had it registered before it
	// was forgotten and started.
	CopyEnd        int64
	CopyRegistered bool
	WatchMs        int64
	Exec           int64
	// Marks are the probe window's; Observed are this program's, from the
	// copy or the exec on.
	Marks    []record
	Observed []record
	// Note is anything the run has to say beside the numbers.
	Note string
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// markdown is the scenario's section of the summary.
func (r result) markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "### %s\n\n`%s`\n\n", r.Name, r.Bundle)
	if !r.Started {
		if at, ok := firstRegistered(r.Observed, r.CopyEnd); ok {
			fmt.Fprintf(&b, "- registered by the copy alone, first seen %d ms after it\n", at-r.CopyEnd)
		} else {
			fmt.Fprintf(&b, "- not registered in %d ms of watching after the copy\n", r.WatchMs)
		}
		r.dumps(&b)
		r.note(&b)
		return b.String()
	}
	fmt.Fprintf(&b, "- registered by the copy into place, before it was forgotten and started: %s\n", yesNo(r.CopyRegistered))
	if at, ok := firstRegistered(r.Observed, r.Exec); ok {
		fmt.Fprintf(&b, "- first seen registered by the observer %d ms after exec\n", at-r.Exec)
	} else {
		b.WriteString("- never seen registered by the observer after exec\n")
	}
	b.WriteString("\n| mark | ms into the process | quick look registered | dump started there: registered, ms into the process |\n| --- | --- | --- | --- |\n")
	dumps := map[string]record{}
	for _, m := range r.Marks {
		if m.Kind == "dump" {
			dumps[m.Mark] = m
		}
	}
	for _, m := range r.Marks {
		if m.Kind != "look" {
			continue
		}
		dump := "—"
		if d, ok := dumps[m.Mark]; ok {
			dump = fmt.Sprintf("%s, %d–%d ms", yesNo(d.Registered), d.SinceStart, d.SinceStart+d.EndUnixMs-d.UnixMs)
			if d.Err != "" {
				dump += " (" + d.Err + ")"
			}
		}
		fmt.Fprintf(&b, "| %s | %d | %s | %s |\n", m.Mark, m.SinceStart, yesNo(m.Registered), dump)
	}
	r.dumps(&b)
	r.note(&b)
	return b.String()
}

// dumps lists the observer's own dumps, timed from the copy or the exec.
func (r result) dumps(b *strings.Builder) {
	from := r.CopyEnd
	if r.Started {
		from = r.Exec
	}
	var lines []string
	for _, o := range r.Observed {
		if o.Kind == "dump" {
			lines = append(lines, fmt.Sprintf("%d–%d ms %s", o.UnixMs-from, o.EndUnixMs-from, yesNo(o.Registered)))
		}
	}
	if len(lines) > 0 {
		fmt.Fprintf(b, "\nThe observer's dumps, ms after the %s: %s\n", map[bool]string{true: "exec", false: "copy"}[r.Started], strings.Join(lines, "; "))
	}
}

func (r result) note(b *strings.Builder) {
	if r.Note != "" {
		fmt.Fprintf(b, "\n%s\n", r.Note)
	}
}
