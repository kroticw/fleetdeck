# A dev app beside the installed app

A change to the window is looked at in the real window, with the real fleet, before it is released: a dev app built from the working tree and opened beside the installed fleetdeck. The installed app, its panel and the dev app run at the same time, and the dev app reaches nothing of the installed app's but the fleet daemon, the boards and the files named under "Shared on purpose".

## Building and opening it

```bash
make dev-app                    # build from this tree and open it on 127.0.0.1:7778
make dev-app DEV_PORT=7779      # on another port: one port per tree that runs a dev app
make dev-app DEV_OPEN=0         # build only, and print the command that opens it
```

- **What is built.** `bin/fleetdeck-dev.app`, under the identifier `dev.fleetdeck.dev` (`supervisor.DevBundleID`) and the name `fleetdeck dev`, with the window and the panel from this tree. `VERSION` stays `dev`, so the panel's header says `dev` and the short commit, with `*` for a tree with changes. The window's title and its app menu say `fleetdeck dev`.
- **Its port is written into it.** Opened from Finder or the Dock, with no `-url`, it opens on the `DEV_PORT` it was built with.
- **Who opens it.** Opening a window happens on the screen of whoever is at the machine. A session builds with `DEV_OPEN=0` and hands the printed `open` command to the person watching the screen; it does not open the window itself. A session does not build over a dev app that is running either: `make dev-app` removes the bundle first.
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
| Panel's port | 7777, the port of its URL | `DEV_PORT`, 7778 by default |
| Config the panel reads and writes | `~/.config/fleetdeck/config.yaml` | `~/.config/fleetdeck/dev/<port>/config.yaml`, a copy |
| Banners | sent | off in the copy |
| Panel's log | `~/Library/Logs/fleetdeck.log` | `~/Library/Logs/fleetdeck-dev.log`, one for every dev app, appended to |
| Window's log | not kept | `~/Library/Logs/fleetdeck-dev-window.log`, written by the window, appended to |
| Panel widths | the app's own defaults | the suite `dev.fleetdeck.dev.widths` |
| Updates | looks for a newer version | none |

- **The port reaches the panel.** Every window starts its panel with `--port`, the port of the URL it looks at, and the keeper starts every later panel with the same arguments, the restart after an update's swap included. The installed app, opened with no `-url`, passes `--port 7777`. Before this, a window started its panel with no port, the panel listened on `server.port`, and a window on another URL started a panel on the installed app's port.
- **Refused before anything starts.** The dev app does not start on 7777, the port the installed app hands its panel, nor on `server.port` of the operator's config, where a panel started without `--port` listens. It does not start without an operator's config to copy, or with `--handover` or `--canonical`, which only an update passes. The window writes its own log to `~/Library/Logs/fleetdeck-dev-window.log` from its first line, what it says of a bad command line included, so why it refused is there however it was opened.
- **It stops no process but its own panel.** Before it signals anything on its port, its keeper asks the kernel which binary listens there (`lsof -d txt`) and stops it only when that binary is the panel inside this dev app's bundle. Anything else is left running and used as it is, and the window's log says `port N is held by <path>, not by this dev app`. A port that answers while nothing is found listening on it is not stopped either, and neither is a process other than the one a press to replace was for. That covers the installed panel, and the panel of a dev app built in another tree, which has the same identifier and may have the same port. An orphan of this dev app's own panel, left by a window that crashed, is replaced.
- **No updates, no update's side effects.** It looks for no newer version, so it never writes `~/.config/fleetdeck/last-update-check`, which the installed app reads. Its Update button is never shown; asked anyway, it says a dev app does not update. It removes no bundle an earlier update left behind, and never takes a panel over, so it does not have LaunchServices register or forget `/Applications/fleetdeck.app`, and takes no update lock.
- **LaunchServices.** macOS registers the dev bundle when it is opened, under `dev.fleetdeck.dev`. What `dev.fleetdeck.window` opens — the Dock, Spotlight, `open -b` — stays `/Applications/fleetdeck.app`. To check it before, while and after a dev app runs, without sending anything to an app:

  ```bash
  osascript -l JavaScript -e 'ObjC.import("AppKit"); function run(a){var u=$.NSWorkspace.sharedWorkspace.URLForApplicationWithBundleIdentifier(a[0]); return u.isNil()?"":u.path.js}' dev.fleetdeck.window
  ```

