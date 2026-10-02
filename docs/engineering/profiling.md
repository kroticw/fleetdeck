# Profiling the panel

The panel's cost shows only under a live fleet: what one poll cycle costs depends on how many sessions run, how many project directories the machine has collected under `~/.claude/projects`, and how big the board is. A benchmark reproduces none of that, so a running panel can serve Go's own profiles.

Like the rest of `docs/engineering/`, this page is English only.

## Turning it on

```bash
FLEETDECK_PPROF=127.0.0.1:6060 fleetdeck
go tool pprof http://127.0.0.1:6060/debug/pprof/profile?seconds=30
```

- **Unset, nothing happens.** No listener is opened and no handler is registered. That is every ordinary start, the app's included.
- **Loopback only.** The profile handlers answer anyone, with no authentication, and one request can keep the panel profiling for as long as it asks. An address that is not a loopback IP literal is refused, and so is a host name: what a name resolves to can change between two starts.
- **A bad address stops the start.** The panel refuses to start rather than run with a setting that silently did nothing.

## What the poll cycle keeps between cycles

These are the hot spots the profiles pointed at, and the benchmarks beside them measure each one with and without its cache:

- **A session's transcript path** (`cmd/fleetdeck/collect.go`, `transcriptPath`). `transcript.Locate` reads every project directory on the machine. Its answer for a running session does not change, so it is kept for a minute and checked with one `stat`. It is not kept for longer because a project directory renamed under a running panel leaves its old copy readable, and only a fresh lookup picks the newer one. Benchmark: `internal/transcript`, `BenchmarkLocate` and `BenchmarkLocateCacheHit`.
- **Parsed cards** (`internal/board/cache.go`). A card is parsed again only when its file's size or modification time changes. Benchmark: `internal/board`, `BenchmarkScan` and `BenchmarkScanCached`.
- **The snapshot sent over the socket** (`internal/server/ws.go`). A snapshot identical to the last one sent on a connection is not sent again, so an idle page does not rebuild itself every tick.

```bash
go test -run '^$' -bench . ./internal/board/ ./internal/transcript/
```
