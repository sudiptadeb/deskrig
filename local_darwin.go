package main

// The local target: this Mac's own desktop. Commands from a shell outside the desktop session
// run again inside it, as a job (session_darwin.go).

import (
	"os"
	"strconv"
	"strings"
	"time"
)

func localDo(verb string, args []string) {
	if !inSession() {
		switch verb {
		case "frames":
			localFrames(args)
		case "install":
			localInstall(args)
		}
		var limit time.Duration
		if verb != "run" && verb != "input" && verb != "request-permission" {
			limit = time.Minute
		}
		relayToSession(append([]string{"--target", "local", verb}, args...), limit, verb == "input")
	}
	openSessionStdin()
	if verb != "run" && verb != "input" && verb != "watch-run" && verb != "request-permission" {
		dropTimer := time.AfterFunc(time.Minute, func() { fail(1, "%s didn't finish in a minute", verb) })
		defer dropTimer.Stop()
	}
	switch verb {
	case "status":
		printStatus()
	case "windows":
		printWindows(len(args) > 0 && args[0] == "--all")
	case "shot":
		which := ""
		if len(args) > 1 {
			which = args[1]
		}
		shot(args[0], which)
	case "raise":
		raise(args[0])
	case "dismiss-notifications":
		dismissNotifications()
	case "input":
		inputLoop()
	case "run":
		runCommand(args)
	case "request-permission":
		findPermission(args[0]).request()
	case "check-permission":
		if p := findPermission(args[0]); p.check == nil || !p.check() {
			finish(1)
		}
	case "watch-run":
		pid, _ := strconv.Atoi(args[0])
		ratio, _ := strconv.ParseFloat(args[2], 64)
		secs, _ := strconv.Atoi(args[3])
		watchRun(pid, args[1], ratio, time.Duration(secs)*time.Second)
	default:
		doAction(verb, args)
	}
	finish(0)
}

func localInstall(args []string) {
	if len(args) != 1 {
		usage()
	}
	dir, err := os.MkdirTemp("", "deskrig-install-")
	if err != nil {
		fail(1, "%v", err)
	}
	defer os.RemoveAll(dir)
	dmg := dir + "/app.dmg"
	fetchFile(args[0], dmg)
	cmd := commandWithOutput("/bin/bash", "-c", installScript, "install", dmg)
	err = cmd.Run()
	os.RemoveAll(dir)
	finish(exitCode(err))
}

// What `deskrig --target local status` says, from inside the desktop session.
func hostStatus() map[string]string {
	out, _ := output(selfPath, "--target", "local", "status")
	fields := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if k, v, ok := strings.Cut(line, ":"); ok {
			fields[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return fields
}
