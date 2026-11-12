#!/bin/bash
# Builds deskrig for this Mac's architecture, or both, signed with the project's key, so a
# source build is the same program to macOS as a release and keeps its permissions.
#
#   ./build.sh            # build/deskrig-darwin-<arch>
#   ./build.sh install    # and install it to ~/.local/bin/deskrig
#   ./build.sh all        # both architectures, for a release
#
# The key, release-signing.pem (certificate then key, PEM), is read from $KEYKOSH_ROOT/deskrig
# when KEYKOSH_ROOT is set, else from .keys/ in this repo. DESKRIG_SIGNING_KEY names another
# file (CI's). DESKRIG_VERSION sets the version; by default it comes from git.
set -euo pipefail

cd "$(dirname "$0")"
KEYS=${KEYKOSH_ROOT:+$KEYKOSH_ROOT/deskrig}
KEYS=${KEYS:-$PWD/.keys}
KEY=${DESKRIG_SIGNING_KEY:-$KEYS/release-signing.pem}
if [ ! -f "$KEY" ]; then
    echo "no signing key at $KEY" >&2
    [ -n "${KEYKOSH_ROOT:-}" ] && [ -z "${DESKRIG_SIGNING_KEY:-}" ] && echo "run keykosh unlock" >&2
    echo "Without the project's key, put your own self-signed code-signing PEM there; macOS then" >&2
    echo "sees your builds as a different program from the releases." >&2
    exit 1
fi
IDENTIFIER=io.github.sudiptadeb.deskrig
VERSION=${DESKRIG_VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}
RCODESIGN_VERSION=0.29.0
RCODESIGN_URL=https://github.com/indygreg/apple-platform-rs/releases/download/apple-codesign%2F$RCODESIGN_VERSION/apple-codesign-$RCODESIGN_VERSION-macos-universal.tar.gz

mkdir -p build/tools
WORK=$(mktemp -d build/work.XXXXXX)
trap 'rm -rf "$WORK"' EXIT

# Apple's codesign won't sign with a key macOS doesn't trust, so rcodesign signs.
RCODESIGN=$(command -v rcodesign || echo build/tools/rcodesign)
if [ ! -x "$RCODESIGN" ]; then
    curl -fsSL -o "$WORK/r.tar.gz" "$RCODESIGN_URL"
    curl -fsSL -o "$WORK/r.sha256" "$RCODESIGN_URL.sha256"
    [ "$(shasum -a 256 "$WORK/r.tar.gz" | cut -d' ' -f1)" = "$(cut -d' ' -f1 "$WORK/r.sha256")" ]
    tar xzf "$WORK/r.tar.gz" -C "$WORK"
    install -m 755 "$(find "$WORK" -type f -name rcodesign | head -1)" build/tools/rcodesign
fi

# What macOS checks the permissions against: this identifier, signed by this key.
LEAF=$(openssl x509 -in "$KEY" -outform der | shasum -a 1 | cut -d' ' -f1)
REQUIREMENT="identifier \"$IDENTIFIER\" and certificate leaf = H\"$LEAF\""
echo "$REQUIREMENT" | csreq -r- -b "$WORK/requirement.bin"

ARCHES=$(uname -m)
[ "${1:-}" = all ] && ARCHES="arm64 x86_64"
for arch in $ARCHES; do
    goarch=$arch
    [ "$arch" = x86_64 ] && goarch=amd64
    out=build/deskrig-darwin-$goarch
    MACOSX_DEPLOYMENT_TARGET=14.0 CGO_ENABLED=1 GOOS=darwin GOARCH=$goarch CC="clang -arch $arch" \
        go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "$WORK/deskrig" .
    "$RCODESIGN" sign --pem-file "$KEY" --timestamp-url none --binary-identifier "$IDENTIFIER" \
        --code-requirements-path "$WORK/requirement.bin" "$WORK/deskrig" > /dev/null
    codesign --verify --strict -R="$REQUIREMENT" "$WORK/deskrig"
    mv -f "$WORK/deskrig" "$out"
    echo "built $out $VERSION"
done

if [ "${1:-}" = install ]; then
    mkdir -p "$HOME/.local/bin"
    cp "build/deskrig-darwin-$(uname -m | sed s/x86_64/amd64/)" "$HOME/.local/bin/deskrig.new"
    # Replace, don't rewrite: macOS can kill a binary changed in place, and keeps the
    # permissions for this path.
    mv -f "$HOME/.local/bin/deskrig.new" "$HOME/.local/bin/deskrig"
    echo "installed $HOME/.local/bin/deskrig"
fi
