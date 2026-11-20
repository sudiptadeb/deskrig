package main

// KDE Plasma (KWin), on Wayland and X11. KWin lists and raises windows only for its own
// scripts, so deskrig loads a short script for each question, and the script answers by
// calling deskrig back on the session's D-Bus. Shots come from KWin's ScreenShot2 interface,
// which KWin serves only to programs whose .desktop file asks for it (`deskrig setup` writes one
// for deskrig's path in the guest).

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/godbus/dbus/v5"
)

type kde struct {
	conn    *dbus.Conn
	answers chan string
}

func (k *kde) name() string { return "kde" }

const kdeCallbackPath = "/io/github/sudiptadeb/Deskrig"

type kdeCallback struct{ answers chan string }

func (c kdeCallback) Answer(s string) *dbus.Error {
	c.answers <- s
	return nil
}

func (k *kde) connect() error {
	if k.conn != nil {
		return nil
	}
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return fmt.Errorf("can't reach the session's D-Bus: %v", err)
	}
	k.conn, k.answers = conn, make(chan string, 1)
	return conn.Export(kdeCallback{k.answers}, kdeCallbackPath, "io.github.sudiptadeb.Deskrig")
}

var kdeScriptCount atomic.Int64

// Runs body as a KWin script; body calls answer(<string>) once.
func (k *kde) script(body string) (string, error) {
	if err := k.connect(); err != nil {
		return "", err
	}
	name := fmt.Sprintf("deskrig-%d-%d", os.Getpid(), kdeScriptCount.Add(1))
	path := filepath.Join(runtimeDir(), name+".js")
	src := fmt.Sprintf("function answer(s) { callDBus(%q, %q, %q, %q, String(s)); }\n%s\n",
		k.conn.Names()[0], kdeCallbackPath, "io.github.sudiptadeb.Deskrig", "Answer", body)
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		return "", err
	}
	defer os.Remove(path)
	scripting := k.conn.Object("org.kde.KWin", "/Scripting")
	var id int32
	if err := scripting.Call("org.kde.kwin.Scripting.loadScript", 0, path, name).Store(&id); err != nil {
		return "", fmt.Errorf("KWin doesn't take scripts: %v", err)
	}
	defer scripting.Call("org.kde.kwin.Scripting.unloadScript", 0, name)
	if id < 0 {
		return "", fmt.Errorf("KWin didn't load deskrig's script")
	}
	if err := k.conn.Object("org.kde.KWin", dbus.ObjectPath(fmt.Sprintf("/Scripting/Script%d", id))).
		Call("org.kde.kwin.Script.run", 0).Err; err != nil {
		return "", err
	}
	select {
	case s := <-k.answers:
		return s, nil
	case <-time.After(10 * time.Second):
		return "", fmt.Errorf("KWin's script didn't answer")
	}
}

func (k *kde) check() error {
	_, err := k.script(`answer("ok");`)
	return err
}

func (k *kde) screen() (int, int, error) {
	s, err := k.script(`const z = workspace.virtualScreenSize; answer(z.width + " " + z.height);`)
	if err != nil {
		return 0, 0, err
	}
	var w, h int
	_, err = fmt.Sscanf(s, "%d %d", &w, &h)
	return w, h, err
}

// Window types in Mutter's numbering, which `windows --all` prints on every desktop.
const kdeWindowsScript = `
function kind(w) {
  if (w.desktopWindow) return 1;
  if (w.dock) return 2;
  if (w.modal) return 4;
  if (w.dialog) return 3;
  if (w.utility) return 7;
  if (w.splash) return 8;
  if (w.dropdownMenu) return 9;
  if (w.popupMenu || w.popupWindow) return 10;
  if (w.tooltip) return 11;
  if (w.notification || w.criticalNotification || w.onScreenDisplay) return 12;
  if (w.normalWindow) return 0;
  return 15;
}
answer(JSON.stringify(workspace.stackingOrder.slice().reverse().map(w => ({
  uuid: w.internalId.toString(), pid: w.pid, appId: w.desktopFileName || "", cls: w.resourceClass || "",
  title: w.caption || "", x: Math.round(w.frameGeometry.x), y: Math.round(w.frameGeometry.y),
  w: Math.round(w.frameGeometry.width), h: Math.round(w.frameGeometry.height),
  type: kind(w), minimized: w.minimized, focused: w.active }))));
`

type kdeWindow struct {
	linuxWindow
	UUID string `json:"uuid"`
	Cls  string `json:"cls"`
}

func (k *kde) windows() ([]linuxWindow, error) {
	s, err := k.script(kdeWindowsScript)
	if err != nil {
		return nil, err
	}
	var kws []kdeWindow
	if err := json.Unmarshal([]byte(s), &kws); err != nil {
		return nil, err
	}
	ws := make([]linuxWindow, len(kws))
	for i, kw := range kws {
		w := kw.linuxWindow
		w.Ref = kw.UUID
		w.App = appName(w.AppID)
		if w.App == "" {
			w.App = kw.Cls
		}
		ws[i] = w
	}
	return ws, nil
}

func (k *kde) raise(win linuxWindow) error {
	u := win.Ref
	s, err := k.script(fmt.Sprintf(`
const w = workspace.stackingOrder.find(w => w.internalId.toString() === %q);
if (w) { if (w.minimized) w.minimized = false; workspace.activeWindow = w; answer("ok"); } else answer("gone");`, u))
	if err != nil {
		return err
	}
	if s != "ok" {
		return fmt.Errorf("the window has gone")
	}
	return nil
}

