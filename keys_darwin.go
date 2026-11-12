package main

// Key names for input: macOS key codes on a US keyboard. A combination is names joined by +:
// cmd+t, ctrl+cmd+f, shift+tab, return, f5. A bare number is a macOS key code.

import (
	"strconv"
	"strings"
	"unicode"
)

var modifierAliases = map[string]string{"command": "cmd", "option": "opt", "alt": "opt", "control": "ctrl"}

var modifierFlags = map[string]uint64{"cmd": 1 << 20, "shift": 1 << 17, "opt": 1 << 19, "ctrl": 1 << 18}
var modifierCodes = map[string]int{"cmd": 55, "shift": 56, "opt": 58, "ctrl": 59}


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
		if _, ok := modifierFlags[name]; ok {
			mods = append(mods, name)
		} else {
			key = name
		}
	}
	return
}

var namedKeys = map[string]int{
	"return": 36, "enter": 36, "tab": 48, "space": 49, "delete": 51, "backspace": 51, "forwarddelete": 117,
	"esc": 53, "escape": 53, "left": 123, "right": 124, "down": 125, "up": 126,
	"home": 115, "end": 119, "pageup": 116, "pagedown": 121,
	"f1": 122, "f2": 120, "f3": 99, "f4": 118, "f5": 96, "f6": 97, "f7": 98, "f8": 100,
	"f9": 101, "f10": 109, "f11": 103, "f12": 111,
}

// US layout: the key that types each character, unshifted.
var characterKeys = map[rune]int{
	'a': 0, 's': 1, 'd': 2, 'f': 3, 'h': 4, 'g': 5, 'z': 6, 'x': 7, 'c': 8, 'v': 9, 'b': 11, 'q': 12,
	'w': 13, 'e': 14, 'r': 15, 'y': 16, 't': 17, '1': 18, '2': 19, '3': 20, '4': 21, '6': 22, '5': 23,
	'=': 24, '9': 25, '7': 26, '-': 27, '8': 28, '0': 29, ']': 30, 'o': 31, 'u': 32, '[': 33, 'i': 34,
	'p': 35, 'l': 37, 'j': 38, '\'': 39, 'k': 40, ';': 41, '\\': 42, ',': 43, '/': 44, 'n': 45, 'm': 46,
	'.': 47, '`': 50,
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
	if n, err := strconv.Atoi(name); err == nil {
		return n, true
	}
	if k, ok := namedKeys[strings.ToLower(name)]; ok {
		return k, true
	}
	if r := []rune(name); len(r) == 1 {
		k, ok := characterKeys[unicode.ToLower(r[0])]
		return k, ok
	}
	return 0, false
}

// The US key that types a character, and whether it needs Shift.
func keyFor(c rune) (code int, shift bool, ok bool) {
	switch c {
	case ' ':
		return 49, false, true
	case '\n':
		return 36, false, true
	case '\t':
		return 48, false, true
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
