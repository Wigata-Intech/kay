package app

import (
	"strings"

	"github.com/Wigata-Intech/kay/internal/tui"
)

// secretView is a masked single-input modal (passphrases, passwords) over
// the frame it interrupts. Constructors must set input.Masked; respond is
// called exactly once — Enter submits the value, Esc cancels — before the
// view pops.
type secretView struct {
	overlayBase
	title   string
	label   string
	input   tui.TextInput
	respond func(value string, ok bool)
}

func (v *secretView) Title() string { return strings.ToLower(v.title) }

func (v *secretView) Draw(w, h int) []string {
	inputW := w - tui.VisibleWidth(v.label) - 12
	if inputW < 8 {
		inputW = 8
	}
	m := tui.Modal{
		Title: v.title,
		Text:  []string{v.label + ": " + v.input.Render(inputW, true)},
		Hint:  "Enter submit · Esc cancel",
	}
	return m.Render(v.base, w, h)
}

func (v *secretView) Handle(ev tui.Event) Action {
	switch {
	case v.input.HandleEvent(ev):
		return None()
	case ev.Key == tui.KeyEnter:
		v.respond(v.input.Value(), true)
		return Pop()
	case ev.Key == tui.KeyEsc:
		v.respond("", false)
		return Pop()
	}
	return None()
}
