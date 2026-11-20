package main

// `deskrig install <app.dmg | URL>`: the .app in a disk image, copied to /Applications on the
// target, replacing the one there. The same script runs here and inside a VM.

const installScript = `set -e
dmg=$1
mnt=$(mktemp -d /tmp/deskrig-dmg.XXXXXX)
/usr/bin/hdiutil attach -quiet -nobrowse -readonly -mountpoint "$mnt" "$dmg"
trap '/usr/bin/hdiutil detach -quiet "$mnt" 2>/dev/null || true; rmdir "$mnt" 2>/dev/null || true' EXIT
app=$(ls -d "$mnt"/*.app 2>/dev/null | head -1)
[ -n "$app" ] || { echo "no .app in $dmg" >&2; exit 1; }
base=$(basename "$app")
# A copy still running would keep running the old version, and the next open would only bring
# it forward: quit it, and wait until it has gone.
running=$(/usr/bin/pgrep -f "^/Applications/$base/Contents/MacOS/" | /usr/bin/tr '\n' ' ' || true)
if [ -n "$running" ]; then
  echo "quitting the running $base (pid ${running% })"
  kill $running 2>/dev/null || true
  for i in $(seq 1 100); do
    /usr/bin/pgrep -f "^/Applications/$base/Contents/MacOS/" >/dev/null || break
    [ "$i" = 50 ] && /usr/bin/pkill -9 -f "^/Applications/$base/Contents/MacOS/"
    sleep 0.1
  done
  if /usr/bin/pgrep -f "^/Applications/$base/Contents/MacOS/" >/dev/null; then
    echo "$base is still running after kill -9" >&2
    exit 1
  fi
fi
rm -rf "/Applications/$base"
/usr/bin/ditto "$app" "/Applications/$base"
info="/Applications/$base/Contents/Info"
echo "installed /Applications/$base: $(/usr/bin/defaults read "$info" CFBundleIdentifier)" \
  "$(/usr/bin/defaults read "$info" CFBundleShortVersionString 2>/dev/null)" \
  "($(/usr/bin/defaults read "$info" CFBundleVersion 2>/dev/null))"
`
