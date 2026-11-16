package main

import "testing"

// A digit in a chord is the digit key (kVK_ANSI_0..9), not key code 0..9 (a, s, d, f, ...).
func TestKeyCodeDigitsAreDigitKeys(t *testing.T) {
	want := map[string]int{"0": 29, "1": 18, "2": 19, "3": 20, "4": 21, "5": 23, "6": 22, "7": 26, "8": 28, "9": 25}
	for name, code := range want {
		mods, key := parseCombo("cmd+" + name)
		got, ok := keyCode(key)
		if len(mods) != 1 || mods[0] != "cmd" || !ok || got != code {
			t.Errorf("cmd+%s: mods %v key %q code %d %v, want cmd and code %d", name, mods, key, got, ok, code)
		}
	}
	if got, ok := keyCode("113"); !ok || got != 113 {
		t.Errorf("113: code %d %v, want the raw key code 113", got, ok)
	}
}
