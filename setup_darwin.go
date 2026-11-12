package main

// First setup: every permission deskrig may need, asked for once; the agent skill; and, if
// wanted, a macOS VM. It asks when someone is at a terminal; an agent runs the steps one by one.

import (
	_ "embed"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

//go:embed skills/deskrig/SKILL.md
var skillText string

var installedPath = func() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local/bin/deskrig")
}()

// An answer from the terminal, even under `curl ... | sh`; "" with no terminal.
func ask(question string, yes bool) string {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return ""
	}
	defer tty.Close()
	hint := " [y/N] "
	if yes {
		hint = " [Y/n] "
	}
	fmt.Fprint(tty, question+hint)
	var answer string
	fmt.Fscanln(tty, &answer)
	answer = strings.ToLower(strings.TrimSpace(answer))
	if answer == "" {
		if yes {
			return "y"
		}
		return "n"
	}
	return answer[:1]
}

// Asks macOS from inside the desktop session, which puts deskrig in each settings page. Local
// Network is asked for by reaching the local network.
func requestPermissionsInSession() {
	requestPermissions()
	if c, err := net.Dial("udp", "224.0.0.251:5353"); err == nil {
		c.Write([]byte{0})
		c.Close()
	}
	requestDevicePermissions()
}

// The ones macOS can't be asked about from code: each is asked for once, and setup can only
// say what to switch on.
var unchecked = []struct{ name, pane, why string }{
	{"app management", "Privacy_AppBundles", "so `install` can replace apps in /Applications"},
	{"local network", "Privacy_LocalNetwork", "for devices and machines on this network"},
	{"automation: System Events", "Privacy_Automation", "for AppleScript UI scripting in tests"},
	{"developer tools", "Privacy_DevTools", "so what it starts skips the first-launch security scan"},
}

func openPane(pane string) {
	output(selfPath, "--target", "local", "run", "--", "/usr/bin/open",
		"x-apple.systempreferences:com.apple.preference.security?"+pane)
}

func setupPermissions(wait time.Duration) {
	if selfPath != installedPath {
		say("this copy is at %s, not %s. macOS ties permissions to the path, so they're granted to this copy only.",
			selfPath, installedPath)
	}
	status := hostStatus()
	if status["on console"] != "true" {
		fail(2, "nobody is logged in at this Mac's screen, so there's no desktop to grant permissions on. "+
			"Log in at the screen (Screen Sharing works), then run `deskrig setup permissions` again.")
	}
	missing := func() []permission {
		var m []permission
		for _, p := range permissions {
			if status[p.name] != "true" {
				m = append(m, p)
			}
		}
		return m
	}
	// Asking for some waits for the person's answer, so this runs while they switch the rest on.
	requesting := exec.Command(selfPath, "--target", "local", "request-permissions")
	requesting.Start()
	defer requesting.Process.Signal(os.Interrupt)
	go requesting.Wait()
	// Asks for Automation of System Events; the prompt waits for an answer, so this doesn't.
	asking := exec.Command(selfPath, "--target", "local", "run", "--", "/usr/bin/osascript",
		"-e", `tell application "System Events" to count processes`)
	asking.Start()
	go asking.Wait()

	fmt.Printf("permissions: in System Settings > Privacy & Security, switch on deskrig (%s) under:\n", selfPath)
	for _, p := range permissions {
		done := ""
		if status[p.name] == "true" {
			done = " (on)"
		}
		fmt.Printf("  - %s%s\n", p.name, done)
	}
	for _, u := range unchecked {
		fmt.Printf("  - %s, %s: Allow when macOS asks, or switch it on there\n", u.name, u.why)
	}
	fmt.Println("If deskrig isn't listed in a page, add it with + and Cmd+Shift+G to type the path.")
	for _, p := range missing() {
		openPane(p.pane)
	}
	openPane(unchecked[0].pane)
	openPane(unchecked[3].pane)

	said := map[string]bool{}
	end := time.Now().Add(wait)
	for time.Now().Before(end) {
		status = hostStatus()
		for _, p := range permissions {
			if status[p.name] == "true" && !said[p.name] {
				fmt.Printf("permissions: %s is on\n", p.name)
				said[p.name] = true
			}
		}
		if len(missing()) == 0 {
			fmt.Println("permissions: all on")
			return
		}
		time.Sleep(2 * time.Second)
	}
	var names []string
	for _, p := range missing() {
		names = append(names, p.name)
	}
	fail(2, "permissions: still off after %v: %s. Run `deskrig setup permissions` once they're on.",
		wait, strings.Join(names, ", "))
}

var skillPath = func() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude/skills/deskrig/SKILL.md")
}()

func setupSkill() {
	os.MkdirAll(filepath.Dir(skillPath), 0o755)
	if err := os.WriteFile(skillPath, []byte(skillText), 0o644); err != nil {
		fail(1, "can't write %s: %v", skillPath, err)
	}
	fmt.Printf("skill: wrote %s (other agents: `deskrig skill` prints it)\n", skillPath)
}

func setupVM() {
	vmCreate("macos", defaultImage, 4, 8192, "1440x900pt")
}

func has(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

func setup(args []string) {
	fmt.Printf("deskrig %s, %s\n", version, selfPath)
	setupPermissions(10 * time.Minute)
	answer := ""
	switch {
	case has(args, "--skill"):
		answer = "y"
	case has(args, "--no-skill"):
		answer = "n"
	default:
		answer = ask("Install the agent skill for Claude Code, in ~/.claude/skills/deskrig?", true)
	}
	switch answer {
	case "y":
		setupSkill()
	case "":
		fmt.Println("skill: `deskrig setup skill` installs it for Claude Code; `deskrig skill` prints it")
	}
	switch {
	case has(args, "--vm"):
		answer = "y"
	case has(args, "--no-vm"):
		answer = "n"
	default:
		answer = ask(fmt.Sprintf("Set up a macOS VM target, \"macos\"? It downloads Tart and a macOS image, about %d GB.", imageGB), false)
	}
	switch answer {
	case "y":
		setupVM()
	case "":
		fmt.Printf("vm: `deskrig setup vm` makes a macOS VM target (about %d GB)\n", imageGB)
	}
	fmt.Println("done. `deskrig help` lists the commands.")
}

func update() {
	cmd := commandWithOutput("/bin/sh", "-c",
		"curl -fsSL https://raw.githubusercontent.com/sudiptadeb/deskrig/main/install.sh | DESKRIG_NO_SETUP=1 sh")
	finish(exitCode(cmd.Run()))
}
