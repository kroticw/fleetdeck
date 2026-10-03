//go:build darwin

package main

import (
	"errors"
	"log"

	"golang.org/x/sys/unix"
)

// exitOf is closed when the process pid exits, as the kernel tells it
// (kqueue, NOTE_EXIT): the window started by an update removes the bundle
// swapped out the moment the old window quits, rather than at its next try
// minutes later (supervisor.Takeover.OldWindowQuit).
//
// still says whether pid is still the process meant. It is asked once the
// watch is in place, because a PID is given to another process once its own
// has gone: a process gone before then is told as gone at once. Where the
// kernel cannot be asked, the channel is never closed and the tries every
// so often are what remains.
func exitOf(pid int, still func() bool) <-chan struct{} {
	quit := make(chan struct{})
	kq, err := unix.Kqueue()
	if err != nil {
		log.Printf("fleetdeck-window: cannot watch process %d for its exit: %v", pid, err)
		return quit
	}
	watch := []unix.Kevent_t{{
		Ident:  uint64(pid),
		Filter: unix.EVFILT_PROC,
		Flags:  unix.EV_ADD | unix.EV_ONESHOT,
		Fflags: unix.NOTE_EXIT,
	}}
	if _, err := unix.Kevent(kq, watch, nil, nil); err != nil {
		_ = unix.Close(kq)
		if !errors.Is(err, unix.ESRCH) {
			log.Printf("fleetdeck-window: cannot watch process %d for its exit: %v", pid, err)
			return quit
		}
		close(quit)
		return quit
	}
	if !still() {
		_ = unix.Close(kq)
		close(quit)
		return quit
	}
	go func() {
		defer panics.in("in the goroutine watching the old window quit").guard()
		defer func() { _ = unix.Close(kq) }()
		got := make([]unix.Kevent_t, 1)
		for {
			n, err := unix.Kevent(kq, nil, got, nil)
			if errors.Is(err, unix.EINTR) {
				continue
			}
			if err != nil {
				log.Printf("fleetdeck-window: stopped watching process %d for its exit: %v", pid, err)
				return
			}
			if n > 0 {
				close(quit)
				return
			}
		}
	}()
	return quit
}
