package app

import (
	"strings"

	"github.com/Wigata-Intech/kay/internal/config"
	"github.com/Wigata-Intech/kay/internal/dashboard"
	"github.com/Wigata-Intech/kay/internal/tui"
)

// execView is the `x` screen: a command line over the host's pooled
// connection with the output in a scrollable pager. Enter (re)runs the typed
// command; the run is asynchronous so the console stays responsive. Tab
// moves focus between the command line and the output, so the vim scroll
// keys work in the output without stealing letters from the command.
type execView struct {
	c        *Console
	srv      config.Server
	client   dashboard.Client
	input    tui.TextInput
	pager    tui.Pager
	running  bool
	status   string
	focusOut bool // true: the output pane owns j/k/g/G
}

func (c *Console) execView(srv config.Server, client dashboard.Client) View {
	return &execView{c: c, srv: srv, client: client}
}

func (v *execView) Title() string {
	if v.c.anon() {
		return "exec @ " + v.c.maskAlias(v.srv.Alias)
	}
	return "exec @ " + v.srv.Alias
}

func (v *execView) Hints() string {
	if v.focusOut {
		return "j/k scroll · Enter rerun · Tab command · Esc back"
	}
	return "Enter run · Tab output · Esc back"
}

func (v *execView) Draw(w, h int) []string {
	cw := w
	if cw > 120 {
		cw = 120
	}
	innerH := h - 3
	if innerH < 1 {
		innerH = 1
	}
	prompt := "$ "
	if !v.focusOut {
		prompt = tui.Bold(prompt)
	}
	out := []string{tui.ClampLine(prompt+v.input.Render(cw-4, !v.focusOut), cw)}
	title := "Output"
	switch {
	case v.running:
		title = "Output — running…"
	case v.status != "":
		title = "Output — " + v.status
	}
	out = append(out, tui.Box(title, v.pager.Render(cw-4, innerH), cw, innerH)...)
	return tui.ClampAll(out, w, h)
}

func (v *execView) Handle(ev tui.Event) Action {
	switch {
	case ev.Key == tui.KeyTab:
		v.focusOut = !v.focusOut
	case !v.focusOut && v.input.HandleEvent(ev):
	case v.focusOut && v.scroll(ev):
	case ev.Key == tui.KeyUp:
		v.pager.ScrollBy(-1)
	case ev.Key == tui.KeyDown:
		v.pager.ScrollBy(1)
	case ev.Key == tui.KeyPgUp:
		v.pager.ScrollBy(-10)
	case ev.Key == tui.KeyPgDn:
		v.pager.ScrollBy(10)
	case ev.Key == tui.KeyEnter:
		v.start()
	case ev.Key == tui.KeyEsc:
		return Pop()
	}
	return None()
}

// scroll applies the vim scroll keys while the output pane has focus.
func (v *execView) scroll(ev tui.Event) bool {
	switch ev.Rune {
	case 'j':
		v.pager.ScrollBy(1)
	case 'k':
		v.pager.ScrollBy(-1)
	case 'g':
		v.pager.ScrollTop()
	case 'G':
		v.pager.ScrollBottom()
	default:
		return false
	}
	return true
}

// start launches the typed command on the host unless one is already running.
// The result comes back through the broker so the UI goroutine applies it.
func (v *execView) start() {
	cmd := strings.TrimSpace(v.input.Value())
	if cmd == "" || v.running {
		return
	}
	v.running = true
	v.status = ""
	go func() {
		out, err := v.client.Run(cmd)
		v.c.post(func() {
			v.running = false
			if err != nil {
				v.status = tui.FirstLine(err.Error())
			}
			v.pager.Rows = strings.Split(strings.TrimRight(out, "\n"), "\n")
			v.pager.ScrollTop()
		})
	}()
}
