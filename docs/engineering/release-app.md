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
- **Decided, and since superseded: the release was sealed ad hoc.** It cost nothing and needed no certificate, and it was the difference between "inconvenient" and "impossible". It is not a Developer ID signature: it identifies no one, and Gatekeeper still refuses such an app until the person makes an exception (section 2). The release is now signed with a Developer ID and notarized instead — section 8 — and the ad-hoc seal is what every build without a certificate still gets: a developer's, and CI's check job, which runs `make dist-app` on a runner that has none.
- **Read and measured: `syspolicy_check distribution`** reports both of our bundles as `Adhoc Signed App` and `Notary Ticket Missing`, and reports the unsealed one's codesign error as fatal. It also reports `Internal Xprotect Error` for both, and not for a Developer ID app on the same machine (OrbStack.app). Why is not known. It did not stop the sealed app from opening once allowed. **Measured on 2026-09-12:** on the notarized bundle the same command answers `App passed all pre-distribution checks and is ready for distribution`, with no Xprotect error — so that error tracked the seal, not the machine.

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

**Measured: App Translocation.** Opened from outside Applications, even after approval, the stand-in ran from `/private/var/folders/…/T/AppTranslocation/<uuid>/d/fleetdeck.app/…`. **Read:** first-run setup takes the statusline command's path from `os.Executable()` beside the panel (`ensureStatusline` in `cmd/fleetdeck/init.go`), so a first launch from Downloads would record a path that later disappears. Hence the guide's step "drag it to Applications before the first launch".

**Measured on 2026-09-12, on the real v0.3.0 release downloaded by Firefox: the drag is the thing, and being in Applications is not.**

| How the app got into `/Applications` | Where it ran from |
| --- | --- |
| `mv` in a shell | `/private/var/folders/…/AppTranslocation/…` |
| Finder's own `move`, over an Apple Event | `/private/var/folders/…/AppTranslocation/…` |
| Dragged with a mouse in Finder | `/Applications/…`, and the quarantine attribute still on it |

So the guide's step 2 does what it was written to do, and a person who follows it gets an app that knows where it lives. What does **not** work is installing the app by moving it programmatically — an installer, a `make` target, a script copied off a page — and that is worth knowing before anyone writes one.

Removing `com.apple.quarantine` also stops translocation, which is the other half of the mechanism: it is the attribute that arms it, and the drag that disarms it.

**Read off the attribute, not from Apple's documentation:** a pristine download carries flags `0081`; after a mouse drag into Applications and a launch the bundle carries `01c1` and the binaries inside `00c1`. The `01` appears to be the mark of the user's own move, but that reading is inference from three observations, not something Apple states here.

**One confound, stated rather than hidden:** the scripted-move row was measured on a copy that had already been launched once (flags `00c1`), while the mouse-drag row started from a pristine `0081`. The two rows differ in the starting flags as well as in how the app was moved, so "Finder's scripted move does not clear translocation" is measured for that starting state and not isolated from it. The row that matters for a person — the pristine copy, dragged, untranslocated — has no such confound.

## 4. Building one app for both architectures

