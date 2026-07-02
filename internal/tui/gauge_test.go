package tui_test

import (
	"strings"
	"testing"

	"github.com/Wigata-Intech/kay/internal/tui"
)

func TestBar(t *testing.T) {
	old := tui.ColorEnabled
	tui.ColorEnabled = false
	defer func() { tui.ColorEnabled = old }()

	tests := []struct {
		name  string
		pct   float64
		width int
		want  string
	}{
		{"half", 50, 10, "[█████·····]"},
		{"full", 100, 4, "[████]"},
		{"empty", 0, 4, "[····]"},
		{"negative clamps empty", -20, 4, "[····]"},
		{"over 100 clamps full", 150, 4, "[████]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tui.Bar(tt.pct, tt.width); got != tt.want {
				t.Errorf("Bar(%v,%d) = %q, want %q", tt.pct, tt.width, got, tt.want)
			}
		})
	}
}

func TestGauge(t *testing.T) {
	old := tui.ColorEnabled
	tui.ColorEnabled = false
	defer func() { tui.ColorEnabled = old }()

	got := tui.Gauge("CPU", 50, 10, "4 cores")
	for _, want := range []string{"CPU", "50%", "4 cores", "[█████·····]"} {
		if !strings.Contains(got, want) {
			t.Errorf("Gauge missing %q: %q", want, got)
		}
	}
}

func TestSparkline(t *testing.T) {
	old := tui.ColorEnabled
	tui.ColorEnabled = false
	defer func() { tui.ColorEnabled = old }()

	t.Run("empty is a collecting note", func(t *testing.T) {
		if got := tui.Sparkline(nil, 8); !strings.Contains(got, "collecting") {
			t.Errorf("Sparkline(nil) = %q, want a collecting note", got)
		}
	})
	t.Run("renders blocks", func(t *testing.T) {
		if got := tui.Sparkline([]float64{0, 50, 100}, 8); got != "▁▅█" {
			t.Errorf("Sparkline = %q, want %q", got, "▁▅█")
		}
	})
	t.Run("clamps and truncates to width", func(t *testing.T) {
		got := tui.Sparkline([]float64{-10, 200, 200, 200}, 2)
		if r := []rune(got); r[0] != '█' || len(r) != 2 {
			t.Errorf("Sparkline clamp/trunc = %q", got)
		}
	})
}
