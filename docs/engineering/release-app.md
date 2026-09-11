# Release app and Gatekeeper: field notes

These are facts from making the fleetdeck app installable from a release (`make dist-app`, 2026-09-11): a zip a person downloads, unpacks, drags to Applications and opens, on a Mac that never saw this repository. Each fact says how it was established: **measured** (observed on this machine), **read** (in code, a man page or Apple's documentation), or **decided** (a choice, with its reason). The code comments hold the details; this page holds what cost something to learn.

Read this before changing `scripts/build-dist-app.sh`, `scripts/verify-dist-app.sh`, the `dist-app` targets in the `Makefile`, `cmd/fleetdeck-window/Info.plist`, or the release workflow. Read section 1 before anyone suggests that a downloaded app "just needs signing", and section 5 before writing any shell script that is a gate.

The page is named after the code it is about, like [window-and-panel.md](window-and-panel.md) and [workspace.md](workspace.md). The person's side of it — the steps, in order — is in the getting-started guide, [in English](../en/getting-started.md#installing-the-app-from-a-release) and [in Russian](../ru/getting-started.md#установка-приложения-из-выпуска).

## 1. Without a seal, a downloaded app is "damaged", and that is a dead end

This is the fact the whole release depends on.

- **Measured: the bundle `make window-app` builds has a broken bundle signature.** The Go linker signs each arm64 binary ad hoc on its own (`flags=0x20002(adhoc,linker-signed)`, identifier `a.out`). That is enough to run the binary, but it seals nothing else. `codesign --verify --deep --strict` on the bundle says `code has no resources but signature indicates they must be present`, and `spctl --assess --type execute` fails with the same error, status 1.
- **Measured: sealing the whole bundle ad hoc fixes the signature, not the trust.** `codesign --force --sign -` on the helpers and then on the bundle gives `valid on disk` and `satisfies its Designated Requirement`. `spctl` then answers `rejected`, status 3: an ordinary "not notarized" refusal, not a signature error.
- **Measured on the operator's screen**, macOS 26.6.2 (25G83), arm64, Russian, with stand-in bundles (section 6), both quarantined as a Firefox download and unpacked with `ditto`:
  - **Unsealed**, as `window-app` leaves it: macOS said the app is damaged and cannot be opened. That was reported from the screen, not captured word for word. The kernel log has AMFI's `Unrecoverable CT signature issue, bailing out` for it. Apple's documented way to open an unverified app does not reach this case, and no way to open it from the user interface was found.
  - **Sealed**: the dialog «Файл «fleetdeck» не был открыт» — «Apple не удалось подтвердить, что файл «fleetdeck» не содержит вредоносного ПО, которое может нанести вред Вашему Mac или конфиденциальности Ваших данных.», with the buttons «Переместить в Корзину» (highlighted) and «Готово». This was captured. syspolicyd logged `rejecting due to lack of matching active rule`.
- **Decided: the release is sealed ad hoc.** It costs nothing and needs no certificate, and it is the difference between "inconvenient" and "impossible". It is not a Developer ID signature: it identifies no one, and Gatekeeper still refuses the app until the person makes an exception (section 2). An app signed with a Developer ID and notarized opens with a double click. That needs an Apple Developer Program membership, and it is not done.
- **Read and measured: `syspolicy_check distribution`** reports both of our bundles as `Adhoc Signed App` and `Notary Ticket Missing`, and reports the unsealed one's codesign error as fatal. It also reports `Internal Xprotect Error` for both, and not for a Developer ID app on the same machine (OrbStack.app). Why is not known. It did not stop the sealed app from opening once allowed.

## 2. The path a person takes, reconstructed from the log

**Measured.** With a fresh sealed stand-in, which had a code hash no earlier stand-in had, the unified log (`/usr/bin/log`: in zsh, a bare `log` is a builtin) gives the operator's steps to the second:

| Time | Event |
| --- | --- |
| 23:33:12 | Double click in Finder. syspolicyd: `rejecting due to lack of matching active rule`. The dialog is shown. |
| 23:33:18 | «Готово». CoreServicesUIAgent: `Saving rejection record`, and the app's process is killed (`_LSKillApplication`, `-128`). |
| 23:33:19 | «Все равно открыть» in Privacy & Security. syspolicyd: `Adding user intent`. The app is opened again, rejected again, and the warning is shown again. |
| 23:33:22 | The warning is confirmed. syspolicyd: `Caller indicated a Gatekeeper override occurred`. The stand-in runs. |

After that the quarantine flags changed from `0083` to `00c3`, and the app opened with a double click without a word. This matches Apple's instructions for macOS 26 ([mh40616](https://support.apple.com/guide/mac-help/mh40616/mac)). They also say, **read**: the button is there "for about an hour" after the attempt to open the app, and a login password is asked for. Whether a password or Touch ID was asked here was not recorded: the confirmation took three seconds.

**Measured, and first read wrong:** the Security part of Privacy & Security was screenshotted twice, at 23:24:02 and at 23:34:30, and showed no fleetdeck line either time. Both shots were taken *after* an approval, when the line had already gone. The operator, who looked at the right moment, saw it. A screenshot of a line that lives between two events proves nothing unless it is taken between them.

## 3. Quarantine travels through every unpacker

**Measured.** A zip and a tar.gz of the sealed app were stamped with a Firefox-style `com.apple.quarantine` value (`0083;<hex time>;Firefox;<uuid>`, the format read off real downloads on this machine). Each was then unpacked with every command-line tool at hand:

| Unpacker | zip | tar.gz |
| --- | --- | --- |
| `ditto -x -k` | quarantined, app and binaries | — |
| `/usr/bin/unzip` | quarantined | — |
| `tar --extract` (bsdtar) | quarantined | quarantined |

The control is the same archives unstamped: no attribute on anything. So unpacking in Terminal is not a way around Gatekeeper, and nothing about the archive format is. The codesign seal survived every round trip.

**Measured: App Translocation.** Opened from outside Applications, even after approval, the stand-in ran from `/private/var/folders/…/T/AppTranslocation/<uuid>/d/fleetdeck.app/…`. **Read:** first-run setup takes the statusline command's path from `os.Executable()` beside the panel (`ensureStatusline` in `cmd/fleetdeck/init.go`), so a first launch from Downloads would record a path that later disappears. Hence the guide's step "drag it to Applications before the first launch". **Not measured:** that a Finder move keeps a quarantined app from being translocated.

## 4. Building one app for both architectures

- **Measured: the window cross-builds with cgo** when clang is told the target: `CGO_ENABLED=1 GOARCH=amd64 CC="clang -arch x86_64"` on an arm64 Mac. A plain `GOARCH` switch turns cgo off, and the window then fails to compile. That failure is why `dist` excludes it.
- **Decided: one universal zip, not one per architecture.** A person does not have to know which processor their Mac has. Every slice is built with cgo, the way `window-app` builds on its host, so the slices differ in nothing but the architecture.
- **Measured: `go version -m` on a universal binary reads the first slice only**, and says nothing about the others. An x86_64+arm64 binary reported `GOARCH=amd64` alone. The verification takes each slice out with `lipo -thin`, and the test reads each slice through `debug/macho` and `debug/buildinfo` at the slice's offset.
- **Measured: both slices run.** From the release zip, the panel answers `v0.1.0` natively and under Rosetta (`arch -x86_64`). The release workflow does not run the x86_64 slice, because Rosetta is not part of GitHub's documented macOS image; there it is checked by its build record.
- **Decided: every command goes in the bundle, `BIN_NAMES` and not `DIST_BIN_NAMES`.** First-run setup wires the statusline to the `fleetdeck-status` beside the panel and writes nothing when it is not there, so a bundle without it installs a fleet with no statusline.
- **Decided: no source tree in the window.** `window-app` writes `main.treeDir` and the tool paths for the Update button. A release is built on a CI runner, and a window carrying the runner's checkout would offer to update from a tree that exists on no machine the app is installed on. Without `treeDir` the window shows no Update button (`updateUnavailable` in `cmd/fleetdeck-window/update.go`), and a new version is a new download. The ldflags of every slice are checked to be exactly the version and nothing else.
- **Decided: the tag without its `v`** goes into `CFBundleShortVersionString` and `CFBundleVersion`, where Finder's Get Info reads it. Every other plist key is `window-app`'s, and the test and the verification both compare them.

## 5. A gate that passes when it breaks

This defect was in the gate written for this very change, and a mutation pass found it. No test had.

- **Measured:** `verify-dist-app.sh` first had a `case … in amd64) … ;; esac` inside `$(…)`. The bash 3.2 that is `/bin/sh` on macOS cannot parse a case pattern's `)` there, and reports `syntax error near unexpected token ';;'`.
- **Measured: with an `EXIT` trap set, that syntax error ends the script with status 0.** A three-line reproduction gave status 2 without the trap and 0 with it. A trap that tries to keep `$?` (`status=$?; …; exit $status`) still gave 0, because `$?` is already 0 there. A trap on signals only gave 2. So every check below the error — the architectures, the ldflags, the seal, the panel's version — was skipped, and `make dist-app` passed. The mutants `one-arch-only` and `tree-leaks-in` got through the gate and were caught only by the Go test.
- **Decided, in both scripts:** no `EXIT` trap. The scratch directory is removed by `fail` and at the end, and a trap covers signals only. A command that fails under `set -e` leaves the directory in `$TMPDIR`, which is the cheaper failure of the two. The case moved into a function.
- **Decided: the gate's last line is its proof of reaching the end.** The test asserts `verify-dist-app: <zip> ok` in `make dist-app`'s output. The mutant `verify-stops-silently` (an `exit 0` before the seal check) is caught by that assertion alone.
- **Not fixed, only reported:** `scripts/verify-dist.sh`, the gate for the tarballs, has the same `EXIT` trap. It has no syntax error today, but one added later would pass the same way.

**Measured: the mutation pass**, run in a copy of the tree, final code:

| | Result |
| --- | --- |
| Mutants of `build-dist-app.sh`, run as CI runs them | 12 of 12 killed |
| The same 12 against a gate that passes everything and prints its `ok` line | 12 of 12 killed by the Go test alone |
| Mutants of `verify-dist-app.sh` itself (the early exit, the old case) | 2 of 2 killed |
| Subtests no mutant failed | 0 of 6 |

The mutants: no bundle seal; one architecture only; no plist version; `treeDir` leaking into the ldflags; a stale zip kept; `make dist`'s tarballs removed; no `fleetdeck-status`; a stray `.DS_Store`; the wrong icon; an unversioned panel; the bundle changed after sealing; `fleetdeck-status` not executable. The control, the unmutated copy, passed. The first pass's harness had its own blind spot: once the test began asserting the gate's `ok` line, a stand-in gate that printed nothing killed every mutant by that one assertion, and measured no subtest at all. The stand-in now prints the line.

## 6. Stands that could not touch the fleet

- **Decided: the Gatekeeper stands were stand-ins.** They are bundles shaped exactly like the real one (the same `Info.plist`, the same icon, the same layout, the same signing steps), but both executables are a 2 MB Go program. It appends a line to a file and exits, links no WebKit, opens no window and no port. The real window would have met the operator's panel on 7777. The stand-in cannot. Checked before each open: size, `otool -L`, and the quarantine value.
- **Decided: a repeat needs a new code hash, not a new copy.** The first open changes a stand-in's quarantine state for good, so a repeat needs a fresh one. It was built with a different `-X` value, so its code hash differed from the earlier one (`9465fe66…` against `38f715c9…`), and no earlier approval could carry over to it. Whether an approval carries over to a copy with the same hash was not tried.
- **Measured: `open` on a quarantined app waits until its dialog is closed.** A screenshot taken after `open` returns never shows the dialog. It has to be taken from a second command while the dialog is up.
- **Measured: `screencapture -x file.png` captures the main display only.** One file per display is needed for the others.
- **Measured: a log watcher can be blind by construction.** `log stream … | grep --line-buffered … | cut …` delivered nothing: `cut` buffers its output. The events were in the log all along.
- The screen was used at a moment the operator gave, between 23:21 and 23:35, and nothing on it was clicked by a script.

## 7. `window-app` and `install` are unchanged

**Measured.** A fingerprint of what `make window-app` and `make install` produce was taken before any change and again on the branch. It covers the file list with modes, the `Info.plist` and icon hashes, each binary's build record (`go version -m`, which carries `-ldflags` and every build setting) and its signature identifier. The two were identical over 103 lines; only the module's pseudo-version, which comes from the commit, was left out. The control was a copy of the tree with two deliberate changes: one more `-X` in the window's ldflags, and one character in `Info.plist`. The fingerprint showed both. `TestTheAppBundleCarriesThePanelWhereTheWindowLooksForIt` and the `install` tests still run the real targets.

## 8. Not verified

- **A clean Mac.** There is no macOS virtual machine here (Parallels holds only a Windows VM). The stand-in for a clean machine was the quarantine attribute set by hand in Firefox's format, the operator's own screen, and stand-in bundles. A Mac without Claude Code, git or the Command Line Tools was not tried.
- **A real browser download.** The attribute was stamped, not set by a browser. Safari unpacks "safe" downloads itself; that path was not tried.
- **The first run of the real app from a release**, all the way to a working fleet. The Gatekeeper path was measured with stand-ins, the app's contents with the tests, and the two were never put together on one screen.
- **The "damaged" dialog's exact words**, the second warning in the Open Anyway path, and whether a password was asked.
- **The release workflow itself.** `make dist-app` runs in CI's macOS leg through the test, but the new steps of `release.yaml` run only on a tag, and no tag was made.
- **A new version over an installed one:** whether Gatekeeper asks again. It is expected to, because the code is different.
