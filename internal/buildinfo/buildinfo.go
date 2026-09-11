// Package buildinfo says which build of the panel is running: what it would
// serve a browser, where the binary is, which commit it came from and when it
// was built.
//
// Each field answers its own question, and none of them answers another's:
//
//   - Web is a hash of the embedded web interface. It changes exactly when
//     what a browser would receive changes, and it is the only field the page
//     compares to decide it is out of date. The commit cannot do that job: a
//     commit touching only Go code changes it while the page stays the same,
//     and two builds of one commit with different uncommitted edits share it
//     while their pages differ.
//   - Executable is the path of the running binary. More than one install of
//     fleetdeck can exist on a machine, and "which one is answering on the
//     port" is otherwise a question for a terminal.
//   - Revision, Modified and CommitTime come from the vcs settings `go build`
//     stamps into every binary built inside a checkout. Modified is kept, not
//     hidden: it is what says a revision label does not describe the contents.
//     An untracked file is enough to set it, so it is shown as a note beside
//     the revision and never used as a trigger.
//   - BuiltAt is when the binary file was written. It is not CommitTime: a
//     binary built today from yesterday's commit carries yesterday's commit
//     time, and that divergence is the very thing worth seeing.
package buildinfo

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime/debug"
	"time"
)

// Fingerprint describes the running build. Zero-valued fields mean "not
// known", never "false" or "none": a binary built outside a checkout has no
// revision to report, and says so by leaving it empty.
type Fingerprint struct {
	Web        string    `json:"web"`
	Executable string    `json:"executable,omitempty"`
	Revision   string    `json:"revision,omitempty"`
	Modified   bool      `json:"modified,omitempty"`
	CommitTime time.Time `json:"commitTime,omitzero"`
	BuiltAt    time.Time `json:"builtAt,omitzero"`
}

// WebHash hashes every regular file in fsys by path and content.
//
// fs.WalkDir visits entries in lexical order, so the result depends only on
// what the files are, never on the order anything happened to list them. Each
// file contributes its path, a NUL, its length and then its bytes: without
// the separator and the length, "ab" holding "c" and "a" holding "bc" would
// feed the hash the same bytes.
func WebHash(fsys fs.FS) (string, error) {
	h := sha256.New()
	err := fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		data, err := fs.ReadFile(fsys, path)
		if err != nil {
			return err
		}
		h.Write([]byte(path))
		h.Write([]byte{0})
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(data)))
		h.Write(size[:])
		h.Write(data)
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("hashing the web interface: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Read fingerprints the running binary over the web interface it serves.
func Read(web fs.FS) (Fingerprint, error) {
	exe, err := os.Executable()
	if err != nil {
		exe = ""
	} else if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		// A symlink in PATH points at an install somewhere else, and that
		// somewhere else is what the operator needs to know.
		exe = resolved
	}
	info, _ := debug.ReadBuildInfo()
	return read(web, exe, info)
}

func read(web fs.FS, exe string, info *debug.BuildInfo) (Fingerprint, error) {
	hash, err := WebHash(web)
	if err != nil {
		return Fingerprint{}, err
	}
	f := Fingerprint{Web: hash, Executable: exe}
	if info != nil {
		fromBuildInfo(&f, info)
	}
	if exe != "" {
		if st, err := os.Stat(exe); err == nil {
			f.BuiltAt = st.ModTime()
		}
	}
	return f, nil
}

func fromBuildInfo(f *Fingerprint, info *debug.BuildInfo) {
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			f.Revision = s.Value
		case "vcs.modified":
			f.Modified = s.Value == "true"
		case "vcs.time":
			if at, err := time.Parse(time.RFC3339, s.Value); err == nil {
				f.CommitTime = at
			}
		}
	}
}
