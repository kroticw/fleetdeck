# A dev app beside the installed app

A change to the window is looked at in the real window, with the real fleet, before it is released: a dev app built from the working tree and opened beside the installed fleetdeck. The installed app, its panel and the dev app run at the same time, and the dev app reaches nothing of the installed app's but the fleet daemon and the board, which are the point of looking at it.

## Building and opening it

```bash
make dev-app                    # build from this tree and open it on 127.0.0.1:7778
make dev-app DEV_PORT=7779      # on another port: one port per tree that runs a dev app
make dev-app DEV_OPEN=0         # build only, and print the command that opens it
```

- **What is built.** `bin/fleetdeck-dev.app`, under the identifier `dev.fleetdeck.dev` (`supervisor.DevBundleID`) and the name `fleetdeck dev`, with the window and the panel from this tree. `VERSION` stays `dev`, so the panel's header says `dev` and the short commit, with `*` for a tree with changes. The window's title is `fleetdeck dev`.
- **Who opens it.** Opening a window happens on the screen of whoever is at the machine. A session builds with `DEV_OPEN=0` and hands the printed `open` command to the person watching the screen; it does not open the window itself.
- **The SDK.** The first line of the output names the macOS SDK the build links against (see "The SDK" below).

## Closing it

Quit it: ⌘Q, or Quit from its Dock menu. Its panel was started with `--owner-pid` and goes by itself once the window's process is gone. The red button only hides the window, and the panel stays. To check that nothing of it is left:

```bash
lsof -nP -iTCP:7778 -sTCP:LISTEN     # nothing listens on the dev port
ps -axo pid,command | grep '[f]leetdeck-dev.app'
```

## Kept apart from the installed app

| What | The installed app | The dev app |
| --- | --- | --- |
| Identifier | `dev.fleetdeck.window` | `dev.fleetdeck.dev` |
| Panel's port | `server.port` of the config, 7777 by default | `DEV_PORT`, 7778 by default |
| Config the panel reads and writes | `~/.config/fleetdeck/config.yaml` | `~/.config/fleetdeck/dev/config.yaml`, a copy |
| Banners | sent | off in the copy |
| Panel's log | `~/Library/Logs/fleetdeck.log` | `~/Library/Logs/fleetdeck-dev.log`, one for every dev app, appended to |
| Window's log | not kept | `~/Library/Logs/fleetdeck-dev-window.log` |
| Panel widths | the app's own defaults | the suite `dev.fleetdeck.dev.widths` |
| Updates | looks for a newer version | none |

- **The port reaches the panel.** The window starts its panel with `--port`, the port of the URL it looks at, and the keeper starts every later panel with the same arguments, the restart after an update's swap included. Before this, a window on another URL started a panel on `server.port`, which is the installed panel's.
- **Refused before anything starts.** The dev app does not start on the installed panel's port (`server.port` of the operator's config, or 7777 when there is none), without an operator's config to copy, or with `--handover` or `--canonical`, which only an update passes. Why is the last line of the window's log.
- **It stops no process but its own panel.** Before it signals anything on its port, its keeper asks the kernel which binary listens there (`lsof -d txt`) and stops it only when that binary is the panel inside this dev app's bundle. Anything else is left running and used as it is, and the window's log says `port N is held by <path>, not by this dev app`. That covers the installed panel, and the panel of a dev app built in another tree, which has the same identifier and, by default, the same port. An orphan of this dev app's own panel, left by a window that crashed, is replaced.
- **No updates, no update's side effects.** It looks for no newer version, so it never writes `~/.config/fleetdeck/last-update-check`, which the installed app reads. Its Update button is never shown; asked anyway, it says a dev app does not update. It removes no bundle an earlier update left behind, and never takes a panel over, so it does not have LaunchServices register or forget `/Applications/fleetdeck.app`. Its update lock would be its own bundle's; it never takes one.
- **LaunchServices.** macOS registers the dev bundle when it is opened, under `dev.fleetdeck.dev`. What `dev.fleetdeck.window` opens — the Dock, Spotlight, `open -b` — stays `/Applications/fleetdeck.app`. To check it before, while and after a dev app runs, without sending anything to an app:

  ```bash
  osascript -l JavaScript -e 'ObjC.import("AppKit"); function run(a){var u=$.NSWorkspace.sharedWorkspace.URLForApplicationWithBundleIdentifier(a[0]); return u.isNil()?"":u.path.js}' dev.fleetdeck.window
  ```

## The config copy

The dev app's window writes the copy at every start, from the operator's config as it is then, over whatever an earlier dev app left there, with `notify.enabled.*` off and `server.port` set to the dev app's port, so a dev panel started without `--port` would still keep off the installed panel's. What the dev panel wrote to it is gone at the next start. What a dev panel may write, each only when asked from its page:

- `orchestrator.session`, pinning or unpinning the orchestrator;
- `session_labels`, a session's name;
- `fleets`, a fleet added;
- a fleet's orchestrator, appointed from the wizard.

Why a copy: a panel reads its config once, at its start, and changes it by reading the file, changing it and writing it back under a lock only that process holds (`internal/config`). Two panels on one file lose each other's changes, and neither sees the other's until it starts again.

## Shared on purpose

- **The fleet daemon.** The dev panel lists, opens and types into the real sessions. Attaching and resizing a terminal from it is real, for everyone attached.
- **The board.** Cards made or changed from the dev panel are real cards, committed to the board's git history.
- **Adding a fleet.** Besides the config copy, it makes the fleet's board and writes the folder into `permissions.additionalDirectories` in `~/.claude/settings.json`, as the installed panel does.

## Several trees at once

Every tree builds its dev app under the same identifier and, by default, the same port. A second dev app on a port where another one's panel answers uses that panel as it is, and names it over its page as a panel of another build. Give each tree its own `DEV_PORT`. `dev.fleetdeck.dev` may open any of the trees' bundles by name; open a dev app by its path, as `make dev-app` does. To have LaunchServices forget a dev bundle that is gone or no longer wanted:

```bash
/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister -u /path/to/bin/fleetdeck-dev.app
```

## The SDK

Every cgo build on darwin — `window-app`, `dist-app`, `dev-app`, `make test` — links against `SDKROOT` when one is set, and otherwise against the SDK of the developer directory `xcode-select` chose (`xcrun --sdk macosx --show-sdk-path`). The build prints which. Left to itself, clang took the Command Line Tools' `MacOSX.sdk` even with Xcode's linker selected: on a macOS 27 machine on 2026-09-15 that SDK was 27.0, Xcode's linker knew SDKs up to 26.5, and every window build failed to link on `Security.tbd` (`arm64e.x1`). With Xcode's own SDK the same build linked. Off darwin nothing of this runs.
