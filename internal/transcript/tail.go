package transcript

import (
	"bytes"
	"errors"
	"fmt"
	"io"
)

// readerAtSeeker is the subset of *os.File that reverseLines needs. It exists
// so tests can hand reverseLinesFrom a fake that returns a deliberately short
// read, to exercise the "read must fill the buffer" assertion below without
// needing a real file that misbehaves.
type readerAtSeeker interface {
	io.ReaderAt
	io.Seeker
}

// defaultStartChunk is the size of the first tail read. It grows by doubling
// until it covers a complete line or the whole file, so a small answer (one
// recent line, or a handful of steps) never forces a full read of a file that
// is tens of megabytes long, per the design's "read from the tail" rule for
// large transcripts.
const defaultStartChunk int64 = 64 * 1024

// reverseLines walks the lines of f from the end of the file to the beginning,
// calling visit once per line, newest first. It stops as soon as visit returns
// true, or when the beginning of the file is reached. Lines handed to visit are
// only valid for the duration of that call.
func reverseLines(f readerAtSeeker, visit func(line []byte) bool) error {
	return reverseLinesFrom(f, defaultStartChunk, visit)
}

// reverseLinesFrom is reverseLines with the first chunk size as an explicit
// parameter, so a test can shrink it to force chunk growth deterministically
// against small fixtures instead of needing multi-megabyte ones — without
// reaching for a shared package variable that a parallel test could race on.
func reverseLinesFrom(f readerAtSeeker, startChunk int64, visit func(line []byte) bool) error {
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
		n, err := f.ReadAt(buf, offset)
		if err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("read transcript tail: %w", err)
		}
		if n != len(buf) {
			// A short read here would silently leave the unread tail of buf
			// zero-padded, which reverseLines would then treat as real bytes
			// of the file and corrupt line boundaries. Surface it instead of
			// trusting the partial read.
			return fmt.Errorf("read transcript tail: got %d of %d bytes at offset %d", n, len(buf), offset)
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
