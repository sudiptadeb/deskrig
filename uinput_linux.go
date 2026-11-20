package main

// Input on Linux: three virtual devices made through uinput, a pointer with absolute
// coordinates, a keyboard and a touchpad. To the kernel they are hardware, so every compositor,
// Wayland or X11, takes them as it takes a real mouse, keyboard and trackpad. A touchpad scroll
// reaches the app as a real two-finger scroll, and the app does its own momentum after the
// fingers lift.
//
// A compositor takes a moment to pick up a new device, so the devices live in one input server
// per desktop session (`deskrig input-serve`), which every deskrig command talks to over a Unix
// socket, one action per line, answered "ok" or "error: ...", as `deskrig input` does. It starts
// on first use and leaves after ten idle minutes.

import (
	"bufio"
	"fmt"
	"math"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

const (
	uiSetEvbit   = 0x40045564
	uiSetKeybit  = 0x40045565
	uiSetAbsbit  = 0x40045567
	uiSetPropbit = 0x4004556e
	uiDevCreate  = 0x5501
	uiDevDestroy = 0x5502
	uiDevSetup   = 0x405c5503
	uiAbsSetup   = 0x401c5504

	evSyn, evKey, evAbs        = 0, 1, 3
	btnLeft, btnRight, btnMid  = 0x110, 0x111, 0x112
	btnToolFinger, btnTouch    = 0x145, 0x14a
	btnToolDoubletap           = 0x14d
	absX, absY                 = 0, 1
	absMtSlot, absMtX, absMtY  = 0x2f, 0x35, 0x36
	absMtTrackingID            = 0x39
	propPointer, propButtonpad = 0, 2

	// The touchpad: 160 x 160 mm at 40 units a millimetre.
	padUnitsPerMM = 40
	padMM         = 160
)

type uinputSetup struct {
	bustype, vendor, product, version uint16
	name                              [80]byte
	ffMax                             uint32
}

type uinputAbsSetup struct {
	code                                    uint16
	_                                       uint16
	value, min, max, fuzz, flat, resolution int32
}

type uinputDevice struct{ fd int }

func uinputIoctl(fd int, req, arg uintptr) error {
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), req, arg); e != 0 {
		return e
	}
	return nil
}

func newDevice(name string, product uint16, evs, keys, abs, props []int, ranges map[int][3]int32) (*uinputDevice, error) {
	fd, err := syscall.Open("/dev/uinput", syscall.O_WRONLY|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("can't open /dev/uinput: %v (`deskrig setup` lets the desktop's user use it)", err)
	}
	set := func(req uintptr, values []int) {
		for _, v := range values {
			if err == nil {
				err = uinputIoctl(fd, req, uintptr(v))
			}
		}
	}
	set(uiSetEvbit, evs)
	set(uiSetKeybit, keys)
	set(uiSetAbsbit, abs)
	set(uiSetPropbit, props)
	for code, r := range ranges {
		s := uinputAbsSetup{code: uint16(code), min: r[0], max: r[1], resolution: r[2]}
		if err == nil {
			err = uinputIoctl(fd, uiAbsSetup, uintptr(unsafe.Pointer(&s)))
		}
	}
	s := uinputSetup{bustype: 0x06, vendor: 0x1d6b, product: product, version: 1}
	copy(s.name[:], name)
	if err == nil {
		err = uinputIoctl(fd, uiDevSetup, uintptr(unsafe.Pointer(&s)))
	}
	if err == nil {
		err = uinputIoctl(fd, uiDevCreate, 0)
	}
	if err != nil {
		syscall.Close(fd)
		return nil, fmt.Errorf("can't make the %s: %v", name, err)
	}
	return &uinputDevice{fd}, nil
}

func (d *uinputDevice) emit(typ, code int, value int32) {
	var b [24]byte // struct input_event on 64-bit: a timeval, then type, code and value
	*(*uint16)(unsafe.Pointer(&b[16])) = uint16(typ)
	*(*uint16)(unsafe.Pointer(&b[18])) = uint16(code)
	*(*int32)(unsafe.Pointer(&b[20])) = value
	syscall.Write(d.fd, b[:])
}

func (d *uinputDevice) sync() { d.emit(evSyn, 0, 0) }

