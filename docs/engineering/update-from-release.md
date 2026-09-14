# Updating an app installed from a release: field notes

These are facts from making the fleetdeck app update itself when it was installed from a release rather than built from a checkout (2026-09-12). Each says how it was established: **measured** (observed on this machine), **read** (in code, a man page or Apple's documentation), or **decided** (a choice, with its reason).

Read this before changing `internal/supervisor/seal.go`, `internal/supervisor/releasesource.go`, `cmd/fleetdeck-window/way.go` or `cmd/fleetdeck-window/atstart.go`. Read section 2 before anyone suggests that checking a downloaded app "just needs `codesign --verify`", and section 1 before anyone adds a GitHub API call.

The companion page is [release-app.md](release-app.md), which is about making the release; this one is about installing the next one over it. The person's side is in the getting-started guide, [in English](../en/getting-started.md#updating) and [in Russian](../ru/getting-started.md#обновление).

## 0. What was wrong, and why nobody complained about it

**Measured on the operator's machine, 2026-09-12:** `/Applications/fleetdeck.app`, v0.3.0, signed and notarized, running. It had no Update button. The window's log said `no update button: this app was built without its source tree written in`.

The mechanism that existed updated a checkout: `git pull`, build beside the installed app, hand the panel over, swap. A release is built on a CI runner, so it carries no checkout, so `updateUnavailable` returned a reason and no button was created (`cmd/fleetdeck-window/update.go`, as it was).

**Decided: the silence was the defect, not the missing feature.** A person with no Update button does not conclude "this build cannot update itself". They conclude nothing, because there is nothing on screen to conclude it from — and so nobody reported it for a whole release. Every refusal in this page is therefore loud: the button exists in every build, and a build that cannot use it says which of the three reasons applies.

## 1. Asking what the newest version is, without the API

- **Measured: the anonymous GitHub API allowance was already spent.** `api.github.com/rate_limit` on the operator's machine answered `limit 60, remaining 0, used 60`, resetting 40 minutes later. Sixty calls an hour is for a whole address, and other tooling on the same machine — `gh`, other sessions — shares it. An update check built on the API would answer 403 on exactly the machines that do the most work.
- **Measured: `/releases/latest` answers with the tag in a redirect.** `https://github.com/kroticw/fleetdeck/releases/latest` gives `302` with `Location: .../releases/tag/v0.3.0`. It is not the API, nothing counts it, and the tag is the whole answer. The download address is then predictable: `/releases/download/<tag>/fleetdeck-<tag>-macos.zip`, the name `scripts/build-dist-app.sh` writes. Built from a name and not read off the release, which cuts both ways: a release that carries other assets — since 2026-09-12 it also carries a `.dmg` a person installs from — cannot lure this onto one of them, and a release that stopped carrying the zip would leave every already-installed copy asking for a file that is not there, for good. The zip is the update channel and stays one; see `docs/engineering/release-app.md` section 9.
- **Decided: the redirect is not followed.** Following it downloads a web page for a string already in its header. `Releases.Latest` sets `CheckRedirect` to stop and read the header.
- **Decided: a release older than the running one is not an update.** It answers "nothing newer" rather than an error, because it is not a fault — but installing it would move a person backwards onto a build they have already left, which is the one outcome an update must never produce quietly.
- **Measured: versions cannot be compared as text.** `"v0.10.0" < "v0.9.0"` as strings, so an app on v0.9.0 that compared them that way would refuse every update for ever. `Newer` parses the three numbers.
- **Decided: the releases address is a constant in the binary.** An app that could be pointed somewhere else by a setting, an environment variable or a config file would be a way to hand somebody a different program, and the whole of section 2 would then be guarding a door with the wall removed.

## 2. Checking what came back: three questions, and none answers another's

This is the part the feature is for. All three were measured on 2026-09-12, on the operator's machine, against three bundles built from the same app: the notarized v0.3.0 in `/Applications`, a copy re-signed ad hoc, and a copy with one byte appended to its icon.

| | `codesign --verify --deep --strict` | `codesign --display` | `spctl --assess --type execute` |
| --- | --- | --- | --- |
| Notarized release | status 0 | Developer ID, team `PTLLPQ8LY4`, ticket stapled | `accepted`, `source=Notarized Developer ID` |
| Re-signed ad hoc | **status 0** | `Signature=adhoc`, `TeamIdentifier=not set` | `rejected`, status 3 |
| One byte changed | status 1, `a sealed resource is missing or invalid` | **Developer ID, team `PTLLPQ8LY4`, ticket stapled** | status 1 |

The two bold cells are the whole argument:

- **Measured: `--verify` passes an ad-hoc bundle.** It asks whether the contents match the signature, not who made the signature. Anybody can seal anything ad hoc — it is what `make window-app` does on a developer's own machine — so integrity alone says nothing about origin.
- **Measured: `--display` lies about a bundle changed after signing.** It reads the signature and does not check it, so a tampered release still prints the original certificate, the original team identifier and `Notarization Ticket=stapled`. Every identity fact is attacker-controlled until integrity has passed.

**Decided: integrity first, then identity, then the system's own verdict.** `Seal.Verify` runs them in that order, and the order is load-bearing rather than tidy.

**Measured on a real signature from another team, 2026-09-12.** A copy of the installed app was re-signed with a different Apple certificate that happens to be on this machine — `--options runtime --timestamp --deep`, so everything about it is as a release would be:

```text
Authority=Apple Development: … (D3P4AYQ2B2)
Authority=Apple Worldwide Developer Relations Certification Authority
Authority=Apple Root CA
Timestamp=12 Sep 2026 at 13:26:26
TeamIdentifier=GPTSXA3D2U          ← not ours
codesign --verify --deep --strict  → status 0
spctl --assess --type execute      → rejected, status 3
```

`Verify` refused it as `not-developer-id`: an **Apple Development** certificate is not a **Developer ID Application** one, and the chain goes through Apple's Worldwide Developer Relations authority rather than the Developer ID one. So a signature that is real, current and made by a real Apple developer still does not get an app installed here. `codesign --verify` passing it with status 0 is the third time on this page that integrity alone proves nothing.

**Not measured: a genuine Developer ID certificate belonging to another team**, which would be refused as `wrong-team` rather than `not-developer-id`. There is one Developer ID on this machine. That path is covered by a test that asks for a team the installed app is not signed by.

- **Decided: the team identifier is read from the running app, not written into the code.** `TeamIDOf` asks `codesign` about the bundle the window is running from. A constant would stop anybody else's fork from ever updating itself; reading it means a fork updates from its own releases and nothing signed by anyone else can replace this app. An attacker who can already rewrite `/Applications/fleetdeck.app` has won before this question is asked, so reading from there costs nothing.
- **Measured: `stapler` lives inside Xcode.** `xcrun --find stapler` answers `/Applications/Xcode.app/Contents/Developer/usr/bin/stapler`. `scripts/verify-dist-app.sh` may use it — that gate runs on a CI runner — but the app may not: it runs on a person's Mac, which need never have had Xcode. `codesign` (`/usr/bin`) and `spctl` (`/usr/sbin`) are part of macOS. Both are called by absolute path, never through `PATH`.
- **Decided: `spctl` answering `accepted` is not enough on its own.** A rule added to a machine makes it accept an app for a reason that is not notarization, so the source line is checked too: only `source=Notarized Developer ID` counts. This is the check a mutation pass found missing (section 5).
- **Decided: the check runs on the bundle in the place it will be started from.** `Stage` unpacks into the staging directory beside the installed app, checks the bundle there, and moves it within that directory. Checking one copy and starting another leaves a gap between the two, however short.
- **Decided: nothing survives a refusal.** A rejected download left on disk is a program a person could find in Finder and open.

## 3. Quarantine: the attribute this must not set

**Read, from [release-app.md](release-app.md) section 3, measured there on the real v0.3.0:** the `com.apple.quarantine` attribute is what arms App Translocation, and a mouse drag in Finder is what disarms it. An app moved into `/Applications` programmatically and still carrying the attribute runs from `/private/var/folders/…/AppTranslocation/…`.

**Decided: the downloaded app is not given a quarantine attribute.** Go's HTTP client sets none — it is LaunchServices and browsers that do — and the update must not add one. If it did, the replaced app would run translocated, and `os.Executable()`, the canonical path and `ensureStatusline` would all record a path that disappears.

What that gives up is Gatekeeper's own check at launch, and section 2 is what replaces it — with a check that is stricter, because Gatekeeper asks "did Apple notarize this" and this also asks "is this the same team as the app being replaced".

**Not yet measured:** that the app after an update runs from `/Applications/fleetdeck.app` and not from a translocation path. It is on the acceptance list.

## 4. Replacing the running app: what was reused, and what was not

**Decided: one update path, two sources.** `supervisor.Source` is the seam. Below it nothing is duplicated — the lock, the staging directory beside the installed app, the handover to a new window, the swap through `renamex_np(RENAME_SWAP)`, and the invariant that something is always at the canonical path. `TreeSource` builds from a checkout; `ReleaseSource` downloads a release. The end-to-end tests that held the tree path before the change hold it after.

- **Found in v0.10.1: two holes in how v0.10.0's new window heard its keeper during a takeover.** An event arriving while the takeover's 16-slot buffer was full was dropped, and the wait for the panel took any answer from a panel of its own keeper without checking it was the one started during that wait. v0.10.1 keeps every event (`KeeperEvents`) and counts only the answer of the panel whose start it saw. An update from v0.10.0 runs the new window's code, v0.10.1's, so neither reaches the operator's update.
- **Measured: the release panel reports a commit, so `Takeover` needed no change.** `/api/snapshot` from the installed v0.3.0 answers `revision: 76d4cc2373cc…`, the commit of the tag, because the release is built inside a checkout and `go build` stamps it. The new window compares its own `ownRevision()` with what its panel reports, and that comparison works identically for both kinds of build.
- **Decided, and it is a behaviour change: checking no longer fast-forwards the tree.** `Update` used to call `Tree.Forward` and then compare; `TreeSource.Check` calls `Tree.Check`, which fetches and reports without moving anything. Somebody's working copy should not move because a button asked a question.
- **Decided: the lock lives beside whatever the build updates from** — the source tree for a build that has one, the installed app otherwise — so that two windows of the same installed app cannot update it at once.
- **Measured: `/Applications` is writable by the admin group** (`drwxrwxr-x root admin`), and the staging directory is a sibling of the app, so the swap is a rename within one filesystem. A person not in that group gets a refusal naming the directory, before anything is downloaded.

## 5. The mutation pass, and the mutant that lived

**Measured.** Four mutants against the three checks in `Seal.Verify`, each applied to the final code and reverted after:

| Mutant | Result |
| --- | --- |
| The integrity check never fires | killed |
| The team identifier is never compared | killed |
| The Developer ID check never fires | killed |
| `source=Notarized Developer ID` is not required | **survived** |

The survivor was a real hole: nothing asked whether Gatekeeper had accepted the app *because Apple notarized it* rather than for some other reason. It could not be reached with the real `spctl` — making it accept an app for another reason means adding a rule to the machine's security policy, which no test may do — so `spctl` is stood in for in that one case, and what is measured there is this code's reading of the answer rather than the system's behaviour. Two tests were written, the mutant was killed, and the pass then stood at four of four.

**The lesson is the one release-app.md section 8 already recorded in other words:** a mutant that survives is worth more than the ones that die, and a pass where everything dies on the first try has usually measured the harness.

## 6. Finding a newer version while the window runs

This section replaced "the startup check" on 2026-09-13 (`cmd/fleetdeck-window/watch.go`).

- **Decided, by the operator, 2026-09-13: the Update button is on screen only while there is something to update to.** Its appearing is the notice. From 2026-09-12 the button stood there always and meant "press and I will go and look": it was pressed for nothing, and on the day a release came out nothing on screen changed. This reverses the 2026-09-12 rule that every build shows the button, including a build that cannot update; such a build now shows nothing, and says why in its log.
- **Decided: the window looks while it runs, not only at startup.** The 2026-09-12 rule — one question at startup, at most once a day, no timer — could not satisfy "appears by itself": a window left open for a week never learned of anything.
- **Decided: the releases page is asked at most every six hours (`askEvery`).** A release published in the morning is on screen by the afternoon, and the page gets at most four HEAD requests a day per machine however long the window is open. Section 1 is why these requests cost nothing against the API allowance.
- **Decided: a local look every ten minutes (`lookEvery`) decides whether to ask, by the wall clock and the mark on disk.** Not a timer set for six hours: a Mac's monotonic clock stops while it sleeps, so such a timer on a laptop closed overnight fires hours late. The look reads a file and asks nothing while the answer there is fresh.
- **Decided: the mark keeps the answer, not only the time of the question.** `~/.config/fleetdeck/last-update-check` is JSON: when, for which running version, and what was offered. A window opened again inside the six hours shows what the last question found; with only the time it would show no button for the rest of the interval, a false "nothing newer". An answer about another running version is not used: after an update the version it offered is the one running.
- **Decided: no network is not an answer.** A question that fails tells the page nothing — neither "there is a version" nor "there is none" — keeps a version already found, and is not written down, so the next look, within ten minutes, asks again. That is how a network coming back is noticed with no hook into the system's network state. It also means an offline laptop tries every ten minutes; the attempt fails before it leaves the machine.
- **Decided: finding nothing is said only when it takes something back.** The page hears "none" when a version it was shown is gone, not after every look.
- **Decided: the page asks what is known as it loads.** A report sent before the page was there, or before a reload, is lost; `fleetdeckUpdateKnown` answers with what the window knows. The page takes only a found version from that answer, so it cannot undo a report that arrived while the answer was on its way.
- **Kept: a question nobody asked says nothing about refusals.** "GitHub could not be reached" on a laptop opening on a train is noise. Pressing the button, once there is one, shows every refusal.
- **Kept: every way of failing to read the mark means ask** — unreadable, from the future, in the plain-time format of the previous version, or missing.
- **Changed: a window started by a handover looks too.** It stays open as long as the one it replaced would have, and the answer kept for the version before it does not apply.

## 7. Refusals are codes, not sentences

**Decided.** The window sends the page a reason code and the particulars separately: `seal:wrong-team` with `the downloaded app is signed by team ZZZZZZZZZZ, not PTLLPQ8LY4`. The page is read in the reader's own language (`web/js/i18n.js` picks by `navigator.language`), and a sentence built in Go arrives already in English. The code chooses the sentence; the detail carries what a person acts on — which team, how much room, what could not be reached.

A code the page does not know still shows the detail rather than an empty line: an older window talking to a newer page must not produce a blank where a refusal belongs.

## 8. What the acceptance measured, and on what

**Measured, 2026-09-12, against the real releases page** (`FLEETDECK_NETWORK_TEST=1`, `TestARealReleaseCanReplaceAnInstalledApp`). Not a stand-in server and not a local file:

- The releases page was asked what was newest and answered `v0.4.0` — published that morning, and not by this work.
- The 39.6 MB archive was downloaded from GitHub, unpacked with `ditto`, and put through all three checks. It passed: whole, Developer ID, team `PTLLPQ8LY4` — the same team as the app being replaced — notarized, `source=Notarized Developer ID`.
- A bundle at a canonical path of the test's own, holding v0.3.0, was exchanged with it through `renamex_np(RENAME_SWAP)`.
- The app at the canonical path was then asked what it was, by running its panel rather than by reading anything around it: `v0.4.0`. The bundle swapped out, kept beside it, still answered `v0.3.0`.
- No quarantine attribute anywhere on the installed app (section 3).

The whole thing took 8.5 seconds.

### On a stand that was really installed from a release

**Measured, 2026-09-12** (`TestAStandInstalledFromAReleaseUpdatesItself`, in `cmd/fleetdeck-window`). The run above leaves out the window; this one does not.

A stand was built the way a release is — `make dist-app VERSION=v0.3.9 SIGN_IDENTITY=…`, both architectures, Developer ID, hardened runtime, secure timestamp, through the release gate under `EXPECT_SEAL=developer-id` — and unpacked to a canonical path of its own. No notarization ticket: what is checked is what comes down, not what is running. It was given a `HOME` and a port of its own, the port in a configuration file rather than on a command line, because the panel the new window starts is started by that window with its own arguments.

What ran, in order, with nothing stubbed below the press:

```text
check
download v0.4.0          ← the real releases page, 39.6 MB
verify   v0.4.0          ← whole, Developer ID, team PTLLPQ8LY4, notarized
handover
handover:alive           ← a second process, from the downloaded bundle
handover:panel 4afe4536…
handover:swapped
handover:done
done     v0.4.0
```

Afterwards: the app at the canonical path answered `v0.4.0` when its panel was run; the panel listening on the stand's port reported `v0.4.0` and an executable inside the canonical bundle; the replaced `v0.3.9` was beside it in `.fleetdeck-update/`; and no quarantine attribute anywhere. 5.6 seconds.

**Measured: `updateWay` chose the release path on the stand** without being told to — the decision the whole change turns on, made from the stand's own signature and version.

**Measured: nothing reached the operator's fleet.** Their panel was answering on 7777 throughout, from `/Applications/fleetdeck.app`, and afterwards still reported `v0.3.0` with its three sessions. No `.fleetdeck-update` appeared in `/Applications`.

**Measured, and it was the test's error rather than the app's:** the panel reports its path through `EvalSymlinks`, so on macOS a bundle under `/var/folders/…` is reported under `/private/var/folders/…`. The first run failed on that comparison. It is not translocation — the path holds no `AppTranslocation` component, which is now checked for by name.

### In CI, from v0.10.0 as released

**Run by CI's `update-from-v0-10-0`**, on `macos-26`, for every pull request and every push to master (`scripts/updatecheck`). The installed app is v0.10.0's release zip, downloaded from GitHub and put at `/Applications/fleetdeck.app` on the runner. The program is built inside a checkout of the tag — `v0.10.0^{commit}` is compared with `fec135e`, because the runner has no key to verify the tag's signature — against that tag's `internal/supervisor`, and runs its `Update.Run`: v0.10.0's 2436 ms handover deadline, and v0.10.0's keeper stopping and starting v0.10.0's panel. The new window is this branch's app, started the way v0.10.0 starts it — `--url --handover --canonical` and no deadline flag, as the program's child, with the stand's `FLEETDECK_STAND_SOCKET`. What the program copies from v0.10.0's window, which cannot be imported — the deadline, the launch arguments, the panel's arguments and the keeper's settings — is compared with the tag's source before anything runs.

It passes only when the new window reports `alive`, `panel`, `swapped` and `done` in that order within the deadline; the panel at the URL reports this branch's commit and an executable inside `/Applications/fleetdeck.app`; and `/Applications/.fleetdeck-update/fleetdeck.app` answers `v0.10.0`. LaunchServices is then looked at in two phases. While the program — the old window — still runs and the new window has said the handover is done, so the bundle swapped out is still on disk, three looks only observe: the time each `lsregister -dump` starts, whether that bundle is on disk then, the records it holds, and what `NSWorkspace.URLForApplicationWithBundleIdentifier` opens for `dev.fleetdeck.stand` (this build) and `dev.fleetdeck.window` (v0.10.0). On the operator's machine that moment lasts a fraction of a second, since their v0.10.0 window quits as soon as it reads done; the program stretches it. Once the program has exited, a step of its own requires the new window to remove that bundle within 30 s, and then, in each of ten looks, `dev.fleetdeck.stand` to open `/Applications/fleetdeck.app`, `dev.fleetdeck.window` to open nothing in `/Applications/.fleetdeck-update`, and no record of a path in `/Applications/.fleetdeck-update` that is on disk as the look starts. A record of a path gone from disk is printed and not failed on. The first and last dump of each phase are kept in the job's artifact. The old version is pinned to v0.10.0.

**Measured on the runner, 2026-09-14.** Before the new window took the panel over ahead of making its window, the job failed by the deadline in every run: making the web view alone took 1.1–1.6 s after the process started, the glass frame another 0.8 s or more, and the takeover had not begun when v0.10.0 gave up. With the takeover first, the new window reported `alive` 51 ms, `panel` 399 ms, `swapped` 809 ms and `done` 963 ms after it was started, against v0.10.0's 2436 ms. One `lsregister -dump` took 8 s.

Each mutant was a temporary commit on the branch, reverted after its run:

| Mutant | Result |
| --- | --- |
| The takeover is not subscribed to the keeper's events | killed: "the new window reported "alive" and then nothing", 2456 ms after it was started (run 34864215411) |
| The new window never reports done | killed: "the new window reported "swapped" and then nothing", 2455 ms after it was started (run 34864654648) |
| The window does not tell LaunchServices again once it is ready | survived while LaunchServices was read only after the bundle swapped out was removed (run 34865270413); killed while it was also read with that bundle still on disk: "LaunchServices still knows what the update swapped out: /Applications/.fleetdeck-update/fleetdeck.app", in every look for 60 s (run 34866422965). That read was then dropped as a condition — see below — so this job no longer catches it |
| The takeover does not tell LaunchServices where the app is before it reports swapped | killed: "LaunchServices still knows what the update swapped out: /Applications/.fleetdeck-update/fleetdeck.app", in every look for 60 s, the first holding only that path (run 34866996904) |
| Retire never removes the bundle swapped out — in the first try, the tries every retireEvery or at the next start — but says it did | killed: "the new window removing the bundle swapped out at /Applications/.fleetdeck-update/fleetdeck.app did not happen within 30s"; in all ten looks taken all the same, with that bundle on disk and the old window gone, `dev.fleetdeck.window` opened nothing and LaunchServices held only `/Applications/fleetdeck.app` (run 34870684087) |

**Measured, from that mutant:** the new window, started as v0.10.0 starts it, checks in with LaunchServices from the staged path when its window is made, and the record stays while the bundle is on disk. Removing the bundle swapped out took the record with it, which is why a LaunchServices check made only after that removal could not tell a window that registers again from one that does not.

**Observed, from the mutant that drops the takeover's own registration before swapped (run 34866996904):** the first look held only the staged path, as record `0x1ae4`; every later look held `/Applications/fleetdeck.app` as `0x1ae8` and the staged path again, as a new record `0x1aec`. The window's registration after it is made took the first staged record away, and something registered the staged path again straight after it.

**Measured without a mutant (run 34867562033):** the new window said "telling it again that the app is at /Applications/fleetdeck.app" at 16:20:06.538. The LaunchServices dump then records `/Applications/fleetdeck.app` as `0x1ad0`, registered at 16:20:06, and `/Applications/.fleetdeck-update/fleetdeck.app` as `0x1ad8` — `dev.fleetdeck.window`, team `PTLLPQ8LY4`, version 0.10, the version swapped out — registered at 16:20:07, while that bundle was still on disk. The bundle was removed at 16:20:15.763, and the record stayed, marked "Bundle node not found on disk", for every look of the next 60 s. So LaunchServices registers the staged path again about half a second after it is told to forget it, as long as the bundle is there, and nothing in this program or the window holds it off.

**Measured: what the app's identifier opens meanwhile (run 34869975049).** With the bundle swapped out still on disk, `URLForApplicationWithBundleIdentifier("dev.fleetdeck.window")` answered nothing in the first look, and `/Applications/.fleetdeck-update/fleetdeck.app` — v0.10.0 — in the two looks that started 10 and 18 s after the new window said the handover was done. Once the bundle was removed it answered nothing in all ten looks, while `dev.fleetdeck.stand` opened `/Applications/fleetdeck.app` throughout. On a person's machine both are one identifier, so until the bundle swapped out is removed, opening the app by its identifier may start the version just replaced; removing the bundle is what holds.

This job therefore requires nothing of LaunchServices while the bundle swapped out is on disk, and a window that does not tell LaunchServices again once it is ready is no longer caught here: `TestAWindowStartedByAnUpdateTakesThePanelOverBeforeItsWindowIsMade` holds it.

## 9. Not verified

- **The button, the download and the seal, in CI's update from v0.10.0.** `update-from-v0-10-0` replaces exactly three things: the source — this branch's app is unpacked from a local archive, where v0.10.0 downloads a release; `Seal.Verify` — not asked, since this branch's app is sealed ad hoc on a runner with no certificate; and the press — `Update.Run` is called directly. A green job means v0.10.0's handover reaches done with this branch's window on a runner. It does not mean the button, the download or the signature check were tried.
- **The press itself.** Everything above the button is measured; the button calls `supervisor.Update` through `runUpdate`, and the acceptance calls `supervisor.Update`. A click into a WKWebView cannot be made from a test.
- **An app in `/Applications` replacing itself**, rather than a stand at a path of its own. The difference is the directory's permissions, which are checked and refused in words, and App Translocation, which needs a quarantine attribute this never sets.
- **Two published releases.** The stand was built locally rather than downloaded, so "the app a person installed" has still not been the thing doing the updating.
- **A machine with no Xcode and no Command Line Tools.** `codesign` and `spctl` are part of macOS and are called by absolute path, so this is expected to hold; it is read from where the binaries live, not measured on such a machine.
- **A person not in the admin group**, who cannot write beside the installed app. The refusal exists and is tested with a stand-in path; it has not been met by a real account.
- **What Apple revoking a certificate looks like from inside the app.** `spctl` is expected to refuse, and the refusal would arrive as `seal:not-notarized`.
- **A download interrupted part way** — a network that drops at 20 MB. The archive is removed on any failure, so a retry starts clean; this is read from the code rather than measured.
