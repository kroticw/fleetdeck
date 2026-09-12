# Configuration

fleetdeck is configured with a single YAML file. This page lists every key it recognizes, what each one defaults to, and exactly what happens when a value is wrong.

## Configuration keys

<!-- fleetdeck:config-keys: this table is checked against internal/config by TestConfigurationPageListsEveryKey. A key declared there must have a row here, in both languages, with all four columns filled. -->

| YAML path | Type | Default | What breaks on a bad value |
| --- | --- | --- | --- |
| `board.path` | string | unset (empty) | not validated |
| `docs.paths` | list of strings | unset (empty) | not validated |
| `orchestrator.session` | string | unset (empty) | not validated |
| `session_labels` | map of session UUID to string | unset (empty) | not validated; a key that is not a session's UUID is kept and matches nothing |
| `notify.enabled.waiting` | boolean | `true` | not validated (must be a boolean) |
| `notify.enabled.failed` | boolean | `true` | not validated (must be a boolean) |
| `notify.enabled.silent` | boolean | `true` | not validated (must be a boolean) |
| `notify.enabled.card_blocked` | boolean | `true` | not validated (must be a boolean) |
| `notify.silence_after` | duration | `30m` | negative: `notify.silence_after must not be negative, got %s`. Bare number: `must be a duration string like "30s", not a bare number (%s)`. Non-scalar value (for example a list): `line %d: must be a duration string like "30s"`. Invalid duration text: `line %d:` followed by Go's own parsing error, for example `time: invalid duration "abc"` |
| `daemon.poll_interval` | duration | `2s` | zero or negative: `daemon.poll_interval must be positive, got %s`. Bare number, non-scalar value, or invalid text: the same three shapes as `notify.silence_after` above |
| `usage.enabled` | boolean | `true` | not validated (must be a boolean) |
| `server.port` | integer | `7777` | outside the range 1 to 65535: `server.port must be between 1 and 65535, got %d` |
| `statusline.wrap` | string (shell command) | unset (no wrapping) | not validated before it runs; a command that cannot be run, or that exits non-zero, is taken as no wrapping at all and `fleetdeck-status` prints its own line |
| `statusline.rate_limits_path` | string (file path) | unset (nothing is written) | not validated; a path that cannot be written is reported nowhere, and the panel reads the account's limits over the network instead |
| `name` | string | the folder above `board.path`, `main` when that says nothing | empty, spaces around it or a control character: `the top-level fleet: name ...`; the name of another fleet: see [Several fleets](#several-fleets) |
| `fleets` | list of fleets | unset (empty) | see [Several fleets](#several-fleets) |
| `fleets[].name` | string | none: required | empty, spaces around it or a control character: `fleets[N]: name ...`; the name of another fleet: `fleets[1]: name "clining" is already the name of fleets[0]` |
| `fleets[].board.path` | string | none: required | missing: `fleets[N]: board path must be set`; another fleet's board: `fleets[1]: board "..." is already the board of the top-level fleet ("main")` |
| `fleets[].docs.paths` | list of strings | unset (empty) | not validated, as `docs.paths` |
| `fleets[].orchestrator.session` | string | unset (empty) | another fleet's orchestrator: `fleets[1]: orchestrator "06a1f607" is already the orchestrator of the top-level fleet ("main")` |

`fleets[]` stands for any entry of the `fleets` list: the entries are alike, so the page describes their keys once rather than per fleet.

Switching one of the `notify.enabled.*` keys on turns a rule on, not a guarantee that its banner is seen — see the README's [Limitations](../../README.md#limitations) section for why.

`board.path` names the board's root directory, not the directory cards live in directly: the panel reads cards from a `cards` subdirectory underneath it (`<board.path>/cards/*.md`), matching the layout `plugin/templates/board/` lays out (`cards/`, `archive/`, `scripts/`, `README.md`). A `board.path` that exists but has no `cards` subdirectory is reported as the wrong directory. An empty `cards` subdirectory is an empty board, not an error: a new board starts that way.

`docs.paths` names the directories the panel's documentation section reads. Every markdown file under them is listed, and a document is served only when it resolves to somewhere inside one of them: a symlink inside a documentation directory that points out of it is refused, exactly as a card write outside the board directory is. Nothing but markdown is served, so a directory holding notes and credentials side by side hands out only the notes. Configuring no directory at all, and configuring directories that turn out not to be readable, are both reported as such rather than shown as an empty documentation set — "there is no documentation" and "the directory you named is not there" are different statements, and only one of them is fixed by editing this file.

`session_labels` is the operator's own name for a session, keyed by the session's transcript UUID rather than by its short id: the daemon may hand a short id to another session later, and a label that survives that is the whole reason to key on the UUID instead. The panel writes these entries — renaming a session in the orchestrator column adds, changes or removes exactly one of them — and an empty name removes the entry rather than recording a blank one, so a configuration that only ever gained entries cannot fill up with labels for sessions nobody remembers. A session the daemon already reports a name for needs no entry here; this is for the sessions that started without one and cannot be renamed after the fact.

`statusline.wrap` and `statusline.rate_limits_path` are read by `fleetdeck-status`, the statusline reporter Claude Code runs for every session, and only when its own `-wrap` and `-rate-limits-path` flags were not given. `wrap` names a statusline command of your own: `fleetdeck-status` passes its stdin through to it and prints what came back unchanged, so a status line you already like keeps working with the panel behind it. `rate_limits_path` is the file the rate-limit windows on that stdin are written to, and that file is the panel's first source for the account's limits, ahead of the network. Both are unset by default, and unset means off: no wrapping, and nothing written. There is deliberately no fallback path for `rate_limits_path` — an implicit one that a hand-run invocation touched without meaning to caused two incidents in one evening.

## Several fleets

One panel can keep several fleets — each with its own board, documentation and orchestrator — and a browser tab shows one of them at a time, the way an IDE shows one project.

The top-level `board`, `docs` and `orchestrator` keys are the first fleet, exactly as in a configuration written before there were fleets, and `name` names it. Every further fleet is an entry of `fleets` with the same keys, listed in the table above as `fleets[].*`; its `board.path` is required:

```yaml
board:
  path: /Users/me/fleetdeck/board
docs:
  paths:
    - /Users/me/fleetdeck/docs
orchestrator:
  session: 06a1f607
name: main
fleets:
  - name: clining
    board:
      path: /Users/me/clining-fleet/board
    docs:
      paths:
        - /Users/me/clining-fleet/docs
    orchestrator:
      session: 1a2b3c4d
```

A configuration the panel could not tell its fleets apart in is refused, and the error names where in the file the mistake is — `the top-level fleet` or `fleets[N]`:

- two fleets with one name: `fleets[1]: name "clining" is already the name of fleets[0]`;
- one session as the orchestrator of two fleets: it would read two boards;
- two fleets on one board: they would claim the same sessions;
- an entry of `fleets` without `board.path`.

What the fleets share: the one panel on `server.port`, the Claude Code sessions themselves, the account limits, the notification rules, the session labels and the statusline. There is one panel, not one per fleet, because every session's statusline reports to `127.0.0.1:7777` and nowhere else: a panel on another port would never hear from any session.

Which sessions are a fleet's is not stored anywhere: a session is in a fleet when it is that fleet's orchestrator or when a card on that fleet's board names it in its `session` field. A tab shows its fleet's sessions, then the sessions no fleet claims — shown in every fleet, so a waiting question is never left where no tab looks — and each other fleet as one line with how many of its sessions wait for an answer. The header's switcher carries the same count for every fleet, and a banner names the fleet it comes from.

The fleet a tab shows is in its address, `?fleet=NAME`; no parameter is the first fleet. Switching to another fleet reloads the page: the terminals of the fleet left are closed and nothing is sent to its sessions.

`fleetdeck init --fleet NAME --workspace DIR` adds a fleet — see [Getting started](getting-started.md#adding-a-second-fleet). A running panel shows it after a restart.

A fleetdeck from before this key existed refuses a configuration that uses `name` or `fleets` with `unknown configuration key "fleets"`: update the app before adding a second fleet, and do not go back to an older one while the configuration has one.

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

`fleetdeck` reads its configuration from the path the `--config` flag names, defaulting to `~/.config/fleetdeck/config.yaml` (`defaultConfigPath` in `cmd/fleetdeck/main.go`) when the flag is not given. `fleetdeck init` writes that default path the first time it runs, if nothing is there yet. A panel that finds no file at its path serves the setup page instead of the board and writes the file there once a workspace is chosen — see [`getting-started.md`](getting-started.md#first-launch-choosing-the-workspace). A file that exists and does not parse is an error, never a reason to offer setup.

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

A stand of the first launch itself has no configuration file, so there is no `server.port` to keep it off the operator's port: `--port` gives it one. It also needs a home of its own, because setup writes the configuration, Claude Code's `settings.json` and the board's git history under the home directory:

```bash
tmp=$(mktemp -d) && HOME="$tmp/home" fleetdeck -port 7799 -stand-socket "$tmp/no-daemon-here.sock"
```

Open `http://127.0.0.1:7799/` and the setup page proposes `$tmp/home/fleetdeck`. Everything setup writes lands under `$tmp/home`.

The orchestrator wizard can start a session, and a socket does not stop that: it starts one with `claude --bg`, and a real claude reaches the real daemon whatever socket the panel reads. So a stand starts sessions only with the claude it is given in `--stand-claude`, and without one it starts none — the wizard's new-session button is off and says why. The panel never looks a claude up on a stand, the same way it never discovers the daemon there. `--stand-claude` without `--stand-socket`, or given empty, is refused at startup. A stand claude is anything that prints what `claude --bg` prints — a line `backgrounded · <short id> · <name>` — and makes the stand's daemon list that session.

The cost of this isolation, so it is not found only by hitting it: a client bound to `--stand-socket` does not notice a daemon behind it restarting under a new socket, unlike ordinary discovery (`cmd/fleetdeck/main.go`'s own `daemonClient`). A one-shot acceptance stand never runs long enough to care; a long-lived test fleet built on this flag would need restarting alongside its daemon.

## Where other files live

The panel's log is `~/Library/Logs/fleetdeck.log`: the fleetdeck app appends both output streams of a panel it starts there. See [`getting-started.md`](getting-started.md#the-app-and-the-panel) for how the app starts and keeps the panel.
