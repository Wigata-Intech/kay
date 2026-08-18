package app

import (
	"strings"

	"github.com/Wigata-Intech/kay/internal/tui"
)

// noticeView is a dismissable message modal (results of async work) over the
// frame it interrupts.
type noticeView struct {
	overlayBase
	title string
	text  []string
}

func (v *noticeView) Title() string { return strings.ToLower(v.title) }

func (v *noticeView) Draw(w, h int) []string {
	m := tui.Modal{Title: v.title, Text: v.text, Hint: "any key to dismiss"}
	return m.Render(v.base, w, h)
}

func (*noticeView) Handle(tui.Event) Action { return Pop() }
