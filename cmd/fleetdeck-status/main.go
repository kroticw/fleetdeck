// Command fleetdeck-status is the statusline reporter. Claude Code runs it for
// every session: it prints the status line and forwards the same structured
// data to the local panel, which cannot obtain it any other way.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

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
}

func parse(r io.Reader) (statusInput, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return statusInput{}, fmt.Errorf("read stdin: %w", err)
	}
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

func main() {
	in, err := parse(os.Stdin)
	if err != nil {
		fmt.Println("Claude | status unavailable")
		return
	}
	endpoint := os.Getenv("FLEETDECK_ENDPOINT")
	if endpoint == "" {
		endpoint = "http://127.0.0.1:7777/api/status"
	}
	// A panel that is down must never cost the user their status line.
	_ = report(endpoint, in)
	fmt.Println(render(in))
}
