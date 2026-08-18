package tui_test

import (
	"strings"
	"testing"

	"github.com/Wigata-Intech/kay/internal/tui"
)

func newForm(labels ...string) *tui.Form {
	f := &tui.Form{}
	for _, l := range labels {
		f.Fields = append(f.Fields, tui.Field{Label: l})
	}
	return f
}

func TestFormFocus(t *testing.T) {
	tests := []struct {
		name string
		evs  []tui.Event
		want int
	}{
		{"starts on the first field", nil, 0},
		{"tab advances", []tui.Event{key(tui.KeyTab)}, 1},
		{"tab wraps", []tui.Event{key(tui.KeyTab), key(tui.KeyTab), key(tui.KeyTab)}, 0},
		{"shift-tab goes back and wraps", []tui.Event{key(tui.KeyShiftTab)}, 2},
		{"enter advances until the last field", []tui.Event{key(tui.KeyEnter), key(tui.KeyEnter)}, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newForm("a", "b", "c")
			for _, ev := range tt.evs {
				f.HandleEvent(ev)
			}
			if got := f.Focus(); got != tt.want {
				t.Errorf("Focus() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestFormConsumption(t *testing.T) {
	t.Run("typing lands in the focused field", func(t *testing.T) {
		f := newForm("alias", "host")
		if !f.HandleEvent(rn('w')) {
			t.Error("rune not consumed")
		}
		f.HandleEvent(key(tui.KeyTab))
		f.HandleEvent(rn('h'))
		if got := f.Values(); got[0] != "w" || got[1] != "h" {
			t.Errorf("Values() = %q, want [w h]", got)
		}
	})

	t.Run("enter on the last field is the submit signal", func(t *testing.T) {
		f := newForm("only")
		if f.HandleEvent(key(tui.KeyEnter)) {
			t.Error("Enter on the last field consumed, want left to the caller")
		}
	})

	t.Run("esc is never consumed", func(t *testing.T) {
		f := newForm("a", "b")
		if f.HandleEvent(key(tui.KeyEsc)) {
			t.Error("Esc consumed, want left to the caller")
		}
	})

	t.Run("empty form consumes nothing", func(t *testing.T) {
		f := &tui.Form{}
		if f.HandleEvent(rn('x')) {
			t.Error("empty form consumed a rune")
		}
	})

	t.Run("non-key events are ignored", func(t *testing.T) {
		f := newForm("a")
		if f.HandleEvent(tui.Event{Type: tui.EventQuit}) {
			t.Error("EventQuit consumed")
		}
	})
}

func TestFormRender(t *testing.T) {
	old := tui.ColorEnabled
	tui.ColorEnabled = false
	defer func() { tui.ColorEnabled = old }()

	t.Run("labels right-aligned to a tight colon gutter", func(t *testing.T) {
		f := newForm("alias", "h")
		f.Fields[0].Input.SetValue("web")
		got := f.Render(30)
		if len(got) != 2 {
			t.Fatalf("Render lines = %d, want 2", len(got))
		}
		if !strings.HasPrefix(got[0], "alias: web") {
			t.Errorf("line 0 = %q, want aligned alias field", got[0])
		}
		if !strings.HasPrefix(got[1], "    h: ") {
			t.Errorf("line 1 = %q, want the label right-aligned", got[1])
		}
	})

	t.Run("validation message renders under its field", func(t *testing.T) {
		f := newForm("port")
		f.Fields[0].Error = "must be a number"
		got := f.Render(40)
		if len(got) != 2 || !strings.Contains(got[1], "must be a number") {
			t.Errorf("Render = %q, want the error under the field", got)
		}
	})

	t.Run("narrow width keeps one input column", func(t *testing.T) {
		f := newForm("a-very-long-label")
		for _, l := range f.Render(10) {
			if w := tui.VisibleWidth(l); w > 10 {
				t.Errorf("line wider than 10: %q", l)
			}
		}
	})
}

func TestFormCursorFollowsFocus(t *testing.T) {
	old := tui.ColorEnabled
	tui.ColorEnabled = true
	defer func() { tui.ColorEnabled = old }()

	f := newForm("a", "b")
	got := f.Render(20)
	if !strings.Contains(got[0], "\x1b[7m") {
		t.Errorf("focused field has no cursor: %q", got[0])
	}
	if strings.Contains(got[1], "\x1b[7m") {
		t.Errorf("unfocused field shows a cursor: %q", got[1])
	}
}
