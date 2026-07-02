package tui

import "strings"

// Columns places blocks side by side, each padded to its own widest visible line,
// separated by gap spaces. It generalises Join to any number of blocks and is the
// building block for responsive multi-column layouts. Blocks may differ in line
// count; the last column is not right-padded.
func Columns(blocks [][]string, gap int) []string {
	if len(blocks) == 0 {
		return nil
	}
	widths := make([]int, len(blocks))
	maxLines := 0
	for i, b := range blocks {
		for _, l := range b {
			if v := VisibleWidth(l); v > widths[i] {
				widths[i] = v
			}
		}
		if len(b) > maxLines {
			maxLines = len(b)
		}
	}
	sep := strings.Repeat(" ", gap)
	out := make([]string, 0, maxLines)
	for row := 0; row < maxLines; row++ {
		var sb strings.Builder
		for i, b := range blocks {
			if i > 0 {
				sb.WriteString(sep)
			}
			var cell string
			if row < len(b) {
				cell = b[row]
			}
			if i < len(blocks)-1 {
				sb.WriteString(PadVisible(cell, widths[i]))
			} else {
				sb.WriteString(cell)
			}
		}
		out = append(out, sb.String())
	}
	return out
}

// ColumnCount picks how many columns of content to lay out for a terminal of the
// given width, given the minimum usable width of one column. It never returns
// more than max, and always at least 1. This centralises the responsive
// breakpoint decision so views share one rule.
func ColumnCount(width, minColWidth, gap, max int) int {
	if width < minColWidth || minColWidth <= 0 {
		return 1
	}
	// n columns need n*minColWidth + (n-1)*gap columns of space.
	n := 1
	for n < max {
		need := (n+1)*minColWidth + n*gap
		if need > width {
			break
		}
		n++
	}
	return n
}
