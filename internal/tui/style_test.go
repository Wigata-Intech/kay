package tui

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestVisibleWidthIgnoresSGR(t *testing.T) {
	old := ColorEnabled
	ColorEnabled = true
	defer func() { ColorEnabled = old }()

	plain := "hello"
	colored := Red("hello")
	if VisibleWidth(colored) != len(plain) {
		t.Errorf("VisibleWidth(colored) = %d, want %d", VisibleWidth(colored), len(plain))
	}
	if VisibleWidth("ab"+Green("cd")+"ef") != 6 {
		t.Errorf("mixed width = %d, want 6", VisibleWidth("ab"+Green("cd")+"ef"))
	}
}

func TestTruncateAndPad(t *testing.T) {
	if got := Truncate("hello world", 5); got != "hell…" {
		t.Errorf("Truncate = %q", got)
	}
	if got := Truncate("hi", 5); got != "hi" {
		t.Errorf("Truncate noop = %q", got)
	}
	if got := Pad("hi", 5); got != "hi   " || utf8.RuneCountInString(got) != 5 {
		t.Errorf("Pad = %q", got)
	}
	if got := PadLeft("42", 5); got != "   42" {
		t.Errorf("PadLeft = %q", got)
	}
}

func TestPadVisibleIgnoresSGR(t *testing.T) {
	old := ColorEnabled
	ColorEnabled = true
	defer func() { ColorEnabled = old }()

	got := PadVisible(Red("ab"), 5) // visible "ab" -> pad to 5
	if VisibleWidth(got) != 5 {
		t.Errorf("PadVisible width = %d, want 5 (%q)", VisibleWidth(got), got)
	}
	// Too long: clamped to width.
	if w := VisibleWidth(PadVisible("abcdefgh", 4)); w != 4 {
		t.Errorf("PadVisible clamp width = %d, want 4", w)
	}
}

func TestBoxDimensions(t *testing.T) {
	old := ColorEnabled
	ColorEnabled = false
	defer func() { ColorEnabled = old }()

	lines := Box("Processes", []string{"row1", "row2"}, 20, 4)
	if len(lines) != 6 { // top + 4 inner + bottom
		t.Fatalf("box lines = %d, want 6", len(lines))
	}
	for i, l := range lines {
		if VisibleWidth(l) != 20 {
			t.Errorf("box line %d width = %d, want 20 (%q)", i, VisibleWidth(l), l)
		}
	}
	if !strings.Contains(lines[0], "Processes") {
		t.Errorf("title missing in top border: %q", lines[0])
	}
}

func TestClampLineKeepsWidth(t *testing.T) {
	old := ColorEnabled
	ColorEnabled = true
	defer func() { ColorEnabled = old }()

	line := "abc" + Red("defghij")
	clamped := ClampLine(line, 5)
	if VisibleWidth(clamped) != 5 {
		t.Errorf("clamped visible width = %d, want 5 (%q)", VisibleWidth(clamped), clamped)
	}
}
