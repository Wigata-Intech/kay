package tui_test

import (
	"testing"

	"github.com/Wigata-Intech/kay/internal/tui"
)

func TestStatusBar(t *testing.T) {
	old := tui.ColorEnabled
	tui.ColorEnabled = false
	defer func() { tui.ColorEnabled = old }()

	tests := []struct {
		name  string
		left  string
		right string
		width int
		want  string
	}{
		{"left and right split the width", "kay", "q quit", 15, "kay      q quit"},
		{"exact fit keeps one space", "abc", "def", 7, "abc def"},
		{"collision drops the right side", "a long breadcrumb", "hints", 20, "a long breadcrumb   "},
		{"empty right pads the left", "kay", "", 6, "kay   "},
		{"left alone overflows and clamps", "0123456789", "", 4, "0123"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tui.StatusBar(tt.left, tt.right, tt.width)
			if got != tt.want {
				t.Errorf("StatusBar(%q,%q,%d) = %q, want %q", tt.left, tt.right, tt.width, got, tt.want)
			}
			if w := tui.VisibleWidth(got); w != tt.width {
				t.Errorf("visible width = %d, want %d", w, tt.width)
			}
		})
	}
}
