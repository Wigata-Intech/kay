package app

import (
	"fmt"
	"strings"

	"github.com/Wigata-Intech/kay/internal/config"
	"github.com/Wigata-Intech/kay/internal/keys"
	"github.com/Wigata-Intech/kay/internal/tui"
)

// installView is the `i` screen: shows the command to authorize the server's
// key manually, and `p` pushes it now over a password login (masked prompt,
// asynchronous, result as a modal).
type installView struct {
	c       *Console
	srv     config.Server
	pub     string // authorized_keys line; empty when unreadable
	loadErr string
	pushing bool
}

func (c *Console) installView(srv config.Server) View {
	v := &installView{c: c, srv: srv}
	k, err := c.store.FindKey(srv.KeyName)
	if err == nil {
		var pub string
		if pub, err = keys.ReadPublic(k.PublicPath); err == nil {
			v.pub = strings.TrimSpace(pub)
		}
	}
	if err != nil {
		v.loadErr = err.Error()
	}
	return v
}

func (v *installView) Title() string {
	if v.c.anon() {
		return "install @ " + v.c.maskAlias(v.srv.Alias)
	}
	return "install @ " + v.srv.Alias
}

func (v *installView) Hints() string {
	if v.canPush() {
		return "p push over password · Esc back"
	}
	return "Esc back"
}

func (v *installView) Draw(w, h int) []string {
	cw := w
	if cw > 120 {
		cw = 120
	}
	var body []string
	if v.loadErr != "" {
		body = []string{"", "  " + tui.Red(v.loadErr), ""}
	} else {
		user, host, keyName, pub := v.srv.User, v.srv.Host, v.srv.KeyName, v.pub
		if v.c.anon() {
			user, host, keyName = "user", "demo.host", v.c.maskKey(v.srv.KeyName)
			// The trailing authorized_keys comment is the key name.
			if i := strings.LastIndex(pub, " "); i > 0 {
				pub = pub[:i+1] + keyName
			}
		}
		body = []string{
			"",
			fmt.Sprintf("  To authorize key %q, run this on %s@%s:", keyName, user, host),
			"",
			"    mkdir -p ~/.ssh && chmod 700 ~/.ssh",
			"    printf '%s\\n' 'PUBLIC-KEY' >> ~/.ssh/authorized_keys",
			"    chmod 600 ~/.ssh/authorized_keys",
			"",
			"  PUBLIC-KEY is this single line (shown wrapped):",
			"",
		}
		for _, chunk := range wrapChunks(pub, cw-10) {
			body = append(body, "    "+chunk)
		}
		body = append(body, "", "  Or press "+tui.Bold("p")+" to push it for you.", "")
	}
	if v.pushing {
		body = append(body, "  "+tui.Yellow("pushing…"), "")
	}
	out := tui.Box("Install key", body, cw, len(body))
	return tui.ClampAll(out, w, h)
}

// wrapChunks splits s into width-sized pieces so long single-line data (an
// authorized_keys line) is shown whole instead of silently truncated.
func wrapChunks(s string, width int) []string {
	if width < 1 {
		width = 1
	}
	r := []rune(s)
	out := make([]string, 0, len(r)/width+1)
	for len(r) > width {
		out = append(out, string(r[:width]))
		r = r[width:]
	}
	return append(out, string(r))
}

// canPush reports whether the p action is currently available.
func (v *installView) canPush() bool {
	return v.c.InstallKey != nil && !v.pushing && v.loadErr == ""
}

func (v *installView) Handle(ev tui.Event) Action {
	switch {
	case ev.Rune == 'p':
		return v.push()
	case ev.Rune == 'q', ev.Key == tui.KeyEsc:
		return Pop()
	}
	return None()
}

// push asks for the login password, then installs the key asynchronously; the
// result surfaces as a notice modal through the broker.
func (v *installView) push() Action {
	if !v.canPush() {
		return None()
	}
	return Push(&secretView{
		title: "Install key",
		label: fmt.Sprintf("Password for %s@%s", v.srv.User, v.srv.Host),
		input: tui.TextInput{Masked: true},
		respond: func(password string, ok bool) {
			if !ok {
				return
			}
			v.pushing = true
			go func() {
				err := v.c.InstallKey(v.srv, password)
				v.c.post(func() {
					v.pushing = false
					if err != nil {
						v.c.Push(&noticeView{title: "Install failed", text: []string{tui.FirstLine(err.Error())}})
						return
					}
					v.c.Push(&noticeView{title: "Key installed", text: []string{
						fmt.Sprintf("Key %q installed on %s.", v.srv.KeyName, v.srv.Alias),
						"Verify with c (connect).",
					}})
				})
			}()
		},
	})
}
