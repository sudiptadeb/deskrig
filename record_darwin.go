package main

// Recording: every frame the target shows, captured where it is drawn (inside a VM, the VM
// itself), with the time each was on screen, so a test can check timing after the fact.
//
// A recording is a folder:
//
//	frames.tsv   frame, time, width, height, file: one line per frame, written once the
//	             file is whole, so lines can come late and out of order. time is when the
//	             frame was on screen, UTC to the microsecond (2026-11-12T16:45:05.123456Z),
//	             by the target's clock. A file of "-" is a frame that was dropped because
//	             encoding fell behind.
//	<time>.png   the frames, named by that time: 20261112T164505.123456Z.png
//	events.tsv   start, end (UTC, as above), action: each input action (deskrig click, key, ..., and input's
//	             lines) while recording, on the same clock as the frames
//	info.json    pointsWide, pointsHigh, pixelsPerPoint
//	done         written when the recording has stopped and every frame is in; holds the count
//
// On a VM the folder is in the VM's data folder, shared with this Mac, so the files appear
// here while it records.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const recordLimit = 30 * time.Minute

// Holds the folder being recorded into, while recording.
var recordMarker = filepath.Join(deskrigHome, "recording")

func newRecordingName() string { return time.Now().UTC().Format("20060102T150405.000Z") }

// record start [app] | record stop, on this Mac (or, run by the host, inside a VM).
// "start-in <dir> [app]" records into a given folder.
func localRecord(args []string) {
	switch args[0] {
	case "start", "start-in":
		dir := filepath.Join(deskrigHome, "recordings", newRecordingName())
		if args[0] == "start-in" {
			if len(args) < 2 {
				usage()
			}
			dir, args = args[1], args[1:]
		}
		app := ""
		if len(args) > 1 {
			app = args[1]
		}
		fmt.Println(startRecording(dir, app))
	case "stop":
		dir, n := stopRecording()
		fmt.Printf("{\"dir\":%q,\"frames\":%d}\n", dir, n)
	default:
		usage()
	}
}

func startRecording(dir, app string) string {
	need("screen recording")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fail(1, "can't make %s: %v", dir, err)
	}
	os.WriteFile(filepath.Join(dir, "events.tsv"), []byte("start\tend\taction\n"), 0o644)
	a, _ := capture(captureRequest{Op: "record-start", Dir: dir, App: app})
	if a.Error != "" {
		fail(1, "%s", a.Error)
	}
	info, _ := json.Marshal(map[string]any{"dir": dir, "pointsWide": a.Wide, "pointsHigh": a.High,
		"pixelsPerPoint": a.Scale})
	os.WriteFile(filepath.Join(dir, "info.json"), info, 0o644)
	os.WriteFile(recordMarker, []byte(dir), 0o644)
	return string(info)
}

func stopRecording() (string, int) {
	data, err := os.ReadFile(recordMarker)
	if err != nil {
		fail(1, "not recording")
	}
	a, _ := capture(captureRequest{Op: "record-stop"})
	os.Remove(recordMarker)
	if a.Error != "" {
		fail(1, "%s", a.Error)
	}
	return string(data), a.Frames
}

// Every time deskrig writes is UTC to the microsecond, so times from different machines line up.
func utcTime(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000000Z") }

// Notes an input action in the recording's events.tsv, if one is running.
func noteEvent(start time.Time, verb string, args []string) {
	dir, err := os.ReadFile(recordMarker)
	if err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(string(dir), "events.tsv"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s\t%s\t%s\n", utcTime(start), utcTime(time.Now()), strings.Join(append([]string{verb}, args...), " "))
}
