package main

// The local target on Linux: the logged-in GNOME or KDE desktop of this user, on Wayland or X11.
// deskrig runs this inside a Linux VM, from the Mac through Tart's guest agent, and the same
// commands work on a Linux machine of its own. What it prints matches the Mac's, so one test
// script runs on every target.

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func initApp() {}

func init() { settle() }

// From a VM's shared folder, deskrig runs a copy of itself at a fixed path in the user's home:
// KWin grants shots by a program's path, and the shared copy's name changes with each version.
func settle() {
	if os.Geteuid() == 0 || !strings.HasPrefix(selfPath, "/mnt/shared/") {
		return
	}
	home, _ := os.UserHomeDir()
	fixed := filepath.Join(home, ".local/bin/deskrig")
	data, err := os.ReadFile(selfPath)
	if err != nil {
		return
	}
	if old, _ := os.ReadFile(fixed); !bytes.Equal(old, data) {
		os.MkdirAll(filepath.Dir(fixed), 0o755)
		if os.WriteFile(fixed+".new", data, 0o755) != nil || os.Rename(fixed+".new", fixed) != nil {
			return
		}
	}
	syscall.Exec(fixed, append([]string{fixed}, os.Args[1:]...), os.Environ())
}

func ownerUID(info fs.FileInfo) int {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return int(st.Uid)
	}
	return -1
}

// Chromium's scroll for a millimetre of two-finger travel on deskrig's touchpad, measured with
// `scroll` holding the fingers still before they lift (no fling): Arch Linux ARM with Plasma
// 6.8 on Wayland moved 2,244 px for 20 mm; GNOME 46 on Ubuntu 24.04, Wayland and Xorg.
const (
	scrollPxPerMMKDE      = 112.2
	scrollPxPerMMGnome    = 20.0
	scrollPxPerMMGnomeX11 = 20.0
)

func localDo(verb string, args []string) {
	switch verb {
	case "frames":
		fail(2, "frames isn't on Linux desktops yet; on a Linux VM target it watches the VM's window from the Mac")
	case "record":
		fail(2, "record isn't on Linux targets yet")
	case "request-permission", "check-permission", "capture-serve":
		usage()
	case "install":
		linuxInstall(args)
	case "run":
		if _, err := findSession(); err != nil {
			fail(2, "%v", err)
		}
		runCommand(args)
	}
	s, err := findSession()
	if err != nil {
		fail(2, "%v", err)
	}
	if verb == "status" {
		printLinuxStatus(s)
		finish(0)
	}
	d := s.desktop()
	if verb == "input-serve" {
		inputServe()
		finish(0)
	}
	if err := d.check(); err != nil {
		fail(2, "%v", err)
	}
	done := time.AfterFunc(time.Minute, func() { fail(1, "%s didn't finish in a minute", verb) })
	defer done.Stop()
	switch verb {
	case "windows":
		printLinuxWindows(d, len(args) > 0 && args[0] == "--all")
	case "shot":
		which := ""
		if len(args) > 1 {
			which = args[1]
		}
		linuxShot(d, args[0], which)
	case "raise":
		linuxRaise(d, args[0])
	case "dismiss-notifications":
		n, err := d.dismissNotifications()
		if err != nil {
			fail(1, "%v", err)
		}
		fmt.Printf("closed %d notifications\n", n)
	case "input":
		done.Stop()
		c := connectInput(d)
		fmt.Println("ready")
		in := newLineScanner(os.Stdin)
		for in.Scan() {
			if strings.TrimSpace(in.Text()) == "" {
				continue
			}
			answer, err := c.send(in.Text())
			if err != nil {
				fail(1, "%v", err)
			}
			fmt.Println(answer)
		}
	default:
		c := connectInput(d)
		answer, err := c.send(strings.TrimSpace(verb + " " + strings.Join(args, " ")))
		if err != nil {
			fail(1, "%v", err)
		}
		if answer != "ok" {
			fail(1, "%s", strings.TrimPrefix(answer, "error: "))
		}
	}
	finish(0)
}

