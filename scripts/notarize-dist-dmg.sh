#!/bin/sh
#
# notarize-dist-dmg.sh takes the signed disk image `make dist-dmg` wrote, has Apple
# look at it, and staples Apple's answer into the image itself.
#
# The app inside has already been notarized and stapled by
# scripts/notarize-dist-app.sh, and that is not this. Gatekeeper asks about the
# thing a person opens, and the first thing a person opens is the image -- before
# macOS has seen the app at all. An image without a ticket of its own puts the
# "cannot be verified" dialog one step earlier in the journey than the app's own
# notarization was bought to prevent.
#
# The order here is shorter than the zip's, because a disk image has somewhere to
# put a ticket and a zip does not:
#
#   1. Submit the image. Apple reads it, and the app inside it, and answers.
#   2. Staple the ticket into the image. No third step: unlike the zip, nothing
#      has to be repacked afterwards, because the image is itself the container
#      the ticket lands in.
#
# It is a script of its own, and not a step inside build-dist-dmg.sh, because it
# goes to Apple over the network and takes minutes, while that script has to stay
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
# about provisioning profiles and push notifications and never mention the role.
#
# Usage: notarize-dist-dmg.sh <dist-dir> <version>
set -eu

if [ "$#" -ne 2 ]; then
	echo "usage: $0 <dist-dir> <version>" >&2
	exit 2
fi

dist_dir=$1
version=$2

work=

fail() {
	echo "notarize-dist-dmg: $*" >&2
	[ -z "$work" ] || rm -rf "$work"
	exit 1
}

dmg="$dist_dir/fleetdeck-$version-macos.dmg"
[ -f "$dmg" ] || fail "$dmg is not there: build it with make dist-dmg first"

for var in APPLE_API_KEY APPLE_API_KEY_ID APPLE_API_ISSUER; do
	eval "value=\${$var:-}"
	[ -n "$value" ] || fail "$var is not set: notarization needs an App Store Connect API key (see docs/engineering/signing-secrets.md)"
done
[ -f "$APPLE_API_KEY" ] || fail "APPLE_API_KEY does not name a file"

work=$(mktemp -d)
# No EXIT trap: under the bash 3.2 that is /bin/sh on macOS it turns a syntax
# error into status 0. See scripts/dist-app-checks.sh.
trap 'rm -rf "$work"; exit 1' INT TERM HUP

echo "notarize-dist-dmg: submitting $dmg to Apple; this waits for their answer and takes minutes"
# --wait, and no retry around it. A submission already in flight does not go
# faster for being sent twice, and a second one is a second thing to reconcile.
xcrun notarytool submit "$dmg" \
	--key "$APPLE_API_KEY" --key-id "$APPLE_API_KEY_ID" --issuer "$APPLE_API_ISSUER" \
	--wait --output-format json >"$work/submission.json" 2>"$work/submission.err" ||
	fail "notarytool submit failed: $(cat "$work/submission.err")"

status=$(plutil -extract status raw "$work/submission.json" 2>/dev/null || echo "(no status in notarytool's answer)")
submission=$(plutil -extract id raw "$work/submission.json" 2>/dev/null || echo "")
if [ "$status" != "Accepted" ]; then
	# The submission log says which piece and which check, which the status never
	# does. Printed here because a rejection with no reason costs another round trip.
	[ -z "$submission" ] || xcrun notarytool log "$submission" \
		--key "$APPLE_API_KEY" --key-id "$APPLE_API_KEY_ID" --issuer "$APPLE_API_ISSUER" >&2 2>/dev/null || true
	fail "Apple answered '$status', not 'Accepted'"
fi
echo "notarize-dist-dmg: Apple accepted submission $submission"

xcrun stapler staple "$dmg" || fail "the ticket would not staple to the image"
xcrun stapler validate "$dmg" >/dev/null || fail "the ticket stapled and then did not validate"

rm -rf "$work"
echo "notarize-dist-dmg: $dmg (notarized and stapled)"