- **Measured: the window cross-builds with cgo** when clang is told the target: `CGO_ENABLED=1 GOARCH=amd64 CC="clang -arch x86_64"` on an arm64 Mac. A plain `GOARCH` switch turns cgo off, and the window then fails to compile. That failure is why `dist` excludes it.
- **Decided: one universal zip, not one per architecture.** A person does not have to know which processor their Mac has. Every slice is built with cgo, the way `window-app` builds on its host, so the slices differ in nothing but the architecture.
- **Measured: `go version -m` on a universal binary reads the first slice only**, and says nothing about the others. An x86_64+arm64 binary reported `GOARCH=amd64` alone. The verification takes each slice out with `lipo -thin`, and the test reads each slice through `debug/macho` and `debug/buildinfo` at the slice's offset.
- **Measured: both slices run.** From the release zip, the panel answers `v0.1.0` natively and under Rosetta (`arch -x86_64`). The release workflow does not run the x86_64 slice, because Rosetta is not part of GitHub's documented macOS image; there it is checked by its build record. Running it under Rosetta on the operator's machine had a cost: it raised macOS's Intel notice under the name "fleetdeck" (section 6). Do not run a slice under Rosetta from a bundle that carries the working app's identifier.
- **Measured: what the x86_64 slice costs.** The release zip is 39.7 MB with both slices and 18.9 MB with arm64 alone. It raises no notice by being there (section 6).
- **Decided: every command goes in the bundle, `BIN_NAMES` and not `DIST_BIN_NAMES`.** First-run setup wires the statusline to the `fleetdeck-status` beside the panel and writes nothing when it is not there, so a bundle without it installs a fleet with no statusline.
- **Decided: no source tree in the window.** `window-app` writes `main.treeDir` and the tool paths for the Update button. A release is built on a CI runner, and a window carrying the runner's checkout would offer to update from a tree that exists on no machine the app is installed on. Without `treeDir` the window updates from the releases page instead (`updateWay` in `cmd/fleetdeck-window/way.go`, and [update-from-release.md](update-from-release.md)). Until 2026-09-12 it showed no Update button at all, which is the defect that page is about. The ldflags of every slice are checked to be exactly the version and nothing else.
- **Decided, by the operator: the header shows the version, not the commit.** A person who downloaded the app cannot read a commit hash. The build fingerprint carries `version`, taken from the same `internal/version` that answers `fleetdeck version`, so the two cannot differ. A release shows its tag. A checkout build reports `dev`, and shows `dev` with the short commit so it never passes for a release. A panel from before this change reports no version, and shows the commit as it did. Checked by tests and by seven mutants, all killed. **Measured end to end:** a stand panel built with `VERSION=v9.9.9` (its own HOME, port 7791 and `-stand-socket`) answered `/api/snapshot` with `build.version` `v9.9.9`, and the real `brandHTML` rendered that snapshot as `fleetdeck v9.9.9*`. The asterisk was there because the tree had uncommitted edits. It was not looked at in a browser.
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
- **Measured: a stand bundle with the working app's bundle identifier speaks in its name.** LaunchServices registers every `.app` that is opened, run or looked at, by its `CFBundleIdentifier`. After this work, `lsregister -dump` listed five bundles from this session's scratch directory: four as `dev.fleetdeck.window`, the operator's own identifier, and one as `dev.fleetdeck.standin`, the only stand given an identifier of its own. The operator's machine held 19 `fleetdeck.app` registrations in all, from several sessions' scratch directories, all but that one as `dev.fleetdeck.window`. A system notice about any of them names "fleetdeck", and it looks like a notice about the operator's app (next point). **Not measured:** how long a registration outlives its deleted directory. Check with `lsregister -dump | grep 'path:.*fleetdeck.app'` (`lsregister` is under `/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/`), and give every stand a bundle identifier of its own.
- **Measured: running a slice under Rosetta is what raises macOS 26's Intel notice, not having the slice.** At 23:39:06 this session ran the release panel's x86_64 slice with `arch -x86_64` from a stand bundle, to prove the slice runs. Rosetta's `oahd-helper` started. At 23:39:07 `ecosystemanalyticsd` ran a "rosetta analysis" on exactly that bundle's path, and at 23:39:09 `ecosystemd` connected to `com.apple.ecosystem.agent.notifications`. The operator later saw «Поддержка приложений для процессоров Intel скоро прекратится. В этой версии приложения «fleetdeck» есть компонент, который не будет работать в следующих версиях macOS» on launching their own build, which is arm64 only. That is the stand's notice under the shared name (previous point). Do Not Disturb had been on since 23:30:09, so when the notice was shown was not established. The control has two parts. First, the log holds 46 native runs of panels from `fleetdeck.app` bundles that evening, universal release ones among them, and none was followed by a Rosetta analysis. Second, a universal stand-in opened through LaunchServices (`open -n`) ran its arm64 slice (it recorded `goarch=arm64`), with no analysis after it. So a person on Apple silicon who opens the release gets the arm64 slice, and goes through no Rosetta unless someone ticks "Open using Rosetta" in Get Info. That a person on an Intel Mac runs the x86_64 slice natively, and so gets no notice either, is **read** from how a universal binary works, not measured (section 8). **Not measured:** whether macOS also scans installed apps for Intel code without running them.
- **Measured: `open` on a quarantined app waits until its dialog is closed.** A screenshot taken after `open` returns never shows the dialog. It has to be taken from a second command while the dialog is up.
- **Measured: `screencapture -x file.png` captures the main display only.** One file per display is needed for the others.
- **Measured: a log watcher can be blind by construction.** `log stream … | grep --line-buffered … | cut …` delivered nothing: `cut` buffers its output. The events were in the log all along.
- The screen was used at a moment the operator gave, between 23:21 and 23:35, and nothing on it was clicked by a script.

