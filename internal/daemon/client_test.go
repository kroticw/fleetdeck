package daemon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

	trimmed := strings.TrimSuffix(line, "\n")
	resp := fn(t, []byte(trimmed))
	conn.Write(resp)
}

// loadFixtureLine reads the pretty-printed fixture and compacts it to the single-line
// form the real daemon actually sends over the wire (see the addendum: a response is
// one line terminated by '\n'). The fixture stays pretty-printed on disk for human review.
func loadFixtureLine(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/list_sessions.json")
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, data); err != nil {
		t.Fatalf("compacting fixture: %v", err)
	}
	buf.WriteByte('\n')
	return buf.Bytes()
}

// TestFixtureParsesViaListSessions serves the fixture over a fake socket and feeds it
// through the production ListSessions parser, rather than unmarshalling it into a
// locally declared struct. Renaming the "jobs" key in client.go must break this test.
func TestFixtureParsesViaListSessions(t *testing.T) {
	line := loadFixtureLine(t)

	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	go func() {
		serveOnce(t, listener, func(t *testing.T, req []byte) []byte {
			return line
		})
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})
	client.proto = 1

	sessions, err := client.ListSessions(context.Background())
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) == 0 {
		t.Fatal("fixture contains no sessions to test with")
	}
}

// TestWaitingAgainstFixture parses the fixture through ListSessions and checks that it
// carries both real waiting forms plus a plainly working session, and that Waiting()
// agrees with the raw fields on every record.
func TestWaitingAgainstFixture(t *testing.T) {
	line := loadFixtureLine(t)

	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	go func() {
		serveOnce(t, listener, func(t *testing.T, req []byte) []byte {
			return line
		})
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})
	client.proto = 1

	sessions, err := client.ListSessions(context.Background())
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}

	var foundForm1, foundForm2, foundWorking bool
	for _, s := range sessions {
		want := s.State == "blocked" || s.Tempo == "blocked" || s.Needs != ""
		if s.Waiting() != want {
			t.Errorf("Waiting() disagrees with raw fields for %+v", s)
		}
		switch {
		case s.State == "blocked" && s.Tempo != "blocked":
			foundForm1 = true
		case s.Tempo == "blocked" && s.Needs != "":
			foundForm2 = true
		case !s.Waiting():
			foundWorking = true
		}
	}

	if !foundForm1 {
		t.Error("fixture should contain the state=blocked waiting form")
	}
	if !foundForm2 {
		t.Error("fixture should contain the tempo=blocked/needs waiting form")
	}
	if !foundWorking {
		t.Error("fixture should contain at least one plainly working session")
	}
}

// TestWaitingForm1 covers a session reporting through its own status that it awaits a
// decision, with the reason in Detail. Tempo may still read "active" here.
func TestWaitingForm1(t *testing.T) {
	s := Session{State: "blocked", Tempo: "active", Needs: "", Detail: "awaiting a decision"}
	if !s.Waiting() {
		t.Error("state=blocked must be waiting even with tempo=active and empty needs")
	}
}

// TestWaitingForm2 covers the daemon detecting a session parked on a rendered question.
func TestWaitingForm2(t *testing.T) {
	s := Session{State: "working", Tempo: "blocked", Needs: "answer: Which colour should the probe use? (Red · Green · Blue)"}
	if !s.Waiting() {
		t.Error("tempo=blocked with a non-empty needs must be waiting")
	}
}

// TestWaitingNegative covers a session that is not waiting by any form.
func TestWaitingNegative(t *testing.T) {
	s := Session{State: "working", Tempo: "active", Needs: ""}
	if s.Waiting() {
		t.Error("state=working, tempo=active, needs=\"\" must not be waiting")
	}
}

func TestMissingSocketErrDaemonUnavailable(t *testing.T) {
	client := New("/nonexistent/socket/path", func() (string, error) {
		return "key", nil
	})

	_, err := client.ListSessions(context.Background())
	if !errors.Is(err, ErrDaemonUnavailable) {
		t.Errorf("expected ErrDaemonUnavailable, got %v", err)
	}
	if err.Error() == ErrDaemonUnavailable.Error() {
		t.Errorf("expected the wrapped error to carry the underlying cause, got %v", err)
	}
}

