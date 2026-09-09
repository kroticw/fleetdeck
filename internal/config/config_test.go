package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadMissingFileReturnsDefaults(t *testing.T) {
	got, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))
	if err != nil {
		t.Fatalf("missing file must not be an error, got %v", err)
	}
	if got.ServerPort != Default().ServerPort {
		t.Fatalf("missing file must yield defaults, got %+v", got)
	}
}

func TestLoadBrokenFileIsAnError(t *testing.T) {
	p := filepath.Join(t.TempDir(), "broken.yaml")
	os.WriteFile(p, []byte("server_port: [1,2\n"), 0o600)
	if _, err := Load(p); err == nil {
		t.Fatal("a broken config must fail loudly, not fall back to defaults")
	}
}

func TestLoadOverridesOnlyGivenKeys(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	os.WriteFile(p, []byte("server_port: 9001\n"), 0o600)
	got, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.ServerPort != 9001 {
		t.Fatalf("port not applied: %d", got.ServerPort)
	}
	if got.DaemonPollInterval != Default().DaemonPollInterval {
		t.Fatalf("untouched key must keep its default, got %v", got.DaemonPollInterval)
	}
}

func TestSaveThenLoadRoundTrips(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	want := Default()
	want.BoardPath = "/tmp/board"
	want.Notify.SilenceAfter = 45 * time.Minute
	if err := Save(p, want); err != nil {
		t.Fatal(err)
	}
	got, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.BoardPath != want.BoardPath || got.Notify.SilenceAfter != want.Notify.SilenceAfter {
		t.Fatalf("round trip lost data: %+v", got)
	}
}
