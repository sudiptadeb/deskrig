# deskrig

One testing interface for agents: the commands an agent needs to test an app the way a person
would (screenshots of one window, real clicks, keys and trackpad scrolls, a live stream of
frames, installing the app), the same on every **target**. A test script written once runs on
this Mac's own desktop or in a clean macOS VM by changing only the target.

| Target kind | Where | Status |
|---|---|---|
| `local` | this Mac's desktop | now |
| `tart` | a macOS VM under [Tart](https://tart.run) | now |
| `android` | an Android emulator | next |
| Linux VMs, iOS simulators | | later |

A target kind exists on a machine that can run it; macOS VMs need a Mac.

## Install

    curl -fsSL https://raw.githubusercontent.com/sudiptadeb/deskrig/main/install.sh | sh

Or ask your agent:

> Install deskrig: follow https://raw.githubusercontent.com/sudiptadeb/deskrig/main/docs/agent-install.md

The binary goes to `~/.local/bin/deskrig`, always: macOS ties deskrig's permissions to that
path, and `deskrig update` replaces the file there. Then the installer runs the first setup.

## First setup

`deskrig setup` does each of these, asking first where it downloads anything:

1. **Permissions, once, one at a time.** Everything deskrig may need now or later, so updates
   never ask again: Screen & System Audio Recording, Accessibility, Input Monitoring, Full Disk
   Access, Automation (System Events), camera, microphone, Bluetooth, App Management, Developer
   Tools and Local Network. For each it asks macOS, opens the page in System Settings and waits
   until it's on. Where macOS has no way to ask (Full Disk Access, App Management, Developer
   Tools), it shows deskrig in Finder to drag into the list. This needs someone at the Mac's
   screen.
2. **The agent skill**: `~/.claude/skills/deskrig/SKILL.md` for Claude Code. `deskrig skill`
   prints it for any other agent.
3. **A macOS VM target**, if you say yes: it installs Tart and pulls a macOS image, about
   40 GB, under `~/.deskrig`, and adds the target `macos`. No sudo.

Each step runs on its own too: `deskrig setup permissions`, `deskrig setup skill`,
`deskrig setup vm`.

## Targets

    deskrig targets                     # * marks the default
    deskrig use pb-test                 # the default for everyone on this Mac
    deskrig --target pb-test shot a.png # one command
    DESKRIG_TARGET=pb-test ./my-test.sh # a whole script

`--target` wins over `DESKRIG_TARGET`, which wins over the default, which starts as `local`.
Targets other than `local` live in `~/.deskrig/config.json`. `deskrig vm create` writes it,
and you can edit it by hand:

```json
{
  "default": "local",
  "targets": {
    "pb-test": {"kind": "tart", "vm": "pb-test", "tartHome": "~/PW_TOOLS/macos-vm/tart-home"}
  }
}
```

