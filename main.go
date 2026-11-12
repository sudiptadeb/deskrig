// deskrig: one testing interface for agents. The same commands see and drive a desktop
// (screenshots, windows, real clicks, keys and scrolls, frames, installing an app) on whichever
// target is chosen: this Mac's own desktop, or a macOS VM.
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
)

var version = "dev"

// AppKit wants the main thread.
func init() { runtime.LockOSThread() }

const usageText = `deskrig: see and drive a desktop, for agents testing apps.

Targets (--target <name>, else DESKRIG_TARGET, else the default; local is this Mac):
  deskrig targets                     the targets, the default marked
  deskrig use <name>                  make it the default
  deskrig vm create <name>            a macOS VM target; [--image I] [--cpus 4] [--memory 8192] [--display 1440x900pt]
                                      takes a VM Tart already has under that name (TART_HOME picks Tart's home)
  deskrig vm start|stop|delete [name] the default target's VM if no name

The same on every target:
  deskrig status                      logged in? unlocked? permissions?
  deskrig windows [--all]             app, pid, x, y, w, h, title, front to back
  deskrig shot <out.png> [app|pid]    one app's window, or the whole screen
  deskrig raise <app|pid>
  deskrig click|dblclick|move <x> <y>
  deskrig down|up
  deskrig drag <x1> <y1> <x2> <y2>
  deskrig scroll <x> <y> <dy> [steps] [hold ms]   a trackpad scroll
  deskrig key <combo>                 cmd+t, return, esc, shift+tab, ctrl+cmd+f, f5
  deskrig type <text>
  deskrig input                       one action per line on stdin, answered ok or error
  deskrig run -- <command...>         a command on the target, inside its desktop session
  deskrig install <app.dmg|URL>       the .app to /Applications on the target
  deskrig frames [app|pid]            keep the latest frame in a mapped file; prints its path
  deskrig dismiss-notifications       close the notification banners on screen

Setup:
  deskrig setup [permissions|skill|vm] [--skill|--no-skill] [--vm|--no-vm]
  deskrig skill                       the agent skill, for any agent
  deskrig update                      install the latest release
  deskrig version
`

func usage() {
	fmt.Fprint(os.Stderr, usageText)
	finish(2)
}

var deskVerbs = map[string]int{ // the fewest arguments each takes
	"status": 0, "windows": 0, "shot": 1, "raise": 1, "click": 2, "dblclick": 2, "move": 2, "down": 0, "up": 0,
	"drag": 4, "scroll": 3, "key": 1, "type": 1, "input": 0, "run": 1, "install": 1, "frames": 0, "dismiss-notifications": 0,
	"watch-run": 4, "request-permissions": 0,
}

func main() {
	args := os.Args[1:]
	targetFlag := ""
	for len(args) > 0 && strings.HasPrefix(args[0], "-") {
		switch {
		case (args[0] == "--target" || args[0] == "-t") && len(args) > 1:
			targetFlag, args = args[1], args[2:]
		case strings.HasPrefix(args[0], "--target="):
			targetFlag, args = strings.TrimPrefix(args[0], "--target="), args[1:]
		case args[0] == "-h" || args[0] == "--help":
			fmt.Print(usageText)
			return
		case args[0] == "--version":
			fmt.Println(version)
			return
		default:
			usage()
		}
	}
	if len(args) == 0 {
		usage()
	}
	verb, rest := args[0], args[1:]
	if inSession() {
		initApp()
	}
	switch verb {
	case "help":
		fmt.Print(usageText)
	case "version":
		fmt.Println(version)
	case "skill":
		fmt.Print(skillText)
	case "update":
		update()
	case "setup":
		switch {
		case len(rest) > 0 && rest[0] == "permissions":
			setupPermissions(10 * 60e9)
		case len(rest) > 0 && rest[0] == "skill":
			setupSkill()
		case len(rest) > 0 && rest[0] == "vm":
			setupVM()
		default:
			setup(rest)
		}
	case "targets":
		listTargets()
	case "use":
		if len(rest) != 1 {
			usage()
		}
		c := loadConfig()
		lookupTarget(c, rest[0])
		c.Default = rest[0]
		saveConfig(c)
		fmt.Println("default target:", rest[0])
	case "vm":
		vmMain(targetFlag, rest)
	default:
		least, ok := deskVerbs[verb]
		if !ok || len(rest) < least {
			usage()
		}
		if verb == "run" {
			if rest[0] == "--" {
				rest = rest[1:]
			}
			if len(rest) == 0 {
				usage()
			}
		}
		t := chosenTarget(targetFlag)
		switch t.Kind {
		case "local":
			localDo(verb, rest)
		case "tart":
			if verb == "watch-run" || verb == "request-permissions" {
				usage()
			}
			t.do(verb, rest)
		default:
			fail(2, "target %s has kind %q, which this deskrig doesn't know", t.Name, t.Kind)
		}
	}
	finish(0)
}

func listTargets() {
	c := loadConfig()
	def := c.Default
	if def == "" {
		def = "local"
	}
	for _, name := range targetNames(c) {
		t := lookupTarget(c, name)
		mark := " "
		if name == def {
			mark = "*"
		}
		detail := "this Mac's desktop"
		if t.Kind == "tart" {
			state := "stopped"
			if t.running() {
				state = "running"
			}
			detail = fmt.Sprintf("tart VM %s, %s", t.VM, state)
		}
		fmt.Printf("%s %s\t%s\t%s\n", mark, name, t.Kind, detail)
	}
}

func vmMain(targetFlag string, args []string) {
	if len(args) == 0 {
		usage()
	}
	sub, args := args[0], args[1:]
	if sub == "create" {
		if len(args) == 0 || strings.HasPrefix(args[0], "-") {
			usage()
		}
		name := args[0]
		image, cpus, memory, display := defaultImage, 4, 8192, "1440x900pt"
		for i := 1; i+1 < len(args); i += 2 {
			switch args[i] {
			case "--image":
				image = args[i+1]
			case "--cpus":
				cpus, _ = strconv.Atoi(args[i+1])
			case "--memory":
				memory, _ = strconv.Atoi(args[i+1])
			case "--display":
				display = args[i+1]
			default:
				usage()
			}
		}
		vmCreate(name, image, cpus, memory, display)
		return
	}
	name := targetFlag
	if len(args) > 0 {
		name = args[0]
	}
	t := chosenTarget(name)
	if t.Kind != "tart" {
		fail(2, "target %s isn't a VM: `deskrig vm %s <name>`", t.Name, sub)
	}
	switch sub {
	case "start":
		vmStart(t)
	case "stop":
		vmStop(t)
	case "delete":
		vmDelete(t)
	default:
		usage()
	}
}
