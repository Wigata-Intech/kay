package tui_test

import (
	"strings"
	"testing"

	"github.com/Wigata-Intech/kay/internal/tui"
)

func TestColumns(t *testing.T) {
	old := tui.ColorEnabled
	tui.ColorEnabled = false
	defer func() { tui.ColorEnabled = old }()

	t.Run("pads each column to its widest line", func(t *testing.T) {
		out := tui.Columns([][]string{{"a", "bbbb"}, {"1", "2"}}, 2)
		if len(out) != 2 {
			t.Fatalf("lines = %d, want 2", len(out))
		}
		// left col width = 4 ("bbbb"), gap 2 → right starts at col 6.
		if out[0] != "a"+strings.Repeat(" ", 3)+"  "+"1" {
			t.Errorf("row 0 = %q", out[0])
		}
		if out[1] != "bbbb"+"  "+"2" {
			t.Errorf("row 1 = %q", out[1])
		}
	})

	t.Run("three columns, differing heights", func(t *testing.T) {
		out := tui.Columns([][]string{{"x"}, {"y1", "y2"}, {"z"}}, 1)
		if len(out) != 2 { // max of the three block heights
			t.Fatalf("lines = %d, want 2", len(out))
		}
		if !strings.HasSuffix(out[0], "z") {
			t.Errorf("row 0 should end with the third column: %q", out[0])
		}
	})

	t.Run("empty input", func(t *testing.T) {
		if out := tui.Columns(nil, 2); out != nil {
			t.Errorf("Columns(nil) = %v, want nil", out)
		}
	})
}

func TestColumnsDivided(t *testing.T) {
	old := tui.ColorEnabled
	tui.ColorEnabled = false
	defer func() { tui.ColorEnabled = old }()

	t.Run("fills each column to width and inserts the divider", func(t *testing.T) {
		out := tui.ColumnsDivided([][]string{{"a"}, {"b1", "b2"}}, 4, " | ")
		if len(out) != 2 { // max of the two column heights
			t.Fatalf("lines = %d, want 2", len(out))
		}
		if out[0] != "a   "+" | "+"b1  " {
			t.Errorf("row 0 = %q", out[0])
		}
		if out[1] != "    "+" | "+"b2  " { // shorter column padded blank, divider continues
			t.Errorf("row 1 = %q", out[1])
		}
	})

	t.Run("guards", func(t *testing.T) {
		if tui.ColumnsDivided(nil, 4, " | ") != nil {
			t.Error("nil cols → nil")
		}
		if tui.ColumnsDivided([][]string{{"x"}}, 0, " | ") != nil {
			t.Error("colWidth 0 → nil")
		}
	})
}

func TestColumnCount(t *testing.T) {
	tests := []struct {
		name                          string
		width, minCol, gap, max, want int
	}{
		{"too narrow → 1", 60, 40, 2, 3, 1},
		{"fits two", 84, 40, 2, 3, 2},
		{"fits three", 128, 40, 2, 3, 3},
		{"capped at max", 400, 40, 2, 3, 3},
		{"exactly one", 40, 40, 2, 3, 1},
		{"zero minCol → 1", 200, 0, 2, 3, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tui.ColumnCount(tt.width, tt.minCol, tt.gap, tt.max); got != tt.want {
				t.Errorf("ColumnCount(%d,%d,%d,%d) = %d, want %d",
					tt.width, tt.minCol, tt.gap, tt.max, got, tt.want)
			}
		})
	}
}
