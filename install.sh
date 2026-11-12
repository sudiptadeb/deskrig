#!/bin/sh
# Install deskrig from a GitHub release, then run its first setup.
#
#   curl -fsSL https://raw.githubusercontent.com/sudiptadeb/deskrig/main/install.sh | sh
#
# DESKRIG_VERSION=v0.1.0 pins a release (default: the latest). DESKRIG_NO_SETUP=1 skips the
# setup, as `deskrig update` does.
#
# The binary always goes to ~/.local/bin/deskrig: macOS ties deskrig's permissions to that
# path, and an update replaces the file there, so they stay.
set -eu

REPO=sudiptadeb/deskrig
VERSION=${DESKRIG_VERSION:-latest}

say() { printf 'deskrig: %s\n' "$*" >&2; }
die() { say "$*"; exit 1; }

[ "$(uname -s)" = Darwin ] || die "deskrig runs on macOS for now"
case $(uname -m) in
x86_64 | amd64) arch=amd64 ;;
arm64 | aarch64) arch=arm64 ;;
*) die "unsupported architecture $(uname -m)" ;;
esac
# Under Rosetta uname reports x86_64; prefer the native binary.
if [ "$arch" = amd64 ] && [ "$(sysctl -n sysctl.proc_translated 2>/dev/null || echo 0)" = 1 ]; then
	arch=arm64
fi

if [ "$VERSION" = latest ]; then
	base="https://github.com/$REPO/releases/latest/download"
else
	base="https://github.com/$REPO/releases/download/$VERSION"
fi
asset="deskrig-darwin-$arch"
dir="$HOME/.local/bin"
mkdir -p "$dir"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

say "downloading $asset ($VERSION)"
curl -fsSL -o "$tmp/$asset" "$base/$asset" || die "download failed: $base/$asset"
curl -fsSL -o "$tmp/SHA256SUMS" "$base/SHA256SUMS" || die "download failed: $base/SHA256SUMS"
want=$(awk -v f="$asset" '$2 == f || $2 == "*" f {print $1}' "$tmp/SHA256SUMS")
[ -n "$want" ] || die "$asset is not listed in SHA256SUMS"
got=$(shasum -a 256 "$tmp/$asset" | awk '{print $1}')
[ "$want" = "$got" ] || die "checksum mismatch for $asset"

chmod 755 "$tmp/$asset"
# Replace, don't rewrite: macOS can kill a binary changed in place.
mv -f "$tmp/$asset" "$dir/deskrig"
say "installed $dir/deskrig $("$dir/deskrig" version)"

case ":$PATH:" in
*":$dir:"*) ;;
*) say "$dir is not on your PATH; add: export PATH=\"$dir:\$PATH\"" ;;
esac

if [ -z "${DESKRIG_NO_SETUP:-}" ]; then
	if [ -e /dev/tty ] && (: </dev/tty) 2>/dev/null; then
		"$dir/deskrig" setup </dev/tty
	else
		"$dir/deskrig" setup
	fi
fi
