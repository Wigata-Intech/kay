package tui_test

import (
	"strings"
	"testing"

	"github.com/Wigata-Intech/kay/internal/tui"
)

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