## The config copy

The dev app's window writes the copy at every start, from the operator's config as it is then, over the copy an earlier start on the same port left, with `notify.enabled.*` off and `server.port` set to the dev port, so a dev panel started without `--port` would still keep off the installed panel's. There is one copy per port, so dev apps from two trees on two ports do not write over each other's.

What a dev panel may write to it, each only when asked from its page:

- `orchestrator.session`, pinning or unpinning the orchestrator;
- `session_labels`, a session's name;
- `fleets`, a fleet added;
- a fleet's orchestrator, appointed from the wizard.

At the next start all of that is replaced from the operator's config, with one exception: **a fleet made from a dev panel is kept**, since the panel asks to be restarted to work in it. Beside each copy, `copied-fleets.json` records the names of the operator's fleets it was made with. A fleet of the previous copy that is not among them was made in a dev app, and is carried into the new copy whole, unless a fleet of the operator's config has its name now — that one stays — or its board or orchestrator, when the copy would not load with both, and the window's log says the fleet was left out. A fleet the operator removed or renamed since the previous copy is not brought back.

On a port with no copy yet, the fleets are taken from `~/.config/fleetdeck/dev/config.yaml`, the one copy all dev apps shared before copies were kept per port. It has no record of which fleets were the operator's, so only its fleets on a board no fleet of the operator's config is on are carried. That file is read and left as it is.

To drop a fleet made in a dev app: quit the dev app, then remove the fleet's entry from `~/.config/fleetdeck/dev/<port>/config.yaml`. Removing the whole `dev/<port>` directory starts the next copy from the operator's config alone — unless the shared `dev/config.yaml` is still there, whose fleets are then carried again.

Why a copy: a panel reads its config once, at its start, and changes it by reading the file, changing it and writing it back under a lock only that process holds (`internal/config`). Two panels on one file lose each other's changes, and neither sees the other's until it starts again.

## Shared on purpose

- **The fleet daemon.** The dev panel lists, opens and types into the real sessions. Attaching and resizing a terminal from it is real, for everyone attached.
- **The boards.** Cards made or changed from the dev panel are real cards, committed to the board's git history.
- **Adding a fleet** makes its board or workspace for real, and adds the folder to `permissions.additionalDirectories` in `~/.claude/settings.json`, the file every Claude Code session on the machine reads. It does not set Claude Code's statusline: the steps say `statusline: not set by a dev app`, and the statusline stays the installed app's.

## Several trees at once

Every tree builds its dev app under the same identifier and, by default, the same port. A second dev app on a port where another one's panel answers uses that panel as it is, and names it over its page as a panel of another build. Give each tree its own `DEV_PORT`; each port has its own config copy. `dev.fleetdeck.dev` may open any of the trees' bundles by name; open a dev app by its path, as `make dev-app` does. To have LaunchServices forget a dev bundle that is gone or no longer wanted:

```bash
/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister -u /path/to/bin/fleetdeck-dev.app
```

## The SDK

Every cgo build on darwin — `window-app`, `dist-app`, `dev-app`, `make test` — links against `SDKROOT` when one is set, and otherwise against the SDK of the developer directory `xcode-select` chose (`xcrun --sdk macosx --show-sdk-path`). `scripts/darwin-sdkroot.sh` makes the choice, and the build prints it. Left to itself, clang took the Command Line Tools' `MacOSX.sdk` even with Xcode's linker selected: on a macOS 27 machine on 2026-09-15 that SDK was 27.0, Xcode's linker knew SDKs up to 26.5, and every window build failed to link on `Security.tbd` (`arm64e.x1`). With Xcode's own SDK the same build linked. When `xcrun` names no SDK — an Xcode whose license nobody has accepted exits 69 — `window-app`, `dist-app` and `dev-app` stop and say what `xcrun` said; `make test` says it too and runs its tests without `SDKROOT`. Off darwin nothing of this runs.

`/usr/bin/make`, `git`, `clang` and `xcrun` are `xcrun`'s shims on macOS. Under an `SDKROOT` or `DEVELOPER_DIR` that names no SDK or developer directory they do not run the tool at all, and ask macOS to install the Command Line Tools — a dialog on the screen of whoever is at the machine. Give them only paths that exist.
