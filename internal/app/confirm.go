package app

import (
	"strings"

	"github.com/Wigata-Intech/kay/internal/tui"
)

// confirmView is a yes/no modal over the frame it interrupts. respond is
// called exactly once — with the user's answer, y/Enter for yes, n/Esc for
// no — before the view pops.
type confirmView struct {
	overlayBase
	title   string
	text    []string
	respond func(bool)
}

func (v *confirmView) Title() string { return strings.ToLower(v.title) }

func (v *confirmView) Draw(w, h int) []string {
	m := tui.Modal{Title: v.title, Text: v.text, Hint: "y yes · n no"}
	return m.Render(v.base, w, h)
}

func (v *confirmView) Handle(ev tui.Event) Action {
	switch {
	case ev.Rune == 'y', ev.Key == tui.KeyEnter:
		v.respond(true)
		return Pop()
	case ev.Rune == 'n', ev.Key == tui.KeyEsc:
		v.respond(false)
		return Pop()
	}
	return None()
}
