package main

import (
	"os"
	"path/filepath"
	"testing"
)

// A bundle's header carries the app's metadata file as text; a .flatpakref is a key file.
func TestFlatpakAppID(t *testing.T) {
	dir := t.TempDir()
	bundle := filepath.Join(dir, "app.flatpak")
	os.WriteFile(bundle, []byte("flatpak\x00app/org.example.App/aarch64/master\x00metadata\x00"+
		"[Application]\nname=org.example.App\nruntime=org.freedesktop.Platform/aarch64/26.08\nsdk=x\n\x00more"), 0o644)
	id, runtime, err := flatpakAppID(bundle)
	if err != nil || id != "org.example.App" || runtime != "org.freedesktop.Platform/aarch64/26.08" {
		t.Errorf("bundle: %q %q %v", id, runtime, err)
	}
	ref := filepath.Join(dir, "app.flatpakref")
	os.WriteFile(ref, []byte("[Flatpak Ref]\nTitle=App\nName=org.example.App\nBranch=stable\n"), 0o644)
	id, runtime, err = flatpakAppID(ref)
	if err != nil || id != "org.example.App" || runtime != "" {
		t.Errorf("flatpakref: %q %q %v", id, runtime, err)
	}
}

// Every character a US keyboard types has a Linux key, as on the Mac.
func TestLinuxKeysCoverUSKeyboard(t *testing.T) {
	for _, c := range `abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789` + "`" + `-=[]\;',./~!@#$%^&*()_+{}|:"<>?` + " \n\t" {
		if _, _, ok := keyFor(c); !ok {
			t.Errorf("no key for %q", c)
		}
	}
	if code, ok := keyCode("1"); !ok || code != 2 {
		t.Errorf("1: %d %v, want the 1 key, evdev 2", code, ok)
	}
}
