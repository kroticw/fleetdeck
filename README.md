# fleetdeck

Control panel for a fleet of Claude Code background sessions: live sessions, kanban board, notes and docs in one local window.

fleetdeck talks to the local Claude Code daemon over its Unix control socket to list sessions, read their screens, and send text or keystrokes into them, and it serves that view (plus a kanban board and docs) as a local web UI.

## Building and testing

Requires Go 1.27 (see `go.mod`) and, for linting, `golangci-lint` v2.13.2 on `PATH`.

```sh
make build   # go build ./... (or every binary under cmd/*, once one exists)
make test    # go test ./... -race -count=1
make lint    # go vet, gofmt -l, golangci-lint run
```

`make verify-ldflags` builds and runs the one test that checks the release build's version-injection symbol path (`internal/version`) actually still resolves; it is not part of `make test` because it requires its own `-ldflags`, and CI runs it as a separate step.

## Configuration

The config file is YAML, loaded from a path the binary is given explicitly (there is no implicit default location yet). A missing file is not an error — it just means every setting below falls back to its default. An unrecognised key, a malformed value, or more than one YAML document in the file is an error load fails loudly on, never something silently ignored.

```yaml
board:
  path: /path/to/board          # default: unset
docs:
  paths: [/path/to/docs]        # default: unset
orchestrator:
  session: some-session-id      # default: unset
notify:
  enabled:
    waiting: true                # default: true
    failed: true                 # default: true
    silent: true                 # default: true
    card_blocked: true           # default: true
  silence_after: 30m             # default: 30m
daemon:
  poll_interval: 2s              # default: 2s
usage:
  enabled: true                  # default: true
server:
  port: 7777                     # default: 7777
```

Durations (`poll_interval`, `silence_after`) must be a duration string such as `"30s"` or `"2s"` — a bare number is rejected with a message that says so, rather than a raw Go/YAML type-mismatch error. `silence_after: 0` means notifications are never silenced — every occurrence is reported, with no cooldown window at all — while a negative value is rejected outright as ambiguous. See `internal/config` for the full implementation and its tests.

## Packages

- `internal/daemon` — a client for the daemon's Unix control socket: discovery, ownership/security checks on the socket and the control key file, and the `ping`, `list`, `reply`, and `attach` (screen read / key send) operations. The full wire protocol it implements is documented in [`docs/protocol/daemon-control-socket.md`](docs/protocol/daemon-control-socket.md) — read that first before changing anything in this package.
- `internal/config` — loads and saves the YAML config file described above.
- `internal/version` — the build version, injected at link time by `make build`/`make verify-ldflags`; see that package's own tests for how the injection is verified.

Both `internal/daemon` and `internal/config` are standard-library-only (`internal/config` also uses `gopkg.in/yaml.v3`) and know nothing about the web server built on top of them.
