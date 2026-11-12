package main

// Frames: the latest frame of a window (or a VM's screen), kept current in a mapped file by a
// watch job, for a test that needs frames faster than one shot per command.
//
//	offset  size  what
//	0       8     "DESKRIG1"
//	8       8     frame count, little-endian uint64; odd while a frame is being written
//	16      4     width in pixels, uint32
//	20      4     height in pixels, uint32
//	24      4     bytes per row, uint32
//	28      4     1 while the window is covered or off the desktop: the picture may be stale
//	32      8     capture time, seconds since 1970, float64
//	40      ...   the pixels, BGRA, rows of `bytes per row`
//
// A reader copies the pixels while the count is even, and keeps the copy if the count is the
// same afterwards. A VM's window draws nothing while covered; `deskrig raise` the app
// (or a VM's window by its pid) to bring it back, and the watch picks up again. The file grows or shrinks when the window does. Pixels per point is the
// width in pixels over pointsWide, which `deskrig frames` prints.

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const framesFor = 7 * 24 * time.Hour

func frameCount(file string) uint64 {
	f, err := os.Open(file)
	if err != nil {
		return 0
	}
	defer f.Close()
	b := make([]byte, 16)
	if n, _ := f.Read(b); n < 16 || string(b[:8]) != "DESKRIG1" {
		return 0
	}
	return binary.LittleEndian.Uint64(b[8:])
}

// Starts a watch job, replacing one under the same label, and waits for its first frame.
// Returns the size it watches, in points.
func startFrames(label string, pid int, file string, screenRatio float64) (float64, float64) {
	os.MkdirAll(filepath.Dir(file), 0o755)
	os.Remove(file)
	log := file + ".log"
	os.Remove(log)
	// A window just brought back from the Dock or another Space can show nothing new for a
	// while, and ScreenCaptureKit sends only changes; a fresh watch gets a whole frame.
	for attempt := 0; attempt < 2; attempt++ {
		startJob(label, []string{selfPath, "--target", "local", "watch-run", strconv.Itoa(pid), file,
			strconv.FormatFloat(screenRatio, 'f', -1, 64), strconv.Itoa(int(framesFor.Seconds()))}, log, true)
		for i := 0; i < 80; i++ {
			if frameCount(file) > 0 {
				data, _ := os.ReadFile(log)
				var p int
				var w, h float64
				fmt.Sscanf(string(data), "watching pid %d, %g x %g points", &p, &w, &h)
				return w, h
			}
			if jobPid(label) == 0 && i > 10 {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if data, _ := os.ReadFile(log); len(strings.TrimSpace(string(data))) > 0 {
			break
		}
	}
	stopJob(label)
	data, _ := os.ReadFile(log)
	fail(1, "no frames from pid %d's window: %s", pid, strings.TrimSpace(string(data)))
	return 0, 0
}

func printFrames(file string, wide, high float64) {
	data, _ := json.Marshal(map[string]any{"file": file, "pointsWide": wide, "pointsHigh": high})
	fmt.Println(string(data))
}

// A window of this Mac, by app name or pid.
func localFrames(args []string) {
	if len(args) != 1 {
		usage()
	}
	app, pid := appOrPid(args[0])
	if pid == 0 {
		out, code := output(selfPath, "--target", "local", "windows")
		if code != 0 {
			fail(code, "%s", out)
		}
		for _, line := range strings.Split(out, "\n") {
			f := strings.Split(line, "\t")
			if len(f) > 1 && f[0] == app {
				pid, _ = strconv.Atoi(f[1])
				break
			}
		}
		if pid == 0 {
			fail(1, "no window on screen for %s", app)
		}
	}
	file := filepath.Join(deskrigHome, "frames", strconv.Itoa(pid)+".bin")
	w, h := startFrames(fmt.Sprintf("%s.frames.%d", labelPrefix, pid), pid, file, 0)
	printFrames(file, w, h)
	finish(0)
}
