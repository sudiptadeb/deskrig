package main

// `deskrig install <app.flatpak | app.flatpakref | URL>` on Linux: a Flatpak bundle, or an app
// from a repository its .flatpakref names, installed for this user. A copy of the app that is
// running would keep the old version, so it is quit, and deskrig waits until it has gone.

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func newLineScanner(f *os.File) *bufio.Scanner {
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 1<<20), 1<<20)
	return s
}

// app id -> active commit, for this user's apps.
func flatpakApps() map[string]string {
	out, _ := output("flatpak", "list", "--user", "--app", "--columns=application,active")
	apps := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if f := strings.Fields(line); len(f) == 2 {
			apps[f[0]] = f[1]
		}
	}
	return apps
}

func linuxInstall(args []string) {
	if len(args) != 1 {
		usage()
	}
	if _, err := findSession(); err != nil {
		fail(2, "%v", err)
	}
	from := args[0]
	kind := ""
	switch {
	case strings.HasSuffix(from, ".flatpak"):
		kind = "--bundle"
	case strings.HasSuffix(from, ".flatpakref"):
		kind = "--from"
	default:
		fail(2, "on Linux, install takes a Flatpak bundle (.flatpak) or a .flatpakref, as a path or a URL")
	}
	dir, err := os.MkdirTemp("", "deskrig-install-")
	if err != nil {
		fail(1, "%v", err)
	}
	defer os.RemoveAll(dir)
	local := filepath.Join(dir, filepath.Base(from))
	fetchFile(from, local)
	before := flatpakApps()
	cmd := commandWithOutput("flatpak", "install", "--user", "--noninteractive", "-y", "--reinstall", kind, local)
	if err := cmd.Run(); err != nil {
		os.RemoveAll(dir)
		finish(exitCode(err))
	}
	after := flatpakApps()
	var app string
	for id, commit := range after {
		if before[id] != commit {
			app = id
		}
	}
	if app == "" {
		fail(1, "flatpak installed nothing new from %s", from)
	}
	if out, _ := output("flatpak", "ps", "--columns=application"); strings.Contains(out, app) {
		fmt.Printf("quitting the running %s\n", app)
		output("flatpak", "kill", app)
		for i := 0; i < 100; i++ {
			out, _ := output("flatpak", "ps", "--columns=application")
			if !strings.Contains(out, app) {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if out, _ := output("flatpak", "ps", "--columns=application"); strings.Contains(out, app) {
			fail(1, "%s is still running after flatpak kill", app)
		}
	}
	info, _ := output("flatpak", "info", "--user", app)
	ver := ""
	for _, line := range strings.Split(info, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), ":"); ok && strings.TrimSpace(k) == "Version" {
			ver = strings.TrimSpace(v)
		}
	}
	fmt.Printf("installed %s %s (%s)\n", app, ver, after[app])
	os.RemoveAll(dir)
	finish(0)
}
