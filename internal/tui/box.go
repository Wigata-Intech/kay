package tui

import "strings"

// Box frames content in a titled border. It always returns exactly
// innerHeight+2 lines, each exactly width columns wide (by visible width).
// Content is padded/truncated to innerHeight rows and to the inner width using
// visible-width math, so embedded colour/reverse codes keep their alignment.
func Box(title string, content []string, width, innerHeight int) []string {
	if width < 4 {
		width = 4
	}
	if innerHeight < 0 {
		innerHeight = 0
	}
	inner := width - 4 // "│ " + text + " │"

	out := make([]string, 0, innerHeight+2)
	out = append(out, boxTop(title, width))
	for i := 0; i < innerHeight; i++ {
		var line string
		if i < len(content) {
			line = content[i]
		}
		out = append(out, Dim("│ ")+PadVisible(line, inner)+Dim(" │"))
	}
	out = append(out, Dim("└"+strings.Repeat("─", width-2)+"┘"))
	return out
}

// Join places two blocks side by side: the left block is padded to its widest
// line, then a gap, then the right block. Handles differing line counts and
// preserves any styling in the left block (visible-width padding).
func Join(left, right []string, gap int) []string {
	lw := 0
	for _, l := range left {
		if v := VisibleWidth(l); v > lw {
			lw = v
		}
	}
	n := len(left)
	if len(right) > n {
		n = len(right)
	}
	sep := strings.Repeat(" ", gap)
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		var l, r string
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		out = append(out, PadVisible(l, lw)+sep+r)
	}
	return out
}

func boxTop(title string, width int) string {
	// ┌─ title ─...─┐   ("┌─ " = 3, " " before fill = 1, "┐" = 1)
	t := Truncate(title, width-6)
	used := 5 + len([]rune(t))
	fill := width - used
	if fill < 0 {
		fill = 0
	}
	return Dim("┌─ ") + Cyan(t) + Dim(" "+strings.Repeat("─", fill)+"┐")
}
