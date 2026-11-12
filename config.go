package main

// Targets: where the commands act. `local` is this machine's own desktop and is always there;
// the others are in ~/.deskrig/config.json, which `deskrig vm create` writes and a person may
// edit:
//
//	{
//	  "default": "pb-test",
//	  "targets": {
//	    "pb-test": {"kind": "tart", "vm": "pb-test", "tartHome": "~/PW_TOOLS/macos-vm/tart-home"}
//	  }
//	}
//
// A command picks its target by --target, else DESKRIG_TARGET, else the default, else local.

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Target struct {
	Kind     string `json:"kind"`               // local, tart
	VM       string `json:"vm,omitempty"`       // tart: the VM's name in Tart; the target's name if empty
	TartHome string `json:"tartHome,omitempty"` // tart: Tart's TART_HOME; deskrig's own if empty

	Name string `json:"-"`
}

type Config struct {
	Default string            `json:"default,omitempty"`
	Targets map[string]Target `json:"targets,omitempty"`
}

var deskrigHome = func() string {
	if h := os.Getenv("DESKRIG_HOME"); h != "" {
		return h
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".deskrig")
}()

var configPath = filepath.Join(deskrigHome, "config.json")

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, _ := os.UserHomeDir()
		return home + p[1:]
	}
	return p
}

func loadConfig() Config {
	var c Config
	data, err := os.ReadFile(configPath)
	if errors.Is(err, fs.ErrNotExist) {
		return c
	}
	if err != nil {
		fail(1, "can't read %s: %v", configPath, err)
	}
	if err := json.Unmarshal(data, &c); err != nil {
		fail(1, "%s isn't valid: %v", configPath, err)
	}
	return c
}

func saveConfig(c Config) {
	data, _ := json.MarshalIndent(c, "", "  ")
	os.MkdirAll(deskrigHome, 0o755)
	tmp := configPath + ".new"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		fail(1, "can't write %s: %v", configPath, err)
	}
	os.Rename(tmp, configPath)
}

func lookupTarget(c Config, name string) Target {
	if name == "local" {
		return Target{Kind: "local", Name: "local"}
	}
	t, ok := c.Targets[name]
	if !ok {
		fail(2, "no target %q: `deskrig targets` lists them", name)
	}
	t.Name = name
	if t.Kind == "tart" {
		if t.VM == "" {
			t.VM = name
		}
		t.TartHome = expandHome(t.TartHome)
	}
	return t
}

func chosenTarget(flag string) Target {
	c := loadConfig()
	name := flag
	if name == "" {
		name = os.Getenv("DESKRIG_TARGET")
	}
	if name == "" {
		name = c.Default
	}
	if name == "" {
		name = "local"
	}
	return lookupTarget(c, name)
}

func targetNames(c Config) []string {
	names := []string{"local"}
	var rest []string
	for n := range c.Targets {
		rest = append(rest, n)
	}
	sort.Strings(rest)
	return append(names, rest...)
}
