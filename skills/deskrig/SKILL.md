---
name: deskrig
description: See and drive a real desktop to test an app (screenshots of one window, real clicks, keys, trackpad scrolls, a frame stream, installing a DMG) on this Mac or in a clean macOS VM, with the same commands on every target. Use whenever a test, a check or a bug needs a real window, from an agent shell that has no display.
---

# deskrig

One set of commands for testing apps on a real desktop. A **target** says where they act:
`local` is this Mac's own desktop; others, such as a macOS VM, are in `~/.deskrig/config.json`.
A test script written against these commands runs on any target unchanged.

Agent shells on a Mac run in a Background session: `screencapture` fails there and
ScreenCaptureKit hangs. deskrig runs itself inside the desktop session, so use it for every
screenshot and every real click or key. Never call `screencapture` or `osascript` UI
scripting for this.

## Pick the target

    deskrig targets                   # the targets; * marks the default
    deskrig --target pb-test status   # one command on one target
    export DESKRIG_TARGET=pb-test     # every command in this shell or script

`--target` wins over `DESKRIG_TARGET`, which wins over the default (`deskrig use <name>`).
Ask the person before changing the default: it's shared by everyone on this Mac.

## First, check

    deskrig status

You need `on console: true`, `screen locked: false`, and the permissions `true`. If not:

- Nobody logged in, or locked: only a person can fix it, at the screen or over Screen Sharing.
  Say so and stop; don't try to work around it.
- A permission is false on local: `deskrig setup permissions` opens System Settings and waits
  up to 10 minutes while the person switches deskrig on. Tell them what to switch on.
- A VM target that isn't running: `deskrig vm start <name>`, about a minute.

## Commands, on any target

    deskrig windows                   # app, pid, x, y, w, h, title, front to back
    deskrig shot out.png "App Name"   # that app's largest window, even if covered
    deskrig shot out.png              # the whole screen
    deskrig raise "App Name"          # before any input
    deskrig click 100 200             # also dblclick, move, down, up
    deskrig drag 100 200 300 200
    deskrig scroll 600 400 20 10      # trackpad: 10 steps of 20 px, with momentum
    deskrig key cmd+t                 # also return, esc, tab, shift+tab, ctrl+cmd+f, f5
    deskrig type "hello"
    deskrig run -- open -a "App"      # a command on the target, in its desktop session
    deskrig install app.dmg           # or a URL: the .app to /Applications, replacing it

Coordinates are the target's points from the top left of its main display; `windows` uses the
same points. A shot is in pixels: 2x the points on a Retina Mac, 1x in a VM.

Look at every shot you take before you trust a result.

For a test with many actions, `deskrig input` takes one action per line on stdin and answers
`ok` or `error: ...` per line: `click x y`, `dblclick x y`, `move x y`, `down`, `up`,
`drag x1 y1 x2 y2`, `scroll x y dy [steps] [hold ms]`, `key combo`, `type text`. The Python
client in the deskrig repo (`clients/python/deskrig.py`) wraps it.

For many frames a second (animations, timing), `deskrig frames "App"` (on a VM: `deskrig
frames`, the VM's screen) keeps the latest frame in a mapped file and prints
`{"file", "pointsWide", "pointsHigh"}`; the format is at the top of `frames_darwin.go`.

## Traps

- **Input lands on whatever is on top.** `raise` the target first. If clicks are lost on
  local, `deskrig windows --all`: system windows at layers above 0, such as notification
  banners, can sit invisibly over the app and take the clicks.
- `run` and `install` act on the target: on a VM, inside the VM. `install` on local replaces
  this Mac's own copy of the app; ask the person first.
- On local, an app started from your shell may never put its window on screen. Start it with
  `deskrig run -- open -a "App"`.
- Local is a real person's desktop. Don't click outside the window under test, and don't
  upload a whole-screen shot anywhere.

## VM targets

    deskrig vm create <name>          # once; about 40 GB the first time. Ask the person first.
    deskrig vm start <name>
    deskrig vm stop <name>            # ask first if you didn't start it: VMs are shared

Input runs inside the guest, so it is exact. Each command takes about half a second; use
`input` for long tests.

- `vm start` fails with a "host key" error when nobody has logged in at this Mac's screen
  since it booted. Only a person logging in fixes it.
- The VM runs in a window on this Mac. `frames` watches that window, so it needs this Mac
  logged in and unlocked, and the window not minimised; shots and input don't.
- Resting the pointer on a window's green button opens a menu that takes the next click.
