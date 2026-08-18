package tui

import "strings"

// TextInput is a single-line text editor driven by decoded key events: cursor
// movement, insert/delete, horizontal scrolling, and a masked mode for
// secrets. It only renders to a string — the caller owns layout, focus, and
// what Enter/Tab/Esc mean.
type TextInput struct {
	Masked bool // render every rune as '*' (passwords, passphrases)
	value  []rune
	cursor int // rune index in [0, len(value)]
	off    int // first visible rune, kept so the cursor stays in view
}

// Value returns the current text.
func (t *TextInput) Value() string { return string(t.value) }

// SetValue replaces the text and puts the cursor at the end.
func (t *TextInput) SetValue(s string) {
	t.value = []rune(s)
	t.cursor = len(t.value)
}

// HandleEvent applies one key event and reports whether it was consumed.
// Keys the input has no meaning for (Enter, Tab, Esc, …) are left to the
// caller; edit keys at their boundary (Backspace at the start) are consumed
// as no-ops so they never leak into the surrounding view.
func (t *TextInput) HandleEvent(ev Event) bool {
	if ev.Type != EventKey {
		return false
	}
	switch ev.Key {
	case KeyRune:
		t.value = append(t.value[:t.cursor], append([]rune{ev.Rune}, t.value[t.cursor:]...)...)
		t.cursor++
	case KeyBackspace:
		if t.cursor > 0 {
			t.value = append(t.value[:t.cursor-1], t.value[t.cursor:]...)
			t.cursor--
		}
	case KeyLeft:
		if t.cursor > 0 {
			t.cursor--
		}
	case KeyRight:
		if t.cursor < len(t.value) {
			t.cursor++
		}
	case KeyHome:
		t.cursor = 0
	case KeyEnd:
		t.cursor = len(t.value)
	default:
		return false
	}
	return true
}

// Render returns the input as exactly width visible columns, scrolled so the
// cursor is always in view. When focused the cursor is a reverse-video cell
// (a trailing space when it sits past the last rune); unfocused inputs render
// without one.
func (t *TextInput) Render(width int, focused bool) string {
	if width <= 0 {
		return ""
	}
	cells := make([]rune, 0, len(t.value)+1)
	if t.Masked {
		for range t.value {
			cells = append(cells, '*')
		}
	} else {
		cells = append(cells, t.value...)
	}
	cells = append(cells, ' ') // the end-of-text cursor slot

	// Pull the window back when deletions left slack on the right, then keep
	// the cursor inside it.
	if max := len(cells) - width; t.off > max {
		t.off = max
	}
	if t.off < 0 {
		t.off = 0
	}
	if t.cursor < t.off {
		t.off = t.cursor
	}
	if t.cursor >= t.off+width {
		t.off = t.cursor - width + 1
	}

	end := t.off + width
	if end > len(cells) {
		end = len(cells)
	}
	var b strings.Builder
	for i := t.off; i < end; i++ {
		if focused && i == t.cursor {
			b.WriteString(Reverse(string(cells[i])))
		} else {
			b.WriteRune(cells[i])
		}
	}
	if pad := width - (end - t.off); pad > 0 {
		b.WriteString(strings.Repeat(" ", pad))
	}
	return b.String()
}
