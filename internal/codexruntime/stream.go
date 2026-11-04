package codexruntime

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"time"
)

const streamWriteTimeout = 5 * time.Second

// streamFrame travels both ways after terminal.attach: the runtime sends
// output, repaint resets, lease changes and the end of the process; a view
// sends keystrokes and, while it holds the lease, its geometry.
type streamFrame struct {
	Data     []byte `json:"data,omitempty"`
	Offset   uint64 `json:"offset,omitempty"`
	Reset    bool   `json:"reset,omitempty"`
	Writable *bool  `json:"writable,omitempty"`
	End      string `json:"end,omitempty"`
	Input    []byte `json:"input,omitempty"`
	Cols     int    `json:"cols,omitempty"`
	Rows     int    `json:"rows,omitempty"`
}

// streamer is a handler result that takes over its connection once the
// response is written. Close releases it when the response cannot be sent.
type streamer interface {
	stream(ctx context.Context, conn net.Conn, r *bufio.Reader) error
	Close() error
}

type attachResult struct {
	Offset   uint64 `json:"offset"`
	Writable bool   `json:"writable"`
	Reset    bool   `json:"reset"`
	term     *terminal
	view     *terminalView
}

// attachStream starts a view at offset when that output is still retained,
// and otherwise (or for a fresh view after overflow) at a repaint.
func (t *terminal) attachStream(ctx context.Context, offset *uint64) (*attachResult, error) {
	start, reset := uint64(0), false
	if offset != nil {
		start = *offset
	}
	_, total, lost, _ := t.output(start)
	if start > total {
		return nil, ErrInvalidFrame
	}
	if lost {
		var err error
		if start, err = t.repaint(ctx); err != nil {
			return nil, err
		}
		reset = true
	}
	v := t.attach(start)
	return &attachResult{Offset: start, Writable: v.Writable(), Reset: reset, term: t, view: v}, nil
}

func (a *attachResult) Close() error { return a.view.Close() }

func (a *attachResult) stream(ctx context.Context, conn net.Conn, r *bufio.Reader) error {
	v := a.view
	defer func() { _ = v.Close() }()
	var wmu sync.Mutex
	// A view that stops reading is cut off by the write deadline; the PTY
	// itself never waits for it.
	send := func(f streamFrame) error {
		wmu.Lock()
		defer wmu.Unlock()
		if err := conn.SetWriteDeadline(time.Now().Add(streamWriteTimeout)); err != nil {
			return err
		}
		return writeFrame(conn, f)
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		defer func() { _ = conn.Close() }()
		buf := make([]byte, 32<<10)
		for {
			n, err := v.Read(buf)
			if n > 0 && send(streamFrame{Data: buf[:n], Offset: v.position()}) != nil {
				return
			}
			switch {
			case err == nil:
			case errors.Is(err, ErrReplayLost):
				from, rerr := a.term.repaint(ctx)
				if rerr != nil {
					_ = send(streamFrame{End: "lost"})
					return
				}
				v.seek(from)
				if send(streamFrame{Reset: true, Offset: from}) != nil {
					return
				}
			case errors.Is(err, io.EOF):
				_ = send(streamFrame{End: "exited"})
				return
			default:
				return
			}
		}
	}()
	// shortcut: lease changes are polled every 200 ms; add a lease-change
	// channel to terminal if that latency ever matters.
	go func() {
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		last := a.Writable
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
			}
			if w := v.Writable(); w != last {
				last = w
				if send(streamFrame{Writable: &w}) != nil {
					_ = conn.Close()
					return
				}
			}
		}
	}()
	for {
		var f streamFrame
		if err := readFrame(r, &f); err != nil {
			return nil
		}
		// Without the lease input and resize are dropped, never queued.
		if len(f.Input) > 0 {
			_, _ = v.Write(f.Input)
		}
		if f.Cols != 0 || f.Rows != 0 {
			_ = v.Resize(ctx, f.Cols, f.Rows)
		}
	}
}

func (v *terminalView) position() uint64 {
	v.readMu.Lock()
	defer v.readMu.Unlock()
	return v.offset
}

func (v *terminalView) seek(offset uint64) {
	v.readMu.Lock()
	defer v.readMu.Unlock()
	v.offset = offset
}

// TerminalEvent is one runtime frame: output, a repaint that starts a blank
// screen, a lease change, or the end of the stream ("exited" or "lost").
type TerminalEvent struct {
	Data     []byte
	Reset    bool
	Writable *bool
	End      string
}

// TerminalStream is one attached view. Closing it detaches the view; the
// process and other views are unaffected.
type TerminalStream struct {
	Writable bool
	Reset    bool
	conn     net.Conn
	r        *bufio.Reader
	wmu      sync.Mutex
	offset   uint64
	stop     func() bool
}

// AttachTerminal opens a view at offset, or at the start of retained output
// when offset is nil. Reset reports that the view starts from a repaint.
func (c Client) AttachTerminal(ctx context.Context, offset *uint64) (*TerminalStream, error) {
	var result attachResult
	conn, r, err := c.open(ctx, "terminal.attach", map[string]any{"offset": offset}, &result)
	if err != nil {
		return nil, err
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		_ = conn.Close()
		return nil, err
	}
	// The view lives as long as ctx, so a forgotten Close still releases its lease.
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	return &TerminalStream{Writable: result.Writable, Reset: result.Reset, conn: conn, r: r, offset: result.Offset, stop: stop}, nil
}

// Next waits until deadline for the next frame.
func (s *TerminalStream) Next(deadline time.Time) (TerminalEvent, error) {
	if err := s.conn.SetReadDeadline(deadline); err != nil {
		return TerminalEvent{}, err
	}
	var f streamFrame
	if err := readFrame(s.r, &f); err != nil {
		return TerminalEvent{}, err
	}
	if f.Offset != 0 {
		s.offset = f.Offset
	}
	return TerminalEvent{Data: f.Data, Reset: f.Reset, Writable: f.Writable, End: f.End}, nil
}

// Offset is the output position a reconnecting view can resume from.
func (s *TerminalStream) Offset() uint64 { return s.offset }

func (s *TerminalStream) Input(p []byte) error { return s.send(streamFrame{Input: p}) }

func (s *TerminalStream) Resize(cols, rows int) error {
	if !validGeometry(cols, rows) {
		return errors.New("invalid terminal geometry")
	}
	return s.send(streamFrame{Cols: cols, Rows: rows})
}

func (s *TerminalStream) send(f streamFrame) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	if err := s.conn.SetWriteDeadline(time.Now().Add(streamWriteTimeout)); err != nil {
		return err
	}
	return writeFrame(s.conn, f)
}

func (s *TerminalStream) Close() error {
	s.stop()
	return s.conn.Close()
}
