package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// serveOnce reads one request, processes it, and sends one response.
// It closes the connection after.
func serveOnce(t *testing.T, listener net.Listener, fn func(t *testing.T, req []byte) []byte) {
	t.Helper()
	conn, err := listener.Accept()
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	defer conn.Close()

	reader := bufio.NewReader(conn)
	line, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		t.Fatalf("ReadString: %v", err)
	}

	resp := fn(t, []byte(line[:len(line)-1])) // strip \n
	conn.Write(resp)
}

func TestFixtureParsesThreeSessions(t *testing.T) {
	data, err := os.ReadFile("testdata/list_sessions.json")
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}

	var resp struct {
		Ok   bool      `json:"ok"`
		Op   string    `json:"op"`
		Jobs []Session `json:"jobs"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		t.Fatalf("parsing fixture: %v", err)
	}

	if len(resp.Jobs) == 0 {
		t.Fatal("fixture contains no sessions to test with")
	}
	if len(resp.Jobs) != 3 {
		t.Errorf("expected 3 sessions, got %d", len(resp.Jobs))
	}
}

func TestWaiting(t *testing.T) {
	data, err := os.ReadFile("testdata/list_sessions.json")
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}

	var resp struct {
		Jobs []Session `json:"jobs"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		t.Fatalf("parsing fixture: %v", err)
	}

	// Verify that the fixture has at least one waiting session
	foundWaiting := false
	foundNotWaiting := 0

	for _, s := range resp.Jobs {
		if s.Waiting() {
			foundWaiting = true
			// Verify the waiting session has tempo="blocked" and needs != ""
			if s.Tempo != "blocked" {
				t.Errorf("waiting session has tempo=%q, expected blocked", s.Tempo)
			}
			if s.Needs == "" {
				t.Error("waiting session has empty needs")
			}
		} else {
			foundNotWaiting++
		}
	}

	if !foundWaiting {
		t.Error("fixture should have at least one waiting session")
	}
	if foundNotWaiting == 0 {
		t.Error("fixture should have at least one non-waiting session")
	}
}

func TestMissingSocketErrDaemonUnavailable(t *testing.T) {
	client := New("/nonexistent/socket/path", func() (string, error) {
		return "key", nil
	})

	_, err := client.ListSessions(context.Background())
	if err != ErrDaemonUnavailable {
		t.Errorf("expected ErrDaemonUnavailable, got %v", err)
	}
}

func TestTruncatedJSONIsError(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Skipf("sandbox denies unix socket bind: %v", err)
	}
	defer listener.Close()

	go func() {
		serveOnce(t, listener, func(t *testing.T, req []byte) []byte {
			// Return truncated JSON (missing closing brace)
			return []byte(`{"ok":true,"op":"list","jobs":[`)
		})
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})

	_, err = client.ListSessions(context.Background())
	if err == nil {
		t.Error("expected error for truncated JSON, got nil")
	}
}

func TestSilentDaemonTimesOut(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Skipf("sandbox denies unix socket bind: %v", err)
	}
	defer listener.Close()

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			t.Logf("Accept: %v", err)
			return
		}
		defer conn.Close()
		// Don't send anything; just wait
		select {}
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err = client.ListSessions(ctx)
	if err == nil {
		t.Error("expected timeout error, got nil")
	}
}

func TestRequestEndsWithNewline(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Skipf("sandbox denies unix socket bind: %v", err)
	}
	defer listener.Close()

	requestLines := make([]string, 0)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			reader := bufio.NewReader(conn)
			line, err := reader.ReadString('\n')
			if err != nil {
				conn.Close()
				return
			}
			requestLines = append(requestLines, line)

			// Send a ping response on the first request
			if len(requestLines) == 1 {
				conn.Write([]byte(`{"ok":true,"op":"ping","version":"2.1.263","proto":1}` + "\n"))
			} else {
				// Send a list response
				conn.Write([]byte(`{"ok":true,"op":"list","jobs":[]}` + "\n"))
			}
			conn.Close()
		}
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})

	// Trigger requests
	client.Ping(context.Background())
	client.ListSessions(context.Background())

	// Wait a bit for requests to be processed
	time.Sleep(100 * time.Millisecond)

	// Verify each request ends with exactly one \n
	for i, line := range requestLines {
		if !strings.HasSuffix(line, "\n") {
			t.Errorf("request %d does not end with newline: %q", i, line)
		}
		// Verify only one newline at the end
		if strings.Count(line, "\n") != 1 {
			t.Errorf("request %d has multiple newlines: %q", i, line)
		}
	}
}

