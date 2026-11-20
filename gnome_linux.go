package main

// GNOME, on Wayland and Xorg: deskrig's Shell extension answers on D-Bus (linux/gnome).

import (
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/godbus/dbus/v5"
)

//go:embed linux/gnome
var gnomeExtension embed.FS

const (
	gnomeExtensionUUID    = "deskrig@sudiptadeb.github.io"
	gnomeExtensionVersion = 1 // VERSION in linux/gnome/extension.js
	gnomePath             = "/io/github/sudiptadeb/Deskrig"
	gnomeIface            = "io.github.sudiptadeb.Deskrig."
)

type gnome struct{ obj dbus.BusObject }

func (g *gnome) name() string { return "gnome" }

func (g *gnome) call(method string, args ...any) *dbus.Call {
	if g.obj == nil {
		conn, err := dbus.ConnectSessionBus()
		if err != nil {
			return &dbus.Call{Err: fmt.Errorf("can't reach the session's D-Bus: %v", err)}
		}
		g.obj = conn.Object("org.gnome.Shell", gnomePath)
	}
	c := g.obj.Call(gnomeIface+method, 0, args...)
	if c.Err != nil && strings.Contains(c.Err.Error(), "No such interface") || c.Err != nil && strings.Contains(c.Err.Error(), "UnknownMethod") {
		c.Err = fmt.Errorf("deskrig's GNOME Shell extension isn't running: `deskrig setup`, then log in again")
	}
	return c
}

func (g *gnome) check() error {
	var v uint32
	if err := g.call("Version").Store(&v); err != nil {
		return err
	}
	if v != gnomeExtensionVersion {
		return fmt.Errorf("GNOME Shell runs deskrig's extension version %d, this deskrig needs %d: "+
			"`deskrig setup`, then log in again", v, gnomeExtensionVersion)
	}
	return nil
}

func (g *gnome) screen() (int, int, error) {
	var w, h int32
	err := g.call("Screen").Store(&w, &h)
	return int(w), int(h), err
}

func (g *gnome) windows() ([]linuxWindow, error) {
	var s string
	if err := g.call("Windows").Store(&s); err != nil {
		return nil, err
	}
	var ws []linuxWindow
	err := json.Unmarshal([]byte(s), &ws)
	for i := range ws {
		ws[i].Ref = strconv.FormatUint(ws[i].ID, 10)
	}
	return ws, err
}

func (g *gnome) raise(w linuxWindow) error {
	var ok bool
	if err := g.call("Raise", w.ID).Store(&ok); err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("the window has gone")
	}
	return nil
}

var shotCount atomic.Int64

// GNOME Shell writes the PNG to a file of the session's, which deskrig reads and removes.
func (g *gnome) shot(win *linuxWindow) ([]byte, error) {
	var id uint64
	if win != nil {
		id = win.ID
	}
	path := filepath.Join(runtimeDir(), fmt.Sprintf("shot-%d-%d.png", os.Getpid(), shotCount.Add(1)))
	defer os.Remove(path)
	var w, h int32
	if err := g.call("Shot", id, path).Store(&w, &h); err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

func (g *gnome) dismissNotifications() (int, error) {
	var n int32
	err := g.call("DismissNotifications").Store(&n)
	return int(n), err
}

// Puts the extension where GNOME Shell finds it and turns it on. GNOME loads a new or changed
// extension only at the next login.
func installGnomeExtension() (changed bool, err error) {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".local/share/gnome-shell/extensions", gnomeExtensionUUID)
	os.MkdirAll(dir, 0o755)
	for _, name := range []string{"metadata.json", "extension.js"} {
		data, _ := gnomeExtension.ReadFile("linux/gnome/" + name)
		old, _ := os.ReadFile(filepath.Join(dir, name))
		if string(old) == string(data) {
			continue
		}
		changed = true
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			return false, err
		}
	}
	out, _ := output("gsettings", "get", "org.gnome.shell", "enabled-extensions")
	if !strings.Contains(out, gnomeExtensionUUID) {
		list := strings.TrimSpace(out)
		if strings.HasPrefix(list, "@as") || list == "[]" || list == "" {
			list = fmt.Sprintf("['%s']", gnomeExtensionUUID)
		} else {
			list = strings.TrimSuffix(list, "]") + fmt.Sprintf(", '%s']", gnomeExtensionUUID)
		}
		if o, code := output("gsettings", "set", "org.gnome.shell", "enabled-extensions", list); code != 0 {
			return false, fmt.Errorf("can't turn on the extension: %s", strings.TrimSpace(o))
		}
		changed = true
	}
	output("gsettings", "set", "org.gnome.shell", "disable-user-extensions", "false")
	return changed, nil
}

// Waits for a just-started GNOME Shell to load the extension.
func (g *gnome) waitReady(limit time.Duration) error {
	end := time.Now().Add(limit)
	for {
		g.obj = nil
		err := g.check()
		if err == nil || time.Now().After(end) {
			return err
		}
		time.Sleep(time.Second)
	}
}
