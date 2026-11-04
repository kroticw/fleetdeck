package codexruntime

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kroticw/fleetdeck/internal/executor"
)

const testRuntimeKey executor.Key = "codex:01a1a6d4-90a4-70f3-9130-a59ec873d0ba"

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenStore(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func runtimeRecord() Record {
	return Record{Version: ProtocolVersion, Key: testRuntimeKey, Nonce: "test-runtime-identity-32-bytes-long", PID: 42, CWD: "/repo", Board: "/board", Card: "/board/cards/T-001.md", Lifecycle: "starting", CreatedAt: time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)}
}

func TestStoreWritesAtomicallyAndConfinesKeys(t *testing.T) {
	s := testStore(t)
	owner, err := s.Acquire(testRuntimeKey)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = owner.Close() }()
	record := runtimeRecord()
	if err := owner.Save(record); err != nil {
		t.Fatal(err)
	}
	got, err := s.Read(string(testRuntimeKey))
	if err != nil || !reflect.DeepEqual(got, record) {
		t.Fatalf("record=%+v err=%v", got, err)
	}
	info, err := os.Stat(filepath.Join(s.root.Name(), strings.TrimPrefix(string(testRuntimeKey), "codex:")+".json"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("record mode: %v %v", info, err)
	}
	for _, raw := range []string{"../other", "abc12345", "codex:../../other", "/tmp/record"} {
		if _, err := s.Read(raw); !errors.Is(err, executor.ErrInvalidKey) {
			t.Fatalf("unsafe key %q: %v", raw, err)
		}
	}
	s.rename = func(_, _ string) error { return errors.New("injected rename failure") }
	record.Lifecycle = "live"
	if err := owner.Save(record); err == nil {
		t.Fatal("interrupted write succeeded")
	}
	got, err = s.Read(string(testRuntimeKey))
	if err != nil || got.Lifecycle != "starting" {
		t.Fatalf("interrupted write replaced record: %+v %v", got, err)
	}
}

func TestStoreRejectsExposedDirectoryAndFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if s, err := OpenStore(dir); !errors.Is(err, ErrPrivateState) {
		if s != nil {
			_ = s.Close()
		}
		t.Fatalf("exposed directory accepted: %v", err)
	}
	s := testStore(t)
	name := strings.TrimPrefix(string(testRuntimeKey), "codex:") + ".json"
	if err := os.WriteFile(filepath.Join(s.root.Name(), name), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Read(string(testRuntimeKey)); !errors.Is(err, ErrPrivateState) {
		t.Fatalf("exposed record: %v", err)
	}
}

func TestStoreRejectsSymlinkRecordAndLock(t *testing.T) {
	for _, suffix := range []string{".json", ".lock"} {
		t.Run(suffix, func(t *testing.T) {
			s := testStore(t)
			outside := filepath.Join(t.TempDir(), "foreign")
			if err := os.WriteFile(outside, []byte("foreign"), 0o600); err != nil {
				t.Fatal(err)
			}
			name := strings.TrimPrefix(string(testRuntimeKey), "codex:") + suffix
			if err := os.Symlink(outside, filepath.Join(s.root.Name(), name)); err != nil {
				t.Fatal(err)
			}
			if suffix == ".json" {
				if _, err := s.Read(string(testRuntimeKey)); err == nil {
					t.Fatal("symlink record accepted")
				}
			} else {
				if owner, err := s.Acquire(testRuntimeKey); err == nil {
					_ = owner.Close()
					t.Fatal("symlink lock accepted")
				}
			}
			raw, err := os.ReadFile(outside)
			if err != nil || string(raw) != "foreign" {
				t.Fatalf("foreign file changed: %q %v", raw, err)
			}
		})
	}
}

func TestStoreRejectsSymlinkDirectory(t *testing.T) {
	s := testStore(t)
	link := filepath.Join(t.TempDir(), "linked-state")
	if err := os.Symlink(s.root.Name(), link); err != nil {
		t.Fatal(err)
	}
	if got, err := OpenStore(link); !errors.Is(err, ErrPrivateState) {
		if got != nil {
			_ = got.Close()
		}
		t.Fatalf("symlink directory accepted: %v", err)
	}
}

func TestStoreRejectsFutureVersionAndForeignRecord(t *testing.T) {
	s := testStore(t)
	name := strings.TrimPrefix(string(testRuntimeKey), "codex:") + ".json"
	for _, tt := range []struct {
		name   string
		modify func(*Record)
		want   error
	}{
		{"future protocol", func(r *Record) { r.Version++ }, ErrIncompatible},
		{"foreign key", func(r *Record) { r.Key = "codex:02a1a6d4-90a4-70f3-9130-a59ec873d0ba" }, ErrInvalidRecord},
		{"foreign card", func(r *Record) { r.Card = "/other/cards/T-001.md" }, ErrInvalidRecord},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := runtimeRecord()
			tt.modify(&r)
			raw, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(s.root.Name(), name), raw, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Read(string(testRuntimeKey)); !errors.Is(err, tt.want) {
				t.Fatalf("got %v, want %v", err, tt.want)
			}
		})
	}
}

func TestStoreSizeLimitNeverPublishesAnUnreadableRecord(t *testing.T) {
	s := testStore(t)
	owner, err := s.Acquire(testRuntimeKey)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = owner.Close() }()
	r := runtimeRecord()
	r.Profile = "x"
	base, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, size := range []int{maxRecordSize - 1, maxRecordSize} {
		r.Profile = strings.Repeat("x", size-len(base)+1)
		err := owner.Save(r)
		if size == maxRecordSize {
			if !errors.Is(err, ErrInvalidRecord) {
				t.Fatalf("oversize write: %v", err)
			}
		} else if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Read(string(testRuntimeKey)); err != nil {
			t.Fatalf("saved record cannot be read: %v", err)
		}
	}
}

func TestStoreLockChild(_ *testing.T) {
	dir := os.Getenv("FLEETDECK_LOCK_TEST_DIR")
	if dir == "" {
		return
	}
	s, err := OpenStore(dir)
	if err != nil {
		os.Exit(3)
	}
	defer func() { _ = s.Close() }()
	owner, err := s.Acquire(testRuntimeKey)
	if errors.Is(err, executor.ErrConflict) {
		os.Exit(23)
	}
	if err != nil {
		os.Exit(4)
	}
	_ = owner.Close()
	os.Exit(0)
}

func TestStoreLockExcludesAnotherProcessAndReleases(t *testing.T) {
	s := testStore(t)
	owner, err := s.Acquire(testRuntimeKey)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = owner.Close() }()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := func(want int) {
		t.Helper()
		cmd := exec.Command(binary, "-test.run=^TestStoreLockChild$")
		cmd.Env = append(os.Environ(), "FLEETDECK_LOCK_TEST_DIR="+s.root.Name(), "GORACE=atexit_sleep_ms=0")
		err := cmd.Run()
		code := 0
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			code = exit.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		if code != want {
			t.Fatalf("lock child exited %d, want %d", code, want)
		}
	}
	child(23)
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	child(0)
	if err := owner.Save(runtimeRecord()); !errors.Is(err, executor.ErrConflict) {
		t.Fatalf("closed owner saved: %v", err)
	}
}
