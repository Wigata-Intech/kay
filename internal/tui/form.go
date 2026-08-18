package tui

// Field is one labeled entry in a Form. Error holds the field's current
// validation message; the caller sets it on a failed submit and the next
// Render shows it beside the field.
type Field struct {
	Label string
	Input TextInput
	Error string
}

// Form is an ordered set of labeled inputs with one focused field. Editing is
// plain: typing goes into the focused field, Tab/Shift-Tab (and Enter, until
// the last field) move focus. Submit and cancel stay with the caller: Enter
// on the last field and Esc are not consumed.
type Form struct {
	Fields []Field
	focus  int
}

// Focus returns the index of the focused field.
func (f *Form) Focus() int { return f.focus }

// Values returns every field's current text, in order.
func (f *Form) Values() []string {
	out := make([]string, len(f.Fields))
	for i := range f.Fields {
		out[i] = f.Fields[i].Input.Value()
	}
	return out
}

// HandleEvent applies one key event and reports whether it was consumed.
// Enter advances focus until the last field, where it is left to the caller
// as the submit signal; Esc is never consumed (cancel).
func (f *Form) HandleEvent(ev Event) bool {
	if ev.Type != EventKey || len(f.Fields) == 0 {
		return false
	}
	switch ev.Key {
	case KeyTab:
		f.focus = (f.focus + 1) % len(f.Fields)
	case KeyShiftTab:
		f.focus = (f.focus + len(f.Fields) - 1) % len(f.Fields)
	case KeyEnter:
		if f.focus == len(f.Fields)-1 {
			return false // submit signal for the caller
		}
		f.focus++
	default:
		return f.Fields[f.focus].Input.HandleEvent(ev)
	}
	return true
}

// Render lays the form out one field per line — right-aligned label (colons
// form a tight gutter), input window, then any validation message — each line
// exactly width visible columns. The focused field's label is bold so focus
// reads at a glance, not just from the cursor cell.
func (f *Form) Render(width int) []string {
	labelW := 0
	for i := range f.Fields {
		if l := VisibleWidth(f.Fields[i].Label); l > labelW {
			labelW = l
		}
	}
	inputW := width - labelW - 2 // "<label>: <input>"
	if inputW < 1 {
		inputW = 1
	}
	out := make([]string, 0, 2*len(f.Fields))
	for i := range f.Fields {
		fld := &f.Fields[i]
		label := PadLeft(fld.Label, labelW)
		if i == f.focus {
			label = Bold(label)
		} else {
			label = Dim(label)
		}
		line := label + ": " + fld.Input.Render(inputW, i == f.focus)
		out = append(out, ClampLine(line, width))
		if fld.Error != "" {
			out = append(out, ClampLine(Red(Pad("", labelW+2)+fld.Error), width))
		}
	}
	return out
}
