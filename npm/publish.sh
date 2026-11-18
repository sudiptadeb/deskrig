#!/bin/sh
# Publish the npm package for a GitHub release that is already out:
#
#   npm/publish.sh v1.2.3 [--dry-run]
#
# Pins each binary's sha256 and the version in a copy of this folder and publishes the copy,
# so the tree stays clean. Locally npm asks for your 2FA in the browser; in CI it uses the
# workflow's trusted-publisher identity.
set -eu
VERSION=${1:?usage: npm/publish.sh vX.Y.Z [--dry-run]}
shift
DIR=$(cd "$(dirname "$0")" && pwd)
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT INT TERM
cp "$DIR/package.json" "$DIR/run.js" "$DIR/pin.mjs" "$DIR/README.md" "$DIR/LICENSE" "$TMP/"
node "$TMP/pin.mjs" "$VERSION"
cd "$TMP" && npm publish "$@"
