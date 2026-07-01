package tui_test

import (
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/Wigata-Intech/kay/internal/tui"
)

// TestNewReaderReadEvent drives the Reader over an os.Pipe so ReadEvent decodes
// a real byte stream (the constructor and read loop otherwise need a terminal).
func TestNewReaderReadEvent(t *testing.T) {
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer func() { _ = pr.Close() }()

	r := tui.NewReader(pr)
	if r == nil {
		t.Fatal("NewReader returned nil")
	}

	go func() {
		_, _ = pw.Write([]byte{'\t'}) // a Tab key
		_ = pw.Close()
	}()

	ev, err := r.ReadEvent()
	if err != nil {
		t.Fatalf("ReadEvent: %v", err)
	}
	if ev.Type != tui.EventKey || ev.Key != tui.KeyTab {
		t.Errorf("ReadEvent = {type:%d key:%d}, want Tab key", ev.Type, ev.Key)
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

// TestListPager exercises the non-selectable pager path together with the scroll
// helpers. Steps share state, so this reads as an ordered sequence.
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
