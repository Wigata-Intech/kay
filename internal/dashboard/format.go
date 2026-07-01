package dashboard

import (
	"fmt"
	"strings"
	"time"

	"github.com/Wigata-Intech/kay/internal/tui"
)

// colorStatus colours a Docker status string by health/state.
func colorStatus(status string) string {
	ls := strings.ToLower(status)
	switch {
	case strings.Contains(ls, "unhealthy"):
		return tui.Red(status)
	case strings.Contains(ls, "healthy"):
		return tui.Green(status)
	case strings.HasPrefix(status, "Exited"), strings.Contains(ls, "dead"), strings.Contains(ls, "restarting"):
		return tui.Red(status)
	case strings.HasPrefix(status, "Up"):
		return tui.Green(status)
	}
	return status
}

func loadColor(load float64, ncpu int, s string) string {
	switch {
	case load > float64(ncpu):
		return tui.Red(s)
	case load > float64(ncpu)*0.7:
		return tui.Yellow(s)
	default:
		return tui.Green(s)
	}
}

func makeBar(pct float64, width int) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	filled := int(pct/100*float64(width) + 0.5)
	if filled > width {
		filled = width
	}
	return "[" + tui.ThreshColor(strings.Repeat("█", filled), pct) +
		tui.Dim(strings.Repeat("·", width-filled)) + "]"
}

func gaugeLine(label string, pct float64, width int, suffix string) string {
	return fmt.Sprintf("%-4s %s %s  %s",
		label, makeBar(pct, width), tui.ThreshColor(fmt.Sprintf("%3.0f%%", pct), pct), suffix)
}

func validID(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '_' || r == '.' || r == '-'
		if !ok {
			return false
		}
	}
	return true
}

func humanBytes(b float64) string {
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

func humanKB(kb uint64) string { return humanBytes(float64(kb) * 1024) }

func humanDuration(sec float64) string {
	d := time.Duration(sec) * time.Second
	days := int(d.Hours()) / 24
	hh := int(d.Hours()) % 24
	mm := int(d.Minutes()) % 60
	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm", days, hh, mm)
	}
	return fmt.Sprintf("%dh %dm", hh, mm)
}
