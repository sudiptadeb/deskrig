package main

// The Linux desktop deskrig acts on: the logged-in graphical session of this user, found by its
// compositor process, and that compositor's own way to list, raise and capture windows. Wayland
// gives an ordinary program none of these, so GNOME is asked through deskrig's Shell extension
// (linux/gnome) and KDE through a KWin script (kde_linux.go). Both work the same on X11.

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// One window, as every desktop backend reports it. Type follows Mutter's numbering: 0 normal,
// 1 desktop, 2 dock, 3 dialog, 4 modal dialog, 7 utility, higher ones menus and popups.
type linuxWindow struct {
	ID        uint64 `json:"id"`
	Pid       int    `json:"pid"`
	App       string `json:"app"`
	AppID     string `json:"appId"`
	Title     string `json:"title"`
	X         int    `json:"x"`
	Y         int    `json:"y"`
	W         int    `json:"w"`
	H         int    `json:"h"`
	Type      int    `json:"type"`
	Minimized bool   `json:"minimized"`
	Focused   bool   `json:"focused"`

	Ref string `json:"-"` // the desktop's own name for it, which stays while the window lives
}

// The windows `windows` lists without --all: what a person sees as an app's windows.
func (w linuxWindow) ordinary() bool {
	return !w.Minimized && (w.Type == 0 || w.Type == 3 || w.Type == 4 || w.Type == 7)
}

type desktop interface {
	name() string // gnome or kde
	check() error // nil when the helper answers and is current
	screen() (int, int, error)
	windows() ([]linuxWindow, error)
	raise(w linuxWindow) error
	shot(w *linuxWindow) ([]byte, error) // a window, or the screen for nil, as PNG
	dismissNotifications() (int, error)
}

type session struct {
	uid        int
	user       string
	compositor string // the desktop's process deskrig found it by: gnome-shell or plasmashell
	kind       string // wayland or x11
	env        map[string]string
}

var sessionEnvKeys = []string{"WAYLAND_DISPLAY", "DISPLAY", "XAUTHORITY", "XDG_SESSION_TYPE", "XDG_CURRENT_DESKTOP",
	"XDG_SESSION_DESKTOP", "DBUS_SESSION_BUS_ADDRESS", "XDG_RUNTIME_DIR", "DESKTOP_SESSION"}

// This user's desktop session, by the shell running for it. On KDE that is plasmashell, since
// KWin runs with capabilities that keep its environment from its own user. deskrig is often started from
// outside the session (tart exec, ssh), so it takes the session's environment from there and
// passes it on to what it runs.
func findSession() (session, error) {
	s := session{uid: os.Getuid(), env: map[string]string{}}
	s.user = os.Getenv("USER")
	if out, code := output("/usr/bin/id", "-un"); code == 0 {
		s.user = strings.TrimSpace(out)
	}
	procs, _ := filepath.Glob("/proc/[0-9]*")
	for _, p := range procs {
		comm, err := os.ReadFile(p + "/comm")
		if err != nil {
			continue
		}
		name := strings.TrimSpace(string(comm))
		if name != "gnome-shell" && name != "plasmashell" {
			continue
		}
		info, err := os.Stat(p)
		if err != nil || ownerUID(info) != s.uid {
			continue
		}
		environ, err := os.ReadFile(p + "/environ")
		if err != nil {
			continue
		}
		s.compositor = name
		for _, kv := range strings.Split(string(environ), "\x00") {
			if k, v, ok := strings.Cut(kv, "="); ok {
				s.env[k] = v
			}
		}
		break
	}
	if s.compositor == "" {
		return s, fmt.Errorf("no desktop session for %s: nobody is logged in to GNOME or KDE as %s", s.user, s.user)
	}
	runtime := fmt.Sprintf("/run/user/%d", s.uid)
	if s.env["XDG_RUNTIME_DIR"] == "" {
		s.env["XDG_RUNTIME_DIR"] = runtime
	}
	if s.env["DBUS_SESSION_BUS_ADDRESS"] == "" {
		s.env["DBUS_SESSION_BUS_ADDRESS"] = "unix:path=" + s.env["XDG_RUNTIME_DIR"] + "/bus"
	}
	s.kind = s.env["XDG_SESSION_TYPE"]
	if s.kind == "" {
		s.kind = "x11"
		if s.env["WAYLAND_DISPLAY"] != "" {
			s.kind = "wayland"
		}
	}
	// What deskrig itself and everything it starts see.
	for _, k := range sessionEnvKeys {
		if v := s.env[k]; v != "" {
			os.Setenv(k, v)
		}
	}
	return s, nil
}

func (s session) desktop() desktop {
	if s.compositor == "gnome-shell" {
		return &gnome{}
	}
	return &kde{}
}

// deskrig's own files in the session's runtime folder: the input server's socket, shots.
func runtimeDir() string {
	d := filepath.Join(os.Getenv("XDG_RUNTIME_DIR"), "deskrig")
	os.MkdirAll(d, 0o700)
	return d
}

// loginctl's view of the session: whether it's the active one on the seat, and locked.
func (s session) seatState() (active, locked bool) {
	out, _ := output("loginctl", "show-user", strconv.Itoa(s.uid), "-p", "Display", "--value")
	id := strings.TrimSpace(out)
	if id == "" {
		return false, false
	}
	out, _ = output("loginctl", "show-session", id, "-p", "Active", "-p", "LockedHint")
	return strings.Contains(out, "Active=yes"), strings.Contains(out, "LockedHint=yes")
}
