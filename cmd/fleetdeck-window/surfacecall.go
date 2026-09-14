//go:build darwin

package main

import (
	"encoding/json"
	"errors"
	"fmt"
)

// surfaceCall is one binding call a surface's page posted to its message
// handler (hostScript): the promise it waits on, the binding, its argument.
type surfaceCall struct {
	ID   int64           `json:"id"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

var errNotACall = errors.New("not a binding call")

func parseSurfaceCall(message string) (surfaceCall, error) {
	var call surfaceCall
	if err := json.Unmarshal([]byte(message), &call); err != nil {
		return surfaceCall{}, fmt.Errorf("%w: %v", errNotACall, err)
	}
	if call.ID <= 0 || call.Name == "" {
		return surfaceCall{}, fmt.Errorf("%w: %q", errNotACall, message)
	}
	return call, nil
}

// replyScript settles the promise of call id in the page: with the answer, or
// rejected with the error's text.
func replyScript(id int64, value any, err error) string {
	if err != nil {
		text, _ := json.Marshal(err.Error())
		return fmt.Sprintf("window.fleetdeckHost&&window.fleetdeckHost._reply(%d,false,%s)", id, text)
	}
	answer, marshalErr := json.Marshal(value)
	if marshalErr != nil {
		text, _ := json.Marshal(marshalErr.Error())
		return fmt.Sprintf("window.fleetdeckHost&&window.fleetdeckHost._reply(%d,false,%s)", id, text)
	}
	return fmt.Sprintf("window.fleetdeckHost&&window.fleetdeckHost._reply(%d,true,%s)", id, answer)
}

// answerSurfaceCall runs the binding call in message for surface and answers
// with the script that settles its promise, or "" for a message that is no call.
func answerSurfaceCall(b *bridge, surface, message string) string {
	call, err := parseSurfaceCall(message)
	if err != nil {
		return ""
	}
	value, err := b.call(surface, call.Name, call.Args)
	return replyScript(call.ID, value, err)
}

// receiveScript hands the window's message to the page (web/js/host.js).
func receiveScript(message map[string]any) string {
	text, _ := json.Marshal(message)
	return "window.fleetdeckHost&&window.fleetdeckHost.receive(" + string(text) + ")"
}
