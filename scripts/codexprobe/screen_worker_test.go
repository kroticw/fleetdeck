package main

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

type screenWorker struct {
	encoder *json.Encoder
	decoder *json.Decoder
	id      int
	reply   func([]byte)
}

func newScreenWorker(ctx context.Context, t *testing.T, reply func([]byte)) *screenWorker {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for screen verification")
	}
	script, err := filepath.Abs("screen.mjs")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, node, script, "--stream")
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = in.Close(); _ = cmd.Wait() })
	return &screenWorker{encoder: json.NewEncoder(in), decoder: json.NewDecoder(out), reply: reply}
}
func (w *screenWorker) call(t *testing.T, chunk screenChunk, snapshot bool) *screenView {
	t.Helper()
	w.id++
	if err := w.encoder.Encode(struct {
		ID       int         `json:"id"`
		Chunk    screenChunk `json:"chunk"`
		Snapshot bool        `json:"snapshot"`
	}{w.id, chunk, snapshot}); err != nil {
		t.Fatal(err)
	}
	for {
		var response struct {
			ID    int         `json:"id"`
			Reply []byte      `json:"reply"`
			View  *screenView `json:"view"`
		}
		if err := w.decoder.Decode(&response); err != nil {
			t.Fatal(err)
		}
		if len(response.Reply) > 0 && w.reply != nil {
			w.reply(response.Reply)
		}
		if response.ID == w.id {
			return response.View
		}
	}
}
func TestTerminalScreenAnswersCursorQuery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var replies []byte
	worker := newScreenWorker(ctx, t, func(data []byte) { replies = append(replies, data...) })
	view := worker.call(t, screenChunk{Data: []byte("hello\x1b[6n")}, true)
	if string(replies) != "\x1b[1;6R" || view == nil || view.CursorX != 5 {
		t.Fatalf("cursor reply=%q view=%+v", replies, view)
	}
}