## 7. `window-app` and `install` are unchanged

**Measured.** A fingerprint of what `make window-app` and `make install` produce was taken before any change and again on the branch. It covers the file list with modes, the `Info.plist` and icon hashes, each binary's build record (`go version -m`, which carries `-ldflags` and every build setting) and its signature identifier. The two were identical over 103 lines; only the module's pseudo-version, which comes from the commit, was left out. The control was a copy of the tree with two deliberate changes: one more `-X` in the window's ldflags, and one character in `Info.plist`. The fingerprint showed both. `TestTheAppBundleCarriesThePanelWhereTheWindowLooksForIt` and the `install` tests still run the real targets.

## 8. Signing and notarization

**2026-09-12.** Section 1 said an app signed with a Developer ID and notarized opens with a double click, and that it was not done. It is done now. The code is in [`scripts/build-dist-app.sh`](../../scripts/build-dist-app.sh) and [`scripts/notarize-dist-app.sh`](../../scripts/notarize-dist-app.sh); the secrets the workflow needs are in [signing-secrets.md](signing-secrets.md). What cost something to learn:

- **Measured: the three states are three different `spctl` answers**, on the same app built three ways on the same machine:

  | Seal | `spctl --assess --type execute` | status |
  | --- | --- | --- |
  | Ad hoc | `rejected`, no source line | 3 |
  | Developer ID, not notarized | `rejected`, `source=Unnotarized Developer ID` | 3 |
  | Developer ID, notarized, stapled | `accepted`, `source=Notarized Developer ID` | 0 |

  The middle row is the one worth knowing: a signature alone changes nothing a person would notice. Signing is what makes notarization possible, and notarization is what opens the app.

- **Measured: the entitlements list is empty, and that is a result.** A probe was signed exactly the way a release is — Developer ID, `--options runtime`, this repository's `entitlements.plist` — placed in a bundle shaped like the app's, and run from inside it. It did each thing the app does: the `osascript` banner `internal/notify` sends, starting a system binary, starting the panel beside it in the bundle, listening on a loopback port, and reading the home directory. Every one was allowed with no entitlement at all. The system log was watched throughout (`com.apple.TCC`, `amfid`, `taskgated-helper`, `com.apple.syspolicy`, `osascript`), with a deliberate `spctl` call in it as a control so that a watcher reporting nothing could be told from a watcher that was blind: 118 lines, no denial. The real panel was also run out of the signed bundle, in a scratch `HOME` on a port of its own, and served its page.

  **Decided: nothing is copied from the Electron project next door.** Its four entitlements are `allow-jit` and `allow-unsigned-executable-memory`, which are Chromium's in-process V8 and have no bearing on a WKWebView whose JavaScript runs in Apple's own WebContent process; `disable-library-validation`, which is for loading native modules built by someone else, and this app links system frameworks and nothing more; and `network.client`, which is an App Sandbox entitlement and means nothing outside a sandbox. Every entitlement is a hole in the hardened runtime, and a hole opened by copying is one nobody measured.

  The probe bundle was given a `CFBundleIdentifier` of its own, for the reason in section 6.

  **Measured: the window too, WebKit and all.** The window was run out of the notarized bundle, on the operator's screen at a moment they gave, pointed at a stand panel on a port of its own so its keeper would find a panel there and not start one that would meet the real fleet. It opened, WKWebView started its `com.apple.WebKit.WebContent` process, and the panel's setup page rendered — text, the folder field, both buttons — captured in a screenshot. So the hardened runtime costs the window nothing either.

  The system log for that run does hold denials, and they are **not** ours: `WebContent` is refused `iokit-get-properties`, a `coreservicesd` lookup and an audio registrar lookup. The control settles it — the same run with the ad-hoc bundle, no Developer ID and no hardened runtime, produces the same denials against the same Apple process. They are WebKit's own sandbox, which is Apple's profile for Apple's process, and they have nothing to do with how the app above them is signed. A denial in a log is not evidence until something without the change shows it too.

