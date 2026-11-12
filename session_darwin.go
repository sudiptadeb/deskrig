package main

// Running inside the logged-in desktop session.
//
// Agent, ssh and CI shells on a Mac run in a Background launchd session, where screen capture
// fails and ScreenCaptureKit hangs. A job bootstrapped into gui/<uid> runs in the desktop
// session instead. macOS checks the permissions against the job's own program, so the job is
// always deskrig itself, never a shell; a command deskrig starts from there counts as deskrig.

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
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

var domain = fmt.Sprintf("gui/%d", os.Getuid())

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

func launchctl(args ...string) (string, int) { return output("/bin/launchctl", args...) }

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

func xmlEscape(s string) string {
	var b bytes.Buffer
	for _, r := range s {
		switch r {
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '&':
			b.WriteString("&amp;")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func jobPlist(label string, program []string, out, errPath string, env map[string]string) string {
	for _, k := range []string{"PATH", "HOME", "LANG", "TART_HOME", "DESKRIG_HOME"} {
		if _, ok := env[k]; !ok {
			if v, ok := os.LookupEnv(k); ok {
				env[k] = v
			}
		}
	}
	cwd, _ := os.Getwd()
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
`)
	str := func(k, v string) { fmt.Fprintf(&b, "<key>%s</key><string>%s</string>\n", k, xmlEscape(v)) }
	str("Label", label)
	b.WriteString("<key>ProgramArguments</key><array>\n")
	for _, a := range program {
		fmt.Fprintf(&b, "<string>%s</string>\n", xmlEscape(a))
	}
	b.WriteString("</array>\n<key>RunAtLoad</key><true/>\n")
	str("LimitLoadToSessionType", "Aqua")
	str("WorkingDirectory", cwd)
	str("StandardOutPath", out)
	str("StandardErrorPath", errPath)
	b.WriteString("<key>EnvironmentVariables</key><dict>\n")
	for k, v := range env {
		str(k, v)
	}
	b.WriteString("</dict>\n</dict></plist>\n")
	f, err := os.CreateTemp("", "deskrig-*.plist")
	if err != nil {
		fail(1, "can't write a job: %v", err)
	}
	f.WriteString(b.String())
	f.Close()
	return f.Name()
}

func bootstrap(plist string) {
	out, code := launchctl("bootstrap", domain, plist)
	os.Remove(plist)
	if code != 0 {
		fail(2, "can't start in the desktop session (is anyone logged in at this Mac's screen?): %s",
			strings.TrimSpace(out))
	}
}

// Runs these arguments as deskrig inside the desktop session, passes on what it prints as it
// prints it, and ends with its exit code. limit 0 is none. With stdin, this process's input is
// fed to the job's.
func relayToSession(args []string, limit time.Duration, stdin bool) {
	label := fmt.Sprintf("%s.%d", labelPrefix, os.Getpid())
	work, err := os.MkdirTemp("", "deskrig-")
	if err != nil {
		fail(1, "%v", err)
	}
	out, errPath, code := work+"/out", work+"/err", work+"/exit"
	os.WriteFile(out, nil, 0o600)
	os.WriteFile(errPath, nil, 0o600)
	env := map[string]string{inSessionKey: "1", exitFileKey: code}
	if stdin {
		pipe := work + "/in"
		if err := syscall.Mkfifo(pipe, 0o600); err != nil {
			fail(1, "can't make the input pipe: %v", err)
		}
		env[stdinKey] = pipe
		go func() {
			f, err := os.OpenFile(pipe, os.O_WRONLY, 0)
			if err != nil {
				return
			}
			io.Copy(f, os.Stdin)
			f.Close()
		}()
	}
	launchctl("bootout", domain+"/"+label)
	bootstrap(jobPlist(label, append([]string{selfPath}, args...), out, errPath, env))

	outFile, _ := os.Open(out)
	errFile, _ := os.Open(errPath)
	pump := func() {
		io.Copy(os.Stdout, outFile)
		io.Copy(os.Stderr, errFile)
	}
	cleanUp := func() {
		launchctl("bootout", domain+"/"+label)
		os.RemoveAll(work)
	}
	// Ctrl-C here stops the job there.
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-signals
		pump()
		cleanUp()
		os.Exit(130)
	}()
	start := time.Now()
	var goneSince time.Time
	for {
		pump()
		if text, err := os.ReadFile(code); err == nil {
			if value, err := strconv.Atoi(strings.TrimSpace(string(text))); err == nil {
				time.Sleep(20 * time.Millisecond)
				pump()
				cleanUp()
				os.Exit(value)
			}
		}
		// A job that ended without writing its code crashed or was killed.
		if state, _ := launchctl("print", domain+"/"+label); !strings.Contains(state, "state = running") {
			if goneSince.IsZero() {
				goneSince = time.Now()
			} else if time.Since(goneSince) > time.Second {
				pump()
				cleanUp()
				fail(1, "deskrig stopped in the desktop session without an exit code (it crashed)")
			}
		}
		if limit > 0 && time.Since(start) > limit {
			pump()
			cleanUp()
			fail(1, "didn't finish in %v", limit)
		}
		time.Sleep(30 * time.Millisecond)
	}
}

// Starts a long-running job in the desktop session under this label, replacing one already
// there. It isn't waited for.
func startJob(label string, program []string, log string, deskrigInSession bool) {
	launchctl("bootout", domain+"/"+label)
	env := map[string]string{}
	if deskrigInSession {
		env[inSessionKey] = "1"
	}
	bootstrap(jobPlist(label, program, log, log, env))
}

func jobPid(label string) int {
	state, _ := launchctl("print", domain+"/"+label)
	for _, line := range strings.Split(state, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "pid = ") {
			pid, _ := strconv.Atoi(strings.TrimPrefix(line, "pid = "))
			return pid
		}
	}
	return 0
}

func stopJob(label string) { launchctl("bootout", domain+"/"+label) }

// A pause for pacing input. macOS stretches a background process's sleeps, a 20 ms sleep often
// to 150 ms, so short pauses spin instead; they are a few tens of milliseconds.
func pause(d time.Duration) {
	end := time.Now().Add(d)
	if d > 500*time.Millisecond {
		time.Sleep(d - 200*time.Millisecond)
	}
	for time.Now().Before(end) {
	}
}
