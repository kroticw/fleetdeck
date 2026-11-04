# Codex integration measurements

Status: stage 0 in progress. These observations do not establish full Codex support. Product integration remains disabled until the design's control and terminal gates pass.

## Environment and scope

Measured on macOS with `codex-cli 0.166.0` on 2026-11-04. Experiments used an explicitly created disposable Git repository, read-only sandbox and `on-request` approvals. No existing task was submitted, stopped or resumed. No credential file was read or copied. The normal CLI used the user's existing configuration; a disposable CWD does not isolate global plugins, hooks or MCP configuration. No hook-trust bypass was used.

The first normal CLI launch automatically started its managed local daemon. The probe itself never starts, restarts, configures or stops that daemon. A second ordinary client and a separate API connection used the same server.

## Verified control observations

- `codex app-server proxy` did not respond to newline-delimited JSON initialization within the bounded probe deadline. This does not prove the command is unusable; it proves that treating this path as a JSONL transport was not a valid test.
- Connecting to the local control Unix socket using HTTP WebSocket Upgrade returned `101 Switching Protocols`. A masked WebSocket `initialize` request returned `userAgent`, `codexHome`, `platformFamily` and `platformOs`. The probe deliberately does not print these values.
- After `initialized`, `thread/list` accepted `cwd`, `limit`, `modelProviders` and `useStateDbOnly`. A DB-only listing can be empty while a newly created thread is already readable by its ID. An empty list is not proof of the absence of a live thread.
- `thread/start` returned a native ID and an idle thread without submitting a task. Reading that ID through another connection succeeded.
- Ordinary `codex resume <id>` initially failed with `no rollout found` for that newly created, unused thread. Therefore API creation alone is not sufficient evidence that the TUI can resume it.
- A short tool-free test turn was accepted by `turn/start`, returned a native turn ID with `inProgress`, completed, and produced exactly the requested marker. After this persisted turn, ordinary `codex resume <id>` opened the history successfully without `--remote`.
- With the resumed TUI still attached, another API `turn/start` appeared in that same terminal, streamed its answer and completed. This establishes a working API/TUI connection for a persisted thread, not approval routing or exclusive ownership.
- Closing an ordinary TUI printed that running work continues and provided a resume command. TUI exit must therefore not be implemented as agent stop.

The empty-thread bootstrap remains a design question. A harmless initialization turn is permitted by the design's setup-before-task rule, but it costs an inference and does not replace verification of other registration paths. No card task may be sent before its link is saved.

## Read-only control probe

Run against an existing socket and an explicitly selected test repository:

```sh
go run ./scripts/codexprobe --socket /absolute/path/to/control.sock --cwd /absolute/path/to/test-repository
```

The socket can be the managed daemon's control socket. It may legitimately be a symlink created by Codex; Fleetdeck's future private runtime socket has a separate ownership policy.

The tool only initializes a connection and lists thread metadata. Output contains matching thread IDs and runtime status, never previews, instructions or RPC error bodies. Both original and canonical CWD spellings are queried because the server may retain a symlink spelling. Results are checked against the selected directory again locally. Requests and pagination are bounded. Missing sockets fail without starting a server.

The generated local JSON schemas, obtained with `codex app-server generate-json-schema`, establish the accepted parameter shapes for this CLI version. They are not copied into the repository wholesale. Thread creation, message submission and TUI attachment above were separate, manually controlled disposable-session experiments; the read-only probe does not reproduce them yet.

## Remaining stage-0 gates

- Two simultaneous launches in the same CWD with deterministic registration before the card task.
- Approval and user-input routing with API and TUI connected, including either client disconnecting.
- Untrusted hooks and effective global configuration, if registration requires hooks.
- Send-confirmation loss, interruption, verified graceful stop and resume without duplicate work.
- Dedicated PTY owner surviving HTTP-panel exit; slow-reader handling and bounded replay.
- Correct screen reconstruction after resize, prolonged disconnect and replay overflow in inline and fullscreen modes.
- Child-agent events, stale observations and recovery after runtime failure.

A successful marker response does not waive any of these gates.

## Baseline failures before implementation

On the unchanged product tree, `make test` failed in `TestStalledNeedsVocabularyMatchesInstalledCLI`: installed Claude Code `2.1.316` contains new renderings and no longer contains one recorded rendering. `make lint` reported an existing `errcheck` failure at `internal/supervisor/keeper.go:531`. These files were not changed by the probe. `make test-web` ran all 1401 declared tests successfully.

The probe's own tests verify Unix WebSocket framing, initialization, CWD filtering, withholding foreign previews, missing-socket failure and CWD symlinks. They use a local test server and do not contact a model.

## References

- [Implementation design](../superpowers/specs/2026-11-04-codex-executor-design.md)
- [Codex App Server](https://learn.chatgpt.com/docs/app-server)
- [Codex CLI reference](https://learn.chatgpt.com/docs/cli/reference)
