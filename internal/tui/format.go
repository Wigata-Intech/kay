package tui

import (
	"fmt"
	"os"
	"strings"
	"time"

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

// HumanBytes formats a byte count with a binary (1024) unit suffix.
func HumanBytes(b float64) string {
	const unit = 1024.0
	if b < unit {
		return fmt.Sprintf("%.0f B", b)
	}
	v := b / unit
	for _, u := range []string{"K", "M", "G", "T", "P"} {
		if v < unit {
			return fmt.Sprintf("%.1f %sB", v, u)
		}
		v /= unit
	}
	return fmt.Sprintf("%.1f EB", v)
}

// HumanDuration formats a number of seconds as "Nd Nh Nm" (dropping the day
// field under 24h).
func HumanDuration(sec float64) string {
	d := time.Duration(sec) * time.Second
	days := int(d.Hours()) / 24
	hh := int(d.Hours()) % 24
	mm := int(d.Minutes()) % 60
	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm", days, hh, mm)
	}
	return fmt.Sprintf("%dh %dm", hh, mm)
}