func TestTruncatedJSONIsError(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
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
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		// Don't send anything; just hold the connection until the test cleans up.
		<-stop
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

func TestListSessionsCachedProtoTimesOut(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	var (
		mu        sync.Mutex
		connCount int
		hungConns []net.Conn
	)
	t.Cleanup(func() {
		mu.Lock()
		defer mu.Unlock()
		for _, c := range hungConns {
			c.Close()
		}
	})

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}

			mu.Lock()
			connCount++
			n := connCount
			mu.Unlock()

			reader := bufio.NewReader(conn)
			_, _ = reader.ReadString('\n')

			if n == 1 {
				// First request (ping) - respond, then close.
				conn.Write([]byte(`{"ok":true,"op":"ping","version":"2.1.263","proto":1}` + "\n"))
				conn.Close()
				continue
			}

			// Second request (list with cached proto) - never respond, just hold
			// the connection open until the test cleans it up.
			mu.Lock()
			hungConns = append(hungConns, conn)
			mu.Unlock()
		}
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})
	// Set a short deadline so test doesn't wait 30 seconds
	client.defaultDeadlineSecs = 1

	// Prime the cache with a ping
	_, _ = client.Ping(context.Background())

	// Now call ListSessions with context.Background() (no deadline).
	// Without the deadline fallback, this would hang forever.
	// With it, it should timeout after defaultDeadlineSecs (1 second).
	_, err = client.ListSessions(context.Background())
	if err == nil {
		t.Error("expected timeout error for ListSessions with cached proto and no context deadline, got nil")
	}
}

func TestRequestEndsWithNewline(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
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

	// Verify we captured the expected number of requests.
	// If framing broke and requests never ended with \n, ReadString would hang forever
	// and requestLines would be empty. Zero captured requests means the framing hung.
	if len(requestLines) != 2 {
		t.Fatalf("expected 2 captured requests (ping + list), got %d; zero means framing hung", len(requestLines))
	}

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
		t.Fatalf("listen: %v", err)
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
		t.Fatalf("listen: %v", err)
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
		t.Fatalf("listen: %v", err)
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
		t.Fatalf("listen: %v", err)
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
		t.Fatalf("listen: %v", err)
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
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	key, err := ControlKey()
	if !errors.Is(err, ErrNoControlKey) {
		t.Errorf("expected ErrNoControlKey, got %v", err)
	}
	if key != "" {
		t.Errorf("expected empty key, got %q", key)
	}
}

func TestNoControlKeyLeakedInError(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
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

// tempSocket creates a temporary unix socket path with a short name.
// macOS limits socket paths to 104 bytes, so we use a short directory name.
func tempSocket(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "fd")
	if err != nil {
		t.Fatalf("creating temp dir: %v", err)
	}
	t.Cleanup(func() {
		os.RemoveAll(dir)
	})
	return filepath.Join(dir, "s.sock")
}

func TestSendTextRequest(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	var capturedReq map[string]interface{}
	errChan := make(chan error, 1)
	go func() {
		serveOnce(t, listener, func(t *testing.T, req []byte) []byte {
			if err := json.Unmarshal(req, &capturedReq); err != nil {
				errChan <- err
				return nil
			}
			return []byte(`{"ok":true,"op":"reply"}` + "\n")
		})
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "test-key-12345678", nil
	})
	client.proto = 1 // Set proto to avoid ping

	err = client.SendText(context.Background(), "session123", "hello world", true)
	if err != nil {
		t.Fatalf("SendText failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)
	select {
	case err := <-errChan:
		t.Fatalf("error in server: %v", err)
	default:
	}

	// Verify request structure
	if short, ok := capturedReq["short"].(string); !ok || short != "session123" {
		t.Errorf("expected short='session123', got %v", capturedReq["short"])
	}
	if text, ok := capturedReq["text"].(string); !ok || text != "hello world" {
		t.Errorf("expected text='hello world', got %v", capturedReq["text"])
	}
	if auth, ok := capturedReq["auth"].(string); !ok || auth != "test-key-12345678" {
		t.Errorf("expected auth='test-key-12345678', got %v", capturedReq["auth"])
	}
}

func TestSendTextKeyFunctionFailure(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	requestReceived := false
	go func() {
		conn, _ := listener.Accept()
		if conn != nil {
			requestReceived = true
			conn.Close()
		}
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "", ErrNoControlKey
	})
	client.proto = 1

	err = client.SendText(context.Background(), "session123", "hello", true)
	if !errors.Is(err, ErrNoControlKey) {
		t.Errorf("expected ErrNoControlKey, got %v", err)
	}

	time.Sleep(100 * time.Millisecond)
	if requestReceived {
		t.Error("server should not have received any request")
	}
}

