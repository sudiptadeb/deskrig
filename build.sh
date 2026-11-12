#!/bin/bash
# Builds deskrig for this Mac's architecture, or both, signed with a key that stays the same
# from build to build, so macOS keeps its permissions.
#
#   ./build.sh            # build/deskrig-darwin-<arch>, signed with this machine's own key
#   ./build.sh install    # and install it to ~/.local/bin/deskrig
#   ./build.sh all        # both architectures, for a release
#
# DESKRIG_SIGNING_KEY is the PEM (certificate then key) to sign with; releases use the
# project's. DESKRIG_VERSION sets the version; by default it comes from git.
set -euo pipefail

cd "$(dirname "$0")"
KEY=${DESKRIG_SIGNING_KEY:-$HOME/.deskrig/dev-signing.pem}
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

if [ ! -f "$KEY" ]; then
    echo "making this machine's signing key, $KEY"
    mkdir -p "$(dirname "$KEY")"
    (umask 077
     openssl req -x509 -newkey rsa:2048 -nodes -days 3650 -subj "/CN=deskrig local" \
         -addext "extendedKeyUsage=codeSigning" -addext "keyUsage=critical,digitalSignature" \
         -keyout "$WORK/key.pem" -out "$WORK/cert.pem" 2>/dev/null
     cat "$WORK/cert.pem" "$WORK/key.pem" > "$KEY")
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
