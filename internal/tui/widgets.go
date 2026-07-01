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

// List is a selectable set of pre-formatted plain-text rows with a moving
// cursor. The caller formats row columns; List handles selection highlight and
// keeping the cursor visible. For non-selectable scrolling use Pager.
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

// Render returns at most height lines (including any header) each clamped to
// width, with the current row shown in reverse video.
func (l *List) Render(width, height int) []string {
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

// Pager is a scrollable, non-selectable view of pre-formatted rows — used for
// overlays like the detail/logs view. The scroll offset is authoritative (no
// hidden cursor); its upper bound is clamped lazily in Window/Render so callers
// may ScrollBottom/ScrollTo freely.
type Pager struct {
	Rows   []string
	offset int
}

// ScrollBy shifts the viewport by d rows (negative = up); the lower bound is
// clamped here, the upper bound in Window.
func (p *Pager) ScrollBy(d int) {
	p.offset += d
	if p.offset < 0 {
		p.offset = 0
	}
}

func (p *Pager) ScrollTop()     { p.offset = 0 }
func (p *Pager) ScrollBottom()  { p.offset = len(p.Rows) } // clamped in Window
func (p *Pager) ScrollTo(i int) { p.offset = i }           // clamped in Window

// Offset/Len expose scroll state for callers that render rows themselves (e.g. a
// pager that adds search highlighting and horizontal scrolling).
func (p *Pager) Offset() int { return p.offset }
func (p *Pager) Len() int    { return len(p.Rows) }

// Window clamps the offset for the given viewport height and returns the
// [start,end) range of rows that should be displayed.
func (p *Pager) Window(height int) (start, end int) {
	maxOff := len(p.Rows) - height
	if maxOff < 0 {
		maxOff = 0
	}
	if p.offset > maxOff {
		p.offset = maxOff
	}
	if p.offset < 0 {
		p.offset = 0
	}
	end = p.offset + height
	if end > len(p.Rows) {
		end = len(p.Rows)
	}
	return p.offset, end
}

// Render returns the visible rows for the given viewport, each padded to width.
func (p *Pager) Render(width, height int) []string {
	if height <= 0 {
		return nil
	}
	start, end := p.Window(height)
	out := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		out = append(out, Pad(p.Rows[i], width))
	}
	return out
}
