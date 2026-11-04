package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path/filepath"

	"github.com/coder/websocket"
)

type threadReport struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}
type controlReport struct {
	Threads []threadReport `json:"threads"`
}

type rpcMessage struct {
	ID     int             `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params any             `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *struct {
		Code int `json:"code"`
	} `json:"error,omitempty"`
}

// inspectControl observes only the selected disposable working directory.
// The daemon is never started, configured or stopped by this probe.
func inspectControl(ctx context.Context, socket, cwd string) (controlReport, error) {
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}
	defer transport.CloseIdleConnections()
	conn, _, err := websocket.Dial(ctx, "ws://localhost/", &websocket.DialOptions{HTTPClient: &http.Client{Transport: transport}})
	if err != nil {
		return controlReport{}, fmt.Errorf("connect control socket: %w", err)
	}
	defer func() { _ = conn.CloseNow() }()
	conn.SetReadLimit(4 << 20)
	call := func(id int, method string, params any) (json.RawMessage, error) {
		data, marshalErr := json.Marshal(rpcMessage{ID: id, Method: method, Params: params})
		if marshalErr != nil {
			return nil, marshalErr
		}
		if writeErr := conn.Write(ctx, websocket.MessageText, data); writeErr != nil {
			return nil, writeErr
		}
		for {
			_, raw, readErr := conn.Read(ctx)
			if readErr != nil {
				return nil, readErr
			}
			var msg rpcMessage
			if decodeErr := json.Unmarshal(raw, &msg); decodeErr != nil {
				return nil, decodeErr
			}
			if msg.ID != id {
				if msg.ID != 0 {
					return nil, errors.New("unexpected server request or response")
				}
				continue
			}
			if msg.Error != nil {
				return nil, fmt.Errorf("RPC %s failed with code %d", method, msg.Error.Code)
			}
			if msg.Result == nil {
				return nil, errors.New("RPC response has no result")
			}
			return msg.Result, nil
		}
	}
	_, err = call(1, "initialize", map[string]any{"clientInfo": map[string]string{"name": "fleetdeck_probe", "version": "0.0.0"}})
	if err != nil {
		return controlReport{}, err
	}
	if err = conn.Write(ctx, websocket.MessageText, []byte(`{"method":"initialized"}`)); err != nil {
		return controlReport{}, err
	}
	report := controlReport{Threads: []threadReport{}}
	cursor := ""
	for page := 0; page < 20; page++ {
		params := map[string]any{"cwd": cwdSpellings(cwd), "limit": 50, "useStateDbOnly": true, "modelProviders": []string{}}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, callErr := call(page+2, "thread/list", params)
		if callErr != nil {
			return controlReport{}, callErr
		}
		var result struct {
			Data []struct {
				ID     string `json:"id"`
				CWD    string `json:"cwd"`
				Status struct {
					Type string `json:"type"`
				} `json:"status"`
			} `json:"data"`
			NextCursor *string `json:"nextCursor"`
		}
		if err = json.Unmarshal(raw, &result); err != nil {
			return controlReport{}, err
		}
		for _, thread := range result.Data {
			if sameCWD(thread.CWD, cwd) {
				report.Threads = append(report.Threads, threadReport{ID: thread.ID, Status: thread.Status.Type})
			}
		}
		if result.NextCursor == nil || *result.NextCursor == "" {
			return report, nil
		}
		if *result.NextCursor == cursor {
			return controlReport{}, errors.New("thread listing repeated its cursor")
		}
		cursor = *result.NextCursor
	}
	return controlReport{}, errors.New("thread listing exceeded the probe page limit")
}

// cwdSpellings keeps the original spelling because Codex can store it verbatim.
func cwdSpellings(cwd string) []string {
	paths := []string{filepath.Clean(cwd)}
	if resolved, err := filepath.EvalSymlinks(cwd); err == nil && resolved != paths[0] {
		paths = append(paths, resolved)
	}
	return paths
}
func sameCWD(a, b string) bool {
	for _, left := range cwdSpellings(a) {
		for _, right := range cwdSpellings(b) {
			if left == right {
				return true
			}
		}
	}
	return false
}
