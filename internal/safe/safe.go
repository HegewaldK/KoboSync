// Package safe cleans text from books and devices before it reaches the
// terminal. Book metadata and file names are untrusted: an escape sequence
// hidden in a title could otherwise drive the terminal (set the clipboard,
// change the window title, …).
package safe

import "strings"

// Text removes control characters, including newlines, and bidirectional
// overrides that could make text display differently from what it is.
func Text(s string) string {
	return strings.Map(func(r rune) rune {
		if bad(r) || r == '\n' {
			return -1
		}
		return r
	}, s)
}

// Multiline is Text but keeps newlines, for descriptions.
func Multiline(s string) string {
	return strings.Map(func(r rune) rune {
		if r != '\n' && bad(r) {
			return -1
		}
		return r
	}, s)
}

func bad(r rune) bool {
	switch {
	case r < 0x20, r == 0x7f, r >= 0x80 && r <= 0x9f: // C0, DEL, C1 controls
		return true
	case r >= 0x202a && r <= 0x202e, r >= 0x2066 && r <= 0x2069, r == 0x200e, r == 0x200f: // bidi controls
		return true
	}
	return false
}
