#!/bin/sh
#
# notarize-dist-app.sh takes the signed zip `make dist-app` wrote, has Apple look at
# it, and staples Apple's answer into the app. That answer is the whole difference
# between a download a person opens with a double click and one macOS calls damaged
# or unverified: a Developer ID signature says who built the app, and only
# notarization says Apple has seen it. What each state looks like on the screen is in
# docs/engineering/release-app.md.
#
# The order below is the load-bearing part, and it is not the obvious one:
#
#   1. Submit the zip. Apple reads the app out of it and answers Accepted or not.
#   2. Staple the ticket to the *bundle*, not to the zip. There is nowhere in a zip
#      to put it.
#   3. Write the zip again, from the stapled bundle.
#
# Skipping step 3 leaves a published zip whose app has no ticket in it. Such an app
# still opens on a Mac that can reach Apple -- Gatekeeper asks online -- and refuses
# on one that cannot, which is the machine least able to explain why. The ticket
# lands at Contents/CodeResources, beside _CodeSignature rather than inside it
# (measured on a notarized app on this machine, magic "s8ch"), and it is outside what
# the signature seals, so stapling does not break the signature.
#
# It is a script of its own, and not a step inside build-dist-app.sh, because it goes
# to Apple over the network and takes minutes, while that script has to stay
# something `make test` can run offline on a machine with no certificate.
#
# Credentials come from the environment, never from an argument: an argument is
# visible in the process list and in a CI log, and one of these is a private key.
#
#   APPLE_API_KEY      path to the App Store Connect .p8 private key
#   APPLE_API_KEY_ID   the key's identifier
#   APPLE_API_ISSUER   the issuer the key belongs to
#
# The key must have the Admin role. An App Manager key fails with errors that talk
# about provisioning profiles and push notifications and never mention the role --
# learned the expensive way in the freshman-desktop release.
#
# Usage: notarize-dist-app.sh <dist-dir> <version>
set -eu

if [ "$#" -ne 2 ]; then
	echo "usage: $0 <dist-dir> <version>" >&2
	exit 2
fi

dist_dir=$1
version=$2

work=

fail() {
	echo "notarize-dist-app: $*" >&2
	[ -z "$work" ] || rm -rf "$work"
	exit 1
}

zip="$dist_dir/fleetdeck-$version-macos.zip"
[ -f "$zip" ] || fail "$zip is not there: build it with make dist-app first"

for var in APPLE_API_KEY APPLE_API_KEY_ID APPLE_API_ISSUER; do
	eval "value=\${$var:-}"
	[ -n "$value" ] || fail "$var is not set: notarization needs an App Store Connect API key (see docs/engineering/signing-secrets.md)"
done
[ -f "$APPLE_API_KEY" ] || fail "APPLE_API_KEY does not name a file"

work=$(mktemp -d)
# No EXIT trap: under the bash 3.2 that is /bin/sh on macOS it turns a syntax error
# into status 0, and every check after the error is then skipped silently (measured;
# see docs/engineering/release-app.md section 5). Removed by fail and at the end
# instead, and a trap for signals only.
trap 'rm -rf "$work"; exit 1' INT TERM HUP

echo "notarize-dist-app: submitting $zip to Apple; this waits for their answer and takes minutes"
# --wait, and no retry around it. A submission already in flight does not go faster
# for being sent twice, and a second one is a second thing to reconcile.
xcrun notarytool submit "$zip" \
	--key "$APPLE_API_KEY" --key-id "$APPLE_API_KEY_ID" --issuer "$APPLE_API_ISSUER" \
	--wait --output-format json >"$work/submission.json" 2>"$work/submission.err" ||
	fail "notarytool submit failed: $(cat "$work/submission.err")"

status=$(plutil -extract status raw "$work/submission.json" 2>/dev/null || echo "(no status in notarytool's answer)")
submission=$(plutil -extract id raw "$work/submission.json" 2>/dev/null || echo "")
if [ "$status" != "Accepted" ]; then
	# The submission log says which binary and which check, which the status never
	# does. Printed here because a rejection with no reason costs another round trip.
	[ -z "$submission" ] || xcrun notarytool log "$submission" \
		--key "$APPLE_API_KEY" --key-id "$APPLE_API_KEY_ID" --issuer "$APPLE_API_ISSUER" >&2 2>/dev/null || true
	fail "Apple answered '$status', not 'Accepted'"
fi
echo "notarize-dist-app: Apple accepted submission $submission"

ditto -x -k "$zip" "$work/app"
app="$work/app/fleetdeck.app"
[ -d "$app" ] || fail "$zip does not hold fleetdeck.app"

xcrun stapler staple "$app" || fail "the ticket would not staple to the bundle"
xcrun stapler validate "$app" >/dev/null || fail "the ticket stapled and then did not validate"

# Written beside the zip and moved over it, so an interrupted run leaves the signed
# zip it started from rather than half a file under the name a release publishes.
ditto -c -k --norsrc --noextattr --noacl --keepParent "$app" "$work/stapled.zip"
mv "$work/stapled.zip" "$zip"
rm -rf "$work"
echo "notarize-dist-app: $zip (notarized and stapled)"
