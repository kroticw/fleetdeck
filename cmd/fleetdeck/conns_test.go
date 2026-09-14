package main

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// A panel told to stop closes the connections that have asked for nothing
// (T-060), and only those: a request the server has already read is answered
// in full before the panel goes.
func TestAPanelToldToStopAnswersInFullARequestItHasAlreadyRead(t *testing.T) {
	quiet := &quietConns{}
	reading := make(chan struct{})
	body := strings.Repeat("a card that takes a moment to render\n", 4096)
	srv := &http.Server{
		ReadHeaderTimeout: readHeaderTimeout,
		ConnState:         quiet.track,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			close(reading)
			time.Sleep(300 * time.Millisecond)
			fmt.Fprint(w, body)
		}),
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()

	type answer struct {
		body string
		err  error
	}
	answered := make(chan answer, 1)
	go func() {
		client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
		resp, err := client.Get("http://" + ln.Addr().String() + "/")
		if err != nil {
			answered <- answer{err: err}
			return
		}
		defer func() { _ = resp.Body.Close() }()
		b, err := io.ReadAll(resp.Body)
		answered <- answer{body: string(b), err: err}
	}()
	select {
	case <-reading:
	case <-time.After(5 * time.Second):
		t.Fatal("the request never reached the handler")
	}

	if err := shutdown(srv, quiet); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	select {
	case a := <-answered:
		if a.err != nil {
			t.Fatalf("the request in work when the panel was told to stop got %v, want its answer", a.err)
		}
		if len(a.body) != len(body) {
			t.Fatalf("the request in work when the panel was told to stop got %d of %d bytes", len(a.body), len(body))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no answer 5 s after the panel was told to stop")
	}
}
