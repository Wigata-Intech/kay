package tui

import (
	"fmt"
	"strings"
)

// Bar renders a "[████····]" usage bar of the given inner width, coloured by
// threshold (see ThreshColor). pct is clamped to 0..100. It is a generic widget:
// callers that need non-threshold colouring compose their own.
func Bar(pct float64, width int) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	if width < 0 {
		width = 0
	}
	filled := min(int(pct/100*float64(width)+0.5), width)
	return "[" + ThreshColor(strings.Repeat("█", filled), pct) +
		Dim(strings.Repeat("·", width-filled)) + "]"
}

// Gauge renders "LABEL [bar] NN%  suffix" — a labelled, threshold-coloured usage
// bar. width is the inner bar width.
func Gauge(label string, pct float64, width int, suffix string) string {
	return fmt.Sprintf("%-4s %s %s  %s",
		label, Bar(pct, width), ThreshColor(fmt.Sprintf("%3.0f%%", pct), pct), suffix)
}

var sparkRunes = []rune("▁▂▃▄▅▆▇█")

// SparkCells renders each value (0..100) as a block character coloured by its
// own threshold — for per-entity levels (e.g. per-core CPU) where each cell's
// colour should reflect that cell, unlike Sparkline which colours the whole run
// by the latest value.
func SparkCells(v []float64) string {
	var b strings.Builder
	for _, x := range v {
		if x < 0 {
			x = 0
		}
		if x > 100 {
			x = 100
		}
		r := sparkRunes[int(x/100*float64(len(sparkRunes)-1)+0.5)]
		b.WriteString(ThreshColor(string(r), x))
	}
	return b.String()
}

// Sparkline renders values (each 0..100) as a compact block-character trend,
// coloured by the latest value. Returns a dim placeholder when empty. Only the
// most recent width values are shown.
func Sparkline(v []float64, width int) string {
	if len(v) == 0 {
		return Dim("(collecting…)")
	}
	if width > 0 && len(v) > width {
		v = v[len(v)-width:]
	}
	var b strings.Builder
	for _, x := range v {
		if x < 0 {
			x = 0
		}
		if x > 100 {
			x = 100
		}
		b.WriteRune(sparkRunes[int(x/100*float64(len(sparkRunes)-1)+0.5)])
	}
	return ThreshColor(b.String(), v[len(v)-1])
}