func TestSendTextSubmitFalse(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	requestReceived := false
	go func() {
		conn, _ := listener.Accept()
		if conn != nil {
			requestReceived = true
			conn.Close()
		}
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})
	client.proto = 1

	err = client.SendText(context.Background(), "session123", "hello", false)
	var submitErr *ErrSubmitNotSupported
	if !errors.As(err, &submitErr) {
		t.Errorf("expected *ErrSubmitNotSupported, got %T: %v", err, err)
	}

	time.Sleep(100 * time.Millisecond)
	if requestReceived {
		t.Error("server should not have received any request when submit=false")
	}
}

func TestSendTextErrorENOJOB(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	go func() {
		serveOnce(t, listener, func(t *testing.T, req []byte) []byte {
			return []byte(`{"ok":false,"code":"ENOJOB","error":"no such session"}` + "\n")
		})
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})
	client.proto = 1

	err = client.SendText(context.Background(), "missing", "text", true)
	var nojobErr *ErrNojob
	if !errors.As(err, &nojobErr) {
		t.Errorf("expected *ErrNojob, got %T: %v", err, err)
	}
}

func TestReadScreenReturnsStreamedBytes(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, _ := listener.Accept()
		if conn == nil {
			return
		}
		defer conn.Close()

		reader := bufio.NewReader(conn)
		_, _ = reader.ReadString('\n') // read the request

		// Send header line
		headerLine := `{"ok":true,"op":"attach","imarkNonce":"nonce","decModes":{},"via":"local","booting":false,"tempo":"idle","state":"working","cached":false,"stale":false,"workerCliVersion":"2.1.263"}` + "\n"
		conn.Write([]byte(headerLine))

		// Send streamed bytes
		conn.Write([]byte("Hello from terminal"))
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})
	client.proto = 1

	output, err := client.ReadScreen(context.Background(), "session123", 0)
	if err != nil {
		t.Fatalf("ReadScreen failed: %v", err)
	}

	<-done
	expected := "Hello from terminal"
	if output != expected {
		t.Errorf("expected output %q, got %q", expected, output)
	}
}

func TestReadScreenTailBytes(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, _ := listener.Accept()
		if conn == nil {
			return
		}
		defer conn.Close()

		reader := bufio.NewReader(conn)
		_, _ = reader.ReadString('\n') // read the request

		// Send header line
		headerLine := `{"ok":true,"op":"attach"}` + "\n"
		conn.Write([]byte(headerLine))

		// Send streamed bytes
		fullOutput := "0123456789ABCDEFGHIJ"
		conn.Write([]byte(fullOutput))
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})
	client.proto = 1

	output, err := client.ReadScreen(context.Background(), "session123", 5)
	if err != nil {
		t.Fatalf("ReadScreen failed: %v", err)
	}

	<-done
	expected := "FGHIJ"
	if output != expected {
		t.Errorf("expected last 5 bytes %q, got %q", expected, output)
	}
}

func TestAttachOmitsAuthWhenKeyFails(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	var capturedReq map[string]interface{}
	errChan := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, _ := listener.Accept()
		if conn == nil {
			return
		}
		defer conn.Close()

		reader := bufio.NewReader(conn)
		line, _ := reader.ReadString('\n')
		if err := json.Unmarshal([]byte(strings.TrimSuffix(line, "\n")), &capturedReq); err != nil {
			errChan <- err
			return
		}

		// Send header
		conn.Write([]byte(`{"ok":true,"op":"attach"}` + "\n"))
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "", ErrNoControlKey
	})
	client.proto = 1

	_, _ = client.ReadScreen(context.Background(), "session123", 0)

	<-done
	select {
	case err := <-errChan:
		t.Fatalf("error in server: %v", err)
	default:
	}

	// Verify auth key is absent from request
	if _, ok := capturedReq["auth"]; ok {
		t.Error("attach request should not have auth field when key function fails")
	}
}

func TestAttachIncludesAuthWhenKeySucceeds(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	var capturedReq map[string]interface{}
	errChan := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, _ := listener.Accept()
		if conn == nil {
			return
		}
		defer conn.Close()

		reader := bufio.NewReader(conn)
		line, _ := reader.ReadString('\n')
		if err := json.Unmarshal([]byte(strings.TrimSuffix(line, "\n")), &capturedReq); err != nil {
			errChan <- err
			return
		}

		conn.Write([]byte(`{"ok":true,"op":"attach"}` + "\n"))
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "my-control-key", nil
	})
	client.proto = 1

	_, _ = client.ReadScreen(context.Background(), "session123", 0)

	<-done
	select {
	case err := <-errChan:
		t.Fatalf("error in server: %v", err)
	default:
	}

	if auth, ok := capturedReq["auth"].(string); !ok || auth != "my-control-key" {
		t.Errorf("expected auth='my-control-key', got %v", capturedReq["auth"])
	}
}

