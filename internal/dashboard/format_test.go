// White-box: tests for format.go — the pure formatting/colour helpers.
package dashboard

import (
	"strings"
	"testing"

	"github.com/Wigata-Intech/kay/internal/tui"
)

func TestHumanBytes(t *testing.T) {
	tests := []struct {
		name string
		in   float64
		want string
	}{
		{"bytes", 512, "512 B"},
		{"kib", 1024, "1.0 KB"},
		{"mib", 1024 * 1024, "1.0 MB"},
		{"gib", 1024 * 1024 * 1024, "1.0 GB"},
		{"zero", 0, "0 B"},
		{"exabyte", 1024 * 1024 * 1024 * 1024 * 1024 * 1024 * 2, "2.0 EB"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := humanBytes(tt.in); got != tt.want {
				t.Errorf("humanBytes(%v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestHumanKB(t *testing.T) {
	if got := humanKB(1024); got != "1.0 MB" {
		t.Errorf("humanKB(1024) = %q, want %q", got, "1.0 MB")
	}
	if got := humanKB(0); got != "0 B" {
		t.Errorf("humanKB(0) = %q, want %q", got, "0 B")
	}
}

func TestHumanDuration(t *testing.T) {
	tests := []struct {
		name string
		sec  float64
		want string
	}{
		{"hours-minutes", 3660, "1h 1m"},
		{"days", 90000, "1d 1h 0m"},
		{"zero", 0, "0h 0m"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := humanDuration(tt.sec); got != tt.want {
				t.Errorf("humanDuration(%v) = %q, want %q", tt.sec, got, tt.want)
			}
		})
	}
}

func TestMakeBar(t *testing.T) {
	noColor(t)
	tests := []struct {
		name  string
		pct   float64
		width int
		want  string
	}{
		{"half", 50, 10, "[█████·····]"},
		{"full", 100, 4, "[████]"},
		{"empty", 0, 4, "[····]"},
		{"negative-clamps-empty", -20, 4, "[····]"},
		{"over-100-clamps-full", 150, 4, "[████]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := makeBar(tt.pct, tt.width); got != tt.want {
				t.Errorf("makeBar(%v,%d) = %q, want %q", tt.pct, tt.width, got, tt.want)
			}
		})
	}
}

func TestGaugeLine(t *testing.T) {
	noColor(t)
	got := gaugeLine("CPU", 50, 10, "4 cores")
	if !strings.Contains(got, "CPU") || !strings.Contains(got, "50%") || !strings.Contains(got, "4 cores") {
		t.Errorf("gaugeLine missing parts: %q", got)
	}
	if !strings.Contains(got, "[█████·····]") {
		t.Errorf("gaugeLine missing bar: %q", got)
	}
}

func TestLoadColor(t *testing.T) {
	prev := tui.ColorEnabled
	tui.ColorEnabled = true
	t.Cleanup(func() { tui.ColorEnabled = prev })
	tests := []struct {
		name string
		load float64
		ncpu int
		want func(string) string
	}{
		{"green-idle", 1, 4, tui.Green},
		{"yellow-over-70pct", 3, 4, tui.Yellow},
		{"red-over-cores", 5, 4, tui.Red},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, want := loadColor(tt.load, tt.ncpu, "x"), tt.want("x"); got != want {
				t.Errorf("loadColor(%v,%d) = %q, want %q", tt.load, tt.ncpu, got, want)
			}
		})
	}
}

func TestColorStatus(t *testing.T) {
	prev := tui.ColorEnabled
	tui.ColorEnabled = true
	t.Cleanup(func() { tui.ColorEnabled = prev })
	tests := []struct {
		name   string
		status string
		want   func(string) string
	}{
		{"up-green", "Up 2 hours", tui.Green},
		{"healthy-green", "Up 2 hours (healthy)", tui.Green},
		{"unhealthy-red", "Up 2 hours (unhealthy)", tui.Red},
		{"exited-red", "Exited (0) 3 minutes ago", tui.Red},
		{"dead-red", "dead", tui.Red},
		{"restarting-red", "restarting (1)", tui.Red},
		{"unknown-plain", "Created", func(s string) string { return s }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, want := colorStatus(tt.status), tt.want(tt.status); got != want {
				t.Errorf("colorStatus(%q) = %q, want %q", tt.status, got, want)
			}
		})
	}
}

func TestValidID(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"alnum", "abc123", true},
		{"with-allowed-punct", "web_1.2-3", true},
		{"empty", "", false},
		{"space", "web 1", false},
		{"slash", "a/b", false},
		{"semicolon", "id;rm", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := validID(tt.in); got != tt.want {
				t.Errorf("validID(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}
