# Configuration

fleetdeck is configured with a single YAML file. This page lists every key it recognizes, what each one defaults to, and exactly what happens when a value is wrong.

## Configuration keys

| YAML path | Type | Default | What breaks on a bad value |
| --- | --- | --- | --- |
| `board.path` | string | unset (empty) | not validated |
| `docs.paths` | list of strings | unset (empty) | not validated |
| `orchestrator.session` | string | unset (empty) | not validated |
| `notify.enabled.waiting` | boolean | `true` | not validated (must be a boolean) |
| `notify.enabled.failed` | boolean | `true` | not validated (must be a boolean) |
| `notify.enabled.silent` | boolean | `true` | not validated (must be a boolean) |
| `notify.enabled.card_blocked` | boolean | `true` | not validated (must be a boolean) |
| `notify.silence_after` | duration | `30m` | negative: `notify.silence_after must not be negative, got %s`. Bare number: `must be a duration string like "30s", not a bare number (%s)`. Non-scalar value (for example a list): `line %d: must be a duration string like "30s"`. Invalid duration text: `line %d:` followed by Go's own parsing error, for example `time: invalid duration "abc"` |
| `daemon.poll_interval` | duration | `2s` | zero or negative: `daemon.poll_interval must be positive, got %s`. Bare number, non-scalar value, or invalid text: the same three shapes as `notify.silence_after` above |
| `usage.enabled` | boolean | `true` | not validated (must be a boolean) |
| `server.port` | integer | `7777` | outside the range 1 to 65535: `server.port must be between 1 and 65535, got %d` |

Switching one of the `notify.enabled.*` keys on turns a rule on, not a guarantee that its banner is seen — see the README's [Limitations](../../README.md#limitations) section for why.

`board.path` names the board's root directory, not the directory cards live in directly: the panel reads cards from a `cards` subdirectory underneath it (`<board.path>/cards/*.md`), matching the layout `plugin/templates/board/` lays out (`cards/`, `archive/`, `scripts/`, `README.md`). A `board.path` that exists but has no `cards` subdirectory is reported as a distinct, more specific error than an empty board.

`docs.paths` names the directories the panel's documentation section reads. Every markdown file under them is listed, and a document is served only when it resolves to somewhere inside one of them: a symlink inside a documentation directory that points out of it is refused, exactly as a card write outside the board directory is. Nothing but markdown is served, so a directory holding notes and credentials side by side hands out only the notes. Configuring no directory at all, and configuring directories that turn out not to be readable, are both reported as such rather than shown as an empty documentation set — "there is no documentation" and "the directory you named is not there" are different statements, and only one of them is fixed by editing this file.

## Example file

```yaml
board:
  path: /path/to/board
docs:
  paths:
    - /path/to/docs
orchestrator:
  session: some-session-id
notify:
  enabled:
    waiting: true
    failed: true
    silent: true
    card_blocked: true
  silence_after: 30m
daemon:
  poll_interval: 2s
usage:
  enabled: true
server:
  port: 7777
```

## Missing, empty, and malformed files

A configuration file that does not exist is not an error: loading falls back to all the defaults in the table above. A file that exists but decodes to nothing at all — because it is empty, or contains only comments — is treated the same way, as all defaults. A file with genuinely broken YAML syntax is a hard error and loading fails.

## Unknown keys

An unrecognized key is rejected, not ignored. This includes a key that looks close but is not exactly right — for example a flat `board_path` instead of the nested `board: {path: ...}` form. The error names the offending key directly, for example `unknown configuration key "pathx"`, rather than surfacing the underlying YAML library's raw Go type error.

## More than one YAML document

A file containing more than one YAML document (separated by a `---` line partway through the file) is rejected with `parse config %s: file contains more than one YAML document`.

## Writing durations

`notify.silence_after` and `daemon.poll_interval` must be written as a YAML string holding a Go duration, such as `"30s"`, `"2s"`, or `"45m"`. Writing a bare number instead — for example `poll_interval: 2` — is rejected, with an error that names both what was expected ("a duration string") and what was found instead ("a bare number"), rather than a generic type-mismatch message. Writing a non-scalar value, such as a YAML list, is also rejected, and the error names the line where it happened.

## What `silence_after: 0` means, and what a negative value does

`notify.silence_after` is the length of time a session must be silent for before that silence counts as a notification-worthy event. No session can be silent for less than zero time, so a threshold read literally as zero would fire on every session immediately. For that reason, `silence_after: 0` is defined to mean the silence rule is turned off entirely, not "notify instantly." A negative value, by contrast, is rejected outright as an error rather than given any meaning.

## Where the configuration file lives

`fleetdeck` reads its configuration from the path the `--config` flag names, defaulting to `~/.config/fleetdeck/config.yaml` (`defaultConfigPath` in `cmd/fleetdeck/main.go`) when the flag is not given. `fleetdeck init` writes that default path the first time it runs, if nothing is there yet.

## Running an isolated stand

A panel started for testing or acceptance next to a real, working fleet still finds the real daemon: the socket is discovered by the current user's uid under `/tmp`, not by anything `--config` names, so a separate config file, a separate `HOME`, and a separate board do not isolate it. The session list of a stand run this way has shown the real fleet's own sessions and message fragments before, and opening one of them on the stand resizes that session's real terminal for everyone attached to it, including the operator — a stand is not a spectator here by default, it is a second, uninvited hand on the same live sessions.

`--stand-socket` closes this: given a path, the panel connects only to that path and never discovers the real daemon at all — the two code paths share nothing, so there is no way for the real daemon to be found as a fallback. Given but empty (a script's unset variable landing in `--stand-socket=`) is refused at startup rather than read as "not given". Copy this whole block to start a stand that cannot reach a live fleet's daemon, whatever else is running on the machine:

```bash
tmp=$(mktemp -d) && cat > "$tmp/config.yaml" <<'YAML'
server:
  port: 7799
YAML
fleetdeck -config "$tmp/config.yaml" -stand-socket "$tmp/no-daemon-here.sock"
```

The session list on a stand started this way reads empty, or shows a daemon error — never another session's data — because nothing is listening at that path and nothing here will look anywhere else.

The cost of this isolation, so it is not found only by hitting it: a client bound to `--stand-socket` does not notice a daemon behind it restarting under a new socket, unlike ordinary discovery (`cmd/fleetdeck/main.go`'s own `daemonClient`). A one-shot acceptance stand never runs long enough to care; a long-lived test fleet built on this flag would need restarting alongside its daemon.

## Where other files live

The panel's log is `~/Library/Logs/fleetdeck.log`: the fleetdeck app appends both output streams of a panel it starts there. See [`getting-started.md`](getting-started.md#the-app-and-the-panel) for how the app starts and keeps the panel.