func (d *uinputDevice) close() {
	uinputIoctl(d.fd, uiDevDestroy, 0)
	syscall.Close(d.fd)
}

// The server's devices, and where its pointer is.
var (
	pointerDev, keyboardDev, touchpadDev *uinputDevice
	screenW, screenH                     int
	pointerX, pointerY                   float64
	buttonHeld                           bool
)

func makeDevices(w, h int) error {
	closeDevices()
	var err error
	pointerDev, err = newDevice("deskrig pointer", 1, []int{evKey, evAbs}, []int{btnLeft, btnRight, btnMid},
		[]int{absX, absY}, nil, map[int][3]int32{absX: {0, int32(w - 1), 0}, absY: {0, int32(h - 1), 0}})
	if err != nil {
		return err
	}
	keys := make([]int, 0, 248)
	for k := 1; k < 249; k++ {
		keys = append(keys, k)
	}
	keyboardDev, err = newDevice("deskrig keyboard", 2, []int{evKey}, keys, nil, nil, nil)
	if err != nil {
		return err
	}
	const max = padMM * padUnitsPerMM
	touchpadDev, err = newDevice("deskrig touchpad", 3, []int{evKey, evAbs},
		[]int{btnLeft, btnToolFinger, btnTouch, btnToolDoubletap},
		[]int{absX, absY, absMtSlot, absMtX, absMtY, absMtTrackingID}, []int{propPointer, propButtonpad},
		map[int][3]int32{absX: {0, max, padUnitsPerMM}, absY: {0, max, padUnitsPerMM}, absMtSlot: {0, 1, 0},
			absMtX: {0, max, padUnitsPerMM}, absMtY: {0, max, padUnitsPerMM}, absMtTrackingID: {0, 65535, 0}})
	if err != nil {
		return err
	}
	screenW, screenH = w, h
	pointerX, pointerY = float64(w/2), float64(h/2)
	// Until the compositor has taken the new devices, their events go nowhere.
	time.Sleep(1500 * time.Millisecond)
	if strings.Contains(os.Getenv("XDG_CURRENT_DESKTOP"), "KDE") {
		kdeTouchpadSettings()
	}
	return nil
}

func closeDevices() {
	for _, d := range []*uinputDevice{pointerDev, keyboardDev, touchpadDev} {
		if d != nil {
			d.close()
		}
	}
	pointerDev, keyboardDev, touchpadDev = nil, nil, nil
}

func pause(d time.Duration) { time.Sleep(d) }

// The actions `act` (common.go) runs, inside the input server.

func moveTo(x, y float64) {
	x = math.Max(0, math.Min(x, float64(screenW-1)))
	y = math.Max(0, math.Min(y, float64(screenH-1)))
	pointerDev.emit(evAbs, absX, int32(math.Round(x)))
	pointerDev.emit(evAbs, absY, int32(math.Round(y)))
	pointerDev.sync()
	pointerX, pointerY = x, y
	pause(30 * time.Millisecond)
}

func press(down bool) {
	v := int32(0)
	if down {
		v = 1
	}
	pointerDev.emit(evKey, btnLeft, v)
	pointerDev.sync()
	buttonHeld = down
	pause(30 * time.Millisecond)
}

func click(x, y float64, times int) {
	moveTo(x, y)
	for n := 0; n < times; n++ {
		press(true)
		press(false)
	}
}

func drag(x1, y1, x2, y2 float64) {
	moveTo(x1, y1)
	pause(200 * time.Millisecond)
	press(true)
	pause(150 * time.Millisecond)
	const steps = 20
	for s := 1; s <= steps; s++ {
		part := float64(s) / steps
		moveTo(math.Round(x1+(x2-x1)*part), math.Round(y1+(y2-y1)*part))
	}
	pause(150 * time.Millisecond)
	press(false)
}

