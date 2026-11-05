package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type screenChunk struct {
	Data []byte `json:"data"`
	Cols int    `json:"cols,omitempty"`
	Rows int    `json:"rows,omitempty"`
}
type screenView struct {
	Cells        [][][]int      `json:"cells"`
	CursorHidden bool           `json:"cursorHidden"`
	Lines        []string       `json:"lines"`
	CursorX      int            `json:"cursorX"`
	CursorY      int            `json:"cursorY"`
	Modes        map[string]any `json:"modes"`
	Type         string         `json:"type"`
}

func renderScreen(t *testing.T, chunks []screenChunk) screenView {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for terminal screen verification")
	}
	script, err := filepath.Abs("screen.mjs")
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(struct {
		Cols   int           `json:"cols"`
		Rows   int           `json:"rows"`
		Chunks []screenChunk `json:"chunks"`
	}{80, 24, chunks})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, script)
	cmd.Stdin = bytes.NewReader(data)
	raw, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var view screenView
	if err = json.Unmarshal(raw, &view); err != nil {
		t.Fatal(err)
	}
	return view
}

func TestTerminalScreenPreservesSplitUTF8AndModes(t *testing.T) {
	text := []byte("Привет")
	view := renderScreen(t, []screenChunk{{Data: []byte("\x1b[?1049h\x1b[?2004h")}, {Data: text[:1]}, {Data: text[1:]}})
	if !strings.Contains(view.Lines[0], "Привет") || view.Type != "alternate" || view.Modes["bracketedPasteMode"] != true {
		t.Fatalf("screen: %+v", view)
	}
	withoutModes := renderScreen(t, []screenChunk{{Data: text}})
	if reflect.DeepEqual(view, withoutModes) {
		t.Fatal("screen comparison ignores terminal modes")
	}
}

func TestTerminalScreenDetectsAttributesAndCursorVisibility(t *testing.T) {
	plain := renderScreen(t, []screenChunk{{Data: []byte("red")}})
	for _, sequence := range []string{"\x1b[31mred\x1b[0m", "\x1b[?25lred"} {
		changed := renderScreen(t, []screenChunk{{Data: []byte(sequence)}})
		if reflect.DeepEqual(plain, changed) {
			t.Fatalf("screen oracle ignored %q", sequence)
		}
	}
}

// A blank baseline makes omitted repaint cells visible instead of reusing stale content.
func reconnectScreen(t *testing.T, prefix []byte, final screenChunk) screenView {
	t.Helper()
	return renderScreen(t, []screenChunk{{Data: prefix}, {Data: []byte("\x1b[2J\x1b[H\x1b[0m")}, final})
}
func TestTerminalScreenRejectsMissingRepaint(t *testing.T) {
	prefix := []byte("\x1b[?1049hFLEETDECK_PROBE_OK")
	reference := renderScreen(t, []screenChunk{{Data: prefix}})
	candidate := reconnectScreen(t, prefix, screenChunk{Data: []byte("\x1b[?2026h\x1b[?2026l")})
	if reflect.DeepEqual(reference, candidate) {
		t.Fatal("missing repaint accepted because startup content remained")
	}
}
func completedFrameEnd(data []byte) int {
	const boundary = "\x1b[?2026l"
	at := bytes.LastIndex(data, []byte(boundary))
	if at < 0 {
		return 0
	}
	return at + len(boundary)
}
func TestTerminalFrameBoundaryKeepsPartialRemainder(t *testing.T) {
	frame := []byte("\x1b[?2026hhello\x1b[?2026l")
	data := append(append([]byte(nil), frame...), []byte("\x1b[?20")...)
	end := completedFrameEnd(data)
	if !bytes.Equal(data[:end], frame) || string(data[end:]) != "\x1b[?20" {
		t.Fatal("frame split discarded a partial escape sequence")
	}
}

func validRepaintBoundary(recording []byte, view screenView) bool {
	return len(recording) > 0 && completedFrameEnd(recording) == len(recording) && view.Modes["synchronizedOutputMode"] != true && strings.Contains(strings.Join(view.Lines, "\n"), "FLEETDECK_PROBE_OK")
}
func TestTerminalScreenRejectsIncompleteBlankFrame(t *testing.T) {
	prefix := []byte("\x1b[?1049hFLEETDECK_PROBE_OK")
	incomplete := []byte("\x1b[?2026h\x1b[2J\x1b[H")
	raw := append(append([]byte(nil), prefix...), incomplete...)
	reference := renderScreen(t, []screenChunk{{Data: raw}})
	candidate := reconnectScreen(t, prefix, screenChunk{Data: incomplete})
	if !reflect.DeepEqual(reference, candidate) {
		t.Fatal("counterexample did not produce matching blank screens")
	}
	if validRepaintBoundary(raw, reference) {
		t.Fatal("incomplete blank frame accepted")
	}
	complete := []byte("\x1b[?2026hFLEETDECK_PROBE_OK\x1b[?2026l")
	good := renderScreen(t, []screenChunk{{Data: complete}})
	if !validRepaintBoundary(complete, good) {
		t.Fatal("complete marked frame rejected")
	}
}
