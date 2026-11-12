package main

// The tart target: a macOS VM under Tart, for testing an app on a desktop of its own.
//
// deskrig acts inside the guest: it puts a copy of itself in the VM's read-only shared folder
// and runs it through Tart's guest agent (`tart exec`), whose children have the guest's Screen
// Recording and Accessibility. Shots, windows and input are the guest's own, so input is exact
// and a shot works even with the VM's window covered.
//
// The VM runs in a window on this Mac's desktop. `deskrig frames` watches that window from
// here, for a test that needs many frames a second; it is the one thing that needs this Mac's
// own Screen Recording permission.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	tartVersion  = "2.41.1"
	tartURL      = "https://github.com/cirruslabs/tart/releases/download/" + tartVersion + "/tart.tar.gz"
	defaultImage = "ghcr.io/cirruslabs/macos-tahoe-base:latest"
	imageGB      = 40
	keepFreeGB   = 20
	guestShare   = "/Volumes/My Shared Files/share/"
)

var ownTart = filepath.Join(deskrigHome, "tart.app/Contents/MacOS/tart")

func tartBinary() string {
	if _, err := os.Stat(ownTart); err == nil {
		return ownTart
	}
	if p, err := exec.LookPath("tart"); err == nil {
		return p
	}
	fail(2, "Tart isn't installed: `deskrig vm create <name>` installs it")
	return ""
}

func (t Target) tartHome() string {
	if t.TartHome != "" {
		return t.TartHome
	}
	if h := os.Getenv("TART_HOME"); h != "" {
		return h
	}
	return filepath.Join(deskrigHome, "tart")
}

func (t Target) dir() string        { return filepath.Join(deskrigHome, "vms", t.Name) }
func (t Target) label() string      { return labelPrefix + ".vm." + t.Name }
func (t Target) frameLabel() string { return labelPrefix + ".frames.vm." + t.Name }

func (t Target) tart(args ...string) *exec.Cmd {
	cmd := exec.Command(tartBinary(), args...)
	cmd.Env = append(os.Environ(), "TART_HOME="+t.tartHome())
	return cmd
}

// Tart's output and exit code; -1 when it didn't end within the limit.
func (t Target) tartRun(limit time.Duration, args ...string) (string, int) {
	cmd := t.tart(args...)
	var out strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		fail(2, "can't run Tart: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
		return out.String(), cmd.ProcessState.ExitCode()
	case <-time.After(limit):
		cmd.Process.Kill()
		return out.String(), -1
	}
}

func (t Target) running() bool {
	out, _ := t.tartRun(time.Minute, "list", "--format", "json")
	var vms []struct {
		Name    string
		Running bool
	}
	json.Unmarshal([]byte(out), &vms)
	for _, v := range vms {
		if v.Name == t.VM {
			return v.Running
		}
	}
	return false
}

func (t Target) exists() bool {
	out, _ := t.tartRun(time.Minute, "list", "-q")
	for _, n := range strings.Fields(out) {
		if n == t.VM {
			return true
		}
	}
	return false
}

type vmState struct {
	PointsWide float64 `json:"pointsWide"`
	PointsHigh float64 `json:"pointsHigh"`
}

func (t Target) state() vmState {
	var s vmState
	data, err := os.ReadFile(filepath.Join(t.dir(), "state.json"))
	if err != nil || json.Unmarshal(data, &s) != nil || !t.running() {
		fail(2, "VM %s isn't running: `deskrig vm start %s`", t.Name, t.Name)
	}
	return s
}

// A command inside the guest, with this process's input and output.
func (t Target) guestCommand(stdin bool, command ...string) *exec.Cmd {
	args := []string{"exec"}
	if stdin {
		args = append(args, "-i")
	}
	cmd := t.tart(append(append(args, t.VM), command...)...)
	if stdin {
		cmd.Stdin = os.Stdin
	}
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd
}

// deskrig inside the guest: this same binary, shared again whenever it changes.
func (t Target) deskrig(s vmState, stdin bool, args ...string) *exec.Cmd {
	return t.guestCommand(stdin, append([]string{"/usr/bin/env", inSessionKey + "=1", shareSelf(t), "--target", "local"}, args...)...)
}

func runAndFinish(cmd *exec.Cmd) {
	err := cmd.Run()
	if e, ok := err.(*exec.ExitError); ok {
		finish(e.ExitCode())
	} else if err != nil {
		fail(1, "%v", err)
	}
	finish(0)
}

