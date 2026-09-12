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
- **Measured: `/releases/latest` answers with the tag in a redirect.** `https://github.com/kroticw/fleetdeck/releases/latest` gives `302` with `Location: .../releases/tag/v0.3.0`. It is not the API, nothing counts it, and the tag is the whole answer. The download address is then predictable: `/releases/download/<tag>/fleetdeck-<tag>-macos.zip`, the name `scripts/build-dist-app.sh` writes.
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

## 6. The startup check

- **Decided, by the operator, 2026-09-12: one question at startup, at most once a day, silent unless the answer is yes.** Not a poll: no timer runs while the window is open and nothing repeats. Opening and closing the window ten times in an afternoon asks GitHub once.
- **Decided: the startup check says nothing about refusals.** Nobody asked the question, so "GitHub could not be reached" on a laptop opening on a train is noise, and noise is what gets a notice ignored. Pressing the button asks out loud, and then every refusal is shown.
- **Decided: every way of failing to read the mark means ask.** An unreadable mark, a mark from the future, no mark at all — all answer yes. One extra redirect is the cheap mistake; a check that quietly stops asking is the expensive one, and that is the defect this whole page is about.
- **Decided: a window started by a handover asks nothing.** It has just been installed and knows it is the newest.

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

## 9. Not verified

- **The last mile, on a person's screen.** What the acceptance above leaves out is the window: no web view, no handover between two windows, no panel on a port. Those are the same code the tree path has used since 2026-09-11 and the end-to-end tests cover them — but an installed release replacing itself with the next one, from the button, on a screen, has not been done. It needs two published releases carrying this code.
- **That the updated app runs untranslocated** (section 3). The attribute is measured to be absent, which is the thing that arms translocation; that the app then runs from `/Applications` was not watched.
- **A machine with no Xcode and no Command Line Tools.** `codesign` and `spctl` are part of macOS and are called by absolute path, so this is expected to hold; it is read from where the binaries live, not measured on such a machine.
- **A person not in the admin group**, who cannot write beside the installed app. The refusal exists and is tested with a stand-in path; it has not been met by a real account.
- **What Apple revoking a certificate looks like from inside the app.** `spctl` is expected to refuse, and the refusal would arrive as `seal:not-notarized`.
- **A download interrupted part way** — a network that drops at 20 MB. The archive is removed on any failure, so a retry starts clean; this is read from the code rather than measured.
