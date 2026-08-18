package app

import (
	"strings"

	"github.com/Wigata-Intech/kay/internal/tui"
)

// pagerView is a pushed read-only scroll view (public keys, command output).
type pagerView struct {
	title string
	pager tui.Pager
}

func newPagerView(title string, lines []string) *pagerView {
	return &pagerView{title: title, pager: tui.Pager{Rows: lines}}
}

func (v *pagerView) Title() string { return strings.ToLower(v.title) }

func (*pagerView) Hints() string { return "j/k scroll · g/G top/bottom · Esc back" }

func (v *pagerView) Draw(w, h int) []string {
	cw := w
	if cw > 120 {
		cw = 120
	}
	innerH := h - 2
	if innerH < 1 {
		innerH = 1
	}
	out := tui.Box(v.title, v.pager.Render(cw-4, innerH), cw, innerH)
	return tui.ClampAll(out, w, h)
}

func (v *pagerView) Handle(ev tui.Event) Action {
	switch {
	case ev.Rune == 'j', ev.Key == tui.KeyDown:
		v.pager.ScrollBy(1)
	case ev.Rune == 'k', ev.Key == tui.KeyUp:
		v.pager.ScrollBy(-1)
	case ev.Rune == 'g', ev.Key == tui.KeyHome:
		v.pager.ScrollTop()
	case ev.Rune == 'G', ev.Key == tui.KeyEnd:
		v.pager.ScrollBottom()
	case ev.Rune == 'q', ev.Key == tui.KeyEsc:
		return Pop()
	}
	return None()
}
