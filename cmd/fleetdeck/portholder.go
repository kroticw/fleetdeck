package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/kroticw/fleetdeck/internal/buildinfo"
)

// holderTimeout bounds the one question a panel that could not take its port
// asks of whatever holds it. The holder is on this machine's loopback, where a
// panel answers in milliseconds. Something that takes the connection and
// never answers must not turn "the port is taken" into a panel that hangs
// asking who took it.
const holderTimeout = 1 * time.Second

// holderBodyLimit caps how much of the holder's answer is read: a snapshot of
// a busy fleet is far smaller, and a holder that is not a panel may send
// anything.
const holderBodyLimit = 8 << 20

// portHolder says what holds addr, for the error of a panel that could not
// take it. Since the window started the panel from inside its app bundle, two
// panels from two places can be after the same port -- one started by the
// window, one by a script or a terminal -- and a bare "address already in use"
// leaves whoever reads it to guess which one won.
func portHolder(ctx context.Context, addr string) string {
	ctx, cancel := context.WithTimeout(ctx, holderTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/api/snapshot", nil)
	if err != nil {
		return fmt.Sprintf("something unknown (%v)", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fmt.Sprintf("something that took the connection and did not answer within %s", holderTimeout)
		}
		return fmt.Sprintf("nothing that answers HTTP (%v)", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var snap struct {
		Build *buildinfo.Fingerprint `json:"build"`
	}
	if resp.StatusCode != http.StatusOK ||
		json.NewDecoder(io.LimitReader(resp.Body, holderBodyLimit)).Decode(&snap) != nil ||
		snap.Build == nil {
		return fmt.Sprintf("something that is not a fleetdeck panel (GET /api/snapshot answered %s)", resp.Status)
	}
	return fmt.Sprintf("fleetdeck %s running from %s", revisionOf(snap.Build), snap.Build.Executable)
}

// bindError is what a panel that could not take its port stops with.
func bindError(addr string, err error, holder string, own *buildinfo.Fingerprint) error {
	this := "revision unknown"
	if own != nil {
		this = fmt.Sprintf("fleetdeck %s from %s", revisionOf(own), own.Executable)
	}
	return fmt.Errorf("bind %s: %w; the port is held by %s, and this build (%s) did not start", addr, err, holder, this)
}

// revisionOf is a build's commit the way the panel's header shows it: short,
// with an asterisk for a tree that had uncommitted changes.
func revisionOf(b *buildinfo.Fingerprint) string {
	rev := b.Revision
	if rev == "" {
		return "(revision unknown)"
	}
	if len(rev) > 7 {
		rev = rev[:7]
	}
	if b.Modified {
		rev += "*"
	}
	return rev
}
