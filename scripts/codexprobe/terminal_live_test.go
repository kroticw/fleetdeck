package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Opt-in: this attaches only to an explicitly prepared disposable thread.
// It sends terminal-query replies, never prompts, approvals or agent-stop requests.
func TestTerminalLiveCodexRepaint(t *testing.T) {
	thread := os.Getenv("FLEETDECK_LIVE_CODEX_THREAD")
	cwd := os.Getenv("FLEETDECK_LIVE_CODEX_CWD")
	if thread == "" || cwd == "" {
		t.Skip("explicit disposable Codex thread and cwd required")
	}
	if !regexp.MustCompile(`^[0-9a-f-]{36}$`).MatchString(thread) || !filepath.IsAbs(cwd) {
		t.Fatal("invalid live probe target")
	}
	marker, err := os.ReadFile(filepath.Join(cwd, ".fleetdeck-probe"))
	if err != nil || string(marker) != "disposable Fleetdeck integration probe\n" {
		t.Fatal("working directory is not marked as a disposable probe")
	}
	allowed, err := os.ReadFile(filepath.Join(cwd, ".fleetdeck-probe-threads"))
	if err != nil || !strings.Contains("\n"+string(allowed), "\n"+thread+"\n") {
		t.Fatal("thread is not registered in this disposable probe")
	}
	binary, err := exec.LookPath("codex")
	if err != nil {
		t.Fatal(err)
	}
	for _, inline := range []bool{false, true} {
		name := "fullscreen"
		if inline {
			name = "inline"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			var terminal *terminalProbe
			worker := newScreenWorker(ctx, t, func(data []byte) {
				if terminal == nil {
					t.Fatal("unexpected terminal reply before child startup")
				}
				if _, writeErr := terminal.master.Write(data); writeErr != nil {
					t.Fatal(writeErr)
				}
			})
			worker.call(t, screenChunk{}, false) // Warm the emulator before Codex sends queries.
			argv := []string{binary, "resume", thread, "--cd", cwd, "--sandbox", "read-only", "--ask-for-approval", "on-request"}
			if inline {
				argv = append(argv, "--no-alt-screen")
			}
			terminal, err = startTerminal(ctx, argv, cwd, 80, 24, 65536)
			if err != nil {
				t.Fatal(err)
			}
			defer terminal.Close()
			var observed uint64
			var recording []byte
			pump := func() {
				t.Helper()
				data, total, dropped := terminal.Output(observed)
				if dropped {
					t.Fatal("emulator fell behind the live PTY")
				}
				if len(data) > 0 {
					recording = append(recording, data...)
					if len(recording) > 2<<20 {
						t.Fatal("live capture exceeded test memory limit")
					}
					worker.call(t, screenChunk{Data: data}, false)
					observed = total
				}
			}
			wait := func() {
				t.Helper()
				select {
				case <-ctx.Done():
					t.Fatal("live terminal deadline exceeded")
				case <-terminal.done:
					t.Fatal("Codex TUI exited before the probe completed")
				case <-terminal.changed:
				}
			}
			for {
				pump()
				view := worker.call(t, screenChunk{}, true)
				if view != nil && strings.Contains(strings.Join(view.Lines, "\n"), "FLEETDECK_PROBE_OK") && completedFrameEnd(recording) > 0 {
					break
				}
				wait()
			}
			prefix := append([]byte(nil), recording[:completedFrameEnd(recording)]...)
			resize := func(cols int) {
				t.Helper()
				pump()
				before := observed
				worker.call(t, screenChunk{Cols: cols, Rows: 24}, false)
				if resizeErr := terminal.Resize(cols, 24); resizeErr != nil {
					t.Fatal(resizeErr)
				}
				for {
					pump()
					if completedFrameEnd(recording) > int(before) {
						return
					}
					wait()
				}
			}
			for i := 0; ; i++ {
				resize(70 + i%10)
				_, _, overflow := terminal.Output(0)
				if overflow {
					break
				}
			}
			_, _, overflow := terminal.Output(0)
			if !overflow {
				t.Fatal("probe did not exceed its replay buffer")
			}
			pump()
			// Retain everything after an exact complete-frame boundary, including partial
			// escapes. A byte offset at an arbitrary Read boundary is not safe for replay.
			replayFrom := uint64(completedFrameEnd(recording))
			resize(80)
			for {
				pump()
				reference := worker.call(t, screenChunk{}, true)
				if reference != nil && validRepaintBoundary(recording, *reference) {
					// Reference and candidate must stop at exactly the same byte offset.
					data := recording[replayFrom:observed]
					if len(data) > 65536 {
						t.Fatal("candidate repaint exceeded replay buffer")
					}
					candidate := reconnectScreen(t, prefix, screenChunk{Data: data})
					if reflect.DeepEqual(*reference, candidate) {
						return
					}
				}
				// Idle Codex still emits cursor frames; quiescence is not a readiness signal.
				wait()
			}
		})
	}
}
