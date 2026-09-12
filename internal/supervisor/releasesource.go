//go:build darwin

package supervisor

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// BundleName is what the app bundle is called, inside the release archive and
// on disk.
const BundleName = "fleetdeck.app"

// Defaults for a ReleaseSource, each one a bound rather than a guess:
//
//   - DefaultMaxArchiveBytes: the v0.3.0 archive is 39.6 MB, both
//     architectures included. A quarter of a gigabyte leaves the app room to
//     grow several times over and still stops a server that would otherwise
//     write until the disk is full.
//   - DefaultNeedBytes: the archive, the bundle unpacked from it, and the
//     bundle being replaced all live side by side for the length of the swap.
const (
	DefaultMaxArchiveBytes = 256 << 20
	DefaultNeedBytes       = 1 << 30
)

// NoRoomError: there is not enough space beside the installed app to download
// and unpack a new one.
type NoRoomError struct {
	Dir        string
	Need, Free int64
}

func (e *NoRoomError) Error() string {
	return fmt.Sprintf("updating needs %d MB free beside %s, and there are %d MB",
		e.Need>>20, e.Dir, e.Free>>20)
}

// ReleaseSource is where an app installed from a release gets a new one: the
// project's releases page, over the network, checked before it is allowed to
// replace anything.
//
// It is the other half of a pair with Tree, which is where an app built from a
// checkout gets a new one. Update drives either without knowing which it has.
type ReleaseSource struct {
	Releases *Releases
	// Seal is the check the downloaded app must pass. Without one nothing is
	// staged: an update that skipped it would be a way to hand a person any
	// program at all.
	Seal *Seal
	// Running is the version of the app doing the updating, as a release tag.
	Running string
	// Ditto is the absolute path of /usr/bin/ditto, which unpacks the archive
	// the same way Finder's Archive Utility does.
	Ditto  string
	Client *http.Client

	// MaxArchiveBytes and NeedBytes default to the constants above.
	MaxArchiveBytes int64
	NeedBytes       int64

	// Progress, when set, is told how the download is going.
	Progress func(Progress)
}

// Check is the tag of a release newer than the running one, or "" when there
// is nothing to update to.
//
// A release older than the running one answers "" as well, and deliberately:
// that is either a version somebody has unpublished or something answering in
// GitHub's place, and quietly installing it would be moving the person
// backwards onto a build they have already left.
func (r *ReleaseSource) Check(ctx context.Context) (string, error) {
	latest, err := r.Releases.Latest(ctx)
	if err != nil {
		return "", err
	}
	newer, err := Newer(r.Running, latest)
	if err != nil {
		return "", err
	}
	if !newer {
		return "", nil
	}
	return latest, nil
}

// Stage downloads the release tagged tag into dir, unpacks it, and checks it.
// It returns the path of a bundle that has passed every check, and on any
// refusal it leaves dir as it found it: a rejected download must not stay
// anywhere a person or a later run could start it from.
func (r *ReleaseSource) Stage(ctx context.Context, dir, tag string) (string, error) {
	if err := r.roomFor(dir); err != nil {
		return "", err
	}
	archive := filepath.Join(dir, ArchiveName(tag))
	unpacked := filepath.Join(dir, "unpacked")
	// Whatever happens below, only a bundle that passed every check survives
	// this function; the archive never does.
	clean := func() {
		_ = os.Remove(archive)
		_ = os.RemoveAll(unpacked)
	}

	r.say("download", tag)
	if err := r.download(ctx, r.Releases.ArchiveURL(tag), archive); err != nil {
		clean()
		return "", err
	}

	if out, err := exec.CommandContext(ctx, r.Ditto, "-x", "-k", archive, unpacked).CombinedOutput(); err != nil {
		clean()
		return "", fmt.Errorf("unpack %s: %w\n%s", ArchiveName(tag), err, out)
	}
	staged := filepath.Join(unpacked, BundleName)
	if _, err := os.Stat(staged); err != nil {
		clean()
		return "", fmt.Errorf("%s holds no %s", ArchiveName(tag), BundleName)
	}

	// The check runs on the bundle that will be started, in the place it will
	// be started from. Checking one copy and starting another leaves a gap
	// between the two, however short.
	r.say("verify", tag)
	if err := r.Seal.Verify(ctx, staged); err != nil {
		clean()
		return "", err
	}

	// Out of the scratch directory and up beside the installed app, so that
	// Swap can exchange the two in one call: they must be on one filesystem,
	// and a rename within dir keeps them there.
	final := filepath.Join(dir, BundleName)
	_ = os.RemoveAll(final)
	if err := os.Rename(staged, final); err != nil {
		clean()
		return "", fmt.Errorf("put the new app beside the old one: %w", err)
	}
	clean()
	return final, nil
}

// roomFor refuses before anything is downloaded rather than after, so that a
// full disk is a sentence a person can read instead of a write failing part
// way through a 40 MB file.
func (r *ReleaseSource) roomFor(dir string) error {
	need := r.NeedBytes
	if need == 0 {
		need = DefaultNeedBytes
	}
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return fmt.Errorf("ask how much room there is beside %s: %w", dir, err)
	}
	free := int64(st.Bavail) * int64(st.Bsize)
	if free < need {
		return &NoRoomError{Dir: dir, Need: need, Free: free}
	}
	return nil
}

func (r *ReleaseSource) download(ctx context.Context, url, into string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	client := r.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return &ReleasesUnreachableError{URL: url, Err: err}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("downloading %s: the server answered %d", url, resp.StatusCode)
	}

	limit := r.MaxArchiveBytes
	if limit == 0 {
		limit = DefaultMaxArchiveBytes
	}
	f, err := os.Create(into)
	if err != nil {
		return err
	}
	// One byte past the bound, so that hitting it exactly is told from running
	// past it.
	written, copyErr := io.Copy(f, io.LimitReader(resp.Body, limit+1))
	closeErr := f.Close()
	switch {
	case copyErr != nil:
		return fmt.Errorf("downloading %s: %w", url, copyErr)
	case closeErr != nil:
		return fmt.Errorf("downloading %s: %w", url, closeErr)
	case written > limit:
		return fmt.Errorf("downloading %s: the archive is too large (over %d MB)", url, limit>>20)
	}
	return nil
}

func (r *ReleaseSource) say(step, detail string) {
	if r.Progress != nil {
		r.Progress(Progress{Step: step, Detail: detail})
	}
}
