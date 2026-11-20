package main

// `deskrig install <app.flatpak | app.flatpakref | URL>` on Linux: a Flatpak bundle, or an app
// from a repository its .flatpakref names, installed for this user. A copy of the app that is
// running would keep the old version, so it is quit, and deskrig waits until it has gone.

import (
	"bufio"
	"fmt"
	"io"
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

// The app a bundle or .flatpakref installs, and for a bundle the runtime it needs. A bundle's
// header carries the app's metadata file as text: "[Application]", "name=<id>",
// "runtime=<ref>"; a .flatpakref is a key file with Name=<id>.
func flatpakAppID(path string) (id, runtime string, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", "", err
	}
	defer f.Close()
	head := make([]byte, 1<<16)
	n, _ := io.ReadFull(f, head)
	text := string(head[:n])
	marker := "[Application]\nname="
	if strings.HasSuffix(path, ".flatpakref") {
		marker = "\nName="
		text = "\n" + text
	}
	line := func(after string) string {
		i := strings.Index(text, after)
		if i < 0 {
			return ""
		}
		v := text[i+len(after):]
		if j := strings.IndexAny(v, "\n\r\x00"); j >= 0 {
			v = v[:j]
		}
		return strings.TrimSpace(v)
	}
	id = line(marker)
	if id == "" {
		return "", "", fmt.Errorf("%s names no app", filepath.Base(path))
	}
	if kind := "\nruntime="; !strings.HasSuffix(path, ".flatpakref") {
		runtime = line(kind)
	}
	return id, runtime, nil
}

func flatpakRunning(app string) bool {
	out, _ := output("flatpak", "ps", "--columns=application")
	for _, line := range strings.Fields(out) {
		if line == app {
			return true
		}
	}
	return false
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
	app, runtime, err := flatpakAppID(local)
	if err != nil {
		fail(1, "%v", err)
	}
	// A bundle doesn't bring its runtime; a fresh desktop gets it from Flathub.
	if runtime != "" {
		_, user := output("flatpak", "info", "--user", runtime)
		_, system := output("flatpak", "info", "--system", runtime)
		if user != 0 && system != 0 {
			fmt.Printf("installing its runtime %s from Flathub\n", runtime)
			output("flatpak", "remote-add", "--user", "--if-not-exists", "flathub", "https://dl.flathub.org/repo/flathub.flatpakrepo")
			cmd := commandWithOutput("flatpak", "install", "--user", "--noninteractive", "-y", "flathub", "runtime/"+runtime)
			cmd.Stdout = io.Discard
			if err := cmd.Run(); err != nil {
				fail(exitCode(err), "can't install the runtime %s from Flathub", runtime)
			}
		}
	}
	// A copy still running would keep the old version: quit it, and wait until it has gone.
	if flatpakRunning(app) {
		fmt.Printf("quitting the running %s\n", app)
		output("flatpak", "kill", app)
		for i := 0; i < 100 && flatpakRunning(app); i++ {
			time.Sleep(100 * time.Millisecond)
		}
		if flatpakRunning(app) {
			fail(1, "%s is still running after flatpak kill", app)
		}
	}
	// flatpak won't install a bundle over the app, so the old one goes first; its data stays.
	if _, code := output("flatpak", "info", "--user", app); code == 0 {
		if out, code := output("flatpak", "uninstall", "--user", "--noninteractive", "-y", app); code != 0 {
			fail(1, "can't remove the installed %s: %s", app, strings.TrimSpace(out))
		}
	}
	cmd := commandWithOutput("flatpak", "install", "--user", "--noninteractive", "-y", kind, local)
	cmd.Stdout = io.Discard
	if err := cmd.Run(); err != nil {
		os.RemoveAll(dir)
		finish(exitCode(err))
	}
	info, _ := output("flatpak", "info", "--user", app)
	fields := map[string]string{}
	for _, line := range strings.Split(info, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), ":"); ok {
			fields[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	installed := app
	if v := fields["Version"]; v != "" {
		installed += " " + v
	}
	commit := fields["Commit"]
	if len(commit) > 12 {
		commit = commit[:12]
	}
	fmt.Printf("installed %s (%s)\n", installed, commit)
	os.RemoveAll(dir)
	finish(0)
}
