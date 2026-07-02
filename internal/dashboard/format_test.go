// White-box: tests for format.go — the pure formatting/colour helpers.
package dashboard

import (
	"testing"

	"github.com/Wigata-Intech/kay/internal/tui"
)

func TestHumanKB(t *testing.T) {
	if got := humanKB(1024); got != "1.0 MB" {
		t.Errorf("humanKB(1024) = %q, want %q", got, "1.0 MB")
	}
	if got := humanKB(0); got != "0 B" {
		t.Errorf("humanKB(0) = %q, want %q", got, "0 B")
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
