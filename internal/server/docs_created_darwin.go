package server

import (
	"io/fs"
	"syscall"
	"time"
)

// createdAt is when the file was made: macOS keeps a birth time.
func createdAt(info fs.FileInfo) time.Time {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return time.Unix(st.Birthtimespec.Sec, st.Birthtimespec.Nsec)
	}
	return info.ModTime()
}
