# Linux target: design

Status: proposal, 2026-11-20. Nothing here is built yet.

Linux guests become one more kind of Tart VM. Agents use the same commands as on a macOS VM.
The guests are arm64: Ubuntu 24.04 with GNOME on Wayland, Debian 13 with GNOME, Arch Linux ARM
with KDE Plasma 6 on Wayland, and Ubuntu's Xorg session for X11.

## The shape

The macOS VM target works the same way: deskrig puts a copy of itself in the VM's shared
folder and runs it through Tart's guest agent (`tart exec`). The copy acts on the guest's own
desktop. On Linux the copy is a `linux/arm64` build of deskrig, pure Go, cross-compiled on the
Mac. The guest agent ships for Linux arm64, and Cirrus Labs' Linux images already include it.

Inside the guest, deskrig keeps one server per desktop, as the capture server does on macOS. It
holds the virtual input devices and the session's D-Bus connection, and it exits after 10 idle
minutes. A new input device takes the compositor a moment to pick up, so making one per command
would be slow and flaky.

It runs as the auto-logged-in desktop user. The guest setup lets that user open `/dev/uinput`
(a udev rule), so one process, with one user, does everything.

| Command | Where | GNOME, Wayland | KDE Plasma 6, Wayland | X11 (Ubuntu's Xorg GNOME) |
|---|---|---|---|---|
| click, drag, move, down, up | guest | uinput absolute pointer | same | same |
| key, type | guest | uinput keyboard, US layout | same | same |
| scroll | guest | uinput touchpad, two fingers | same | same |
| windows, raise | guest | deskrig's GNOME Shell extension, over D-Bus | a KWin script | the GNOME extension |
| shot (window, even covered) | guest | the extension: Mutter renders the window's actor | KWin `ScreenShot2.CaptureWindow` | the extension |
| shot (screen) | guest | the extension | KWin `ScreenShot2.CaptureWorkspace` | the extension |
| frames | host | the VM's window on this Mac, as for macOS VMs | same | same |
| install | guest | `flatpak install` | same | same |
| run, status | guest | in the desktop session's environment | same | same |
| record | — | later, not asked for | | |

So there are two desktop backends, GNOME and KWin. Input is the same everywhere. X11 needs no
backend of its own, because Ubuntu's Xorg session runs the same GNOME Shell.

### Input: uinput, the same on every desktop

Wayland lets no ordinary app inject input. A uinput device is a real input device to the
kernel, so libinput and every compositor treat it like hardware, on Wayland and X11 alike.

- **Pointer:** an absolute pointer (`ABS_X`/`ABS_Y` plus buttons), like QEMU's USB tablet.
  libinput maps it onto the screen, so a click lands on exact coordinates.
- **Keyboard:** evdev key codes, with modifiers pressed and released like a real keyboard. The
  guest is set to a US layout, as on macOS. In a chord, `cmd` means Super and `opt` means Alt.
  Project B's Linux shortcuts use `ctrl`.
- **Scroll:** a virtual touchpad. Two fingers go down, move in steps, and lift. libinput turns
  that into finger-source scroll events and a scroll stop, and the app (GTK, Chromium) does its
  own kinetic scrolling. The momentum is then the app's real behaviour, not one deskrig makes
  up.

### Windows and shots: one small piece of code per compositor

- **GNOME** (Wayland and Xorg): a GNOME Shell extension, about 150 lines of JS, shipped in
  deskrig and installed by the guest setup. On D-Bus it offers Windows (app id, pid, title,
  frame, stacking order), Raise, and Shot (one window or the screen, as PNG). GNOME 46 (Ubuntu)
  and 48 (Debian) share the ESM extension API. GNOME loads a new extension only at the next
  login, so the setup installs it before the first auto-login.
- **KDE:** deskrig loads a KWin script through `org.kde.kwin.Scripting`; the script reports the
  window list back over D-Bus, as kdotool does. Raise sets `workspace.activeWindow`. KWin's
  `ScreenShot2` is restricted: it serves only programs whose `.desktop` file declares
  `X-KDE-DBUS-Restricted-Interfaces`. The guest setup installs one, for deskrig at a fixed path
  in the guest.

Coordinates are the guest's logical pixels from the top left. The VMs run at scale 1.

### Frames: from the host

The frame stream stays on the host, as for macOS VMs: deskrig watches the VM's window on this
Mac. That needs no new code. Inside the guest, a frame stream on Wayland would mean a PipeWire
screencast per compositor.

### Install: Flatpak

- `deskrig install app.flatpak`, a file or a URL: copied into the share, then `flatpak install
  --user --bundle`.
- `deskrig install app.flatpakref`: installed from our repository, which the ref names.
- As on macOS, a running copy of the app is quit first (`flatpak kill <app id>`), and deskrig
  waits for it to exit.
- Runtimes come from Flathub over 443. Project B can pre-install its runtime when it sets up a
  VM, so installs don't wait on downloads.

### VMs

`deskrig vm create <name> --image <image> --desktop gnome|gnome-x11|kde` runs a guest setup
shipped in deskrig. The setup does the following:

- installs the desktop and logs the user straight in, on Wayland or Xorg as asked;
- turns off lock and blanking;
- sets the US keymap;
- mounts the shared folders (virtiofs);
- adds the uinput rule and the extension, or the KDE `.desktop` file;
- installs Flatpak with Flathub;
- applies our network fixes: https over IPv4 for apt, the netplan mask, https mirrors for
  pacman.

This takes over the desktop half of project-b's `vm-setup-guest.sh`; project-b keeps its
runtime and SDK half.

- Ubuntu 24.04: a clone of `pb-ubuntu` (guarded: `tart clone pb-ubuntu pb-ub-gnome`, only if
  the name is free), so the desktop is already in; or fresh from `ghcr.io/cirruslabs/ubuntu:24.04`.
- Ubuntu Xorg: another clone with `--desktop gnome-x11`.
- Debian 13: `ghcr.io/cirruslabs/debian:trixie`, which is published.
- Arch Linux ARM: Cirrus Labs has no plain Arch image. `ghcr.io/cirruslabs/omarchy` is an
  Arch-based arm64 desktop. If it is Arch Linux ARM underneath, we swap Hyprland for Plasma.
  Otherwise we build the disk from the Arch Linux ARM generic tarball. This is the biggest
  unknown.

## What it costs

- **Time:** about two weeks for one session.

  | Part | Days |
  |---|---|
  | Guest build, server, uinput | 2 |
  | GNOME extension | 2 |
  | KWin | 2 |
  | Session, run, status, install | 1 |
  | Ubuntu and Debian setup | 1 |
  | Arch with KDE | 2 |
  | A pass on all four VMs | 1 |

- **Repo:**
  - About 1,500 lines of Go in `*_linux.go` files beside the `_darwin` ones, plus about 250
    lines of JS (the extension and the KWin script) and the setup scripts.
  - deskrig's first dependency, `godbus/dbus`.
  - Releases gain a `deskrig-linux-arm64` binary. It is unsigned, because Linux has no
    permissions keyed to a signature. amd64 comes with project-b's step 7.
- **This Mac:** about 12 to 15 GB of disk per VM (the Ubuntu one is 12 GB), about 50 GB for
  four. 8 GB of memory per running VM, one VM at a time in the tester's slots.
- **Side effect:** the same Linux code makes `--target local` work on a real Linux desktop, for
  example a laptop for GPU tests, given the uinput rule.

## Risks, checked first in one slot on a clone of pb-ubuntu

1. **Covered-window shots on GNOME.** The plan is that Mutter renders a covered window's actor
   from the extension. If it can't, a window shot raises the window first and crops a screen
   shot.
2. **Kinetic scrolling.** Chromium's touchpad fling on Wayland, from the virtual touchpad.
3. **The Arch Linux ARM image** (see VMs).
4. **The guest agent.** `tart exec` in the cirruslabs Ubuntu image, and its user. If the agent
   is missing, the setup installs its `.deb` or `.rpm`.
5. **KDE on software rendering.** Plasma 6 on llvmpipe with 6 CPUs may be slow enough to change
   how long tests should wait.

## Not doing

- **VNC for input.** It sends only wheel clicks, no touchpad, so there would be no scroll phases
  and no momentum. Its keys are keysyms, not key codes.
- **The portals.** They ask the user to consent, every session.
- **X11 under a window manager other than GNOME's.** It would need an Xlib backend; add it only
  if a test needs it.
- **Frames with no window on the Mac,** as `vm.sh` runs it now. That needs an RFB client against
  Tart's VNC server, about 2 days; add it if the tester wants VMs headless.