- **Measured: `codesign` asks for a secure timestamp by default** whenever it signs with a real identity. Leaving `--timestamp` off changes nothing; `--timestamp=none` is what turns it off, and a signature without one reports `Signed Time=` where a timestamped one reports `Timestamp=`. This was found by a mutant that survived: "build without `--timestamp`" was not a mutant at all. The flag is written out in the build anyway, because a release must not rest on a default staying what it is.

- **Measured: the notarization ticket lands at `Contents/CodeResources`**, beside `_CodeSignature` rather than inside it, magic `s8ch`, about 1.8 KB. Read off a notarized app already on this machine, before the first submission was made. It is outside what the signature seals, so stapling does not break the signature — and it is a new member in the zip, which the gate's exact member list has to know about.

- **Decided: the zip is written twice.** Apple is given the signed zip; the ticket it returns is stapled into the *bundle*, because a zip has nowhere to hold one; the zip is then written again from the stapled bundle. Skipping the last step publishes an app with no ticket in it, which still opens on a Mac that can reach Apple and refuses on one that cannot — the machine least able to explain why.

- **Decided: the gate is told which seal to demand, not left to read one.** `EXPECT_SEAL` follows from `SIGN_IDENTITY` and the release workflow spells out `notarized`. A gate that read the bundle and agreed with whatever it found would pass an unsigned release with a shrug.

- **Measured: the mutation pass**, nine mutants against the new checks, final code:

  | | Result |
  | --- | --- |
  | Mutants of the seal checks | 9 of 9 killed |
  | Unmutated controls (ad hoc, signed, notarized) | 3 of 3 pass |

  The mutants: no hardened runtime; no secure timestamp; an entitlement the repository does not keep; a helper left ad hoc inside a signed bundle; an ad-hoc build asked for `developer-id`; a signed build asked for `notarized`; a ticket that is present and is not a ticket; a binary changed after Apple saw it; and a signed build asked for `adhoc`.

  **The harness had two blind spots of its own, and both were found by looking at which check did each killing.** The first version named every mutant bundle `mutant.app`, so the gate refused all of them on the member list without reaching a single check they were written for — six kills, nothing measured. Then two mutants survived, and neither was a gate defect: "no `--timestamp`" is not a mutation (see above), and re-signing a bundle with the same bytes and the same flags produces the same code hash, so the ticket still matched. A mutant has to be checked for being a mutant.

### The acceptance, on the real release

**Measured, 2026-09-12, v0.3.0.** Not "the command worked" — the app a person gets:

- Downloaded with Firefox from the releases page. The quarantine attribute it arrived with is a real one, set by the browser: `0081;<hex time>;Firefox Developer Edition;<uuid>`. Every earlier measurement in this page used an attribute stamped by hand in that format; this one was not stamped.
- Unpacked by double-clicking the zip, which is Archive Utility. The quarantine attribute was on the bundle afterwards **and** on each binary inside it.
- `spctl --assess --type execute -vv` on the downloaded, quarantined copy: `accepted`, `source=Notarized Developer ID`. The same command on the ad-hoc build of the same app, minutes earlier: `rejected`, status 3.
- Put in Applications and opened. No dialog. `open` returned at once rather than waiting on one — on a quarantined app it does not return until any Gatekeeper dialog is closed, which is how the previous session had to screenshot the refusal from a second command. Nothing about fleetdeck appears in syspolicyd's log: no `rejecting due to lack of matching active rule`, no rejection record, no user intent. The quarantine flags went from `0081` to `00c1` with nobody touching anything; the ad-hoc path needed two confirmations to go from `0083` to `00c3`.
- The window opened on the operator's own running fleet, showing the board and the live sessions.

