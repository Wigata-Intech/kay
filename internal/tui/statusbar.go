package tui

import "strings"

// StatusBar lays out a one-line bar: left-aligned content, right-aligned
// hint, exactly width visible columns. The right side is dropped when the
// two would collide; styling is the caller's.
func StatusBar(left, right string, width int) string {
	gap := width - VisibleWidth(left) - VisibleWidth(right)
	if right == "" || gap < 1 {
		return PadVisible(left, width)
	}
	return left + strings.Repeat(" ", gap) + right
}
