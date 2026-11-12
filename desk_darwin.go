package main

// This Mac's desktop, as deskrig sees it from inside the desktop session: what the local target
// does, and what deskrig does inside a macOS VM. Coordinates are points from the top left of the
// main display.

/*
#cgo CFLAGS: -x objective-c -fobjc-arc -mmacosx-version-min=14.0
#cgo LDFLAGS: -framework AppKit -framework AVFoundation -framework CoreBluetooth -framework ApplicationServices -framework CoreMedia -framework CoreVideo -framework ImageIO -framework ScreenCaptureKit -framework UniformTypeIdentifiers -mmacosx-version-min=14.0 -Wl,-sectcreate,__TEXT,__info_plist,${SRCDIR}/Info.plist
#include <stdlib.h>
#include "native_darwin.h"
*/
import "C"

import (
	"bufio"
	"fmt"
	"math"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

func initApp() { C.dr_init_app() }

func goString(c *C.char) string {
	s := C.GoString(c)
	C.free(unsafe.Pointer(c))
	return s
}

// Full Disk Access has no call to ask about it; reading the system's permission database is
// only possible with it.
func fullDiskAccess() bool {
	f, err := os.Open("/Library/Application Support/com.apple.TCC/TCC.db")
	if err != nil {
		return false
	}
	f.Close()
	return true
}

// System Events answers only while it runs.
func automation(ask bool) bool {
	exec.Command("/usr/bin/open", "-g", "-j", "-b", "com.apple.systemevents").Run()
	for i := 0; i < 20 && C.dr_automation(false) == -600; i++ {
		time.Sleep(250 * time.Millisecond)
	}
	return C.dr_automation(C.bool(ask)) == 0
}

type permission struct {
	name  string // as `status` prints it
	page  string // its page in System Settings > Privacy & Security
	pane  string // that page's address
	how   string // what the person does
	check func() bool
	// Asks macOS, inside the desktop session, which lists deskrig on the page. Some wait for
	// the person's answer.
	request func()
	// Nothing can ask for it, so the person drags deskrig into the page's list.
	drag bool
}

// Everything deskrig may need, set up once. macOS keeps each grant for this path and this
// signing key, so updates keep them. App Management, Developer Tools and Local Network can't
// be checked.
var permissions = []permission{
	{name: "screen recording", page: "Screen & System Audio Recording", pane: "Privacy_ScreenCapture",
		how: "switch on deskrig", check: func() bool { return bool(C.dr_screen_recording(false)) },
		request: func() { C.dr_screen_recording(true) }},
	{name: "accessibility", page: "Accessibility", pane: "Privacy_Accessibility",
		how: "switch on deskrig", check: func() bool { return bool(C.dr_accessibility(false)) },
		request: func() { C.dr_accessibility(true) }},
	{name: "input monitoring", page: "Input Monitoring", pane: "Privacy_ListenEvent",
		how: "switch on deskrig", check: func() bool { return bool(C.dr_input_monitoring(false)) },
		request: func() { C.dr_input_monitoring(true) }},
	{name: "full disk access", page: "Full Disk Access", pane: "Privacy_AllFiles",
		how: "drag deskrig from the Finder window into the list, and switch it on", check: fullDiskAccess, drag: true},
	{name: "automation", page: "Automation", pane: "Privacy_Automation",
		how: "click Allow for deskrig to control System Events", check: func() bool { return automation(false) },
		request: func() { automation(true) }},
	{name: "camera", page: "Camera", pane: "Privacy_Camera", how: "click Allow",
		check: func() bool { return C.dr_media_status(0) == 3 }, request: func() { C.dr_media_request(0) }},
	{name: "microphone", page: "Microphone", pane: "Privacy_Microphone", how: "click Allow",
		check: func() bool { return C.dr_media_status(1) == 3 }, request: func() { C.dr_media_request(1) }},
	{name: "bluetooth", page: "Bluetooth", pane: "Privacy_Bluetooth", how: "click Allow",
		check: func() bool { return C.dr_bluetooth_status() == 3 }, request: func() { C.dr_bluetooth_request(600) }},
	{name: "app management", page: "App Management", pane: "Privacy_AppBundles",
		how: "drag deskrig from the Finder window into the list, and switch it on; it lets `install` replace apps", drag: true},
	{name: "developer tools", page: "Developer Tools", pane: "Privacy_DevTools",
		how:  "drag deskrig from the Finder window into the list, and switch it on; what it starts then skips the first-launch security scan",
		drag: true},
	{name: "local network", page: "Local Network", pane: "Privacy_LocalNetwork",
		how: "click Allow if macOS asks", request: func() {
			if c, err := net.Dial("udp", "224.0.0.251:5353"); err == nil {
				c.Write([]byte{0})
				c.Close()
			}
			time.Sleep(5 * time.Second)
		}},
}

func findPermission(name string) permission {
	for _, p := range permissions {
		if p.name == name {
			return p
		}
	}
	fail(2, "no permission %q", name)
	return permission{}
}

func need(name string) {
	for _, p := range permissions {
		if p.name == name && !p.check() {
			fail(2, "no %s permission for %s: run `deskrig setup permissions`", name, selfPath)
		}
	}
}

func printStatus() {
	var user *C.char
	var onConsole, locked C.bool
	C.dr_session(&user, &onConsole, &locked)
	var w, h C.double
	C.dr_main_display(&w, &h)
	fmt.Println("user:", goString(user))
	fmt.Println("on console:", bool(onConsole))
	fmt.Println("screen locked:", bool(locked))
	for _, p := range permissions {
		if p.check != nil {
			fmt.Printf("%s: %v\n", p.name, p.check())
		}
	}
	fmt.Printf("main display: %d x %d points\n", int(w), int(h))
	fmt.Println("binary:", selfPath)
	fmt.Println("version:", version)
}

type window struct {
	owner               string
	pid, layer          int
	x, y, width, height int
	title               string
}

func listWindows(all bool) []window {
	var ws []window
	for _, line := range strings.Split(goString(C.dr_windows(C.bool(all))), "\n") {
		f := strings.SplitN(line, "\t", 8)
		if len(f) < 8 {
			continue
		}
		n := make([]int, 6)
		for i := range n {
			n[i], _ = strconv.Atoi(f[i+1])
		}
		ws = append(ws, window{f[0], n[0], n[1], n[2], n[3], n[4], n[5], f[7]})
	}
	return ws
}

func printWindows(all bool) {
	for _, w := range listWindows(all) {
		fields := []string{w.owner, strconv.Itoa(w.pid)}
		if all {
			fields = append(fields, strconv.Itoa(w.layer))
		}
		fields = append(fields, strconv.Itoa(w.x), strconv.Itoa(w.y), strconv.Itoa(w.width), strconv.Itoa(w.height), w.title)
		fmt.Println(strings.Join(fields, "\t"))
	}
}

// An app's name, or a pid.
func appOrPid(which string) (string, int) {
	if pid, err := strconv.Atoi(which); err == nil {
		return "", pid
	}
	return which, 0
}

// One window, with whatever overlaps it left out, or the whole display. Out "-" is stdout.
func shot(out, which string) {
	need("screen recording")
	app, pid := appOrPid(which)
	a, png := capture(captureRequest{Op: "shot", App: app, Pid: pid})
	if a.Error != "" {
		fail(1, "%s", a.Error)
	}
	if out == "-" {
		os.Stdout.Write(png)
	} else if err := os.WriteFile(out, png, 0o644); err != nil {
		fail(1, "can't write %s: %v", out, err)
	}
	if out != "-" {
		w, h := pngSize(png)
		fmt.Printf("saved %s %d x %d\n", out, w, h)
	}
}

// In the capture server: one window, or the whole display, as PNG.
func shotPNG(app string, pid int) ([]byte, error) {
	capp := C.CString(app)
	defer C.free(unsafe.Pointer(capp))
	var size C.long
	var cerr *C.char
	data := C.dr_shot(capp, C.int(pid), &size, &cerr)
	if data == nil {
		return nil, fmt.Errorf("%s", goString(cerr))
	}
	defer C.free(data)
	return C.GoBytes(data, C.int(size)), nil
}

func accessibilityOn() bool { return bool(C.dr_accessibility(false)) }

func pngSize(png []byte) (int, int) {
	if len(png) < 24 {
		return 0, 0
	}
	be := func(b []byte) int { return int(b[0])<<24 | int(b[1])<<16 | int(b[2])<<8 | int(b[3]) }
	return be(png[16:20]), be(png[20:24])
}

// Input lands on whatever is on top at that point, so raise the target first.
func raise(which string) {
	need("accessibility")
	app, pid := appOrPid(which)
	for _, w := range listWindows(false) {
		if (pid > 0 && w.pid == pid) || (pid == 0 && w.owner == app) {
			// Right after a login, Accessibility can answer "cannot complete" for a moment.
			e := C.dr_raise(C.int(w.pid))
			for try := 0; e == -25204 && try < 10; try++ {
				time.Sleep(300 * time.Millisecond)
				e = C.dr_raise(C.int(w.pid))
			}
			if e != 0 {
				fail(1, "can't raise %s: %d", which, int(e))
			}
			return
		}
	}
	// Minimised, or on another Space: bring it here.
	if pid == 0 {
		capp := C.CString(app)
		pid = int(C.dr_app_pid(capp))
		C.free(unsafe.Pointer(capp))
	}
	if pid == 0 || !show(pid) {
		fail(1, "no window for %s", which)
	}
}

// Brings pid's windows onto the current desktop, and says whether one arrived.
func show(pid int) bool {
	if C.dr_show(C.int(pid)) != 0 {
		return false
	}
	for i := 0; i < 30; i++ {
		for _, w := range listWindows(false) {
			if w.pid == pid && w.width >= 100 && w.height >= 100 {
				// Let the move between Spaces finish before anyone captures it.
				time.Sleep(1500 * time.Millisecond)
				return true
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

// Notification banners sit over apps, take clicks and show in shots.
func dismissNotifications() {
	need("accessibility")
	n := int(C.dr_dismiss_notifications())
	if n < 0 {
		fail(1, "Notification Center isn't running")
	}
	fmt.Printf("closed %d notifications\n", n)
}

const (
	mouseDown    = 1
	mouseUp      = 2
	mouseMoved   = 5
	mouseDragged = 6
)

var pointerX, pointerY float64

func mouse(kind int, x, y float64, clicks int) {
	C.dr_mouse(C.int(kind), C.double(x), C.double(y), C.int(clicks),
		C.int(math.Round(x-pointerX)), C.int(math.Round(y-pointerY)))
	pointerX, pointerY = x, y
	pause(30 * time.Millisecond)
}

func click(x, y float64, times int) {
	mouse(mouseMoved, x, y, 1)
	for n := 1; n <= times; n++ {
		mouse(mouseDown, x, y, n)
		mouse(mouseUp, x, y, n)
	}
}

func drag(x1, y1, x2, y2 float64) {
	mouse(mouseMoved, x1, y1, 1)
	pause(200 * time.Millisecond)
	mouse(mouseDown, x1, y1, 1)
	pause(150 * time.Millisecond)
	const steps = 20
	for s := 1; s <= steps; s++ {
		part := float64(s) / steps
		mouse(mouseDragged, math.Round(x1+(x2-x1)*part), math.Round(y1+(y2-y1)*part), 1)
	}
	pause(150 * time.Millisecond)
	mouse(mouseUp, x2, y2, 1)
}

// A trackpad scroll at a point: the gesture phases a real two-finger scroll carries, then a
// short momentum tail. Wheel events without phases don't drive macOS's elastic overscroll;
// these do. dy is pixels per step, positive to scroll the content up.
func scroll(x, y, dy float64, steps int, hold time.Duration) {
	mouse(mouseMoved, x, y, 1)
	post := func(delta float64, phase, momentum int) {
		C.dr_scroll(C.double(x), C.double(y), C.int(delta), C.int(phase), C.int(momentum))
		pause(16 * time.Millisecond)
	}
	post(0, 1, 0) // began
	for i := 0; i < steps; i++ {
		post(dy, 2, 0) // changed
	}
	pause(hold)      // fingers still down: a rubber band is at full stretch
	post(0, 4, 0)    // ended
	post(dy/2, 0, 1) // momentum began
	for s := 1; s <= 6; s++ {
		post(dy/float64(2+s), 0, 2)
	}
	post(0, 0, 3) // momentum ended
}

// A combination such as cmd+t, sent with the modifiers as flags; a modifier alone (`key ctrl`)
// is a real press and release.
func key(combo string) error {
	mods, name := parseCombo(combo)
	code := -1
	if name != "" {
		c, ok := keyCode(name)
		if !ok {
			return fmt.Errorf("unknown key %q", name)
		}
		code = c
	} else if len(mods) == 0 {
		return fmt.Errorf("no key in %q", combo)
	}
	holding(mods, func(flags uint64) {
		if code < 0 {
			return
		}
		for _, down := range []bool{true, false} {
			C.dr_key(C.int(code), C.uint64_t(flags), C.bool(down), -1)
			pause(30 * time.Millisecond)
		}
	})
	return nil
}

// Presses the modifiers one by one, runs press with the flags they make, and lets them go in
// reverse, as a person would. Sending only flagged keys leaves the session thinking the
// modifiers are still held, and the next click goes in with them.
func holding(mods []string, press func(flags uint64)) {
	var flags uint64
	for _, m := range mods {
		flags |= modifierFlags[m]
		C.dr_modifier(C.int(modifierCodes[m]), C.uint64_t(flags))
		pause(15 * time.Millisecond)
	}
	press(flags)
	for i := len(mods) - 1; i >= 0; i-- {
		flags &^= modifierFlags[mods[i]]
		C.dr_modifier(C.int(modifierCodes[mods[i]]), C.uint64_t(flags))
		pause(15 * time.Millisecond)
	}
}

// Text, typed as a person would on a US keyboard: each character's own key, with Shift where it
// needs one. A character no key types is sent as itself.
func typeText(text string) {
	post := func(code int, flags uint64, unicode int) {
		for _, down := range []bool{true, false} {
			C.dr_key(C.int(code), C.uint64_t(flags), C.bool(down), C.int(unicode))
			pause(12 * time.Millisecond)
		}
		pause(20 * time.Millisecond)
	}
	for _, c := range text {
		if code, shift, ok := keyFor(c); ok {
			if shift {
				holding([]string{"shift"}, func(flags uint64) { post(code, flags, -1) })
			} else {
				post(code, 0, -1)
			}
			continue
		}
		for _, u := range utf16(c) {
			post(0, 0, int(u))
		}
	}
}

func utf16(r rune) []uint16 {
	if r < 0x10000 {
		return []uint16{uint16(r)}
	}
	r -= 0x10000
	return []uint16{uint16(0xd800 + (r >> 10)), uint16(0xdc00 + (r & 0x3ff))}
}

// One action, as a command's arguments or a line of `input`.
func act(verb string, args []string) error {
	n := make([]float64, 0, len(args))
	for _, a := range args {
		f, err := strconv.ParseFloat(a, 64)
		if err != nil {
			break
		}
		n = append(n, f)
	}
	numbers := func(want ...int) bool {
		for _, w := range want {
			if len(n) == w && len(args) == w {
				return true
			}
		}
		return false
	}
	switch {
	case verb == "click" && numbers(2):
		click(n[0], n[1], 1)
	case verb == "dblclick" && numbers(2):
		click(n[0], n[1], 2)
	case verb == "move" && numbers(2):
		mouse(mouseMoved, n[0], n[1], 1)
	case verb == "down" && numbers(0):
		mouse(mouseDown, pointerX, pointerY, 1)
	case verb == "up" && numbers(0):
		mouse(mouseUp, pointerX, pointerY, 1)
	case verb == "drag" && numbers(4):
		drag(n[0], n[1], n[2], n[3])
	case verb == "scroll" && numbers(3, 4, 5):
		steps, hold := 10, 0.0
		if len(n) > 3 {
			steps = int(n[3])
		}
		if len(n) > 4 {
			hold = n[4]
		}
		scroll(n[0], n[1], n[2], steps, time.Duration(hold)*time.Millisecond)
	case verb == "key" && len(args) == 1:
		return key(args[0])
	case verb == "type":
		typeText(strings.Join(args, " "))
	default:
		return fmt.Errorf("unknown: %s %s", verb, strings.Join(args, " "))
	}
	return nil
}

func startInput() {
	need("accessibility")
	var x, y C.double
	C.dr_pointer(&x, &y)
	pointerX, pointerY = float64(x), float64(y)
}

func doAction(verb string, args []string) {
	startInput()
	start := time.Now()
	if err := act(verb, args); err != nil {
		fail(1, "%v", err)
	}
	noteEvent(start, verb, args)
}

// One action per line on stdin, answered "ok" or "error: ..." per line.
func inputLoop() {
	startInput()
	fmt.Println("ready")
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 1<<20), 1<<20)
	for in.Scan() {
		line := in.Text()
		verb, rest, _ := strings.Cut(line, " ")
		if verb == "" {
			continue
		}
		var args []string
		if verb == "type" || verb == "key" {
			args = []string{rest}
		} else {
			args = strings.Fields(rest)
		}
		start := time.Now()
		if err := act(verb, args); err != nil {
			fmt.Println("error:", err)
		} else {
			noteEvent(start, verb, args)
			fmt.Println("ok")
		}
	}
}

// A command run as deskrig's child, so macOS counts it as deskrig and its permissions cover it.
func runCommand(command []string) {
	path, err := exec.LookPath(command[0])
	if err != nil {
		fail(127, "no %s on PATH", command[0])
	}
	cmd := exec.Command(path, command[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err = cmd.Run()
	if e, ok := err.(*exec.ExitError); ok {
		if s, ok := e.Sys().(syscall.WaitStatus); ok && s.Signaled() {
			finish(128 + int(s.Signal()))
		}
		finish(e.ExitCode())
	} else if err != nil {
		fail(127, "can't run %s: %v", command[0], err)
	}
	finish(0)
}

// Keeps one window's latest frame in a file until the window's process ends or the time is up.
// Whether pid's window is on the current desktop, and whether another app's window is over it.
// A VM's window draws nothing while covered or away, so its frames go stale.
func windowPlace(pid int) (onScreen, covered bool) {
	ws := listWindows(false)
	for i, w := range ws {
		if w.pid != pid || w.width < 100 || w.height < 100 {
			continue
		}
		for _, o := range ws[:i] {
			if o.pid != pid && o.x < w.x+w.width && w.x < o.x+o.width && o.y < w.y+w.height && w.y < o.y+o.height {
				return true, true
			}
		}
		return true, false
	}
	return false, false
}
