// White-box (package app): drives the install view and its push flow.
package app

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/Wigata-Intech/kay/internal/config"
	"github.com/Wigata-Intech/kay/internal/tui"
)

// newInstallConsole seeds a console with a generated key and a server.
func newInstallConsole(t *testing.T) *Console {
	t.Helper()
	c := newTestConsole(t)
	genKey(t, c, "id")
	addTestServer(t, c, "web")
	return c
}

func TestInstallViewDraw(t *testing.T) {
	t.Run("shows the manual command with the public key", func(t *testing.T) {
		c := newInstallConsole(t)
		c.InstallKey = func(config.Server, string) error { return nil }
		v := c.installView(c.store.Servers[0])
		joined := strings.Join(v.Draw(120, 30), "\n")
		for _, want := range []string{"authorized_keys", "ssh-ed25519"} {
			if !strings.Contains(joined, want) {
				t.Errorf("Draw missing %q", want)
			}
		}
		if got := v.Title(); got != "install @ web" {
			t.Errorf("Title() = %q", got)
		}
		if !strings.Contains(v.(*installView).Hints(), "p push") {
			t.Errorf("Hints() = %q", v.(*installView).Hints())
		}
	})

	t.Run("push hint hides when push is unavailable", func(t *testing.T) {
		c := newInstallConsole(t)
		v := c.installView(c.store.Servers[0]) // no InstallKey wired
		if h := v.(*installView).Hints(); strings.Contains(h, "p push") {
			t.Errorf("Hints advertise an inert p: %q", h)
		}
	})

	t.Run("missing key surfaces in the view", func(t *testing.T) {
		c := newInstallConsole(t)
		srv := c.store.Servers[0]
		srv.KeyName = "ghost"
		v := c.installView(srv)
		if joined := strings.Join(v.Draw(120, 30), "\n"); !strings.Contains(joined, "ghost") {
			t.Errorf("Draw missing the key error: %q", joined)
		}
	})

	t.Run("unreadable public key surfaces in the view", func(t *testing.T) {
		c := newInstallConsole(t)
		k, _ := c.store.FindKey("id")
		if err := os.Remove(k.PublicPath); err != nil {
			t.Fatalf("remove pub: %v", err)
		}
		v := c.installView(c.store.Servers[0]).(*installView)
		if v.loadErr == "" {
			t.Error("read failure not recorded")
		}
	})

	t.Run("demo mode masks the server, key name, and comment", func(t *testing.T) {
		c := newInstallConsole(t)
		c.fleetOpts.Anonymize = true
		v := c.installView(c.store.Servers[0])
		joined := strings.Join(v.Draw(120, 40), "\n")
		for _, leak := range []string{"web", `"id"`, "10.0.0.1", "u@"} {
			if strings.Contains(joined, leak) {
				t.Errorf("demo install view leaks %q: %q", leak, joined)
			}
		}
		for _, want := range []string{"user@demo.host", `"key-1"`, "key-1"} {
			if !strings.Contains(joined, want) {
				t.Errorf("demo install view missing %q", want)
			}
		}
		if got := v.Title(); got != "install @ server-1" {
			t.Errorf("Title() = %q, want the masked alias", got)
		}
	})

	t.Run("narrow terminals wrap the whole key, never truncate", func(t *testing.T) {
		c := newInstallConsole(t)
		v := c.installView(c.store.Servers[0]).(*installView)
		var joined strings.Builder
		for _, l := range v.Draw(60, 40) {
			joined.WriteString(strings.TrimRight(strings.TrimLeft(tui.StripSGR(l), "│ "), "│  "))
		}
		flat := strings.ReplaceAll(joined.String(), " ", "")
		if !strings.Contains(flat, strings.ReplaceAll(v.pub, " ", "")) {
			t.Error("wrapped frame does not contain the complete public key")
		}
	})

	t.Run("wrap guards a zero width", func(t *testing.T) {
		if got := wrapChunks("abc", 0); len(got) != 3 {
			t.Errorf("wrapChunks width 0 = %q, want per-rune chunks", got)
		}
	})

	t.Run("wide terminals clamp; other keys wait", func(t *testing.T) {
		c := newInstallConsole(t)
		v := c.installView(c.store.Servers[0])
		for _, l := range v.Draw(200, 30) {
			if w := tui.VisibleWidth(l); w > 120 {
				t.Errorf("line wider than the 120-column clamp: %d", w)
			}
		}
		if act := v.Handle(rn('z')); act.kind != actNone {
			t.Errorf("Handle(z) = %v, want none", act.kind)
		}
	})

	t.Run("pushing marker renders", func(t *testing.T) {
		c := newInstallConsole(t)
		v := c.installView(c.store.Servers[0]).(*installView)
		v.pushing = true
		if joined := strings.Join(v.Draw(120, 30), "\n"); !strings.Contains(joined, "pushing…") {
			t.Errorf("Draw missing the pushing marker: %q", joined)
		}
	})
}