func TestSendKeysWritesBytesToAttach(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	capturedKeys := make([]byte, 0)
	errChan := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, _ := listener.Accept()
		if conn == nil {
			return
		}
		defer conn.Close()

		reader := bufio.NewReader(conn)
		_, _ = reader.ReadString('\n') // read the attach request

		// Send header
		conn.Write([]byte(`{"ok":true,"op":"attach"}` + "\n"))

		// Read the key bytes
		buf := make([]byte, 1024)
		n, err := conn.Read(buf)
		if err != nil && err != io.EOF {
			errChan <- err
			return
		}
		capturedKeys = append(capturedKeys, buf[:n]...)
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})
	client.proto = 1

	err = client.SendKeys(context.Background(), "session123", "hello keys")
	if err != nil {
		t.Fatalf("SendKeys failed: %v", err)
	}

	<-done
	select {
	case err := <-errChan:
		t.Fatalf("error in server: %v", err)
	default:
	}

	expected := []byte("hello keys")
	if !bytes.Equal(capturedKeys, expected) {
		t.Errorf("expected keys %v, got %v", expected, capturedKeys)
	}
}

func TestSendKeysAttachRefusedENOJOB(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	go func() {
		serveOnce(t, listener, func(t *testing.T, req []byte) []byte {
			return []byte(`{"ok":false,"code":"ENOJOB","error":"no such session"}` + "\n")
		})
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})
	client.proto = 1

	err = client.SendKeys(context.Background(), "missing", "x")
	var nojobErr *ErrNojob
	if !errors.As(err, &nojobErr) {
		t.Errorf("expected *ErrNojob, got %T: %v", err, err)
	}
}

func TestSendKeysAttachRefusedEAUTH(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	go func() {
		serveOnce(t, listener, func(t *testing.T, req []byte) []byte {
			return []byte(`{"ok":false,"code":"EAUTH","error":"invalid auth"}` + "\n")
		})
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})
	client.proto = 1

	err = client.SendKeys(context.Background(), "session123", "x")
	var authErr *ErrAuth
	if !errors.As(err, &authErr) {
		t.Errorf("expected *ErrAuth, got %T: %v", err, err)
	}
}

func TestReadScreenAttachRefusedENOJOB(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	go func() {
		serveOnce(t, listener, func(t *testing.T, req []byte) []byte {
			return []byte(`{"ok":false,"code":"ENOJOB","error":"no such session"}` + "\n")
		})
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})
	client.proto = 1

	out, err := client.ReadScreen(context.Background(), "missing", 0)
	var nojobErr *ErrNojob
	if !errors.As(err, &nojobErr) {
		t.Errorf("expected *ErrNojob, got %T: %v", err, err)
	}
	if out != "" {
		t.Errorf("expected empty screen on refused attach, got %q", out)
	}
}

func TestReadScreenAttachRefusedEAUTH(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	go func() {
		serveOnce(t, listener, func(t *testing.T, req []byte) []byte {
			return []byte(`{"ok":false,"code":"EAUTH","error":"invalid auth"}` + "\n")
		})
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})
	client.proto = 1

	out, err := client.ReadScreen(context.Background(), "session123", 0)
	var authErr *ErrAuth
	if !errors.As(err, &authErr) {
		t.Errorf("expected *ErrAuth, got %T: %v", err, err)
	}
	if out != "" {
		t.Errorf("expected empty screen on refused attach, got %q", out)
	}
}

// TestReadScreenContextDeadlineReturnsPartialBuffer covers a session that prints
// continuously (a spinner, say) and so never goes idle. The context deadline is the
// only thing that ends the read, and what was accumulated is a real partial screen,
// not a failure.
func TestReadScreenContextDeadlineReturnsPartialBuffer(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })

	go func() {
		conn, _ := listener.Accept()
		if conn == nil {
			return
		}
		defer conn.Close()

		reader := bufio.NewReader(conn)
		_, _ = reader.ReadString('\n')
		conn.Write([]byte(`{"ok":true,"op":"attach"}` + "\n"))

		for {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := conn.Write([]byte("x")); err != nil {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})
	client.proto = 1

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	output, err := client.ReadScreen(ctx, "session123", 0)
	if err != nil {
		t.Fatalf("expected nil error for a partial screen on deadline, got %v", err)
	}
	if len(output) == 0 {
		t.Fatal("expected a non-empty partial screen, got empty string")
	}
}

