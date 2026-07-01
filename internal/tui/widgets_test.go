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
	out := l.Render(20, 5)
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
	out = l.Render(20, 5)
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

	out := (&tui.List{}).Render(20, 4)
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

// TestPager exercises the scrollable pager: render window, scroll clamping, and
// the Window range. Steps share state, so this reads as an ordered sequence.
func TestPager(t *testing.T) {
	old := tui.ColorEnabled
	tui.ColorEnabled = false
	defer func() { tui.ColorEnabled = old }()

	rows := make([]string, 10)
	for i := range rows {
		rows[i] = "row" + strconv.Itoa(i)
	}
	p := &tui.Pager{Rows: rows}

	if p.Len() != 10 {
		t.Fatalf("Len = %d, want 10", p.Len())
	}
	if p.Offset() != 0 {
		t.Fatalf("initial Offset = %d, want 0", p.Offset())
	}

	// Render from the top: first `height` rows.
	out := p.Render(20, 4)
	if len(out) != 4 {
		t.Fatalf("pager render lines = %d, want 4", len(out))
	}
	if !strings.HasPrefix(strings.TrimSpace(out[0]), "row0") {
		t.Errorf("first line = %q, want row0", out[0])
	}

	// ScrollBy moves the window; ScrollBy below zero is clamped to zero.
	p.ScrollBy(2)
	if p.Offset() != 2 {
		t.Errorf("Offset after ScrollBy(2) = %d, want 2", p.Offset())
	}
	p.ScrollBy(-100)
	if p.Offset() != 0 {
		t.Errorf("Offset after ScrollBy(-100) = %d, want 0 (clamped)", p.Offset())
	}

	// ScrollBottom overshoots deliberately; Window clamps to the last page.
	p.ScrollBottom()
	start, end := p.Window(4)
	if start != 6 || end != 10 {
		t.Errorf("Window(4) after ScrollBottom = (%d,%d), want (6,10)", start, end)
	}

	// ScrollTo an in-range offset, then confirm the render window matches.
	p.ScrollTo(3)
	start, end = p.Window(4)
	if start != 3 || end != 7 {
		t.Errorf("Window(4) after ScrollTo(3) = (%d,%d), want (3,7)", start, end)
	}
	out = p.Render(20, 4)
	if !strings.HasPrefix(strings.TrimSpace(out[0]), "row3") {
		t.Errorf("first line after ScrollTo(3) = %q, want row3", out[0])
	}

	// ScrollTop resets to the first row.
	p.ScrollTop()
	if p.Offset() != 0 {
		t.Errorf("Offset after ScrollTop = %d, want 0", p.Offset())
	}
}

// TestListRenderHeightGuards covers the two early-return branches in Render: a
// non-positive height yields nothing, and a header that consumes the only
// available line leaves just the header.
func TestListRenderHeightGuards(t *testing.T) {
	old := tui.ColorEnabled
	tui.ColorEnabled = false
	defer func() { tui.ColorEnabled = old }()

	t.Run("zero-height", func(t *testing.T) {
		l := &tui.List{}
		l.SetRows([]string{"a", "b"})
		if out := l.Render(20, 0); len(out) != 0 {
			t.Errorf("Render(_,0) = %v, want empty", out)
		}
	})

	t.Run("header-consumes-height", func(t *testing.T) {
		l := &tui.List{Header: "H"}
		l.SetRows([]string{"a", "b"})
		out := l.Render(20, 1)
		if len(out) != 1 || !strings.Contains(out[0], "H") {
			t.Errorf("Render with height 1 and a header = %v, want just the header", out)
		}
	})
}

// TestListMoveAndEnds covers selection movement helpers and the clamp behaviour
// at both ends of the list.
func TestListMoveAndEnds(t *testing.T) {
	l := &tui.List{}
	l.SetRows([]string{"a", "b", "c"})

	l.Move(1)
	if l.Selected != 1 {
		t.Errorf("Selected after Move(1) = %d, want 1", l.Selected)
	}
	l.Move(10) // past the end -> clamps to last index
	if l.Selected != 2 {
		t.Errorf("Selected after Move(10) = %d, want 2 (clamped)", l.Selected)
	}
	l.Move(-100) // past the top -> clamps to 0
	if l.Selected != 0 {
		t.Errorf("Selected after Move(-100) = %d, want 0 (clamped)", l.Selected)
	}
	l.Bottom()
	if l.Selected != 2 {
		t.Errorf("Selected after Bottom() = %d, want 2", l.Selected)
	}
	l.Top()
	if l.Selected != 0 {
		t.Errorf("Selected after Top() = %d, want 0", l.Selected)
	}
}

// TestPagerWindowShortList covers the branch where the rows are shorter than the
// viewport, so the whole set fits and the offset is pinned to zero.
func TestPagerWindowShortList(t *testing.T) {
	p := &tui.Pager{Rows: []string{"only", "two"}}
	p.ScrollTo(5) // deliberately out of range
	start, end := p.Window(10)
	if start != 0 || end != 2 {
		t.Errorf("Window(10) on short list = (%d,%d), want (0,2)", start, end)
	}
}
