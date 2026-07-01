// Package tui is a minimal, dependency-free terminal UI toolkit: screen
// lifecycle, keyboard decoding, styling, and a couple of widgets. It uses only
// the standard library plus golang.org/x/term. The dashboard is built on top of
// it so terminal plumbing lives in exactly one place.
package tui

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// ColorEnabled gates all SGR styling. Set false for dumb terminals / NO_COLOR.
var ColorEnabled = true

const reset = "\x1b[0m"

func sgr(code, s string) string {
	if !ColorEnabled {
		return s
	}
	return "\x1b[" + code + "m" + s + reset
}

// Foreground colours and attributes.
func Red(s string) string     { return sgr("31", s) }
func Green(s string) string   { return sgr("32", s) }
func Yellow(s string) string  { return sgr("33", s) }
func Blue(s string) string    { return sgr("34", s) }
func Magenta(s string) string { return sgr("35", s) }
func Cyan(s string) string    { return sgr("36", s) }
func Dim(s string) string     { return sgr("2", s) }
func Bold(s string) string    { return sgr("1", s) }
func Reverse(s string) string { return sgr("7", s) }

// VisibleWidth returns the display width of s, ignoring SGR escape sequences.
func VisibleWidth(s string) int {
	w := 0
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			i = skipEscape(s, i)
			continue
		}
		_, size := utf8.DecodeRuneInString(s[i:])
		i += size
		w++
	}
	return w
}

// skipEscape returns the index just past an ANSI escape starting at i.
func skipEscape(s string, i int) int {
	i++ // past ESC
	if i < len(s) && s[i] == '[' {
		i++
		for i < len(s) && !isFinalByte(s[i]) {
			i++
		}
		if i < len(s) {
			i++ // past the final letter
		}
	}
	return i
}

func isFinalByte(b byte) bool {
	return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z')
}

// Truncate trims a plain (no-SGR) string to max visible runes, adding an
// ellipsis when it has to cut.
func Truncate(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	if max == 1 {
		return "…"
	}
	r := []rune(s)
	return string(r[:max-1]) + "…"
}

// Pad left-aligns a plain string into exactly width columns.
func Pad(s string, width int) string {
	s = Truncate(s, width)
	if d := width - utf8.RuneCountInString(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}

// PadLeft right-aligns a plain string into exactly width columns (for numbers).
func PadLeft(s string, width int) string {
	s = Truncate(s, width)
	if d := width - utf8.RuneCountInString(s); d > 0 {
		return strings.Repeat(" ", d) + s
	}
	return s
}

// ClampLine ensures a possibly-styled line never exceeds width visible columns,
// preserving escape sequences and closing colour at the cut.
func ClampLine(s string, width int) string {
	if VisibleWidth(s) <= width {
		return s
	}
	var b strings.Builder
	w := 0
	for i := 0; i < len(s) && w < width; {
		if s[i] == 0x1b {
			j := skipEscape(s, i)
			b.WriteString(s[i:j])
			i = j
			continue
		}
		_, size := utf8.DecodeRuneInString(s[i:])
		b.WriteString(s[i : i+size])
		i += size
		w++
	}
	if ColorEnabled {
		b.WriteString(reset)
	}
	return b.String()
}

// StripSGR removes ANSI escape sequences, leaving plain text.
func StripSGR(s string) string {
	if !strings.ContainsRune(s, 0x1b) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			i = skipEscape(s, i)
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// HSlice drops the first hoff visible columns of a plain string (for horizontal
// scrolling). Input must be plain text (no SGR).
func HSlice(s string, hoff int) string {
	if hoff <= 0 {
		return s
	}
	r := []rune(s)
	if hoff >= len(r) {
		return ""
	}
	return string(r[hoff:])
}

// PadVisible pads a possibly-styled string to exactly width visible columns,
// preserving SGR sequences. Truncates (with ClampLine) when too long.
func PadVisible(s string, width int) string {
	s = ClampLine(s, width)
	if d := width - VisibleWidth(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}

func itoa(n int) string { return strconv.Itoa(n) }
