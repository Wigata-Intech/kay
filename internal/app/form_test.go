// White-box (package app): drives the generic form view directly.
package app

import (
	"errors"
	"strings"
	"testing"

	"github.com/Wigata-Intech/kay/internal/tui"
)

func newFormView(submit func([]string) ([]string, error)) *formView {
	return &formView{
		title: "Test form",
		form: tui.Form{Fields: []tui.Field{
			{Label: "First"}, {Label: "Second"},
		}},
		submit: submit,
	}
}

func TestFormView(t *testing.T) {
	t.Run("submit success pops", func(t *testing.T) {
		var got []string
		v := newFormView(func(vals []string) ([]string, error) { got = vals; return nil, nil })
		for _, r := range "one" {
			v.Handle(rn(r))
		}
		v.Handle(key(tui.KeyEnter)) // -> Second
		for _, r := range "two" {
			v.Handle(rn(r))
		}
		if act := v.Handle(key(tui.KeyEnter)); act.kind != actPop {
			t.Errorf("action = %v, want pop", act.kind)
		}
		if len(got) != 2 || got[0] != "one" || got[1] != "two" {
			t.Errorf("submitted values = %q, want [one two]", got)
		}
	})

	t.Run("field errors keep the form open and render", func(t *testing.T) {
		v := newFormView(func([]string) ([]string, error) { return []string{"required", ""}, nil })
		v.Handle(key(tui.KeyEnter)) // -> Second
		if act := v.Handle(key(tui.KeyEnter)); act.kind != actNone {
			t.Errorf("action = %v, want none", act.kind)
		}
		if joined := strings.Join(v.Draw(60, 20), "\n"); !strings.Contains(joined, "required") {
			t.Errorf("Draw missing the field error: %q", joined)
		}
	})

	t.Run("field errors clear on the next successful submit", func(t *testing.T) {
		fail := true
		v := newFormView(func([]string) ([]string, error) {
			if fail {
				return []string{"bad", ""}, nil
			}
			return nil, nil
		})
		v.Handle(key(tui.KeyEnter))
		v.Handle(key(tui.KeyEnter)) // failed submit
		fail = false
		if act := v.Handle(key(tui.KeyEnter)); act.kind != actPop {
			t.Errorf("action = %v, want pop after the fix", act.kind)
		}
	})

	t.Run("whole-form error lands on the status line", func(t *testing.T) {
		v := newFormView(func([]string) ([]string, error) { return nil, errors.New("store save failed") })
		v.Handle(key(tui.KeyEnter))
		if act := v.Handle(key(tui.KeyEnter)); act.kind != actNone {
			t.Errorf("action = %v, want none", act.kind)
		}
		if joined := strings.Join(v.Draw(60, 20), "\n"); !strings.Contains(joined, "store save failed") {
			t.Errorf("Draw missing the status: %q", joined)
		}
	})

	t.Run("esc cancels without submitting", func(t *testing.T) {
		v := newFormView(func([]string) ([]string, error) {
			t.Error("submit called on Esc")
			return nil, nil
		})
		if act := v.Handle(key(tui.KeyEsc)); act.kind != actPop {
			t.Errorf("action = %v, want pop", act.kind)
		}
	})

	t.Run("unhandled keys wait", func(t *testing.T) {
		v := newFormView(nil)
		if act := v.Handle(key(tui.KeyPgUp)); act.kind != actNone {
			t.Errorf("action = %v, want none", act.kind)
		}
	})

	t.Run("draw shows title, fields, and hint; wide screens clamp", func(t *testing.T) {
		v := newFormView(nil)
		joined := strings.Join(v.Draw(60, 20), "\n")
		for _, want := range []string{"Test form", "First", "Second"} {
			if !strings.Contains(joined, want) {
				t.Errorf("Draw missing %q", want)
			}
		}
		for _, l := range v.Draw(200, 30) {
			if w := tui.VisibleWidth(l); w > 120 {
				t.Errorf("line wider than the 120-column clamp: %d", w)
			}
		}
	})

	t.Run("title and hints name the view", func(t *testing.T) {
		v := newFormView(nil)
		if got := v.Title(); got != "test form" {
			t.Errorf("Title() = %q", got)
		}
		if !strings.Contains(v.Hints(), "Tab next field") {
			t.Errorf("Hints() = %q", v.Hints())
		}
	})
}
