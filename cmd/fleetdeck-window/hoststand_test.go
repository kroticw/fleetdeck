//go:build darwin

package main

import (
	"strings"
	"testing"
)

func TestOnlyAStandsHostObjectSaysItIsOnAStand(t *testing.T) {
	if strings.Contains(hostScript("board", glassModeGlass, nil), "stand") {
		t.Fatal("a person's window marks its page as on a stand")
	}
	hostOnStand = true
	defer func() { hostOnStand = false }()
	if !strings.Contains(hostScript("board", glassModeGlass, nil), "host.stand=true;") {
		t.Fatal("a stand's window does not mark its page as on a stand")
	}
}