**Measured: the release app does not take a running panel away from its window.** The operator's panel was answering on 7777, started by their own window, while this was done. `Keeper.replaceable` returns false when the answering panel's owner process is alive (`internal/supervisor/keeper.go`), and that is what happened: the new window used the panel it found, and the operator's window and panel were still running afterwards.

**Not measured, and the loose end of this release:** a first run of the release app all the way through first-run setup, on a machine with no fleet. The app opened on a fleet that already existed, so `ensureStatusline` never ran from a downloaded copy. Section 3 says the path it would record is the right one as long as the person dragged the app in Finder, which the guide tells them to do — but that is the path not yet walked end to end.

## 9. The disk image, and why the zip stays

Added on 2026-09-12, on top of everything above. The release now carries two macOS downloads, with two jobs: `fleetdeck-<version>-macos.dmg` is what a person installs from, `fleetdeck-<version>-macos.zip` is what an installed app fetches when it updates itself.

**Why an image at all.** Section 3 ends with a sentence in the documentation asking people to drag the app to Applications before opening it, because an app opened from Downloads runs from a translocated copy and first-run setup would write that copy's path into Claude Code's settings. A sentence in the documentation is a thing people skip. The image turns the same instruction into the shape of a window: the app on the left, a shortcut to Applications on the right, an arrow between them.

**Why the zip could not simply be replaced.** `internal/supervisor/release.go` builds the download address from a name — `ArchiveName(tag)` is `"fleetdeck-" + tag + "-macos.zip"` — rather than looking at what a release carries. Every copy of v0.5.0 already on somebody's Mac has that spelling compiled into it. A release without the zip answers those copies with a 404, and no later release can undo it: the client that cannot update is the one already installed. Dropping the zip is a one-way door, and the door is behind us.

The same fact is what makes adding the image safe. The updater does not enumerate assets and cannot be lured onto a `.dmg` by anything as weak as an extension or an ordering — it asks for one URL and gets that file or nothing.

**Measured, on this machine, on a real notarized build:**

```text
codesign --display  → Authority=Developer ID Application: … (PTLLPQ8LY4)
                      Authority=Developer ID Certification Authority
                      Authority=Apple Root CA, Timestamp=…
stapler validate <image>                                   → worked
spctl --assess --type open --context context:primary-signature
    notarized image                                        → accepted, source=Notarized Developer ID
    the same image with Firefox's quarantine attribute set → accepted
    an ad-hoc image                                        → rejected, status 3
the app on the mounted image, spctl --assess --type execute → accepted, source=Notarized Developer ID
```

Three things that came out of doing it rather than reading about it:

- **Stapling a disk image does not break its signature.** The ticket lands outside what the signature covers, the same way the app's does (section 8), so the order is sign, submit, staple, and `codesign --verify` still passes afterwards. Measured, because the opposite would have been discovered on a tag.
- **The app inside a quarantined image carries no quarantine attribute of its own.** The mark is on the image. Finder puts one on the copy it writes into Applications, and Gatekeeper has already answered `accepted` for it by then.
- **`hdiutil attach -mountrandom <dir>` needs `<dir>` to exist**, or it fails with "no mountable file systems" — which reads exactly like a corrupt image and is not one.

