package tui_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/Wigata-Intech/kay/internal/tui"
)

// TestListSelectionAndScroll walks one list through render, Bottom(), and
// re-render. The steps share state, so this is an ordered sequence rather than
// a table.
func TestListSelectionAndScroll(t *testing.T) {
	old := tui.ColorEnabled
	tui.ColorEnabled = false // simplify string assertions
	defer func() { tui.ColorEnabled = old }()

	l := &tui.List{Header: "H"}
	rows := make([]string, 10)
	for i := range rows {
		rows[i] = "row" + strconv.Itoa(i)
	}
	l.SetRows(rows)

	// height 5 => header + 3 rows + "more" marker (since 10 > 4 rows)
	out := l.Render(20, 5, true)
	if len(out) != 5 {
		t.Fatalf("render lines = %d, want 5", len(out))
	}
	if !strings.HasPrefix(strings.TrimSpace(out[0]), "H") {
		t.Errorf("header missing: %q", out[0])
	}
	if !strings.Contains(out[len(out)-1], "more") {
		t.Errorf("expected more marker, got %q", out[len(out)-1])
	}

	// Move to the bottom; the window should scroll to include the last row.
	l.Bottom()
	out = l.Render(20, 5, true)
	joined := strings.Join(out, "\n")
	if !strings.Contains(joined, "row9") {
		t.Errorf("bottom not visible after Bottom(): %q", joined)
	}
	if l.Selected != 9 {
		t.Errorf("Selected = %d, want 9", l.Selected)
	}
}

func TestListEmpty(t *testing.T) {
	old := tui.ColorEnabled
	tui.ColorEnabled = false
	defer func() { tui.ColorEnabled = old }()

	out := (&tui.List{}).Render(20, 4, true)
	if len(out) != 1 || !strings.Contains(out[0], "none") {
		t.Errorf("empty list render = %v, want a single \"(none)\" line", out)
	}
}

func TestTabBarWidth(t *testing.T) {
	old := tui.ColorEnabled
	tui.ColorEnabled = false
	defer func() { tui.ColorEnabled = old }()

	bar := tui.TabBar([]string{"Overview", "Processes", "Docker", "Network"}, 1, 40)
	if w := tui.VisibleWidth(bar); w > 40 {
		t.Errorf("tab bar width %d exceeds 40", w)
	}
	if !strings.Contains(bar, "2:Processes") {
		t.Errorf("active tab label missing: %q", bar)
	}
}
