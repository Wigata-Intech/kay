package tui_test

import (
	"os"
	"testing"

	"github.com/Wigata-Intech/kay/internal/tui"
)

func TestThreshColor(t *testing.T) {
	prev := tui.ColorEnabled
	tui.ColorEnabled = true
	t.Cleanup(func() { tui.ColorEnabled = prev })
	tests := []struct {
		name string
		pct  float64
		want func(string) string
	}{
		{name: "green low", pct: 10, want: tui.Green},
		{name: "green just under 70", pct: 69, want: tui.Green},
		{name: "yellow boundary", pct: 70, want: tui.Yellow},
		{name: "yellow just under 90", pct: 89, want: tui.Yellow},
		{name: "red boundary", pct: 90, want: tui.Red},
		{name: "red high", pct: 99, want: tui.Red},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, want := tui.ThreshColor("x", tt.pct), tt.want("x"); got != want {
				t.Errorf("ThreshColor(x, %v) = %q, want %q", tt.pct, got, want)
			}
		})
	}
}

func TestFirstLine(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "single line", in: "only", want: "only"},
		{name: "empty", in: "", want: ""},
		{name: "multiline takes first", in: "first\nsecond\nthird", want: "first"},
		{name: "leading newline", in: "\nrest", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tui.FirstLine(tt.in); got != tt.want {
				t.Errorf("FirstLine(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestClampAll(t *testing.T) {
	prev := tui.ColorEnabled
	tui.ColorEnabled = false
	t.Cleanup(func() { tui.ColorEnabled = prev })

	t.Run("truncates height", func(t *testing.T) {
		out := tui.ClampAll([]string{"a", "b", "c", "d"}, 10, 2)
		if len(out) != 2 {
			t.Errorf("height = %d, want 2", len(out))
		}
	})
	t.Run("clamps width", func(t *testing.T) {
		out := tui.ClampAll([]string{"abcdefghij"}, 4, 5)
		if w := tui.VisibleWidth(out[0]); w > 4 {
			t.Errorf("width = %d, want <= 4", w)
		}
	})
}

func TestSetColorMode(t *testing.T) {
	prev := tui.ColorEnabled
	t.Cleanup(func() { tui.ColorEnabled = prev })

	t.Run("always enables", func(t *testing.T) {
		tui.ColorEnabled = false
		tui.SetColorMode("always")
		if !tui.ColorEnabled {
			t.Error("always: ColorEnabled = false, want true")
		}
	})
	t.Run("never disables", func(t *testing.T) {
		tui.ColorEnabled = true
		tui.SetColorMode("never")
		if tui.ColorEnabled {
			t.Error("never: ColorEnabled = true, want false")
		}
	})
	t.Run("auto with NO_COLOR stays off", func(t *testing.T) {
		t.Setenv("NO_COLOR", "1")
		tui.ColorEnabled = true
		tui.SetColorMode("auto")
		if tui.ColorEnabled {
			t.Error("auto+NO_COLOR: ColorEnabled = true, want false")
		}
	})
	t.Run("auto with dumb TERM stays off", func(t *testing.T) {
		t.Setenv("NO_COLOR", "")
		t.Setenv("TERM", "dumb")
		tui.ColorEnabled = true
		tui.SetColorMode("auto")
		if tui.ColorEnabled {
			t.Error("auto+dumb: ColorEnabled = true, want false")
		}
	})
	t.Run("auto off when stdout is not a terminal", func(t *testing.T) {
		// Under `go test` stdout is a pipe, so auto-detection must resolve false
		// even with a color-friendly environment.
		t.Setenv("NO_COLOR", "")
		t.Setenv("TERM", "xterm-256color")
		if term := os.Getenv("TERM"); term != "xterm-256color" {
			t.Fatalf("TERM setup failed: %q", term)
		}
		tui.ColorEnabled = true
		tui.SetColorMode("auto")
		if tui.ColorEnabled {
			t.Error("auto+pipe: ColorEnabled = true, want false")
		}
	})
}
