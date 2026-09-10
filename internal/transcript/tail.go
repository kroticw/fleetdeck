package transcript

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
)

// startChunk is the size of the first tail read. It grows by doubling until it
// covers a complete line or the whole file, so a small answer (one recent line,
// or a handful of steps) never forces a full read of a file that is tens of
// megabytes long, per the design's "read from the tail" rule for large transcripts.
// It is a var, not a const, so tests can shrink it to force chunk growth
// deterministically against small fixtures instead of needing multi-megabyte ones.
var startChunk int64 = 64 * 1024

// reverseLines walks the lines of f from the end of the file to the beginning,
// calling visit once per line, newest first. It stops as soon as visit returns
// true, or when the beginning of the file is reached. Lines handed to visit are
// only valid for the duration of that call.
func reverseLines(f *os.File, visit func(line []byte) bool) error {
	size, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return fmt.Errorf("seek transcript: %w", err)
	}
	if size == 0 {
		return nil
	}

	chunkSize := startChunk
	visited := 0
	for {
		offset := size - chunkSize
		if offset < 0 {
			offset = 0
		}
		buf := make([]byte, size-offset)
		if _, err := f.ReadAt(buf, offset); err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("read transcript tail: %w", err)
		}

		lines := bytes.Split(buf, []byte("\n"))
		if n := len(lines); n > 0 && len(lines[n-1]) == 0 {
			// Trailing newline: no partial line follows it.
			lines = lines[:n-1]
		}
		if offset > 0 && len(lines) > 0 {
			// The first line of a non-initial chunk may be cut mid-line; it is
			// only trustworthy once a bigger read makes it whole.
			lines = lines[1:]
		}

		// Lines is anchored at the newest end regardless of how much history a
		// bigger chunk prepends, so counting ranks from the end stays correct
		// across iterations even though the slice itself is rebuilt each time.
		total := len(lines)
		stop := false
		for rank := visited; rank < total; rank++ {
			idx := total - 1 - rank
			if visit(lines[idx]) {
				stop = true
				break
			}
		}
		visited = total

		if stop || offset == 0 {
			return nil
		}
		chunkSize *= 2
	}
}
