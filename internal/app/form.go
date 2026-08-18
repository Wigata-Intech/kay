package app

import (
	"strings"

	"github.com/Wigata-Intech/kay/internal/tui"
)

// formView is a pushed screen around a tui.Form: a titled box of labeled
// fields with a status line for whole-form errors. Enter on the last field
// submits, Esc cancels.
type formView struct {
	title  string
	form   tui.Form
	status string
	// submit validates and applies the values. Field-indexed messages land
	// beside their fields, err on the status line; both nil means done and
	// the view pops.
	submit func(values []string) (fieldErrs []string, err error)
}

func (v *formView) Title() string { return strings.ToLower(v.title) }

func (*formView) Hints() string { return "Tab next field · Enter submit · Esc cancel" }

func (v *formView) Draw(w, h int) []string {
	cw := w
	if cw > 120 {
		cw = 120
	}
	body := append([]string{""}, v.form.Render(cw-6)...)
	if v.status != "" {
		body = append(body, "", tui.Red(v.status))
	}
	body = append(body, "")
	out := tui.Box(v.title, body, cw, len(body))
	return tui.ClampAll(out, w, h)
}

func (v *formView) Handle(ev tui.Event) Action {
	if v.form.HandleEvent(ev) {
		return None()
	}
	switch ev.Key {
	case tui.KeyEnter:
		fieldErrs, err := v.submit(v.form.Values())
		for i := range v.form.Fields {
			v.form.Fields[i].Error = ""
			if i < len(fieldErrs) {
				v.form.Fields[i].Error = fieldErrs[i]
			}
		}
		v.status = ""
		if err != nil {
			v.status = err.Error()
		}
		if fieldErrs == nil && err == nil {
			return Pop()
		}
	case tui.KeyEsc:
		return Pop()
	}
	return None()
}
