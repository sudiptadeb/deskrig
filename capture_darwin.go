package main

// The capture server: one deskrig process in the desktop session that does all of the
// session's screen capture (shots, frame watches, recordings). macOS's capture service tells
// its clients apart by executable path, so two deskrig processes capturing at once keep
// knocking each other off and both hang; one process holding every stream avoids that. Other
// deskrig commands ask it over a Unix socket, one JSON line each way; a shot's PNG follows its
// answer. It starts on first use and leaves after ten idle minutes.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

/*
#include <stdlib.h>
#include "native_darwin.h"
*/
import "C"

var (
	captureLabel  = labelPrefix + ".capture"
	captureSocket = filepath.Join(deskrigHome, "capture.sock")
)

type captureRequest struct {
	Op      string  `json:"op"`
	Version string  `json:"version,omitempty"`
	App     string  `json:"app,omitempty"`
	Pid     int     `json:"pid,omitempty"`
	File    string  `json:"file,omitempty"`
	Ratio   float64 `json:"ratio,omitempty"`
	Dir     string  `json:"dir,omitempty"`
}

type captureAnswer struct {
	Error   string  `json:"error,omitempty"`
	Version string  `json:"version,omitempty"`
	Bytes   int     `json:"bytes,omitempty"`
	Wide    float64 `json:"wide,omitempty"`
	High    float64 `json:"high,omitempty"`
	Scale   float64 `json:"scale,omitempty"`
	Frames  int     `json:"frames,omitempty"`
}

// Asks the capture server, starting it (or replacing one from another deskrig version) first.
func capture(req captureRequest) (captureAnswer, []byte) {
	for attempt := 0; ; attempt++ {
		conn, err := net.Dial("unix", captureSocket)
		if err == nil {
			a, data, err := exchange(conn, captureRequest{Op: "hello"})
			if err == nil && a.Version == version {
				a, data, err = exchange(conn, req)
				conn.Close()
				if err != nil {
					fail(1, "the capture server: %v", err)
				}
				return a, data
			}
			if err == nil {
				exchange(conn, captureRequest{Op: "quit"})
			}
			conn.Close()
		}
		if attempt == 2 {
			log, _ := os.ReadFile(filepath.Join(deskrigHome, "capture.log"))
			fail(1, "the capture server didn't start: %s", lastLine(string(log)))
		}
		stopJob(captureLabel)
		os.Remove(captureSocket)
		startCaptureServer()
		for i := 0; i < 50; i++ {
			if _, err := os.Stat(captureSocket); err == nil {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
}

// On a Mac, a job in the desktop session. Inside a VM, deskrig runs under Tart's guest agent,
// whose permissions cover only its own descendants, so the server is a detached child instead.
func startCaptureServer() {
	log := filepath.Join(deskrigHome, "capture.log")
	if !strings.HasPrefix(selfPath, guestShare) {
		startJob(captureLabel, []string{selfPath, "--target", "local", "capture-serve"}, log, true)
		return
	}
	os.MkdirAll(deskrigHome, 0o755)
	out, err := os.Create(log)
	if err != nil {
		fail(1, "can't write %s: %v", log, err)
	}
	defer out.Close()
	cmd := exec.Command(selfPath, "--target", "local", "capture-serve")
	cmd.Env = append(os.Environ(), inSessionKey+"=1")
	cmd.Stdout, cmd.Stderr = out, out
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		fail(1, "can't start the capture server: %v", err)
	}
	cmd.Process.Release()
}

func exchange(conn net.Conn, req captureRequest) (captureAnswer, []byte, error) {
	var a captureAnswer
	line, _ := json.Marshal(req)
	if _, err := conn.Write(append(line, '\n')); err != nil {
		return a, nil, err
	}
	conn.SetReadDeadline(time.Now().Add(2 * time.Minute))
	r := bufio.NewReader(conn)
	text, err := r.ReadBytes('\n')
	if err != nil {
		return a, nil, err
	}
	if err := json.Unmarshal(text, &a); err != nil {
		return a, nil, err
	}
	var data []byte
	if a.Bytes > 0 {
		data = make([]byte, a.Bytes)
		if _, err := io.ReadFull(r, data); err != nil {
			return a, nil, err
		}
	}
	return a, data, nil
}

func lastLine(s string) string {
	for len(s) > 0 && s[len(s)-1] == '\n' {
		s = s[:len(s)-1]
	}
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '\n' {
			return s[i+1:]
		}
	}
	return s
}

type watch struct {
	id, pid int
	file    string
	ratio   float64
	hidden  bool
}

type captureServer struct {
	mu        sync.Mutex
	watches   map[string]*watch // by file
	recording string            // its folder, while recording
	recordEnd time.Time
	lastUsed  time.Time
}

func captureServe() {
	need("screen recording")
	// Frame watches used to be jobs of their own (to v0.1.6); one left running would fight
	// this server for the capture service.
	list, _ := launchctl("list")
	for _, line := range strings.Split(list, "\n") {
		f := strings.Fields(line)
		if len(f) == 3 && strings.HasPrefix(f[2], labelPrefix+".frames.") {
			stopJob(f[2])
		}
	}
	os.Remove(captureSocket)
	l, err := net.Listen("unix", captureSocket)
	if err != nil {
		fail(1, "can't listen on %s: %v", captureSocket, err)
	}
	s := &captureServer{watches: map[string]*watch{}, lastUsed: time.Now()}
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go s.serve(conn)
		}
	}()
	go s.tend()
	// ScreenCaptureKit can answer on the main queue, so the main thread runs its run loop.
	for {
		C.dr_run_loop(0.2)
		s.mu.Lock()
		idle := len(s.watches) == 0 && s.recording == "" && time.Since(s.lastUsed) > 10*time.Minute
		s.mu.Unlock()
		if idle {
			os.Remove(captureSocket)
			finish(0)
		}
	}
}

