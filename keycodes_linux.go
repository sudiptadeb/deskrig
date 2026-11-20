package main

// Linux evdev key codes (linux/input-event-codes.h), US layout.

var modifierCodes = map[string]int{"cmd": 125, "shift": 42, "opt": 56, "ctrl": 29}

var namedKeys = map[string]int{
	"return": 28, "enter": 28, "tab": 15, "space": 57, "delete": 14, "backspace": 14, "forwarddelete": 111,
	"esc": 1, "escape": 1, "left": 105, "right": 106, "down": 108, "up": 103,
	"home": 102, "end": 107, "pageup": 104, "pagedown": 109,
	"f1": 59, "f2": 60, "f3": 61, "f4": 62, "f5": 63, "f6": 64, "f7": 65, "f8": 66,
	"f9": 67, "f10": 68, "f11": 87, "f12": 88,
}

// US layout: the key that types each character, unshifted.
var characterKeys = map[rune]int{
	'a': 30, 's': 31, 'd': 32, 'f': 33, 'h': 35, 'g': 34, 'z': 44, 'x': 45, 'c': 46, 'v': 47, 'b': 48, 'q': 16,
	'w': 17, 'e': 18, 'r': 19, 'y': 21, 't': 20, '1': 2, '2': 3, '3': 4, '4': 5, '6': 7, '5': 6,
	'=': 13, '9': 10, '7': 8, '-': 12, '8': 9, '0': 11, ']': 27, 'o': 24, 'u': 22, '[': 26, 'i': 23,
	'p': 25, 'l': 38, 'j': 36, '\'': 40, 'k': 37, ';': 39, '\\': 43, ',': 51, '/': 53, 'n': 49, 'm': 50,
	'.': 52, '`': 41,
}