// A two-finger touchpad scroll at a point, dy pixels a step, positive to scroll the content up
// (the fingers move up, as with natural scrolling, which the guest setup turns on). The
// fingers lift after hold; the app then does its own momentum, as it would for a person.
func scroll(x, y, dy float64, steps int, hold time.Duration) {
	moveTo(x, y)
	mmPerStep := dy / scrollPixelsPerMM()
	// The fingers first travel the distance before scrolling starts, which X11 doesn't pass on.
	lead := math.Copysign(scrollStartMM(), dy)
	if math.Abs(mmPerStep*float64(steps))+math.Abs(lead) > padMM-20 {
		mmPerStep = math.Copysign((float64(padMM-20)-math.Abs(lead))/float64(steps), mmPerStep)
	}
	total := lead + mmPerStep*float64(steps)
	startY := float64(padMM)/2 + total/2 // moving up means a falling y
	t := touchpadDev
	at := func(y float64) int32 { return int32(math.Round(y * padUnitsPerMM)) }
	put := func(y float64, down bool) {
		for slot := 0; slot < 2; slot++ {
			t.emit(evAbs, absMtSlot, int32(slot))
			if down {
				t.emit(evAbs, absMtTrackingID, int32(1+slot))
				t.emit(evAbs, absMtX, int32((padMM/2-15+30*slot)*padUnitsPerMM))
				t.emit(evAbs, absMtY, at(y))
			} else {
				t.emit(evAbs, absMtTrackingID, -1)
			}
		}
	}
	put(startY, true)
	t.emit(evAbs, absX, int32((padMM/2-15)*padUnitsPerMM))
	t.emit(evAbs, absY, at(startY))
	t.emit(evKey, btnTouch, 1)
	t.emit(evKey, btnToolDoubletap, 1)
	t.sync()
	for i := 1; i <= steps; i++ {
		pause(16 * time.Millisecond)
		y := startY - lead - mmPerStep*float64(i)
		for slot := 0; slot < 2; slot++ {
			t.emit(evAbs, absMtSlot, int32(slot))
			t.emit(evAbs, absMtY, at(y))
		}
		t.emit(evAbs, absY, at(y))
		t.sync()
	}
	pause(hold)
	pause(16 * time.Millisecond)
	put(0, false)
	t.emit(evKey, btnTouch, 0)
	t.emit(evKey, btnToolDoubletap, 0)
	t.sync()
	pause(30 * time.Millisecond)
}

// Finger travel before a scroll starts that the session doesn't turn into scrolling, measured
// as for scrollPixelsPerMM: about 1.2 mm on GNOME's Xorg session, none on Wayland.
func scrollStartMM() float64 {
	if os.Getenv("XDG_SESSION_TYPE") == "x11" && !strings.Contains(os.Getenv("XDG_CURRENT_DESKTOP"), "KDE") {
		return 1.2
	}
	return 0
}

// How far Chromium scrolls for a millimetre of two-finger travel, with the flat touchpad
// acceleration deskrig sets, measured in the VMs (platform_linux.go). The compositor and the
// session type each scale it.
func scrollPixelsPerMM() float64 {
	switch {
	case strings.Contains(os.Getenv("XDG_CURRENT_DESKTOP"), "KDE"):
		return scrollPxPerMMKDE
	case os.Getenv("XDG_SESSION_TYPE") == "x11":
		return scrollPxPerMMGnomeX11
	}
	return scrollPxPerMMGnome
}

func tapKey(code int) {
	for _, v := range []int32{1, 0} {
		keyboardDev.emit(evKey, code, v)
		keyboardDev.sync()
		pause(12 * time.Millisecond)
	}
}

// Presses the modifiers in order, runs press, and lets them go in reverse, as a person would.
func holdingKeys(mods []string, press func()) {
	for _, m := range mods {
		keyboardDev.emit(evKey, modifierCodes[m], 1)
		keyboardDev.sync()
		pause(15 * time.Millisecond)
	}
	press()
	for i := len(mods) - 1; i >= 0; i-- {
		keyboardDev.emit(evKey, modifierCodes[mods[i]], 0)
		keyboardDev.sync()
		pause(15 * time.Millisecond)
	}
}

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
	holdingKeys(mods, func() {
		if code >= 0 {
			tapKey(code)
		}
	})
	pause(20 * time.Millisecond)
	return nil
}

// Text, typed on a US keyboard: each character's own key, with Shift where it needs one. A
// keyboard can't type a character no key types, so such text is refused before any of it is.
func typeText(text string) error {
	for _, c := range text {
		if _, _, ok := keyFor(c); !ok {
			return fmt.Errorf("can't type %q on Linux: no key on a US keyboard types it", c)
		}
	}
	for _, c := range text {
		code, shift, _ := keyFor(c)
		if shift {
			holdingKeys([]string{"shift"}, func() { tapKey(code) })
		} else {
			tapKey(code)
		}
		pause(20 * time.Millisecond)
	}
	return nil
}

