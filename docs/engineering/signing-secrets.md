# Signing secrets: what the release workflow needs, and how to produce them

The release workflow signs the app with a Developer ID certificate and has Apple notarize it. Both halves need material that cannot live in this repository: a private key for the certificate, and a private key for Apple's API. This page says which repository secrets to create and how to get each one's value.

It holds no values, and it never will. Every step below is something a person does on their own machine, with their own Apple account, and pastes into GitHub's secret form. Nothing here should be run by an agent or pasted into a chat: a `.p12` and a `.p8` are private keys, and a key that has been shown to anything is a key to replace.

Why the app needs this at all, and what a person sees without it, is in [release-app.md](release-app.md). What the build does with these secrets is in [`scripts/build-dist-app.sh`](../../scripts/build-dist-app.sh) and [`scripts/notarize-dist-app.sh`](../../scripts/notarize-dist-app.sh).

## The six secrets

| Secret | What it holds |
| --- | --- |
| `MACOS_SIGN_IDENTITY` | The certificate's name, as `codesign` spells it |
| `MACOS_CERTIFICATE_P12_BASE64` | The certificate and its private key, exported and base64-encoded |
| `MACOS_CERTIFICATE_PASSWORD` | The password set while exporting that `.p12` |
| `APPLE_API_KEY_P8` | The App Store Connect private key, the whole file |
| `APPLE_API_KEY_ID` | That key's identifier |
| `APPLE_API_ISSUER` | The issuer the key belongs to |

Create them under **Settings → Secrets and variables → Actions → New repository secret**.

The identity is a secret rather than a line in the `Makefile` for one reason: it carries a person's name and a team identifier. Neither is a secret in the cryptographic sense — both are readable in any signature this repository ships — but neither belongs in a file that is read far more often than a signature is inspected.

## Getting the certificate out of the keychain

The certificate is a **Developer ID Application** one. It is what signs an app distributed outside the App Store; a "Developer ID Installer", an "Apple Development" or an "Apple Distribution" certificate is a different thing and will not do. If there is none, create it in the Apple Developer account under Certificates, Identifiers & Profiles, and download and open it so it lands in the login keychain.

1. Read the exact name the build will be told to use:

   ```bash
   security find-identity -v -p codesigning | grep "Developer ID Application"
   ```

   The quoted part of that line — `Developer ID Application: Name (TEAMID)` — is `MACOS_SIGN_IDENTITY`, without the quotes and without the hash in front of it.

2. Export the certificate **with its private key**. In Keychain Access, under My Certificates, find the same certificate, unfold it so the key under it is included, right-click it and choose Export. Save it as a `.p12` and set a password when asked. That password is `MACOS_CERTIFICATE_PASSWORD`. An empty password will not do: the import step in the workflow needs one.

   A `.p12` exported without the private key imports without complaint and then signs nothing, failing later with a message about no identity being found. If Keychain Access offers no `.p12` in the format list, the key was not included in the selection.

3. Encode it for GitHub, which takes text:

   ```bash
   base64 --input certificate.p12 | pbcopy
   ```

   Paste that as `MACOS_CERTIFICATE_P12_BASE64`. Then delete the `.p12`; it has served its purpose, and it is the private key.

## Getting the App Store Connect key

This is the key `notarytool` authenticates with. It is created in App Store Connect under **Users and Access → Integrations → App Store Connect API**, and it is downloadable exactly once.

The role must be **Admin**. An App Manager key authenticates and then fails, and it fails with errors about provisioning profiles and push notification entitlements that say nothing about the role — this cost a day in the freshman-desktop release, and it is the single most expensive thing on this page to rediscover.

1. Create the key with the Admin role, or find an existing Admin one.
2. Download the `AuthKey_XXXXXXXXXX.p8`. It downloads once and cannot be downloaded again; keep it under `~/.appstoreconnect/private_keys/`, which is where Apple's tools look for it.
3. `APPLE_API_KEY_ID` is the ten-character identifier shown beside the key, and the one in the file's name.
4. `APPLE_API_ISSUER` is the issuer identifier shown above the key list, a UUID. It is the same for every key on the account.
5. `APPLE_API_KEY_P8` is the **whole contents** of the `.p8` file, `-----BEGIN PRIVATE KEY-----` line and all. Paste it as it is; GitHub keeps the line breaks, and the workflow writes it back to a file for `notarytool`.

## Checking it works without cutting a release

The secrets are only exercised by a tag push, which publishes. To try the same material locally first, with the certificate already in the keychain:

```bash
export APPLE_API_KEY=~/.appstoreconnect/private_keys/AuthKey_XXXXXXXXXX.p8
export APPLE_API_KEY_ID=XXXXXXXXXX
export APPLE_API_ISSUER=<the issuer uuid>
make dist-app DISTDIR=/tmp/fleetdeck-dist VERSION=v0.0.0-test \
    SIGN_IDENTITY="Developer ID Application: Name (TEAMID)"
make notarize-app DISTDIR=/tmp/fleetdeck-dist VERSION=v0.0.0-test
```

`notarize-app` ends by asking Gatekeeper about the app it has just built, so it passing is the answer. Notarizing a build under a version no tag will ever carry costs nothing: Apple notarizes the software, not the version.

## When something fails

- **The signing step hangs.** On a Mac, that is the keychain asking for permission to use the key, with nobody there to answer. In the workflow it means `security set-key-partition-list` did not run or did not take.
- **`no codesigning identity matching`**, from the build, before it compiles anything: `MACOS_SIGN_IDENTITY` does not match what is in the keychain, character for character, or the certificate did not import.
- **`Team is not yet configured for notarization`**, or a mention of profiles or push notifications: almost always the App Manager key. Use the Admin one.
- **Apple answers something other than `Accepted`.** The notarization step prints Apple's own submission log, which names the binary and the check that failed. The usual causes are a missing hardened runtime or a signature without a secure timestamp, and the build sets both.