**The window cannot be built, only recorded.** A `.DS_Store` is Finder's private binary record of a folder's window — size, icon view, icon positions, which file is the background — and the only supported way to make one is to drive Finder over AppleScript on a machine with a window server. A release runs on a GitHub runner. So `scripts/build-dmg-layout.sh` produces it once, by hand, and `packaging/dmg/DS_Store` is committed; `scripts/build-dist-dmg.sh` copies it in and touches neither Finder nor AppleScript. The gate then compares the published file with the committed one byte for byte, which is the only honest version of the claim: not "the window looks right", but "the window shipped is the window that was looked at".

Two numbers in that file are load-bearing. The volume is named `fleetdeck`, because the background is recorded as an alias carrying the volume's name beside the relative path, and an image built under another name can come up blank. And the window's height is the background's height plus the title bar — Finder paints the picture into the content area at natural size, anchored top left, so a shorter window scrolls and a taller one shows bare window underneath. Measured on this machine: a Finder with the tab bar, status bar and path bar turned on eats about 52 px more, which is why nothing carrying meaning sits below the icon labels.

**The gate.** `scripts/verify-dist-dmg.sh` asks the app inside the image exactly what `verify-dist-app.sh` asks the app inside the zip — both source `scripts/dist-app-checks.sh`, so there is one piece of code and not two that resemble each other. What it adds is what only an image has: exactly one `.dmg` named for this tag, a volume holding the app, the Applications symlink and the two window files and nothing else, Finder's invisible flag on both of those, the layout matching the repository, the image's own seal and ticket, Gatekeeper's verdict on the image, and — the one claim none of the rest can make — the app on the image being the *same* app as the zip's, compared by code directory hash rather than by version number.

Five mutants, all killed: `Applications` as a folder rather than a symlink; the window files without the invisible flag; a layout that is not the committed one; a stray file on the volume; and an image holding a different build of the same version, signed the same way.

## 10. Not verified

- **A clean Mac.** There is no macOS virtual machine here (Parallels holds only a Windows VM). The stand-in for a clean machine was the quarantine attribute set by hand in Firefox's format, the operator's own screen, and stand-in bundles. A Mac without Claude Code, git or the Command Line Tools was not tried.
- ~~**A real browser download.**~~ Closed on 2026-09-12: v0.3.0 was downloaded with Firefox and carried the browser's own quarantine attribute through Archive Utility to the bundle and its binaries (section 8). Safari, which unpacks "safe" downloads itself, was still not tried.
- **The first run of the real app from a release**, all the way to a working fleet, on a machine with no fleet already set up. The Gatekeeper path is now measured on the real app rather than on stand-ins (section 8), and the app opened on a fleet that already existed — but first-run setup itself has still never run from a downloaded copy, and section 3 says what would happen to the path it records if it did.
- **The "damaged" dialog's exact words**, the second warning in the Open Anyway path, and whether a password was asked.
- ~~**The release workflow itself.**~~ Closed on 2026-09-12: v0.3.0 was cut, and every step ran — the certificate imported into a keychain made for the job, the app signed, Apple's answer waited for, the ticket stapled, the gate passed under `EXPECT_SEAL=notarized`, and the signing material deleted afterwards.
- **The x86_64 slice on a real Intel Mac.** Every x86_64 slice is cross-compiled on an Apple silicon Mac: this machine, the macOS runner of CI's `check` job, and the release job's runner. It has been run only under Rosetta 2 on Apple silicon, which translates it and is not an Intel processor. It has never run on a real Intel Mac, and there is no Intel Mac here to run it on. "An Intel Mac can run the release" follows from how a universal binary works; it has not been measured. (Section 8 is read from the same place for the x86_64 slice's signature: every slice of a universal binary is covered by the one bundle signature, and only the arm64 one has been run under it here.)
- **The image downloaded by a browser from a real release.** Everything above was measured on an image built and notarized here, with the quarantine attribute set by hand; nothing has yet made the trip through GitHub and a browser. The drag into Applications and the first double click from there are the acceptance this is waiting on, and they need a published tag.
- **A new version over an installed one:** whether Gatekeeper asks again. A notarized app is expected not to ask at all, whatever its code hash, but that has not been tried with two signed releases.