// The input server.

func inputSocket() string { return filepath.Join(runtimeDir(), "input.sock") }

const inputIdle = 10 * time.Minute

func inputServe() {
	sock := inputSocket()
	os.Remove(sock)
	l, err := net.Listen("unix", sock)
	if err != nil {
		fail(1, "input server: %v", err)
	}
	defer os.Remove(sock)
	var mu sync.Mutex
	last := time.Now()
	go func() {
		for range time.Tick(30 * time.Second) {
			mu.Lock()
			idle := time.Since(last) > inputIdle
			mu.Unlock()
			if idle {
				closeDevices()
				os.Remove(sock)
				os.Exit(0)
			}
		}
	}()
	for {
		conn, err := l.Accept()
		if err != nil {
			continue
		}
		go func() {
			defer conn.Close()
			in := bufio.NewScanner(conn)
			in.Buffer(make([]byte, 1<<20), 1<<20)
			for in.Scan() {
				line := in.Text()
				verb, rest, _ := strings.Cut(line, " ")
				mu.Lock()
				last = time.Now()
				answer := "ok"
				switch verb {
				case "hello": // hello <version> <width> <height>
					f := strings.Fields(rest)
					if len(f) != 3 || f[0] != version {
						answer = "version " + version
						break
					}
					w, _ := strconv.Atoi(f[1])
					h, _ := strconv.Atoi(f[2])
					if w != screenW || h != screenH || pointerDev == nil {
						if err := makeDevices(w, h); err != nil {
							answer = "error: " + err.Error()
						}
					}
				case "quit":
					closeDevices()
					os.Remove(sock)
					fmt.Fprintln(conn, "ok")
					os.Exit(0)
				case "":
					mu.Unlock()
					continue
				default:
					var args []string
					if verb == "type" || verb == "key" {
						args = []string{rest}
					} else {
						args = strings.Fields(rest)
					}
					if pointerDev == nil {
						answer = "error: no hello"
					} else if err := act(verb, args); err != nil {
						answer = "error: " + err.Error()
					}
				}
				mu.Unlock()
				fmt.Fprintln(conn, answer)
			}
		}()
	}
}

type inputClient struct {
	conn net.Conn
	in   *bufio.Scanner
}

func (c *inputClient) send(line string) (string, error) {
	if _, err := fmt.Fprintln(c.conn, line); err != nil {
		return "", err
	}
	if !c.in.Scan() {
		return "", fmt.Errorf("the input server went away")
	}
	return c.in.Text(), nil
}

// Connects to this session's input server, starting it, or replacing one from another deskrig
// version, first.
func connectInput(d desktop) *inputClient {
	w, h, err := d.screen()
	if err != nil {
		fail(1, "%v", err)
	}
	hello := fmt.Sprintf("hello %s %d %d", version, w, h)
	for attempt := 0; ; attempt++ {
		if conn, err := net.Dial("unix", inputSocket()); err == nil {
			c := &inputClient{conn, bufio.NewScanner(conn)}
			c.in.Buffer(make([]byte, 1<<20), 1<<20)
			answer, err := c.send(hello)
			if err == nil && answer == "ok" {
				return c
			}
			if err == nil && strings.HasPrefix(answer, "error: ") {
				fail(1, "%s", strings.TrimPrefix(answer, "error: "))
			}
			if err == nil {
				c.send("quit")
			}
			conn.Close()
			time.Sleep(200 * time.Millisecond)
		}
		if attempt == 2 {
			log, _ := os.ReadFile(filepath.Join(runtimeDir(), "input.log"))
			fail(1, "the input server didn't start: %s", lastLine(string(log)))
		}
		startInputServer()
	}
}

func startInputServer() {
	log, _ := os.OpenFile(filepath.Join(runtimeDir(), "input.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	cmd := exec.Command(selfPath, "--target", "local", "input-serve")
	cmd.Stdout, cmd.Stderr = log, log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		fail(1, "can't start the input server: %v", err)
	}
	cmd.Process.Release()
	log.Close()
	for i := 0; i < 50; i++ {
		if _, err := os.Stat(inputSocket()); err == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}