func TestInstallViewPush(t *testing.T) {
	pushWith := func(t *testing.T, c *Console, v View, password string) *secretView {
		t.Helper()
		act := v.Handle(rn('p'))
		if act.kind != actPush {
			t.Fatalf("Handle(p) = %v, want the password prompt", act.kind)
		}
		sv := act.next.(*secretView)
		sv.input.SetValue(password)
		sv.Handle(key(tui.KeyEnter))
		return sv
	}

	t.Run("success pushes a notice through the broker", func(t *testing.T) {
		c := newInstallConsole(t)
		var gotSrv config.Server
		var gotPw string
		c.InstallKey = func(srv config.Server, password string) error {
			gotSrv, gotPw = srv, password
			return nil
		}
		v := c.installView(c.store.Servers[0])
		pushWith(t, c, v, "hunter2")
		finishRun(t, c)
		if gotSrv.Alias != "web" || gotPw != "hunter2" {
			t.Errorf("InstallKey got (%q, %q), want (web, hunter2)", gotSrv.Alias, gotPw)
		}
		if len(c.stack) != 1 {
			t.Fatalf("stack size = %d, want the result notice", len(c.stack))
		}
		n := c.stack[0].(*noticeView)
		if n.title != "Key installed" {
			t.Errorf("notice title = %q, want Key installed", n.title)
		}
		if v.(*installView).pushing {
			t.Error("still marked pushing after the result")
		}
	})

	t.Run("failure pushes the error notice", func(t *testing.T) {
		c := newInstallConsole(t)
		c.InstallKey = func(config.Server, string) error { return errors.New("wrong password") }
		v := c.installView(c.store.Servers[0])
		pushWith(t, c, v, "nope")
		finishRun(t, c)
		n := c.stack[0].(*noticeView)
		if n.title != "Install failed" || !strings.Contains(strings.Join(n.text, " "), "wrong password") {
			t.Errorf("notice = %q %q, want the failure", n.title, n.text)
		}
	})

	t.Run("canceling the password does nothing", func(t *testing.T) {
		c := newInstallConsole(t)
		c.InstallKey = func(config.Server, string) error {
			t.Error("InstallKey called on cancel")
			return nil
		}
		v := c.installView(c.store.Servers[0])
		act := v.Handle(rn('p'))
		act.next.Handle(key(tui.KeyEsc))
		if v.(*installView).pushing {
			t.Error("marked pushing after a cancel")
		}
	})

	t.Run("push is inert without a callback, while pushing, or on a load error", func(t *testing.T) {
		c := newInstallConsole(t)
		v := c.installView(c.store.Servers[0]).(*installView)
		if act := v.Handle(rn('p')); act.kind != actNone { // no InstallKey
			t.Errorf("Handle(p) without callback = %v, want none", act.kind)
		}
		c.InstallKey = func(config.Server, string) error { return nil }
		v.pushing = true
		if act := v.Handle(rn('p')); act.kind != actNone {
			t.Errorf("Handle(p) while pushing = %v, want none", act.kind)
		}
		v.pushing = false
		v.loadErr = "no key"
		if act := v.Handle(rn('p')); act.kind != actNone {
			t.Errorf("Handle(p) with load error = %v, want none", act.kind)
		}
	})

	t.Run("esc and q pop", func(t *testing.T) {
		c := newInstallConsole(t)
		for _, ev := range []tui.Event{key(tui.KeyEsc), rn('q')} {
			if act := c.installView(c.store.Servers[0]).Handle(ev); act.kind != actPop {
				t.Errorf("Handle(%+v) = %v, want pop", ev, act.kind)
			}
		}
	})
}
