// Command fleetdeck-status is the statusline reporter. Claude Code runs it for
// every session: it forwards the same structured data to the local panel, which
// cannot obtain it any other way, and prints a status line of its own.
//
// --wrap (or statusline.wrap in config.yaml, when the flag is not given)
// names another statusline command to pass the same stdin through to and
// print unchanged -- the operator's own statusline tool (spec: `claudeline`,
// chosen and liked before this existed) rather than this command's own
// plain render.
//
// This is a command-line value on purpose, not an environment variable:
// Claude Code (including from inside Claude.app, whose processes do not
// inherit the operator's shell PATH or its environment at all) runs the
// exact string in settings.json's statusLine.command directly, so a value
// that has to travel through the environment never reaches it -- and would
// fail silently, with the status line just going quiet, rather than with
// an error naming why.
//
// That pass-through always happens first, and nothing after it -- the
// local-file write, the report to the panel -- may affect what was already
// printed: a broken half on our side must never cost the operator their
// status line.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/kroticw/fleetdeck/internal/config"
	"github.com/kroticw/fleetdeck/internal/usage"
)

type rateWindow struct {
	UsedPercentage float64 `json:"used_percentage"`
	ResetsAt       int64   `json:"resets_at"`
}

type statusInput struct {
	SessionID string `json:"session_id"`
	Model     struct {
		DisplayName string `json:"display_name"`
	} `json:"model"`
	Cost struct {
		TotalUSD float64 `json:"total_cost_usd"`
	} `json:"cost"`
	ContextWindow struct {
		UsedPercentage float64 `json:"used_percentage"`
	} `json:"context_window"`
	// RateLimits mirrors Claude Code's own statusline schema: each window may
	// be independently absent (not a subscriber, or Claude Code dropped it
	// once its resets_at passed), which is why every field here is a
	// pointer rather than a plain struct.
	RateLimits struct {
		FiveHour   *rateWindow `json:"five_hour"`
		SevenDay   *rateWindow `json:"seven_day"`
		SpendLimit *rateWindow `json:"spend_limit"`
	} `json:"rate_limits"`
}

func parse(raw []byte) (statusInput, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return statusInput{}, fmt.Errorf("no status input on stdin")
	}
	var in statusInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return statusInput{}, fmt.Errorf("parse status input: %w", err)
	}
	return in, nil
}

func render(in statusInput) string {
	name := in.Model.DisplayName
	if name == "" {
		name = "Claude"
	}
	return fmt.Sprintf("%s | $%.2f | %.0f%%", name, in.Cost.TotalUSD, in.ContextWindow.UsedPercentage)
}

func report(endpoint string, in statusInput) error {
	body, err := json.Marshal(map[string]any{
		"sessionId":      in.SessionID,
		"model":          in.Model.DisplayName,
		"costUSD":        in.Cost.TotalUSD,
		"contextPercent": in.ContextWindow.UsedPercentage,
	})
	if err != nil {
		return fmt.Errorf("encode report: %w", err)
	}
	client := &http.Client{Timeout: 500 * time.Millisecond}
	resp, err := client.Post(endpoint, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("post report: %w", err)
	}
	// The reporter never reads the response: the panel either took the report
	// or it did not, and a status line is no place to complain about it.
	_ = resp.Body.Close()
	return nil
}

// runWrapped runs wrapCmd through a shell (so a bare path and a command with
// its own flags both work, the same way Claude Code itself runs
// statusLine.command) feeding it stdin verbatim, and returns exactly what it
// wrote to stdout. Any failure to run at all, or a non-zero exit, is treated
// as "the wrap did not work" regardless of whatever partial output came
// back -- the caller falls back to this command's own render rather than
// print something the wrapped tool itself considered a failure.
func runWrapped(wrapCmd string, stdin []byte) ([]byte, error) {
	cmd := exec.Command("sh", "-c", wrapCmd)
	cmd.Stdin = bytes.NewReader(stdin)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("run wrapped statusline command: %w", err)
	}
	return out, nil
}

// writeRateLimits records the rate-limit windows Claude Code already put on
// stdin to the file usage.ReadLocal reads first -- see
// internal/usage/localfile.go for why this is the primary source and the
// network endpoint (internal/usage.Fetcher) only the fallback. Both windows
// must be present: the rest of this codebase already assumes five_hour and
// seven_day arrive together (usage.Fetcher's own network path enforces the
// same rule), and writing one without the other would put a value nothing
// downstream expects into a file every session's collect cycle reads.
func writeRateLimits(in statusInput) error {
	path := usage.LocalFilePath()
	if override := os.Getenv("FLEETDECK_RATE_LIMITS_PATH"); override != "" {
		path = override
	}
	tracePath := usage.TracePath()
	if override := os.Getenv("FLEETDECK_RATE_LIMITS_TRACE_PATH"); override != "" {
		tracePath = override
	}
	return writeRateLimitsTo(path, tracePath, in)
}

