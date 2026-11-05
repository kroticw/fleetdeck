package main

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func (c *liveControl) cleanupOwnTurn(t *testing.T, socket, thread string) {
	t.Helper()
	if !c.owned[thread] {
		t.Fatal("cleanup refused an unowned thread")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cleanup := openLiveControl(ctx, t, socket)
	cleanup.owned[thread] = true
	var turns map[string]json.RawMessage
	cleanup.call(t, "thread/turns/list", map[string]any{"threadId": thread, "limit": 10, "itemsView": "notLoaded"}, &turns)
	active, err := runningProbeTurn(turns)
	if err != nil {
		t.Fatal(err)
	}
	if active != "" {
		cleanup.call(t, "turn/interrupt", map[string]string{"threadId": thread, "turnId": active}, nil)
		// Interrupt is asynchronous: cleaning terminals before the turn ends
		// can race a process that is still being started.
		for active != "" {
			select {
			case <-ctx.Done():
				t.Fatalf("cleanup could not end probe turn on thread %s", thread)
			case <-time.After(100 * time.Millisecond):
			}
			cleanup.call(t, "thread/turns/list", map[string]any{"threadId": thread, "limit": 10, "itemsView": "notLoaded"}, &turns)
			if active, err = runningProbeTurn(turns); err != nil {
				t.Fatal(err)
			}
		}
	} else {
		var state liveThreadResult
		cleanup.call(t, "thread/read", map[string]any{"threadId": thread, "includeTurns": false}, &state)
		if state.Thread.Status.Type != "idle" && state.Thread.Status.Type != "notLoaded" {
			t.Fatalf("cleanup outcome is unknown for probe thread %s", thread)
		}
	}
	cleanup.call(t, "thread/backgroundTerminals/clean", map[string]string{"threadId": thread}, nil)
	if cleanup.terminalCount(t, thread) != 0 {
		t.Fatalf("cleanup left background terminals on probe thread %s", thread)
	}
}
func (c *liveControl) terminalCount(t *testing.T, thread string) int {
	t.Helper()
	if !c.owned[thread] {
		t.Fatal("terminal lookup refused an unowned thread")
	}
	var result map[string]json.RawMessage
	c.call(t, "thread/backgroundTerminals/list", map[string]string{"threadId": thread}, &result)
	raw, present := result["data"]
	if !present || string(raw) == "null" {
		t.Fatal("terminal lookup omitted data; cannot infer absence")
	}
	var terminals []json.RawMessage
	if err := json.Unmarshal(raw, &terminals); err != nil {
		t.Fatal(err)
	}
	if cursor := result["nextCursor"]; len(cursor) > 0 && string(cursor) != "null" && string(cursor) != `""` {
		t.Fatal("probe terminal lookup needs unexpected pagination")
	}
	return len(terminals)
}
func TestControlLiveInterruptStopsProcessAndResumeKeepsHistory(t *testing.T) {
	socket, cwd := liveTarget(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()
	c := openLiveControl(ctx, t, socket)
	thread := c.start(t, cwd, "Fleetdeck process stop and resume probe")
	inFlight := false
	pid := 0
	defer func() {
		if inFlight {
			c.cleanupOwnTurn(t, socket, thread.Thread.ID)
		}
		// Last resort for a failed native stop. The PID came from this
		// thread's own output, so it is the probe's process, not a guess.
		if pid > 1 {
			_ = unix.Kill(pid, unix.SIGKILL)
		}
	}()
	var started struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	inFlight = true
	c.call(t, "turn/start", map[string]any{"threadId": thread.Thread.ID, "effort": "low", "input": []map[string]string{{"type": "text", "text": `Run exactly /bin/sh -c 'printf "PROBE_PID=%s;" "$$"; exec /bin/sleep 30' using exec_command with shell=/bin/sh, login=false, yield_time_ms=1000. Do not run any other command or read files. After sleep finishes reply SLEEP_DONE.`}}}, &started)
	c.active[thread.Thread.ID] = started.Turn.ID
	var output strings.Builder
	c.until(t, func(m liveMessage) bool {
		if m.Method == "turn/completed" {
			t.Fatal("probe turn completed before reporting its live process")
		}
		if m.Method != "item/commandExecution/outputDelta" {
			return false
		}
		var p struct {
			ThreadID string `json:"threadId"`
			Delta    string `json:"delta"`
		}
		if err := json.Unmarshal(m.Params, &p); err != nil {
			t.Fatal(err)
		}
		if p.ThreadID != thread.Thread.ID {
			return false
		}
		if output.Len()+len(p.Delta) > 4096 {
			t.Fatal("probe command produced unexpected output volume")
		}
		output.WriteString(p.Delta)
		var complete bool
		var parseErr error
		pid, complete, parseErr = probePID(output.String())
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		if !complete {
			return false
		}
		return true
	})
	if err := unix.Kill(pid, 0); err != nil {
		t.Fatal("probe process exited before interruption could be tested")
	}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for c.terminalCount(t, thread.Thread.ID) == 0 {
		select {
		case <-ctx.Done():
			t.Fatal("native process was never listed")
		case <-ticker.C:
		}
	}
	c.call(t, "turn/interrupt", map[string]string{"threadId": thread.Thread.ID, "turnId": started.Turn.ID}, nil)
	waitTurn(c, t, thread.Thread.ID, started.Turn.ID, "interrupted")
	c.call(t, "thread/backgroundTerminals/clean", map[string]string{"threadId": thread.Thread.ID}, nil)
	for {
		count := c.terminalCount(t, thread.Thread.ID)
		missing := errors.Is(unix.Kill(pid, 0), unix.ESRCH)
		if count == 0 && missing {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("native stop left the probe process running")
		case <-ticker.C:
		}
	}
	var resumed liveThreadResult
	c.call(t, "thread/resume", map[string]any{"threadId": thread.Thread.ID, "excludeTurns": true}, &resumed)
	if resumed.Thread.ID != thread.Thread.ID || resumed.Thread.Status.Type != "idle" {
		t.Fatal("resume did not preserve an idle native thread")
	}
	if c.terminalCount(t, thread.Thread.ID) != 0 {
		t.Fatal("resume restarted interrupted work")
	}
	var turns map[string]json.RawMessage
	c.call(t, "thread/turns/list", map[string]any{"threadId": thread.Thread.ID, "itemsView": "notLoaded", "limit": 10}, &turns)
	if _, err := runningProbeTurn(turns); err != nil {
		t.Fatal(err)
	}
	var history []struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(turns["data"], &history); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, turn := range history {
		if turn.ID == started.Turn.ID && turn.Status == "interrupted" {
			found = true
		}
	}
	if !found {
		t.Fatal("resume lost the interrupted turn history")
	}
	inFlight = false
}

func TestControlLiveRecoversDeliveryFromClientMessageID(t *testing.T) {
	socket, cwd := liveTarget(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	original := openLiveControl(ctx, t, socket)
	thread := original.start(t, cwd, "Fleetdeck delivery recovery probe")
	inFlight := false
	defer func() {
		if inFlight {
			original.cleanupOwnTurn(t, socket, thread.Thread.ID)
		}
	}()
	inFlight = true
	original.call(t, "turn/start", map[string]any{"threadId": thread.Thread.ID, "clientUserMessageId": thread.Thread.ID, "effort": "low", "input": []map[string]string{{"type": "text", "text": "Reply exactly DELIVERY_RECOVERED. Do not call tools or read files."}}}, nil)
	// Model the application losing its receipt before saving it. Recovery must use
	// the durable client message ID, never a second turn/start or the newest title.
	if err := original.conn.CloseNow(); err != nil {
		t.Fatal(err)
	}
	recovered := openLiveControl(ctx, t, socket)
	recovered.owned[thread.Thread.ID] = true
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		var listing struct {
			Data []struct {
				Status string `json:"status"`
				Items  []struct {
					ID   string `json:"id"`
					Type string `json:"type"`
				} `json:"items"`
			} `json:"data"`
		}
		recovered.call(t, "thread/turns/list", map[string]any{"threadId": thread.Thread.ID, "itemsView": "full", "limit": 10}, &listing)
		matches := 0
		completed := false
		for _, turn := range listing.Data {
			for _, item := range turn.Items {
				if item.Type == "userMessage" && item.ID == thread.Thread.ID {
					matches++
					completed = turn.Status == "completed"
				}
			}
		}
		if matches > 1 {
			t.Fatal("one submission produced duplicate client message IDs")
		}
		if matches == 1 && completed {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("client message ID did not recover a completed submission")
		case <-ticker.C:
		}
	}
	var state liveThreadResult
	recovered.call(t, "thread/read", map[string]any{"threadId": thread.Thread.ID, "includeTurns": false}, &state)
	// The daemon may unload a thread once its last subscriber leaves.
	if state.Thread.Status.Type != "idle" && state.Thread.Status.Type != "notLoaded" {
		t.Fatal("recovered delivery did not finish idle")
	}
	inFlight = false
}

var (
	probeMarker  = regexp.MustCompile(`PROBE_PID=([0-9]+);`)
	probePartial = regexp.MustCompile(`PROBE_PID=[0-9]*$`)
)

func probePID(output string) (int, bool, error) {
	// A tty may echo the command line, so its "PROBE_PID=%s;" is skipped.
	match := probeMarker.FindStringSubmatch(output)
	if match == nil {
		if strings.Contains(strings.ReplaceAll(output, "PROBE_PID=%s", ""), "PROBE_PID=") && !probePartial.MatchString(output) {
			return 0, false, errors.New("invalid probe process marker")
		}
		return 0, false, nil
	}
	pid, err := strconv.Atoi(match[1])
	if err != nil || pid <= 1 {
		return 0, false, errors.New("invalid probe process marker")
	}
	return pid, true, nil
}
func TestControlProcessMarkerCannotUsePartialPID(t *testing.T) {
	if _, complete, err := probePID("PROBE_PID=123"); err != nil || complete {
		t.Fatal("partial process ID accepted")
	}
	pid, complete, err := probePID("PROBE_PID=12345;")
	if err != nil || !complete || pid != 12345 {
		t.Fatalf("pid=%d complete=%v err=%v", pid, complete, err)
	}
	if _, _, err = probePID("PROBE_PID=0;"); err == nil {
		t.Fatal("non-process marker accepted")
	}
	pid, complete, err = probePID(`printf "PROBE_PID=%s;" "$$"` + "\nPROBE_PID=4321;")
	if err != nil || !complete || pid != 4321 {
		t.Fatalf("echoed command line broke parsing: pid=%d complete=%v err=%v", pid, complete, err)
	}
	if _, _, err = probePID("PROBE_PID=x;"); err == nil {
		t.Fatal("malformed marker accepted")
	}
}