func TestProtoNegotiation(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Skipf("sandbox denies unix socket bind: %v", err)
	}
	defer listener.Close()

	requestProtos := make([]interface{}, 0)
	errChan := make(chan error, 1)
	go func() {
		for i := 0; i < 2; i++ {
			serveOnce(t, listener, func(t *testing.T, req []byte) []byte {
				var m map[string]interface{}
				if err := json.Unmarshal(req, &m); err != nil {
					errChan <- err
					return nil
				}
				requestProtos = append(requestProtos, m["proto"])

				if i == 0 { // ping response
					return []byte(`{"ok":true,"op":"ping","version":"2.1.263","proto":7}` + "\n")
				} else { // list response
					return []byte(`{"ok":true,"op":"list","jobs":[]}` + "\n")
				}
			})
		}
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})

	client.Ping(context.Background())
	client.ListSessions(context.Background())

	// Wait a bit for goroutine to finish
	time.Sleep(100 * time.Millisecond)

	// Check for any errors from the goroutine
	select {
	case err := <-errChan:
		t.Fatalf("error in server: %v", err)
	default:
	}

	// First request (ping) should have no proto field
	if proto := requestProtos[0]; proto != nil {
		t.Errorf("ping request should not have proto field, got %v", proto)
	}

	// Second request (list) should have proto 7 from the ping response
	if proto := requestProtos[1]; proto != float64(7) {
		t.Errorf("list request should have proto 7, got %v", proto)
	}
}

func TestPingNoProtoField(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Skipf("sandbox denies unix socket bind: %v", err)
	}
	defer listener.Close()

	errChan := make(chan error, 1)
	go func() {
		serveOnce(t, listener, func(t *testing.T, req []byte) []byte {
			var m map[string]interface{}
			if err := json.Unmarshal(req, &m); err != nil {
				errChan <- err
				return nil
			}

			if _, ok := m["proto"]; ok {
				t.Error("ping request should not have proto field")
			}
			return []byte(`{"ok":true,"op":"ping","version":"2.1.263","proto":1}` + "\n")
		})
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})

	client.Ping(context.Background())

	// Wait a bit for goroutine to finish
	time.Sleep(100 * time.Millisecond)

	// Check for any errors from the goroutine
	select {
	case err := <-errChan:
		t.Fatalf("error in server: %v", err)
	default:
	}
}

func TestErrorCodeEPROTO(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Skipf("sandbox denies unix socket bind: %v", err)
	}
	defer listener.Close()

	go func() {
		serveOnce(t, listener, func(t *testing.T, req []byte) []byte {
			return []byte(`{"ok":false,"code":"EPROTO","error":"proto mismatch"}` + "\n")
		})
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})

	_, err = client.Ping(context.Background())
	var epErr *ErrProto
	if !errors.As(err, &epErr) {
		t.Errorf("expected *ErrProto, got %T: %v", err, err)
	}
}

func TestErrorCodeEAUTH(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Skipf("sandbox denies unix socket bind: %v", err)
	}
	defer listener.Close()

	go func() {
		serveOnce(t, listener, func(t *testing.T, req []byte) []byte {
			return []byte(`{"ok":false,"code":"EAUTH","error":"invalid auth"}` + "\n")
		})
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "test-key", nil
	})

	_, err = client.ListSessions(context.Background())
	var eaErr *ErrAuth
	if !errors.As(err, &eaErr) {
		t.Errorf("expected *ErrAuth, got %T: %v", err, err)
	}

	// Error message should not contain the control key
	if strings.Contains(err.Error(), "test-key") {
		t.Error("error message should not contain control key")
	}
}

func TestErrorCodeEPEERUID(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Skipf("sandbox denies unix socket bind: %v", err)
	}
	defer listener.Close()

	go func() {
		serveOnce(t, listener, func(t *testing.T, req []byte) []byte {
			return []byte(`{"ok":false,"code":"EPEERUID","error":"peer uid mismatch"}` + "\n")
		})
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})

	_, err = client.ListSessions(context.Background())
	var epErr *ErrPeeruid
	if !errors.As(err, &epErr) {
		t.Errorf("expected *ErrPeeruid, got %T: %v", err, err)
	}
}

func TestControlKeyMissingFile(t *testing.T) {
	// Use a temp directory that doesn't exist
	oldHome := os.Getenv("HOME")
	defer os.Setenv("HOME", oldHome)

	tmpDir := t.TempDir()
	os.Setenv("HOME", tmpDir)

	key, err := ControlKey()
	if err != ErrNoControlKey {
		t.Errorf("expected ErrNoControlKey, got %v", err)
	}
	if key != "" {
		t.Errorf("expected empty key, got %q", key)
	}
}

func TestNoControlKeyLeakedInError(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Skipf("sandbox denies unix socket bind: %v", err)
	}
	defer listener.Close()

	go func() {
		serveOnce(t, listener, func(t *testing.T, req []byte) []byte {
			return []byte(`{"ok":false,"code":"EAUTH","error":"invalid auth"}` + "\n")
		})
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "my-secret-key-12345", nil
	})

	_, err = client.ListSessions(context.Background())

	// Error message should not contain the key
	if strings.Contains(err.Error(), "my-secret-key-12345") {
		t.Error("control key leaked into error message")
	}
}

// tempSocket creates a temporary unix socket path
func tempSocket(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	return filepath.Join(dir, "test.sock")
}
