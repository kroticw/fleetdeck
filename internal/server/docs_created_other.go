//go:build !darwin

package server

import (
	"io/fs"
	"time"
)

// createdAt falls back to the last change where no birth time is kept.
func createdAt(info fs.FileInfo) time.Time {
	return info.ModTime()
}
