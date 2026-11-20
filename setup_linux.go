package main

// Setting up a Linux desktop for deskrig, in two halves.
//
// `sudo deskrig setup system --desktop gnome|gnome-x11|kde --user <name>`, as root, once per
// VM: the desktop itself if it's missing, logging the user straight in on Wayland or Xorg, no
// lock or blanking, the US keymap, the user's access to /dev/uinput, Flatpak with Flathub, and our network's limits (443 only, no IPv6). Ubuntu, Debian and Arch
// Linux ARM.
//
// `deskrig setup`, as the desktop's user: deskrig's helper in the desktop (GNOME's extension or
// KWin's permission for shots) and the touchpad settings deskrig's scrolls assume. A new GNOME
// extension needs the user to log in again, which the VM setup does by restarting the login
// manager.

import (
	"fmt"
	"os"
	"path/filepath"
)

func linuxSetup(args []string) {
	if len(args) > 0 && args[0] == "system" {
		systemSetup(args[1:])
		return
	}
	s, err := findSession()
	if err != nil {
		fail(2, "%v", err)
	}
	relogin := false
	switch s.desktop().(type) {
	case *gnome:
		changed, err := installGnomeExtension()
		if err != nil {
			fail(1, "%v", err)
		}
		relogin = changed
		gs := [][]string{
			{"org.gnome.desktop.peripherals.touchpad", "natural-scroll", "true"},
			{"org.gnome.desktop.peripherals.touchpad", "accel-profile", "flat"},
			{"org.gnome.desktop.peripherals.touchpad", "speed", "0.0"},
			{"org.gnome.desktop.session", "idle-delay", "uint32 0"},
			{"org.gnome.desktop.screensaver", "lock-enabled", "false"},
		}
		for _, g := range gs {
			output("gsettings", "set", g[0], g[1], g[2])
		}
		fmt.Println("helper: deskrig's GNOME Shell extension", map[bool]string{true: "installed", false: "up to date"}[changed])
	case *kde:
		if err := kdeAllowShots(); err != nil {
			fail(1, "%v", err)
		}
		fmt.Println("helper: KWin lets", selfPath, "take shots")
	}
	if f, err := os.OpenFile("/dev/uinput", os.O_WRONLY, 0); err != nil {
		fmt.Println("input: no access to /dev/uinput; `sudo deskrig setup system` gives the desktop's user access")
	} else {
		f.Close()
		fmt.Println("input: ok")
	}
	if relogin {
		fmt.Println("log in again (or restart the login manager) for GNOME to load the extension")
	}
}

// KWin serves ScreenShot2 to a program whose .desktop file names the interface, matched by the
// program's path.
func kdeAllowShots() error {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".local/share/applications")
	os.MkdirAll(dir, 0o755)
	entry := fmt.Sprintf("[Desktop Entry]\nType=Application\nName=deskrig\nExec=%s\nNoDisplay=true\n"+
		"X-KDE-DBUS-Restricted-Interfaces=org.kde.KWin.ScreenShot2\n", selfPath)
	return os.WriteFile(filepath.Join(dir, "io.github.sudiptadeb.deskrig.desktop"), []byte(entry), 0o644)
}