`vm` is the VM's name in Tart (the target's name if left out); `tartHome` is Tart's
`TART_HOME` (deskrig's own, `~/.deskrig/tart`, if left out).

## Commands, on every target

    deskrig status                      # logged in? unlocked? permissions?
    deskrig windows [--all]             # app, pid, x, y, w, h, title, front to back
    deskrig shot out.png "Safari"       # that app's front window, even if covered
    deskrig shot out.png                # the whole screen
    deskrig raise "Safari"
    deskrig click 100 200               # also dblclick, move, down, up
    deskrig drag 100 200 300 200
    deskrig scroll 600 400 20 10        # a trackpad scroll with phases and momentum
    deskrig key cmd+t                   # return, esc, shift+tab, ctrl+cmd+f, f5, or a key code
    deskrig type "hello"
    deskrig input                       # one action per line on stdin, answered ok or error
    deskrig run -- open -a "My App"     # a command on the target, in its desktop session
    deskrig install app.dmg             # or a URL: the .app to /Applications, quitting and replacing it
    deskrig frames ["Safari"]           # keep the latest frame in a mapped file
    deskrig record start ["Safari"]     # record every frame, with when it was on screen
    deskrig record stop                 # prints the folder and the frame count
    deskrig dismiss-notifications       # close banners that would sit over the app

Coordinates are the target's points from the top left of its main display. Input lands on
whatever is on top, so `raise` first. If clicks seem lost on local, `windows --all` often shows
an invisible system window over the app.

From Python, `clients/python/deskrig.py`:

    from deskrig import Target
    t = Target()                        # DESKRIG_TARGET, else the default
    t.click(720, 450)
    t.type("hello")
    frames = t.frames()
    frames.wait()
    img = frames.array()
    rec = t.record()
    t.click(720, 450)
    rec.stop()
    rec.frames()                        # [(frame, time on screen, png)]
    rec.events()                        # [(start, end, action)], same clock

**Recording** captures on the target itself: in a VM, inside the VM, so any number of VMs can
record at once whatever their windows on this Mac are doing, and the times are the target's
own. `record start` prints the folder (`{"dir", "pointsWide", "pointsHigh", "pixelsPerPoint"}`).
It fills with PNGs named by the time they were on screen and `frames.tsv` (frame, time, width,
height, file); each line is written once its PNG is whole, so lines can come late and out of
order. Times are UTC to the microsecond (`2026-11-12T16:45:05.123456Z`; file names
`20261112T164505.123456Z.png`), by the target's clock: a VM's can be tens of milliseconds off
this Mac's, so time a frame against `events.tsv`. `events.tsv` lists each input action with its start and end, and `done` appears when
`record stop` has every frame in. A VM's folders are in its data folder, shared with this Mac
(`~/.deskrig/vms/<name>/data/recordings/`), so a test reads them as they arrive. A recording
stops by itself after 30 minutes.

## How each target works

**local.** Agents, ssh sessions and CI jobs on a Mac run in a Background session with no
display, where `screencapture` fails and ScreenCaptureKit hangs. deskrig starts itself as a job
inside the logged-in desktop session, does the work there and hands back the output and exit
code. macOS grants the permissions to that one signed binary at that one path. macOS's capture
service also tells its clients apart by path, so two deskrig processes capturing at once would
knock each other off: one deskrig process per desktop, the capture server, holds every shot,
frame watch and recording, and the rest ask it over a socket. It starts on first use and leaves
after ten idle minutes.

**tart.** deskrig copies itself into the VM's read-only shared folder and runs there through
Tart's guest agent, which already has the guest's Screen Recording and Accessibility. Shots,
windows and input are the guest's own, so typing is exact and a drag moves a window by exactly
what was asked. The VM also gets a writable data folder from this Mac, where recordings land.
The VM runs in a window on this Mac; `frames` watches that window from here, so it needs the
window in view; `record` doesn't. Nothing listens on the network.

    deskrig vm create pb-test           # once; --cpus, --memory, --display 1440x900pt, --image
    deskrig vm start pb-test            # boots it in a window, about 20 s
    deskrig vm stop pb-test
    deskrig vm delete pb-test

`vm create` with a name Tart already has (in `TART_HOME`) adds that VM as a target instead.

## What it needs

- Someone logged in at the Mac's screen, unlocked, for local and for starting a VM. After a
  reboot macOS won't start a macOS VM until someone logs in: the keys it needs stay locked.
- macOS 14 or later. VMs need Apple silicon.

## Building from source

    ./build.sh install

It needs Go and Xcode's command line tools. It signs with `release-signing.pem`, read from
`$KEYKOSH_ROOT/deskrig` when `KEYKOSH_ROOT` is set, else from `.keys/` in this repo, so a
source build is the same program to macOS as a release and keeps the permissions. Without the
project's key, put your own self-signed code-signing PEM in `.keys/release-signing.pem`; macOS
then sees your builds as a different program, needing their own grants.

Releases are signed in CI with the same key (the `DESKRIG_SIGNING_PEM` secret), under the
identifier `io.github.sudiptadeb.deskrig`. Tag `v*` to release.

## License

MIT
