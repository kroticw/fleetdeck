package main

import (
	"errors"

	"golang.org/x/sys/unix"
)

// ownerGone is closed when the process owner exits: a pidfd becomes readable
// then, and nothing is polled on a schedule. The panel ships for macOS (see
// owner_darwin.go); this is here so the rule is built and tested on the Linux
// CI leg too, with the same meaning.
func ownerGone(owner int) <-chan struct{} {
	gone := make(chan struct{})
	fd, err := unix.PidfdOpen(owner, 0)
	if err != nil {
		close(gone)
		return gone
	}
	go func() {
		defer func() { _ = unix.Close(fd) }()
		fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		for {
			if _, err := unix.Poll(fds, -1); errors.Is(err, unix.EINTR) {
				continue
			}
			close(gone)
			return
		}
	}()
	return gone
}