func printLinuxStatus(s session) {
	active, locked := s.seatState()
	d := s.desktop()
	helper := d.check()
	fmt.Println("user:", s.user)
	fmt.Println("on console:", active)
	fmt.Println("screen locked:", locked)
	fmt.Printf("desktop: %s on %s\n", d.name(), s.kind)
	f, err := os.OpenFile("/dev/uinput", os.O_WRONLY, 0)
	if err == nil {
		f.Close()
	}
	fmt.Println("input:", err == nil)
	if helper != nil {
		fmt.Println("helper: false,", helper)
	} else {
		fmt.Println("helper: true")
		if w, h, err := d.screen(); err == nil {
			fmt.Printf("main display: %d x %d points\n", w, h)
		}
	}
	fmt.Println("binary:", selfPath)
	fmt.Println("version:", version)
}

func listLinuxWindows(d desktop, all bool) []linuxWindow {
	ws, err := d.windows()
	if err != nil {
		fail(1, "%v", err)
	}
	if all {
		return ws
	}
	var out []linuxWindow
	for _, w := range ws {
		if w.ordinary() {
			out = append(out, w)
		}
	}
	return out
}

// The same columns as on a Mac; --all adds the window's type where a Mac has its layer.
func printLinuxWindows(d desktop, all bool) {
	for _, w := range listLinuxWindows(d, all) {
		fields := []string{w.App, strconv.Itoa(w.Pid)}
		if all {
			fields = append(fields, strconv.Itoa(w.Type))
		}
		fields = append(fields, strconv.Itoa(w.X), strconv.Itoa(w.Y), strconv.Itoa(w.W), strconv.Itoa(w.H), w.Title)
		fmt.Println(strings.Join(fields, "\t"))
	}
}

// The frontmost ordinary window of an app (its name or app id) or a pid.
func findLinuxWindow(d desktop, which string) (linuxWindow, bool) {
	app, pid := appOrPid(which)
	for _, w := range listLinuxWindows(d, false) {
		if (pid > 0 && w.Pid == pid) || (pid == 0 && (w.App == app || w.AppID == app)) {
			return w, true
		}
	}
	return linuxWindow{}, false
}

func linuxShot(d desktop, out, which string) {
	var win *linuxWindow
	if which != "" {
		w, ok := findLinuxWindow(d, which)
		if !ok {
			fail(1, "no window for %s", which)
		}
		win = &w
	}
	png, err := d.shot(win)
	if err != nil {
		fail(1, "%v", err)
	}
	if out == "-" {
		os.Stdout.Write(png)
		return
	}
	if err := os.WriteFile(out, png, 0o644); err != nil {
		fail(1, "can't write %s: %v", out, err)
	}
	w, h := pngSize(png)
	fmt.Printf("saved %s %d x %d\n", out, w, h)
}

func linuxRaise(d desktop, which string) {
	w, ok := findLinuxWindow(d, which)
	if !ok {
		// A minimised window isn't in the ordinary list.
		for _, m := range listLinuxWindows(d, true) {
			app, pid := appOrPid(which)
			if m.Type == 0 && ((pid > 0 && m.Pid == pid) || (pid == 0 && (m.App == app || m.AppID == app))) {
				w, ok = m, true
				break
			}
		}
	}
	if !ok {
		fail(1, "no window for %s", which)
	}
	if err := d.raise(w); err != nil {
		fail(1, "can't raise %s: %v", which, err)
	}
	for i := 0; i < 20; i++ {
		ws, _ := d.windows()
		for _, o := range ws {
			if o.Ref == w.Ref && o.Focused {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func setup(args []string)            { linuxSetup(args) }
func setupPermissions(time.Duration) { linuxSetup(nil) }
func setupVM()                       { fail(2, "VM targets need a Mac") }

func update() {
	fail(2, "on Linux, deskrig comes with the Mac's deskrig, which puts it in each Linux VM")
}

// Targets other than local are VMs, which run on a Mac.
func (t Target) do(verb string, args []string) {
	fail(2, "target %s is a VM, and VMs need a Mac", t.Name)
}
func (t Target) running() bool { return false }

func vmMain(targetFlag string, args []string) { fail(2, "VM targets need a Mac") }
