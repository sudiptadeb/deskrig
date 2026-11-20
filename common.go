package main

// What every platform shares: ending with an exit code, running helpers, fetching a file, and
// the input actions, which each platform carries out with its own click, key and scroll.

import (
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	inSessionKey = "DESKRIG_IN_SESSION"
	exitFileKey  = "DESKRIG_EXIT_FILE"
	stdinKey     = "DESKRIG_STDIN"
	labelPrefix  = "io.github.sudiptadeb.deskrig"
)

// The exit file is only this process's: a deskrig that a `run` command starts mustn't write it.
var exitFile = func() string {
	f := os.Getenv(exitFileKey)
	os.Unsetenv(exitFileKey)
	return f
}()

func inSession() bool { return os.Getenv(inSessionKey) == "1" }

// Inside a job the input comes through a pipe the caller feeds.
func openSessionStdin() {
	if p := os.Getenv(stdinKey); p != "" {
		os.Unsetenv(stdinKey)
		f, err := os.Open(p)
		if err != nil {
			fail(1, "can't read the input pipe: %v", err)
		}
		os.Stdin = f
	}
}

// Ends deskrig. Inside a job, the exit code goes to a file first, for the caller to pass on.
func finish(code int) {
	os.Stdout.Sync()
	if exitFile != "" {
		os.WriteFile(exitFile, []byte(strconv.Itoa(code)+"\n"), 0o600)
	}
	os.Exit(code)
}

func fail(code int, format string, a ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", a...)
	finish(code)
}

func say(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "deskrig: "+format+"\n", a...)
}

func output(name string, args ...string) (string, int) {
	out, err := exec.Command(name, args...).CombinedOutput()
	code := 0
	if err != nil {
		code = 127
		if e, ok := err.(*exec.ExitError); ok {
			code = e.ExitCode()
		}
	}
	return string(out), code
}

var selfPath = func() string {
	p, err := os.Executable()
	if err != nil {
		return os.Args[0]
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}()

// A path or an http(s) URL, copied to dest.
func fetchFile(from, dest string) {
	var out string
	var code int
	if strings.HasPrefix(from, "http://") || strings.HasPrefix(from, "https://") {
		out, code = output("/usr/bin/curl", "-fsSL", "-o", dest, from)
	} else {
		out, code = output("/bin/cp", from, dest)
	}
	if code != 0 {
		fail(1, "can't get %s: %s", from, strings.TrimSpace(out))
	}
}

func commandWithOutput(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd
}

func exitCode(err error) int {
	if e, ok := err.(*exec.ExitError); ok {
		return e.ExitCode()
	}
	if err != nil {
		return 1
	}
	return 0
}

// An app's name, or a pid.
func appOrPid(which string) (string, int) {
	if pid, err := strconv.Atoi(which); err == nil {
		return "", pid
	}
	return which, 0
}

func pngSize(png []byte) (int, int) {
	if len(png) < 24 {
		return 0, 0
	}
	be := func(b []byte) int { return int(b[0])<<24 | int(b[1])<<16 | int(b[2])<<8 | int(b[3]) }
	return be(png[16:20]), be(png[20:24])
}

// One action, as a command's arguments or a line of `input`.
func act(verb string, args []string) error {
	n := make([]float64, 0, len(args))
	for _, a := range args {
		f, err := strconv.ParseFloat(a, 64)
		if err != nil {
			break
		}
		n = append(n, f)
	}
	numbers := func(want ...int) bool {
		for _, w := range want {
			if len(n) == w && len(args) == w {
				return true
			}
		}
		return false
	}
	switch {
	case verb == "click" && numbers(2):
		click(n[0], n[1], 1)
	case verb == "dblclick" && numbers(2):
		click(n[0], n[1], 2)
	case verb == "move" && numbers(2):
		moveTo(n[0], n[1])
	case verb == "down" && numbers(0):
		press(true)
	case verb == "up" && numbers(0):
		press(false)
	case verb == "drag" && numbers(4):
		drag(n[0], n[1], n[2], n[3])
	case verb == "scroll" && numbers(3, 4, 5):
		steps, hold := 10, 0.0
		if len(n) > 3 {
			steps = int(n[3])
		}
		if len(n) > 4 {
			hold = n[4]
		}
		scroll(n[0], n[1], n[2], steps, time.Duration(hold)*time.Millisecond)
	case verb == "key" && len(args) == 1:
		return key(args[0])
	case verb == "type":
		return typeText(strings.Join(args, " "))
	default:
		return fmt.Errorf("unknown: %s %s", verb, strings.Join(args, " "))
	}
	return nil
}

//go:embed skills/deskrig/SKILL.md
var skillText string

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

// A command run as deskrig's child, so the platform counts it as deskrig (macOS: its permissions cover it).
func runCommand(command []string) {
	path, err := exec.LookPath(command[0])
	if err != nil {
		fail(127, "no %s on PATH", command[0])
	}
	cmd := exec.Command(path, command[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err = cmd.Run()
	if e, ok := err.(*exec.ExitError); ok {
		if s, ok := e.Sys().(syscall.WaitStatus); ok && s.Signaled() {
			finish(128 + int(s.Signal()))
		}
		finish(e.ExitCode())
	} else if err != nil {
		fail(127, "can't run %s: %v", command[0], err)
	}
	finish(0)
}
