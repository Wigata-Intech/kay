package dashboard

import (
	"strings"

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

// humanKB formats a KiB count via tui.HumanBytes.
func humanKB(kb uint64) string { return tui.HumanBytes(float64(kb) * 1024) }
