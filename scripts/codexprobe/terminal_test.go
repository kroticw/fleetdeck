package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestTerminalHelper(_ *testing.T) {
	if os.Getenv("FLEETDECK_PTY_TEST") != "1" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		switch scanner.Text() {
		case "size":
			size, err := unix.IoctlGetWinsize(0, unix.TIOCGWINSZ)
			if err != nil {
				os.Exit(3)
			}
			_, _ = fmt.Fprintf(os.Stdout, "SIZE=%dx%d\n", size.Col, size.Row)
		case "flood":
			_, _ = fmt.Fprint(os.Stdout, strings.Repeat("x", 128*1024)+"FLOOD_DONE\n")
		case "fd":
			fd, _ := strconv.Atoi(os.Getenv("FLEETDECK_PARENT_FD"))
			// Rdev's type differs by platform; its decimal form does not.
			var stat unix.Stat_t
			if err := unix.Fstat(fd, &stat); err == nil && fmt.Sprint(stat.Rdev) == os.Getenv("FLEETDECK_PARENT_RDEV") {
				_, _ = fmt.Fprintln(os.Stdout, "FD_LEAK")
			} else {
				_, _ = fmt.Fprintln(os.Stdout, "FD_SAFE")
			}
		case "quit":
			os.Exit(0)
		}
	}
	if scanner.Err() != nil {
		os.Exit(4)
	}
	os.Exit(0)
}

func TestTerminalOwnsPTYWithoutReaders(t *testing.T) {
	t.Setenv("FLEETDECK_PTY_TEST", "1")
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	terminal, err := startTerminal(ctx, []string{binary, "-test.run=^TestTerminalHelper$"}, t.TempDir(), 80, 24, 4096)
	if err != nil {
		t.Fatal(err)
	}
	defer terminal.Close()
	write := func(text string) {
		t.Helper()
		if _, writeErr := terminal.master.Write([]byte(text + "\n")); writeErr != nil {
			t.Fatal(writeErr)
		}
	}
	waitFor := func(marker string) []byte {
		t.Helper()
		for {
			data, _, _ := terminal.Output(0)
			if strings.Contains(string(data), marker) {
				return data
			}
			select {
			case <-ctx.Done():
				t.Fatalf("missing %q", marker)
				return nil
			case <-terminal.changed:
			}
		}
	}
	write("size")
	waitFor("SIZE=80x24")
	if err = terminal.Resize(101, 33); err != nil {
		t.Fatal(err)
	}
	write("size")
	waitFor("SIZE=101x33")
	write("flood")
	data := waitFor("FLOOD_DONE")
	_, total, dropped := terminal.Output(0)
	if len(data) > 4096 || total < 128*1024 || !dropped {
		t.Fatalf("buffer len=%d total=%d dropped=%v", len(data), total, dropped)
	}
	write("quit")
	select {
	case <-terminal.done:
	case <-ctx.Done():
		t.Fatal("child did not exit")
	}
}

func TestTerminalRejectsRelativeExecutable(t *testing.T) {
	_, err := startTerminal(context.Background(), []string{filepath.Base("/usr/bin/true")}, t.TempDir(), 79, 24, 4096)
	if err == nil {
		t.Fatal("relative executable accepted")
	}
}

func TestTerminalQuietCloseUnblocksRead(t *testing.T) {
	t.Setenv("FLEETDECK_PTY_TEST", "1")
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	terminal, err := startTerminal(ctx, []string{binary, "-test.run=^TestTerminalHelper$"}, t.TempDir(), 79, 23, 8192)
	if err != nil {
		t.Fatal(err)
	}
	if err = terminal.Resize(101, 33); err != nil {
		t.Fatal(err)
	}
	closed := make(chan struct{})
	go func() { terminal.Close(); close(closed) }()
	select {
	case <-closed:
	case <-ctx.Done():
		t.Fatal("quiet terminal close blocked")
	}
}

func TestTerminalMasterIsNotInherited(t *testing.T) {
	t.Setenv("FLEETDECK_PTY_TEST", "1")
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	first, err := startTerminal(ctx, []string{binary, "-test.run=^TestTerminalHelper$"}, t.TempDir(), 80, 24, 4096)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	raw, err := first.master.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var parentFD int
	var stat unix.Stat_t
	var statErr error
	if err = raw.Control(func(fd uintptr) { parentFD = int(fd); statErr = unix.Fstat(int(fd), &stat) }); err != nil {
		t.Fatal(err)
	}
	if statErr != nil {
		t.Fatal(statErr)
	}
	t.Setenv("FLEETDECK_PARENT_FD", strconv.Itoa(parentFD))
	t.Setenv("FLEETDECK_PARENT_RDEV", fmt.Sprint(stat.Rdev))
	second, err := startTerminal(ctx, []string{binary, "-test.run=^TestTerminalHelper$"}, t.TempDir(), 80, 24, 4096)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if _, err = second.master.Write([]byte("fd\n")); err != nil {
		t.Fatal(err)
	}
	for {
		data, _, _ := second.Output(0)
		if strings.Contains(string(data), "FD_LEAK") {
			t.Fatal("second child inherited first terminal master")
		}
		if strings.Contains(string(data), "FD_SAFE") {
			return
		}
		select {
		case <-second.changed:
		case <-ctx.Done():
			t.Fatal("child did not report descriptors")
		}
	}
}

func TestTerminalEnvironmentDoesNotInheritParentTask(t *testing.T) {
	env := terminalEnv([]string{"TERM=dumb", "CODEX_THREAD_ID=parent", "CODEX_INTERNAL_ORIGINATOR_OVERRIDE=app", "PATH=/test/bin", "PROVIDER_SETTING=keep"})
	joined := strings.Join(env, "\n")
	for _, unwanted := range []string{"TERM=dumb", "CODEX_THREAD_ID=", "CODEX_INTERNAL_ORIGINATOR_OVERRIDE="} {
		if strings.Contains(joined, unwanted) {
			t.Fatalf("parent context leaked: %s", unwanted)
		}
	}
	for _, expected := range []string{"TERM=xterm-256color", "PATH=/test/bin", "PROVIDER_SETTING=keep"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("missing %s", expected)
		}
	}
}