func (t Target) do(verb string, args []string) {
	switch verb {
	case "frames":
		t.frames()
	case "install":
		t.install(args)
	}
	s := t.state()
	switch verb {
	case "status":
		fmt.Printf("target: %s (tart VM %s)\n", t.Name, t.VM)
		runAndFinish(t.deskrig(s, false, "status"))
	case "shot":
		// The guest writes the PNG to its output; it lands in the file here.
		out, err := os.Create(args[0])
		if err != nil {
			fail(1, "can't write %s: %v", args[0], err)
		}
		cmd := t.deskrig(s, false, append([]string{"shot", "-"}, args[1:]...)...)
		cmd.Stdout = out
		err = cmd.Run()
		out.Close()
		if err != nil {
			os.Remove(args[0])
			if e, ok := err.(*exec.ExitError); ok {
				finish(e.ExitCode())
			}
			fail(1, "%v", err)
		}
		png, _ := os.ReadFile(args[0])
		w, h := pngSize(png)
		fmt.Printf("saved %s %d x %d\n", args[0], w, h)
		finish(0)
	case "run":
		runAndFinish(t.guestCommand(false, args...))
	case "input":
		runAndFinish(t.deskrig(s, true, "input"))
	default:
		runAndFinish(t.deskrig(s, false, append([]string{verb}, args...)...))
	}
}

func freeGB(path string) int {
	out, _ := output("/bin/df", "-k", path)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	f := strings.Fields(lines[len(lines)-1])
	if len(f) < 4 {
		return 0
	}
	kb, _ := strconv.Atoi(f[3])
	return kb / 1_000_000
}

func installTart() {
	if _, err := os.Stat(ownTart); err == nil {
		return
	}
	say("downloading Tart %s", tartVersion)
	os.MkdirAll(deskrigHome, 0o755)
	tarball := filepath.Join(deskrigHome, "tart.tar.gz")
	if out, code := output("/usr/bin/curl", "-fsSL", "-o", tarball, tartURL); code != 0 {
		fail(1, "can't download Tart from %s: %s", tartURL, out)
	}
	out, code := output("/usr/bin/tar", "-xzf", tarball, "-C", deskrigHome)
	os.Remove(tarball)
	if _, err := os.Stat(ownTart); code != 0 || err != nil {
		fail(1, "can't unpack Tart: %s", out)
	}
}

// Makes a VM, or takes one Tart already has under that name, and adds it as a target.
func vmCreate(name, image string, cpus, memoryMB int, display string) {
	installTart()
	c := loadConfig()
	if c.Targets == nil {
		c.Targets = map[string]Target{}
	}
	t, known := c.Targets[name]
	if !known {
		t = Target{Kind: "tart", VM: name, TartHome: os.Getenv("TART_HOME")}
	}
	t.Name = name
	if t.VM == "" {
		t.VM = name
	}
	if t.exists() {
		fmt.Printf("Tart already has VM %s in %s\n", t.VM, t.tartHome())
	} else {
		if free := freeGB(deskrigHome); free < imageGB+keepFreeGB {
			fail(1, "only %d GB free: the image needs about %d GB and %d GB should stay free", free, imageGB, keepFreeGB)
		}
		say("cloning %s; the first time it downloads about %d GB", image, imageGB)
		clone := t.tart("clone", image, t.VM)
		clone.Stdout, clone.Stderr = os.Stdout, os.Stderr
		if clone.Run() != nil {
			fail(1, "tart clone %s %s failed", image, t.VM)
		}
		if out, code := t.tartRun(2*time.Minute, "set", t.VM, "--cpu", strconv.Itoa(cpus),
			"--memory", strconv.Itoa(memoryMB), "--display", display, "--no-display-refit"); code != 0 {
			fail(1, "tart set failed: %s", out)
		}
	}
	saved := t
	saved.Name = ""
	c.Targets[name] = saved
	if c.Default == "" {
		c.Default = "local"
	}
	saveConfig(c)
	fmt.Printf("target %s: tart VM %s. Start it with `deskrig vm start %s`; use it with `--target %s`\n",
		name, t.VM, name, name)
}

