package tui

import "strings"

// TabBar renders a single-line tab selector, highlighting the active tab.
func TabBar(tabs []string, active, width int) string {
	parts := make([]string, len(tabs))
	for i, t := range tabs {
		label := " " + itoa(i+1) + ":" + t + " "
		if i == active {
			parts[i] = Reverse(label)
		} else {
			parts[i] = label
		}
	}
	return ClampLine(strings.Join(parts, " "), width)
}

// List is a scrollable, selectable set of pre-formatted plain-text rows. The
// caller formats row columns; List handles selection highlight and scrolling.
type List struct {
	Header   string // optional column header (not selectable)
	Rows     []string
	Selected int
	offset   int
}

// SetRows replaces the rows, keeping the selection in range.
func (l *List) SetRows(rows []string) {
	l.Rows = rows
	l.clamp()
}

func (l *List) clamp() {
	if l.Selected >= len(l.Rows) {
		l.Selected = len(l.Rows) - 1
	}
	if l.Selected < 0 {
		l.Selected = 0
	}
}

// Move shifts the selection by d (negative = up).
func (l *List) Move(d int) {
	l.Selected += d
	l.clamp()
}

// Top/Bottom jump the selection to the ends.
func (l *List) Top()    { l.Selected = 0 }
func (l *List) Bottom() { l.Selected = len(l.Rows) - 1; l.clamp() }

// Pager-mode scrolling (used when the list is rendered non-selectable, e.g. the
// detail/logs overlay). The offset's upper bound is clamped during Render.
func (l *List) ScrollBy(d int) {
	l.offset += d
	if l.offset < 0 {
		l.offset = 0
	}
}
func (l *List) ScrollTop()     { l.offset = 0 }
func (l *List) ScrollBottom()  { l.offset = len(l.Rows) } // clamped in Render
func (l *List) ScrollTo(i int) { l.offset = i }           // clamped in Render

// Offset/Len expose pager state for callers that render rows themselves
// (e.g. a pager that adds search highlighting and horizontal scrolling).
func (l *List) Offset() int { return l.offset }
func (l *List) Len() int    { return len(l.Rows) }

// PagerWindow clamps the offset for the given viewport height and returns the
// [start,end) range of rows that should be displayed.
func (l *List) PagerWindow(height int) (start, end int) {
	maxOff := len(l.Rows) - height
	if maxOff < 0 {
		maxOff = 0
	}
	if l.offset > maxOff {
		l.offset = maxOff
	}
	if l.offset < 0 {
		l.offset = 0
	}
	end = l.offset + height
	if end > len(l.Rows) {
		end = len(l.Rows)
	}
	return l.offset, end
}

// Render returns at most height lines (including any header) each clamped to
// width. When selectable, the current row is shown in reverse video.
func (l *List) Render(width, height int, selectable bool) []string {
	var out []string
	if height <= 0 {
		return out
	}
	if l.Header != "" {
		out = append(out, ClampLine(Cyan(l.Header), width))
		height--
	}
	if height <= 0 {
		return out
	}
	if len(l.Rows) == 0 {
		return append(out, Dim(Pad("(none)", width)))
	}

	if !selectable {
		// Pager mode: the scroll offset is authoritative (no hidden cursor).
		return append(out, l.renderPager(width, height)...)
	}
	return append(out, l.renderSelectable(width, height)...)
}

// renderSelectable renders the row window around the cursor, keeping the
// selected row visible, reserving a line for a "more" marker when the list
// overflows, and drawing the selection as a clean reverse-video bar.
func (l *List) renderSelectable(width, height int) []string {
	l.clamp()

	rows := height
	clipped := len(l.Rows) > rows
	if clipped {
		rows-- // reserve a line for the "more" marker
	}
	if rows < 1 {
		rows = 1
	}
	// Keep the selected row inside the visible window.
	if l.Selected < l.offset {
		l.offset = l.Selected
	}
	if l.Selected >= l.offset+rows {
		l.offset = l.Selected - rows + 1
	}
	if l.offset < 0 {
		l.offset = 0
	}
	end := l.offset + rows
	if end > len(l.Rows) {
		end = len(l.Rows)
		l.offset = end - rows
		if l.offset < 0 {
			l.offset = 0
		}
	}
	out := make([]string, 0, rows+1)
	for i := l.offset; i < end; i++ {
		// Non-selected rows keep any colour they carry; the selected row is
		// shown as a clean reverse-video bar (colour stripped) so it reads well.
		var line string
		if i == l.Selected {
			line = Reverse(PadVisible(StripSGR(l.Rows[i]), width))
		} else {
			line = PadVisible(l.Rows[i], width)
		}
		out = append(out, line)
	}
	if clipped {
		if remaining := len(l.Rows) - end; remaining > 0 {
			out = append(out, Dim(Pad("…(+"+itoa(remaining)+" more)", width)))
		} else {
			out = append(out, "") // scrolled to bottom: no marker needed
		}
	}
	return out
}

// renderPager renders rows from the scroll offset, filling height. The offset's
// upper bound is clamped here so ScrollBottom/ScrollTo can set it freely.
func (l *List) renderPager(width, height int) []string {
	maxOff := len(l.Rows) - height
	if maxOff < 0 {
		maxOff = 0
	}
	if l.offset > maxOff {
		l.offset = maxOff
	}
	if l.offset < 0 {
		l.offset = 0
	}
	end := l.offset + height
	if end > len(l.Rows) {
		end = len(l.Rows)
	}
	out := make([]string, 0, end-l.offset)
	for i := l.offset; i < end; i++ {
		out = append(out, Pad(l.Rows[i], width))
	}
	return out
}
