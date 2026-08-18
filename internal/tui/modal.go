package tui

// Modal is a centered message box drawn over a dimmed copy of the frame
// beneath it — confirms, alerts, results. It only renders; the caller
// interprets keys (y/n, Enter, Esc).
type Modal struct {
	Title string
	Text  []string
	Hint  string // key hint under the text, e.g. "y confirm · n cancel"
}

// Render overlays the modal on base (typically the previous frame), stripping
// the base's colour and dimming it so the box reads as focused. It always
// returns exactly height lines of exactly width visible columns.
func (m *Modal) Render(base []string, width, height int) []string {
	if width < 4 { // narrower than a box frame: just dim the base
		out := make([]string, height)
		for i := range out {
			var b string
			if i < len(base) {
				b = base[i]
			}
			out[i] = Dim(Pad(StripSGR(b), width))
		}
		return out
	}
	content := m.Text
	if m.Hint != "" {
		content = append(append([]string{}, m.Text...), "", Dim(m.Hint))
	}
	bw := VisibleWidth(m.Title) + 8 // boxTop frame + a little air
	for _, l := range content {
		if v := VisibleWidth(l) + 4; v > bw {
			bw = v
		}
	}
	if bw > width {
		bw = width
	}
	box := Box(m.Title, content, bw, len(content))
	top := (height - len(box)) / 2
	if top < 0 {
		top = 0
	}
	left := (width - bw) / 2

	out := make([]string, height)
	for i := range out {
		var b string
		if i < len(base) {
			b = base[i]
		}
		plain := Pad(StripSGR(b), width)
		if i < top || i >= top+len(box) {
			out[i] = Dim(plain)
			continue
		}
		r := []rune(plain)
		line := box[i-top]
		if l := string(r[:left]); l != "" {
			line = Dim(l) + line
		}
		if rt := string(r[left+bw:]); rt != "" {
			line += Dim(rt)
		}
		out[i] = line
	}
	return out
}
