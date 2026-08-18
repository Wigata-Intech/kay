package tui_test

import (
	"strings"
	"testing"

	"github.com/Wigata-Intech/kay/internal/tui"
)

// key and rn build the two event shapes the input consumes.
func key(k tui.Key) tui.Event { return tui.Event{Type: tui.EventKey, Key: k} }
func rn(r rune) tui.Event     { return tui.Event{Type: tui.EventKey, Key: tui.KeyRune, Rune: r} }

func typeRunes(in *tui.TextInput, s string) {
	for _, r := range s {
		in.HandleEvent(rn(r))
	}
}

func TestTextInputEditing(t *testing.T) {
	tests := []struct {
		name string
		ops  func(in *tui.TextInput)
		want string
	}{
		{"typing appends", func(in *tui.TextInput) {
			typeRunes(in, "abc")
		}, "abc"},
		{"insert at cursor", func(in *tui.TextInput) {
			typeRunes(in, "ac")
			in.HandleEvent(key(tui.KeyLeft))
			in.HandleEvent(rn('b'))
		}, "abc"},
		{"backspace deletes before cursor", func(in *tui.TextInput) {
			typeRunes(in, "abc")
			in.HandleEvent(key(tui.KeyLeft))
			in.HandleEvent(key(tui.KeyBackspace))
		}, "ac"},
		{"home and end jump", func(in *tui.TextInput) {
			typeRunes(in, "bc")
			in.HandleEvent(key(tui.KeyHome))
			in.HandleEvent(rn('a'))
			in.HandleEvent(key(tui.KeyEnd))
			in.HandleEvent(rn('d'))
		}, "abcd"},
		{"right moves over existing text", func(in *tui.TextInput) {
			typeRunes(in, "ab")
			in.HandleEvent(key(tui.KeyHome))
			in.HandleEvent(key(tui.KeyRight))
			in.HandleEvent(rn('x'))
		}, "axb"},
		{"unicode runes", func(in *tui.TextInput) {
			typeRunes(in, "é漢")
		}, "é漢"},
		{"set value puts cursor at end", func(in *tui.TextInput) {
			in.SetValue("ab")
			in.HandleEvent(rn('c'))
		}, "abc"},
		{"backspace at start is a no-op", func(in *tui.TextInput) {
			typeRunes(in, "a")
			in.HandleEvent(key(tui.KeyHome))
			in.HandleEvent(key(tui.KeyBackspace))
		}, "a"},
		{"left at start and right at end are no-ops", func(in *tui.TextInput) {
			in.HandleEvent(key(tui.KeyLeft))
			in.HandleEvent(key(tui.KeyRight))
			typeRunes(in, "a")
			in.HandleEvent(key(tui.KeyRight))
			in.HandleEvent(rn('b'))
		}, "ab"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var in tui.TextInput
			tt.ops(&in)
			if got := in.Value(); got != tt.want {
				t.Errorf("Value() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTextInputConsumption(t *testing.T) {
	tests := []struct {
		name string
		ev   tui.Event
		want bool
	}{
		{"rune consumed", rn('a'), true},
		{"backspace consumed even at the boundary", key(tui.KeyBackspace), true},
		{"enter left to the caller", key(tui.KeyEnter), false},
		{"tab left to the caller", key(tui.KeyTab), false},
		{"esc left to the caller", key(tui.KeyEsc), false},
		{"non-key event ignored", tui.Event{Type: tui.EventQuit}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var in tui.TextInput
			if got := in.HandleEvent(tt.ev); got != tt.want {
				t.Errorf("HandleEvent(%+v) = %v, want %v", tt.ev, got, tt.want)
			}
		})
	}
}

func TestTextInputRender(t *testing.T) {
	old := tui.ColorEnabled
	tui.ColorEnabled = false
	defer func() { tui.ColorEnabled = old }()

	t.Run("zero width renders nothing", func(t *testing.T) {
		var in tui.TextInput
		if got := in.Render(0, true); got != "" {
			t.Errorf("Render(0) = %q, want empty", got)
		}
	})

	t.Run("short value is padded to width", func(t *testing.T) {
		var in tui.TextInput
		in.SetValue("ab")
		if got := in.Render(5, true); got != "ab   " {
			t.Errorf("Render(5) = %q, want %q", got, "ab   ")
		}
	})

	t.Run("masked hides the value", func(t *testing.T) {
		in := tui.TextInput{Masked: true}
		in.SetValue("secret")
		got := in.Render(8, true)
		if strings.Contains(got, "secret") || !strings.Contains(got, "******") {
			t.Errorf("masked Render = %q, want six asterisks and no plaintext", got)
		}
	})

	t.Run("long value scrolls to keep the cursor visible", func(t *testing.T) {
		var in tui.TextInput
		in.SetValue("abcdefghij")
		if got := in.Render(5, true); got != "ghij " {
			t.Errorf("Render(5) at end = %q, want %q", got, "ghij ")
		}
		in.HandleEvent(key(tui.KeyHome))
		if got := in.Render(5, true); got != "abcde" {
			t.Errorf("Render(5) at start = %q, want %q", got, "abcde")
		}
	})

	t.Run("deleting pulls the window back", func(t *testing.T) {
		var in tui.TextInput
		in.SetValue("abcdefghij")
		_ = in.Render(5, true) // scroll the window to the tail
		for i := 0; i < 8; i++ {
			in.HandleEvent(key(tui.KeyBackspace))
		}
		if got := in.Render(5, true); got != "ab   " {
			t.Errorf("Render(5) after deletions = %q, want %q", got, "ab   ")
		}
	})
}

func TestTextInputCursorHighlight(t *testing.T) {
	old := tui.ColorEnabled
	tui.ColorEnabled = true
	defer func() { tui.ColorEnabled = old }()

	var in tui.TextInput
	in.SetValue("ab")
	in.HandleEvent(key(tui.KeyLeft))
	if got := in.Render(5, true); !strings.Contains(got, "\x1b[7mb\x1b[0m") {
		t.Errorf("Render = %q, want the cursor rune in reverse video", got)
	}
	if got := in.Render(5, false); strings.Contains(got, "\x1b[7m") {
		t.Errorf("unfocused Render = %q, want no cursor highlight", got)
	}
}