const systemSetupScript = `set -euo pipefail
desktop=$1 user=$2
. /etc/os-release
family=$ID
case " ${ID_LIKE:-} " in *" arch "*) family=arch ;; *" debian "*|*" ubuntu "*) [ "$ID" = ubuntu ] || family=debian ;; esac
[ "$ID" = omarchy ] && family=arch
echo "== $PRETTY_NAME ($family), $desktop for $user"
case $family/$desktop in
ubuntu/gnome|ubuntu/gnome-x11|debian/gnome|debian/gnome-x11|arch/kde) ;;
*) echo "deskrig sets up GNOME on Ubuntu and Debian, and KDE on Arch Linux ARM, not $desktop on $PRETTY_NAME" >&2; exit 2 ;;
esac

# The desktop's user may make input devices.
echo 'KERNEL=="uinput", SUBSYSTEM=="misc", TAG+="uaccess", OPTIONS+="static_node=uinput"' > /etc/udev/rules.d/70-deskrig-uinput.rules
echo uinput > /etc/modules-load.d/deskrig-uinput.conf
modprobe uinput || true
udevadm control --reload
udevadm trigger --name-match=uinput --action=change || true

case $family in
ubuntu|debian)
  export DEBIAN_FRONTEND=noninteractive
  # Our network lets only 443 out, with no IPv6: apt over https, IPv4 only.
  sed -i 's#http://\(ports\.ubuntu\.com\|deb\.debian\.org\|security\.debian\.org\)#https://\1#g' \
    /etc/apt/sources.list.d/*.sources /etc/apt/sources.list 2>/dev/null || true
  echo 'Acquire::ForceIPv4 "true";' > /etc/apt/apt.conf.d/99ipv4
  if ! command -v gnome-shell >/dev/null; then
    apt-get update
    if [ "$family" = ubuntu ]; then
      apt-get install -y --no-install-recommends ubuntu-desktop-minimal gnome-terminal flatpak xdg-desktop-portal-gnome
      # Without recommends, netplan hands the network to a NetworkManager that isn't there.
      ln -sf /dev/null /etc/netplan/00-network-manager-all.yaml
      apt-get purge -y update-notifier || true
    else
      apt-get install -y --no-install-recommends gnome-session gnome-shell gdm3 gnome-terminal nautilus \
        flatpak xdg-desktop-portal-gnome dbus-user-session
    fi
  fi
  conf=/etc/gdm3/custom.conf; [ "$family" = debian ] && conf=/etc/gdm3/daemon.conf
  wayland=true; [ "$desktop" = gnome-x11 ] && wayland=false
  cat > $conf <<CONF
[daemon]
AutomaticLoginEnable=true
AutomaticLogin=$user
WaylandEnable=$wayland
CONF
  mkdir -p /etc/dconf/profile /etc/dconf/db/local.d
  printf 'user-db:user\nsystem-db:local\n' > /etc/dconf/profile/user
  cat > /etc/dconf/db/local.d/00-deskrig <<CONF
[org/gnome/desktop/session]
idle-delay=uint32 0
[org/gnome/desktop/screensaver]
lock-enabled=false
[org/gnome/desktop/lockdown]
disable-lock-screen=true
[org/gnome/settings-daemon/plugins/power]
sleep-inactive-ac-type='nothing'
[org/gnome/desktop/input-sources]
sources=[('xkb', 'us')]
CONF
  dconf update
  login=gdm
  ;;
arch)
  if ! pacman -Q plasma-desktop >/dev/null 2>&1; then
    OMARCHY_ALLOW_DIRECT_PACMAN=1 pacman -Syu --noconfirm --needed plasma-desktop kwin sddm konsole flatpak \
      xdg-desktop-portal-kde
  fi
  mkdir -p /etc/sddm.conf.d
  rm -f /etc/sddm.conf.d/autologin.conf
  cat > /etc/sddm.conf.d/zz-deskrig.conf <<CONF
[Autologin]
User=$user
Session=plasma.desktop
CONF
  mkdir -p /etc/xdg
  cat > /etc/xdg/kscreenlockerrc <<CONF
[Daemon]
Autolock=false
LockOnResume=false
Timeout=0
CONF
  cat > /etc/xdg/powerdevilrc <<CONF
[AC][Display]
TurnOffDisplayIdleTimeoutSec=-1
DimDisplayWhenIdle=false
[AC][SuspendAndShutdown]
AutoSuspendAction=0
CONF
  localectl set-x11-keymap us || true
  systemctl enable sddm
  login=sddm
  ;;
esac
flatpak remote-add --if-not-exists flathub https://dl.flathub.org/repo/flathub.flatpakrepo
systemctl set-default graphical.target
echo "== login manager: $login"
`

func systemSetup(args []string) {
	desktopKind, user := "", ""
	for i := 0; i+1 < len(args); i += 2 {
		switch args[i] {
		case "--desktop":
			desktopKind = args[i+1]
		case "--user":
			user = args[i+1]
		}
	}
	if desktopKind != "gnome" && desktopKind != "gnome-x11" && desktopKind != "kde" {
		fail(2, "setup system: --desktop gnome, gnome-x11 or kde")
	}
	if user == "" {
		fail(2, "setup system: --user <the desktop's user>")
	}
	if os.Geteuid() != 0 {
		fail(2, "setup system runs as root: sudo %s setup system --desktop %s --user %s", selfPath, desktopKind, user)
	}
	finish(exitCode(commandWithOutput("/bin/bash", "-c", systemSetupScript, "setup", desktopKind, user).Run()))
}