func (s *captureServer) serve(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			return
		}
		var req captureRequest
		json.Unmarshal(line, &req)
		s.mu.Lock()
		s.lastUsed = time.Now()
		a, data := s.do(req)
		s.mu.Unlock()
		a.Bytes = len(data)
		out, _ := json.Marshal(a)
		conn.Write(append(out, '\n'))
		conn.Write(data)
		if req.Op == "quit" {
			for _, w := range s.watches {
				C.dr_unwatch(C.int(w.id))
			}
			if s.recording != "" {
				s.stopRecording()
			}
			os.Remove(captureSocket)
			finish(0)
		}
	}
}

func (s *captureServer) do(req captureRequest) (captureAnswer, []byte) {
	switch req.Op {
	case "hello", "quit":
		return captureAnswer{Version: version}, nil
	case "shot":
		data, err := shotPNG(req.App, req.Pid)
		if err != nil {
			return captureAnswer{Error: err.Error()}, nil
		}
		return captureAnswer{}, data
	case "watch":
		return s.watch(req)
	case "record-start":
		if s.recording != "" {
			return captureAnswer{Error: "already recording; `deskrig record stop` first"}, nil
		}
		cdir, capp := C.CString(req.Dir), C.CString(req.App)
		defer C.free(unsafe.Pointer(cdir))
		defer C.free(unsafe.Pointer(capp))
		var wide, high, scale C.double
		var cerr *C.char
		if C.dr_record_start(capp, cdir, &wide, &high, &scale, &cerr) != 0 {
			return captureAnswer{Error: goString(cerr)}, nil
		}
		s.recording, s.recordEnd = req.Dir, time.Now().Add(recordLimit)
		return captureAnswer{Wide: float64(wide), High: float64(high), Scale: float64(scale)}, nil
	case "record-stop":
		if s.recording == "" {
			return captureAnswer{Error: "not recording"}, nil
		}
		return captureAnswer{Frames: s.stopRecording()}, nil
	}
	return captureAnswer{Error: "unknown request " + req.Op}, nil
}

func (s *captureServer) stopRecording() int {
	n := int(C.dr_record_stop())
	os.WriteFile(filepath.Join(s.recording, "done"), []byte(fmt.Sprintln(n)), 0o644)
	os.Remove(recordMarker)
	s.recording = ""
	return n
}

// Watches a window into a frames file, replacing an earlier watch of that file.
func (s *captureServer) watch(req captureRequest) (captureAnswer, []byte) {
	if w := s.watches[req.File]; w != nil {
		C.dr_unwatch(C.int(w.id))
		delete(s.watches, req.File)
	}
	// A window covered, minimised or on another Space may draw nothing: bring it here.
	if on, covered := windowPlace(req.Pid); !on || covered {
		if !accessibilityOn() {
			return captureAnswer{Error: "the window is covered or away, and bringing it here needs Accessibility"}, nil
		}
		show(req.Pid)
	}
	cfile := C.CString(req.File)
	defer C.free(unsafe.Pointer(cfile))
	var wide, high C.double
	var cerr *C.char
	id := C.dr_watch(0, C.int(req.Pid), cfile, C.double(req.Ratio), &wide, &high, &cerr)
	if id < 0 {
		return captureAnswer{Error: goString(cerr)}, nil
	}
	s.watches[req.File] = &watch{id: int(id), pid: req.Pid, file: req.File, ratio: req.Ratio}
	return captureAnswer{Wide: float64(wide), High: float64(high)}, nil
}

// Every half second: ends watches whose app has quit, marks frames stale while their window is
// covered or away and watches afresh when it is back, and stops a recording at its limit.
func (s *captureServer) tend() {
	for {
		time.Sleep(500 * time.Millisecond)
		s.mu.Lock()
		for file, w := range s.watches {
			if syscall.Kill(w.pid, 0) != nil {
				C.dr_unwatch(C.int(w.id))
				delete(s.watches, file)
				continue
			}
			on, covered := windowPlace(w.pid)
			hidden := !on || covered
			failed := C.dr_watch_failed(C.int(w.id))
			if failed != nil {
				C.free(unsafe.Pointer(failed))
			}
			if hidden == w.hidden && (failed == nil || hidden) {
				continue
			}
			w.hidden = hidden
			if hidden {
				C.dr_watch_hidden(C.int(w.id), 1)
				continue
			}
			cfile := C.CString(file)
			var wide, high C.double
			var cerr *C.char
			if C.dr_watch(C.int(w.id), C.int(w.pid), cfile, C.double(w.ratio), &wide, &high, &cerr) < 0 {
				fmt.Fprintf(os.Stderr, "can't watch pid %d again: %s\n", w.pid, goString(cerr))
			} else {
				C.dr_watch_hidden(C.int(w.id), 0)
			}
			C.free(unsafe.Pointer(cfile))
		}
		if s.recording != "" && time.Now().After(s.recordEnd) {
			fmt.Fprintf(os.Stderr, "stopped the recording after %v\n", recordLimit)
			s.stopRecording()
		}
		s.mu.Unlock()
	}
}
