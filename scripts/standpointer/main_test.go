package main

import (
	"bytes"
	"strings"
	"testing"
)

// Off a CI runner the pointer is a person's: whatever the environment says
// short of GITHUB_ACTIONS=true, nothing is moved and the refusal says why.
func TestOffARunnerThePointerIsNeverMoved(t *testing.T) {
	for _, env := range []map[string]string{
		{},
		{"GITHUB_ACTIONS": ""},
		{"GITHUB_ACTIONS": "false"},
		{"GITHUB_ACTIONS": "1"},
		{"GITHUB_ACTIONS": "TRUE"},
		{"CI": "true"},
	} {
		moved := false
		var out, errOut bytes.Buffer
		code := run("top", func(name string) (string, bool) {
			v, ok := env[name]
			return v, ok
		}, func(bool) (result, error) {
			moved = true
			return result{}, nil
		}, &out, &errOut)
		if moved || code == 0 || !strings.Contains(errOut.String(), "GITHUB_ACTIONS") {
			t.Errorf("env %v: moved %v, exit %d, said %q; want nothing moved and a refusal naming GITHUB_ACTIONS", env, moved, code, errOut.String())
		}
	}
}

func onRunner(name string) (string, bool) {
	if name == "GITHUB_ACTIONS" {
		return "true", true
	}
	return "", false
}

func TestOnARunnerThePointerGoesWhereAskedAndSaysWhere(t *testing.T) {
	var asked []bool
	var out, errOut bytes.Buffer
	code := run("top", onRunner, func(top bool) (result, error) {
		asked = append(asked, top)
		return result{access: true, askX: 512, askY: 0, atX: 512, atY: 0}, nil
	}, &out, &errOut)
	if code != 0 || len(asked) != 1 || !asked[0] || !strings.Contains(out.String(), "is at (512, 0)") {
		t.Fatalf("exit %d, asked %v, said %q %q", code, asked, out.String(), errOut.String())
	}
}

// A runner whose TCC does not let the process post events leaves the pointer
// where it was: that is a failure, said with where it is.
func TestAPointerThatDidNotMoveIsAFailure(t *testing.T) {
	var out, errOut bytes.Buffer
	code := run("top", onRunner, func(bool) (result, error) {
		return result{access: false, askX: 512, askY: 0, atX: 512, atY: 384}, nil
	}, &out, &errOut)
	if code == 0 || !strings.Contains(out.String(), "post event access false") {
		t.Fatalf("exit %d, said %q", code, out.String())
	}
}

func TestAnUnknownPlaceIsRefused(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run("bottom", onRunner, func(bool) (result, error) {
		t.Fatal("moved to an unknown place")
		return result{}, nil
	}, &out, &errOut); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
}
