package main

// Key names for input, on a US keyboard. A combination is names joined by +: cmd+t, ctrl+cmd+f,
// shift+tab, return, f5. A single character is the key that types it, so cmd+1 is Command and the
// 1 key; a number of two or more digits is the platform's own key code (macOS's virtual key
// codes, Linux's evdev codes). On Linux cmd is the Super key and opt is Alt. Each platform's
// codes are in keycodes_<platform>.go.

import (
	"strconv"
	"strings"
	"unicode"
)

var modifierAliases = map[string]string{"command": "cmd", "option": "opt", "alt": "opt", "control": "ctrl"}

var modifiers = map[string]bool{"cmd": true, "shift": true, "opt": true, "ctrl": true}

// The modifiers and the key of a combination; the key is "" for a modifier alone.
func parseCombo(combo string) (mods []string, key string) {
	parts := strings.Split(combo, "+")
	if combo == "+" {
		parts = []string{"+"}
	} else if strings.HasSuffix(combo, "++") {
		parts = append(parts[:len(parts)-2], "+")
	}
	for _, p := range parts {
		name := p
		if len(p) > 1 {
			name = strings.ToLower(p)
		}
		if a, ok := modifierAliases[name]; ok {
			name = a
		}
		if modifiers[name] {
			mods = append(mods, name)
		} else {
			key = name
		}
	}
	return
}

// The character each shifted one is typed with.
var unshifted = func() map[rune]rune {
	m := map[rune]rune{}
	from, to := []rune("~!@#$%^&*()_+{}|:\"<>?"), []rune("`1234567890-=[]\\;',./")
	for i := range from {
		m[from[i]] = to[i]
	}
	return m
}()

func keyCode(name string) (int, bool) {
	if r := []rune(name); len(r) == 1 {
		k, ok := characterKeys[unicode.ToLower(r[0])]
		return k, ok
	}
	if k, ok := namedKeys[strings.ToLower(name)]; ok {
		return k, true
	}
	if n, err := strconv.Atoi(name); err == nil {
		return n, true
	}
	return 0, false
}

// The US key that types a character, and whether it needs Shift.
func keyFor(c rune) (code int, shift bool, ok bool) {
	switch c {
	case ' ':
		return namedKeys["space"], false, true
	case '\n':
		return namedKeys["return"], false, true
	case '\t':
		return namedKeys["tab"], false, true
	}
	if base, found := unshifted[c]; found {
		return characterKeys[base], true, true
	}
	if unicode.IsUpper(c) {
		if k, found := characterKeys[unicode.ToLower(c)]; found {
			return k, true, true
		}
	}
	code, ok = characterKeys[c]
	return code, false, ok
}
