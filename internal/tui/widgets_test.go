package tui

import (
	"strings"
	"testing"
)

func TestListSelectionAndScroll(t *testing.T) {
	old := ColorEnabled
	ColorEnabled = false // simplify string assertions
	defer func() { ColorEnabled = old }()

	l := &List{Header: "H"}
	rows := make([]string, 10)
	for i := range rows {
		rows[i] = "row" + itoa(i)
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
	old := ColorEnabled
	ColorEnabled = false
	defer func() { ColorEnabled = old }()

	l := &List{}
	out := l.Render(20, 4, true)
	if len(out) != 1 || !strings.Contains(out[0], "none") {
		t.Errorf("empty list render = %v", out)
	}
}

func TestTabBarWidth(t *testing.T) {
	old := ColorEnabled
	ColorEnabled = false
	defer func() { ColorEnabled = old }()

	bar := TabBar([]string{"Overview", "Processes", "Docker", "Network"}, 1, 40)
	if VisibleWidth(bar) > 40 {
		t.Errorf("tab bar width %d exceeds 40", VisibleWidth(bar))
	}
	if !strings.Contains(bar, "2:Processes") {
		t.Errorf("active tab label missing: %q", bar)
	}
}
