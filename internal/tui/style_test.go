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

// TestColorHelpers checks every SGR wrapper twice: with colour on it must wrap
// the text (changing the raw bytes) while leaving the visible width unchanged,
// and with colour off it must return the input verbatim.
func TestColorHelpers(t *testing.T) {
	fns := []struct {
		name string
		fn   func(string) string
	}{
		{"Red", tui.Red},
		{"Green", tui.Green},
		{"Yellow", tui.Yellow},
		{"Blue", tui.Blue},
		{"Magenta", tui.Magenta},
		{"Cyan", tui.Cyan},
		{"Dim", tui.Dim},
		{"Bold", tui.Bold},
		{"Reverse", tui.Reverse},
	}
	const in = "hello"

	old := tui.ColorEnabled
	defer func() { tui.ColorEnabled = old }()

	for _, f := range fns {
		t.Run(f.name+"/enabled", func(t *testing.T) {
			tui.ColorEnabled = true
			got := f.fn(in)
			if got == in {
				t.Errorf("%s(%q) = %q, want wrapped (different from input)", f.name, in, got)
			}
			if !strings.ContainsRune(got, 0x1b) {
				t.Errorf("%s(%q) = %q, want an SGR escape", f.name, in, got)
			}
			if w := tui.VisibleWidth(got); w != len(in) {
				t.Errorf("%s visible width = %d, want %d", f.name, w, len(in))
			}
		})
		t.Run(f.name+"/disabled", func(t *testing.T) {
			tui.ColorEnabled = false
			if got := f.fn(in); got != in {
				t.Errorf("%s(%q) with colour off = %q, want %q", f.name, in, got, in)
			}
		})
	}
}

// TestStripSGR verifies escape sequences are removed while plain text passes
// through untouched (including the fast path for strings with no ESC byte).
func TestStripSGR(t *testing.T) {
	old := tui.ColorEnabled
	tui.ColorEnabled = true
	defer func() { tui.ColorEnabled = old }()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain-passthrough", "plain text", "plain text"},
		{"empty", "", ""},
		{"single-wrap", tui.Red("abc"), "abc"},
		{"nested-wraps", tui.Bold(tui.Green("hi")), "hi"},
		{"mixed", "a" + tui.Cyan("b") + "c", "abc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tui.StripSGR(tt.in); got != tt.want {
				t.Errorf("StripSGR(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestHSlice covers dropping leading columns for horizontal scrolling, including
// the boundary where the offset meets or exceeds the rune count.
func TestHSlice(t *testing.T) {
	tests := []struct {
		name string
		in   string
		hoff int
		want string
	}{
		{"zero-offset", "hello", 0, "hello"},
		{"negative-offset", "hello", -3, "hello"},
		{"drop-two", "hello", 2, "llo"},
		{"multibyte", "héllo", 2, "llo"},
		{"offset-equals-length", "hi", 2, ""},
		{"offset-beyond-length", "hi", 5, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tui.HSlice(tt.in, tt.hoff); got != tt.want {
				t.Errorf("HSlice(%q, %d) = %q, want %q", tt.in, tt.hoff, got, tt.want)
			}
		})
	}
}

// TestPadEdgeCases covers padding paths not exercised elsewhere: truncation when
// content exceeds the width, and the non-positive width guards.
func TestPadEdgeCases(t *testing.T) {
	tests := []struct {
		name  string
		fn    func(string, int) string
		in    string
		width int
		want  string
	}{
		{"pad-truncates", tui.Pad, "hello", 3, "he…"},
		{"pad-zero-width", tui.Pad, "hi", 0, ""},
		{"pad-negative-width", tui.Pad, "hi", -2, ""},
		{"padleft-right-aligns", tui.PadLeft, "42", 5, "   42"},
		{"padleft-exact", tui.PadLeft, "abc", 3, "abc"},
		{"padleft-truncates", tui.PadLeft, "hello", 3, "he…"},
		{"pad-width-one-ellipsis", tui.Pad, "hello", 1, "…"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.fn(tt.in, tt.width); got != tt.want {
				t.Errorf("%s(%q, %d) = %q, want %q", tt.name, tt.in, tt.width, got, tt.want)
			}
		})
	}
}

// TestJoin places two column blocks side by side. Colour is disabled so the
// left-padding math is easy to assert against the widest left line.
func TestJoin(t *testing.T) {
	old := tui.ColorEnabled
	tui.ColorEnabled = false
	defer func() { tui.ColorEnabled = old }()

	t.Run("equal-lengths", func(t *testing.T) {
		out := tui.Join([]string{"a", "bbbb"}, []string{"1", "2"}, 2)
		if len(out) != 2 {
			t.Fatalf("lines = %d, want 2", len(out))
		}
		// widest left line is "bbbb" (4), gap is 2 => right starts at column 6.
		if out[0] != "a"+strings.Repeat(" ", 3)+"  "+"1" {
			t.Errorf("line 0 = %q", out[0])
		}
		if out[1] != "bbbb"+"  "+"2" {
			t.Errorf("line 1 = %q", out[1])
		}
	})

	t.Run("right-longer", func(t *testing.T) {
		out := tui.Join([]string{"L"}, []string{"r0", "r1", "r2"}, 1)
		if len(out) != 3 {
			t.Fatalf("lines = %d, want 3 (max of the two blocks)", len(out))
		}
		// Missing left lines are empty but still padded to the left width (1).
		if out[2] != " "+" "+"r2" {
			t.Errorf("line 2 = %q, want left-pad + gap + r2", out[2])
		}
	})

	t.Run("left-longer", func(t *testing.T) {
		out := tui.Join([]string{"l0", "l1"}, []string{"r0"}, 1)
		if len(out) != 2 {
			t.Fatalf("lines = %d, want 2", len(out))
		}
		if !strings.HasPrefix(out[1], "l1") {
			t.Errorf("line 1 = %q, want left content preserved", out[1])
		}
	})

	t.Run("both-empty", func(t *testing.T) {
		if out := tui.Join(nil, nil, 2); len(out) != 0 {
			t.Errorf("Join(nil,nil) = %v, want empty", out)
		}
	})
}
