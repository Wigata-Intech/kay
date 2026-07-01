package tui

import (
	"os"
	"strings"

	"golang.org/x/term"
)

// SetColorMode sets ColorEnabled from an explicit mode ("always"/"never"); for
// anything else it auto-detects from NO_COLOR, TERM, and whether stdout is a
// terminal. Terminal-capability detection lives here so callers don't repeat it.
func SetColorMode(mode string) {
	switch mode {
	case "always":
		ColorEnabled = true
	case "never":
		ColorEnabled = false
	default:
		ColorEnabled = os.Getenv("NO_COLOR") == "" &&
			os.Getenv("TERM") != "dumb" &&
			term.IsTerminal(int(os.Stdout.Fd()))
	}
}

// ThreshColor colours s by a 0–100 utilisation percentage: >=90 red, >=70
// yellow, else green. The single source for gauge/stat colour thresholds.
func ThreshColor(s string, pct float64) string {
	switch {
	case pct >= 90:
		return Red(s)
	case pct >= 70:
		return Yellow(s)
	default:
		return Green(s)
	}
}

// ClampAll caps lines to at most h rows and clamps each to w visible columns.
func ClampAll(lines []string, w, h int) []string {
	if len(lines) > h {
		lines = lines[:h]
	}
	for i := range lines {
		lines[i] = ClampLine(lines[i], w)
	}
	return lines
}

// FirstLine returns s up to the first newline (the whole string when there is
// none) — for collapsing multi-line errors onto one row.
func FirstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}