// TestConcurrentListSessionsRace exercises the proto cache under concurrent use: a
// *Client is shared between a poller and request handlers, so this must be clean
// under `go test -race`.
func TestConcurrentListSessionsRace(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				reader := bufio.NewReader(conn)
				line, err := reader.ReadString('\n')
				if err != nil {
					return
				}
				var m map[string]interface{}
				_ = json.Unmarshal([]byte(line), &m)
				switch m["op"] {
				case "ping":
					conn.Write([]byte(`{"ok":true,"op":"ping","version":"2.1.263","proto":1}` + "\n"))
				case "list":
					conn.Write([]byte(`{"ok":true,"op":"list","jobs":[]}` + "\n"))
				}
			}(conn)
		}
	}()
	t.Cleanup(func() {
		listener.Close()
		<-done
	})

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})

	const n = 20
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := client.ListSessions(context.Background()); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("ListSessions failed: %v", err)
	}
}

func TestErrorMessageNoControlKey(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	go func() {
		serveOnce(t, listener, func(t *testing.T, req []byte) []byte {
			return []byte(`{"ok":false,"code":"EAUTH","error":"auth failed"}` + "\n")
		})
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "super-secret-key-abc123xyz789", nil
	})
	client.proto = 1

	err = client.SendText(context.Background(), "session123", "text", true)

	// The error message should not contain the control key
	if strings.Contains(err.Error(), "super-secret-key-abc123xyz789") {
		t.Error("error message contains control key value")
	}
}

func TestListSessionsMissingJobsKeyIsError(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	go func() {
		serveOnce(t, listener, func(t *testing.T, req []byte) []byte {
			// Reply with ok=true but no jobs field at all - malformed response
			return []byte(`{"ok":true,"op":"list"}` + "\n")
		})
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})
	client.proto = 1

	sessions, err := client.ListSessions(context.Background())
	if err == nil {
		t.Errorf("expected error for missing jobs field, got nil; sessions=%v", sessions)
	}
	if err != nil && !strings.Contains(err.Error(), "jobs") {
		t.Errorf("expected error to mention 'jobs' field, got: %v", err)
	}
}

func TestListSessionsEmptyJobsArrayIsNotError(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	go func() {
		serveOnce(t, listener, func(t *testing.T, req []byte) []byte {
			// Reply with ok=true and empty jobs array - valid response
			return []byte(`{"ok":true,"op":"list","jobs":[]}` + "\n")
		})
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})
	client.proto = 1

	sessions, err := client.ListSessions(context.Background())
	if err != nil {
		t.Errorf("expected nil error for empty jobs array, got: %v", err)
	}
	if sessions == nil {
		t.Error("expected empty slice for empty jobs array, got nil")
	}
	if len(sessions) != 0 {
		t.Errorf("expected 0 sessions, got %d", len(sessions))
	}
}

func TestReadScreenIdleDetection(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })

	go func() {
		conn, _ := listener.Accept()
		if conn == nil {
			return
		}
		defer conn.Close()

		reader := bufio.NewReader(conn)
		_, _ = reader.ReadString('\n') // read the attach request

		// Send header line
		conn.Write([]byte(`{"ok":true,"op":"attach"}` + "\n"))

		// Send some data
		conn.Write([]byte("Initial output"))

		// Hold the connection open without sending more data. ReadScreen should
		// detect idle and return promptly, not wait for the deadline. Hold until
		// the test cleans up, rather than forever, so the goroutine actually exits.
		<-stop
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})
	client.proto = 1
	client.readIdleTimeout = 200 * time.Millisecond // Short timeout for testing
	client.defaultDeadlineSecs = 2                  // Short deadline so test doesn't hang

	// This should return quickly (within ~500ms) due to idle detection,
	// not wait for the full 2-second deadline
	start := time.Now()
	output, err := client.ReadScreen(context.Background(), "session123", 0)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("ReadScreen failed: %v", err)
	}

	expected := "Initial output"
	if output != expected {
		t.Errorf("expected output %q, got %q", expected, output)
	}

	// Verify it returned promptly (should be < 1s with 200ms idle timeout).
	// Without idle detection, it would wait the full 2 seconds.
	if elapsed > 1*time.Second {
		t.Errorf("ReadScreen took too long (%v), indicates idle detection not working", elapsed)
	}
}