func (k *kde) dismissNotifications() (int, error) {
	s, err := k.script(`
let n = 0;
for (const w of workspace.stackingOrder) if (w.notification || w.criticalNotification) { w.closeWindow(); n++; }
answer(String(n));`)
	if err != nil {
		return 0, err
	}
	var n int
	fmt.Sscanf(s, "%d", &n)
	return n, nil
}

// A window without its shadow, even when covered, or the whole screen, from KWin's ScreenShot2:
// KWin writes the raw pixels to a pipe and says their layout in its answer.
func (k *kde) shot(win *linuxWindow) ([]byte, error) {
	if err := k.connect(); err != nil {
		return nil, err
	}
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	options := map[string]dbus.Variant{
		"include-cursor": dbus.MakeVariant(false), "include-decoration": dbus.MakeVariant(true),
		"include-shadow": dbus.MakeVariant(false), "native-resolution": dbus.MakeVariant(true),
	}
	obj := k.conn.Object("org.kde.KWin", "/org/kde/KWin/ScreenShot2")
	var call *dbus.Call
	if win == nil {
		call = obj.Go("org.kde.KWin.ScreenShot2.CaptureWorkspace", 0, nil, options, dbus.UnixFD(w.Fd()))
	} else {
		call = obj.Go("org.kde.KWin.ScreenShot2.CaptureWindow", 0, nil, win.Ref, options, dbus.UnixFD(w.Fd()))
	}
	<-call.Done
	w.Close()
	if call.Err != nil {
		if strings.Contains(call.Err.Error(), "NoAuthorized") || strings.Contains(call.Err.Error(), "not authorized") {
			return nil, fmt.Errorf("KWin won't give deskrig shots: `deskrig setup` writes the .desktop file KWin checks, for %s", selfPath)
		}
		return nil, fmt.Errorf("KWin's screenshot: %v", call.Err)
	}
	var info map[string]dbus.Variant
	if err := call.Store(&info); err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	num := func(key string) int {
		switch v := info[key].Value().(type) {
		case uint32:
			return int(v)
		case int32:
			return int(v)
		}
		return 0
	}
	width, height, stride := num("width"), num("height"), num("stride")
	if width == 0 || height == 0 || len(raw) < stride*height {
		return nil, fmt.Errorf("KWin's screenshot came back short: %d x %d, %d bytes", width, height, len(raw))
	}
	// QImage formats: 4 RGB32, 5 ARGB32, 6 ARGB32 premultiplied (B, G, R, A in memory on a
	// little-endian machine); 16 RGBX8888, 17 RGBA8888, 18 RGBA8888 premultiplied (R, G, B, A).
	format := num("format")
	ri, bi := 2, 0
	switch format {
	case 4, 5, 6:
	case 16, 17, 18:
		ri, bi = 0, 2
	default:
		return nil, fmt.Errorf("KWin's screenshot came in image format %d, which deskrig doesn't read", format)
	}
	opaque := format == 4 || format == 16
	premultiplied := format == 6 || format == 18
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		row := raw[y*stride:]
		for x := 0; x < width; x++ {
			p := row[4*x : 4*x+4]
			rr, g, b, a := p[ri], p[1], p[bi], p[3]
			if opaque {
				a = 255
			} else if premultiplied && a != 0 && a != 255 {
				rr, g, b = uint8(int(rr)*255/int(a)), uint8(int(g)*255/int(a)), uint8(int(b)*255/int(a))
			}
			o := img.PixOffset(x, y)
			img.Pix[o], img.Pix[o+1], img.Pix[o+2], img.Pix[o+3] = rr, g, b, a
		}
	}
	var buf bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// An app's name from its .desktop file, as GNOME shows it; "" if there is none.
func appName(appID string) string {
	if appID == "" {
		return ""
	}
	home, _ := os.UserHomeDir()
	dirs := []string{filepath.Join(home, ".local/share/flatpak/exports/share/applications"),
		"/var/lib/flatpak/exports/share/applications", filepath.Join(home, ".local/share/applications"),
		"/usr/local/share/applications", "/usr/share/applications"}
	for _, d := range dirs {
		data, err := os.ReadFile(filepath.Join(d, appID+".desktop"))
		if err != nil {
			continue
		}
		inEntry := false
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "[") {
				inEntry = line == "[Desktop Entry]"
			} else if inEntry && strings.HasPrefix(line, "Name=") {
				return strings.TrimPrefix(line, "Name=")
			}
		}
	}
	return ""
}

// KWin keeps each input device's settings itself; deskrig's scrolls assume natural scrolling
// and a flat acceleration, as GNOME's settings give them there.
func kdeTouchpadSettings() {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return
	}
	for i := 0; i < 64; i++ {
		dev := conn.Object("org.kde.KWin", dbus.ObjectPath(fmt.Sprintf("/org/kde/KWin/InputDevice/event%d", i)))
		v, err := dev.GetProperty("org.kde.KWin.InputDevice.name")
		if err != nil || v.Value() != "deskrig touchpad" {
			continue
		}
		dev.SetProperty("org.kde.KWin.InputDevice.naturalScroll", dbus.MakeVariant(true))
		dev.SetProperty("org.kde.KWin.InputDevice.pointerAccelerationProfileFlat", dbus.MakeVariant(true))
		dev.SetProperty("org.kde.KWin.InputDevice.tapToClick", dbus.MakeVariant(false))
	}
}
