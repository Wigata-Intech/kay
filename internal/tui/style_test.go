package tui_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Wigata-Intech/kay/internal/tui"
)

func TestVisibleWidthIgnoresSGR(t *testing.T) {
	old := tui.ColorEnabled
	tui.ColorEnabled = true
	defer func() { tui.ColorEnabled = old }()

	// Colored strings are built after enabling colour so SGR escapes are present.
	tests := []struct {
		name string
		in   string
		want int
	}{
		{"plain", "hello", 5},
		{"fully colored", tui.Red("hello"), 5},
		{"mixed", "ab" + tui.Green("cd") + "ef", 6},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tui.VisibleWidth(tt.in); got != tt.want {
				t.Errorf("VisibleWidth(%q) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		width int
		want  string
	}{
		{"truncates with ellipsis", "hello world", 5, "hell…"},
		{"no-op when it fits", "hi", 5, "hi"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tui.Truncate(tt.in, tt.width); got != tt.want {
				t.Errorf("Truncate(%q, %d) = %q, want %q", tt.in, tt.width, got, tt.want)
			}
		})
	}
}

func TestPad(t *testing.T) {
	tests := []struct {
		name  string
		pad   func(string, int) string
		in    string
		width int
		want  string
	}{
		{"Pad right-fills", tui.Pad, "hi", 5, "hi   "},
		{"PadLeft left-fills", tui.PadLeft, "42", 5, "   42"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.pad(tt.in, tt.width)
			if got != tt.want {
				t.Errorf("pad(%q, %d) = %q, want %q", tt.in, tt.width, got, tt.want)
			}
			if n := utf8.RuneCountInString(got); n != tt.width {
				t.Errorf("pad(%q, %d) width = %d runes, want %d", tt.in, tt.width, n, tt.width)
			}
		})
	}
}

func TestPadVisibleIgnoresSGR(t *testing.T) {
	old := tui.ColorEnabled
	tui.ColorEnabled = true
	defer func() { tui.ColorEnabled = old }()

	tests := []struct {
		name      string
		in        string
		width     int
		wantWidth int
	}{
		{"pads to visible width", tui.Red("ab"), 5, 5},
		{"clamps when too long", "abcdefgh", 4, 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if w := tui.VisibleWidth(tui.PadVisible(tt.in, tt.width)); w != tt.wantWidth {
				t.Errorf("PadVisible(%q, %d) visible width = %d, want %d", tt.in, tt.width, w, tt.wantWidth)
			}
		})
	}
}

// TestBoxDimensions checks structural invariants of a rendered box; the
// assertions are a single sequence over one Box output, not independent cases.
func TestBoxDimensions(t *testing.T) {
	old := tui.ColorEnabled
	tui.ColorEnabled = false
	defer func() { tui.ColorEnabled = old }()

	lines := tui.Box("Processes", []string{"row1", "row2"}, 20, 4)
	if len(lines) != 6 { // top + 4 inner + bottom
		t.Fatalf("box lines = %d, want 6", len(lines))
	}
	for i, l := range lines {
		if w := tui.VisibleWidth(l); w != 20 {
			t.Errorf("box line %d width = %d, want 20 (%q)", i, w, l)
		}
	}
	if !strings.Contains(lines[0], "Processes") {
		t.Errorf("title missing in top border: %q", lines[0])
	}
}

func TestClampLineKeepsWidth(t *testing.T) {
	old := tui.ColorEnabled
	tui.ColorEnabled = true
	defer func() { tui.ColorEnabled = old }()

	clamped := tui.ClampLine("abc"+tui.Red("defghij"), 5)
	if w := tui.VisibleWidth(clamped); w != 5 {
		t.Errorf("clamped visible width = %d, want 5 (%q)", w, clamped)
	}
}