// writeRateLimitsTo is writeRateLimits with both paths pulled out, purely so
// a test can point them at its own temp files instead of the real machine's.
//
// A payload that carries some rate_limits data but not the required pair is
// not silently discarded: the schema drifting (a field renamed, moved, or
// dropped) would otherwise look identical to "no session has ticked its
// statusline in a while" -- exactly the wrong-cause failure this whole task
// exists to stop repeating (spec: the sign-in-vs-rate-limit bug). A payload
// with none of the three fields at all is the ordinary, unremarkable case
// (not a subscriber, or Claude Code has not attached rate_limits yet) and is
// not traced.
func writeRateLimitsTo(path, tracePath string, in statusInput) error {
	if in.RateLimits.FiveHour == nil || in.RateLimits.SevenDay == nil {
		if in.RateLimits.FiveHour != nil || in.RateLimits.SevenDay != nil || in.RateLimits.SpendLimit != nil {
			_ = usage.AppendTrace(tracePath, fmt.Sprintf(
				"rate_limits present but incomplete for the local file's required pair: five_hour=%t seven_day=%t spend_limit=%t",
				in.RateLimits.FiveHour != nil, in.RateLimits.SevenDay != nil, in.RateLimits.SpendLimit != nil,
			))
		}
		return nil
	}
	l := usage.Limits{
		FiveHour: usage.Window{
			Utilization: in.RateLimits.FiveHour.UsedPercentage,
			ResetsAt:    time.Unix(in.RateLimits.FiveHour.ResetsAt, 0),
		},
		SevenDay: usage.Window{
			Utilization: in.RateLimits.SevenDay.UsedPercentage,
			ResetsAt:    time.Unix(in.RateLimits.SevenDay.ResetsAt, 0),
		},
		FetchedAt: time.Now(),
	}
	if in.RateLimits.SpendLimit != nil {
		l.SpendLimit = &usage.Window{
			Utilization: in.RateLimits.SpendLimit.UsedPercentage,
			ResetsAt:    time.Unix(in.RateLimits.SpendLimit.ResetsAt, 0),
		}
	}
	return usage.WriteLocal(path, l)
}

// statusLineOutput decides exactly what this command prints, in isolation
// from every side effect below it in main -- run is injected so a test can
// exercise the fallback path without spawning a real process. wrapCmd set
// and run succeeding always wins: the operator's own tool, printed
// unchanged. Anything else falls back to this command's own render, or the
// fixed "status unavailable" line when even stdin did not parse -- never a
// blank status line.
func statusLineOutput(raw []byte, in statusInput, parseErr error, wrapCmd string, run func(string, []byte) ([]byte, error)) []byte {
	if wrapCmd != "" {
		if out, err := run(wrapCmd, raw); err == nil {
			return out
		}
	}
	if parseErr != nil {
		return []byte("Claude | status unavailable\n")
	}
	return []byte(render(in) + "\n")
}

// resolveWrapCmd is the flag-then-config precedence rule the package doc
// explains: a flag on the actual invoked command line always wins because
// it is guaranteed to reach a process launched however Claude Code launches
// it; config.yaml is read only when the flag was not given at all (an empty
// string is "not given" for this flag -- there is no legitimate case where
// wrapping into an empty command is meant to differ from not wrapping).
func resolveWrapCmd(flagValue, configPath string) string {
	if flagValue != "" {
		return flagValue
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return ""
	}
	return cfg.StatuslineWrap
}

func main() {
	wrapFlag := flag.String("wrap", "", "statusline command to pass stdin through to and print unchanged")
	configPath := flag.String("config", config.DefaultPath(), "path to the configuration file, read only when -wrap is not given")
	flag.Parse()

	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Println("Claude | status unavailable")
		return
	}

	in, parseErr := parse(raw)
	wrapCmd := resolveWrapCmd(*wrapFlag, *configPath)
	// A write failure here means the operator's terminal is gone -- nothing
	// downstream in this process can do anything about that.
	_, _ = os.Stdout.Write(statusLineOutput(raw, in, parseErr, wrapCmd, runWrapped))

	// Everything below is best-effort and strictly after the line above:
	// nothing here may run before the operator's status line is already on
	// the way to their terminal, and no error here changes what was printed.
	if parseErr != nil {
		return
	}
	endpoint := os.Getenv("FLEETDECK_ENDPOINT")
	if endpoint == "" {
		endpoint = "http://127.0.0.1:7777/api/status"
	}
	// A panel that is down must never cost the user their status line.
	_ = report(endpoint, in)
	_ = writeRateLimits(in)
}