func vmStart(t Target) {
	host := hostStatus()
	if host["on console"] != "true" || host["screen locked"] != "false" {
		fail(2, "the VM runs in a window on this Mac's desktop, which needs someone logged in at the screen, "+
			"unlocked. `deskrig --target local status` says: on console %s, screen locked %s",
			host["on console"], host["screen locked"])
	}
	dir := t.dir()
	os.MkdirAll(filepath.Join(dir, "share"), 0o755)
	log := filepath.Join(dir, "tart.log")
	if !t.running() {
		os.Remove(filepath.Join(dir, "state.json"))
		os.Remove(log)
		say("starting %s", t.VM)
		launchTart(t, log)
	}
	ready := false
	for i := 0; i < 120; i++ {
		if !t.running() {
			data, _ := os.ReadFile(log)
			lines := strings.Split(strings.TrimSpace(string(data)), "\n")
			tail := strings.Join(lines[max(0, len(lines)-3):], "\n")
			if strings.Contains(tail, "host key") {
				fail(2, "macOS refused to start the VM: nobody has logged in at this Mac's screen since it booted, "+
					"so the keys Virtualization needs are locked. Log in once, then start again.\n%s", tail)
			}
			if i > 2 {
				fail(2, "the VM stopped: %s", tail)
			}
		} else if _, code := t.tartRun(20*time.Second, "exec", t.VM, "/usr/bin/pgrep", "-x", "Dock"); code == 0 {
			ready = true
			break
		}
		time.Sleep(5 * time.Second)
	}
	if !ready {
		fail(2, "the VM's desktop didn't come up in 10 minutes")
	}
	var s vmState
	out, code := t.tartRun(time.Minute, "exec", t.VM, "/usr/bin/env", inSessionKey+"=1", shareSelf(t), "--target", "local", "status")
	if code != 0 {
		fail(1, "deskrig doesn't run in the VM: %s", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(line, "main display: "); ok {
			fmt.Sscanf(rest, "%g x %g", &s.PointsWide, &s.PointsHigh)
		}
	}
	data, _ := json.MarshalIndent(s, "", "  ")
	os.WriteFile(filepath.Join(dir, "state.json"), data, 0o600)
	// Clicking the wallpaper would hide every window (Show Desktop).
	t.tartRun(time.Minute, "exec", t.VM, "/usr/bin/defaults", "write", "com.apple.WindowManager",
		"EnableStandardClickToShowDesktop", "-bool", "false")
	// First-login banners (tips, background activity) sit over apps and take clicks.
	t.tartRun(time.Minute, "exec", t.VM, "/usr/bin/env", inSessionKey+"=1", shareSelf(t), "--target", "local",
		"dismiss-notifications")
	version, _ := t.tartRun(time.Minute, "exec", t.VM, "/usr/bin/sw_vers", "-productVersion")
	fmt.Printf("ready: %s, macOS %s, %d x %d points\n", t.Name, strings.TrimSpace(version), int(s.PointsWide), int(s.PointsHigh))
}

// Tart runs as a job in the desktop session, since its window needs one.
func launchTart(t Target, log string) {
	program := []string{"/usr/bin/env", "TART_HOME=" + t.tartHome(), tartBinary(), "run", "--no-audio", "--no-clipboard",
		"--dir=share:" + filepath.Join(t.dir(), "share") + ":ro", t.VM}
	startJob(t.label(), program, log, false)
	time.Sleep(3 * time.Second)
}

// Puts this deskrig in the VM's shared folder under a name of its own: the guest can keep an
// old file's contents under a name it has seen. Returns its path inside the guest.
func shareSelf(t Target) string {
	share := filepath.Join(t.dir(), "share")
	data, err := os.ReadFile(selfPath)
	if err != nil {
		fail(1, "can't read %s: %v", selfPath, err)
	}
	sum := sha256.Sum256(data)
	name := "deskrig-" + hex.EncodeToString(sum[:6])
	entries, _ := os.ReadDir(share)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "deskrig-") && e.Name() != name {
			os.Remove(filepath.Join(share, e.Name()))
		}
	}
	if _, err := os.Stat(filepath.Join(share, name)); err != nil {
		os.WriteFile(filepath.Join(share, name+".new"), data, 0o755)
		os.Rename(filepath.Join(share, name+".new"), filepath.Join(share, name))
	}
	return guestShare + name
}

func vmStop(t Target) {
	stopJob(t.frameLabel())
	if t.running() {
		t.tartRun(90*time.Second, "stop", t.VM, "--timeout", "60")
	}
	stopJob(t.label())
	os.Remove(filepath.Join(t.dir(), "state.json"))
	os.Remove(filepath.Join(t.dir(), "frames.bin"))
	fmt.Println("stopped", t.Name)
}

func vmDelete(t Target) {
	vmStop(t)
	if out, code := t.tartRun(2*time.Minute, "delete", t.VM); code != 0 {
		fail(1, "tart delete %s: %s", t.VM, out)
	}
	os.RemoveAll(t.dir())
	c := loadConfig()
	delete(c.Targets, t.Name)
	if c.Default == t.Name {
		c.Default = "local"
	}
	saveConfig(c)
	fmt.Println("deleted", t.Name)
}

// The VM's screen, from its window here: the bottom of the window, under Tart's title bar.
func (t Target) frames() {
	s := t.state()
	pid := jobPid(t.label())
	if pid == 0 {
		fail(2, "the VM's window isn't one deskrig started: `deskrig vm stop %s` and start it again", t.Name)
	}
	file := filepath.Join(t.dir(), "frames.bin")
	startFrames(t.frameLabel(), pid, file, s.PointsHigh/s.PointsWide)
	printFrames(file, s.PointsWide, s.PointsHigh)
	finish(0)
}

func (t Target) install(args []string) {
	if len(args) != 1 {
		usage()
	}
	t.state()
	share := filepath.Join(t.dir(), "share")
	// A new name each time: the guest can keep an old file's data under a name it has seen.
	name := fmt.Sprintf("app-%d.dmg", time.Now().Unix())
	entries, _ := os.ReadDir(share)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".dmg") {
			os.Remove(filepath.Join(share, e.Name()))
		}
	}
	local := filepath.Join(share, name)
	fetchFile(args[0], local)
	err := t.guestCommand(false, "/bin/bash", "-c", installScript, "install", guestShare+name).Run()
	os.Remove(local)
	finish(exitCode(err))
}
