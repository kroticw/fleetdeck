package main

import (
	"errors"

	"golang.org/x/sys/unix"
)

// ownerGone is closed when the process owner exits. The kernel says so
// (kqueue, EVFILT_PROC/NOTE_EXIT): nothing is polled. A process already gone
// when the watch is set up -- ESRCH -- is gone, and the channel is closed at
// once; so is it when the watch cannot be set up at all, since a panel that
// cannot tell whether its window lives must not risk outliving it.
func ownerGone(owner int) <-chan struct{} {
	gone := make(chan struct{})
	kq, err := unix.Kqueue()
	if err != nil {
		close(gone)
		return gone
	}
	var change unix.Kevent_t
	unix.SetKevent(&change, owner, unix.EVFILT_PROC, unix.EV_ADD|unix.EV_ONESHOT)
	change.Fflags = unix.NOTE_EXIT
	if _, err := unix.Kevent(kq, []unix.Kevent_t{change}, nil, nil); err != nil {
		_ = unix.Close(kq)
		close(gone)
		return gone
	}
	go func() {
		defer func() { _ = unix.Close(kq) }()
		events := make([]unix.Kevent_t, 1)
		for {
			if _, err := unix.Kevent(kq, nil, events, nil); errors.Is(err, unix.EINTR) {
				continue
			}
			close(gone)
			return
		}
	}()
	return gone
}
