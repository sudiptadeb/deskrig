package main

// First setup: every permission deskrig may need, asked for once; the agent skill; and, if
// wanted, a macOS VM. It asks when someone is at a terminal; an agent runs the steps one by one.

import (
	_ "embed"
	"fmt"
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

func openPane(pane string) {
	output(selfPath, "--target", "local", "run", "--", "/usr/bin/open",
		"x-apple.systempreferences:com.apple.preference.security?"+pane)
}

func granted(p permission) bool {
	_, code := output(selfPath, "--target", "local", "check-permission", p.name)
	return code == 0
}

// One permission at a time, so macOS's prompts don't pile up: ask, open its page, wait until
// it's on. The ones macOS can't report wait for Enter at a terminal, else move on.
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
	tty, _ := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	var missing []string
	fmt.Printf("permissions for %s, one at a time:\n", selfPath)
	for i, p := range permissions {
		step := fmt.Sprintf("[%d/%d] %s", i+1, len(permissions), p.name)
		if p.check != nil && status[p.name] == "true" {
			fmt.Printf("%s: on\n", step)
			continue
		}
		fmt.Printf("%s: in System Settings > Privacy & Security > %s, %s\n", step, p.page, p.how)
		var asking *exec.Cmd
		if p.request != nil {
			asking = exec.Command(selfPath, "--target", "local", "request-permission", p.name)
			asking.Start()
			go asking.Wait()
			time.Sleep(time.Second)
		}
		if p.drag {
			output(selfPath, "--target", "local", "run", "--", "/usr/bin/open", "-R", selfPath)
		}
		openPane(p.pane)
		on := false
		switch {
		case p.check != nil:
			for end := time.Now().Add(wait); time.Now().Before(end) && !on; {
				time.Sleep(2 * time.Second)
				on = granted(p)
			}
			if on {
				fmt.Printf("%s: on\n", step)
			} else {
				fmt.Printf("%s: still off after %v, moving on\n", step, wait)
				missing = append(missing, p.name)
			}
		case tty != nil:
			fmt.Fprint(tty, "  press Enter when done ")
			fmt.Fscanln(tty)
		default:
			time.Sleep(5 * time.Second)
		}
		if asking != nil {
			asking.Process.Signal(os.Interrupt)
		}
	}
	if len(missing) > 0 {
		fail(2, "permissions: still off: %s. Run `deskrig setup permissions` again for them.", strings.Join(missing, ", "))
	}
	fmt.Println("permissions: all set")
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
