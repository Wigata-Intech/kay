// White-box (package app): drives the exec view with a fake runner.
package app

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/Wigata-Intech/kay/internal/config"
	"github.com/Wigata-Intech/kay/internal/tui"
)

// fakeRunner scripts the host command seam.
type fakeRunner struct {
	mu    sync.Mutex
	out   string
	err   error
	calls int
	last  string
}

func (r *fakeRunner) Run(cmd string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	r.last = cmd
	return r.out, r.err
}

func (r *fakeRunner) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

func newExecView(t *testing.T, r *fakeRunner) (*Console, *execView) {
	t.Helper()
	c := newTestConsole(t)
	return c, c.execView(config.Server{Alias: "web"}, r).(*execView)
}

// finishRun waits for the async result and applies it on this goroutine.
func finishRun(t *testing.T, c *Console) {
	t.Helper()
	waitForQueued(t, c)
	c.runPosted()
}

func TestExecView(t *testing.T) {
	t.Run("enter runs the typed command and shows the output", func(t *testing.T) {
		r := &fakeRunner{out: "line1\nline2\n"}
		c, v := newExecView(t, r)
		for _, ru := range "uptime" {
			v.Handle(rn(ru))
		}
		v.Handle(key(tui.KeyEnter))
		if !v.running {
			t.Fatal("run not started")
		}
		finishRun(t, c)
		if v.running {
			t.Error("still marked running after the result")
		}
		if r.last != "uptime" {
			t.Errorf("command run = %q, want uptime", r.last)
		}
		if joined := strings.Join(v.Draw(80, 24), "\n"); !strings.Contains(joined, "line1") || !strings.Contains(joined, "line2") {
			t.Errorf("Draw missing the output: %q", joined)
		}
	})

	t.Run("a failed run lands on the output title", func(t *testing.T) {
		r := &fakeRunner{out: "partial", err: errors.New("exit status 1")}
		c, v := newExecView(t, r)
		v.input.SetValue("false")
		v.Handle(key(tui.KeyEnter))
		finishRun(t, c)
		if joined := strings.Join(v.Draw(80, 24), "\n"); !strings.Contains(joined, "exit status 1") {
			t.Errorf("Draw missing the error: %q", joined)
		}
	})

	t.Run("empty command and double enter do not run", func(t *testing.T) {
		r := &fakeRunner{}
		c, v := newExecView(t, r)
		v.Handle(key(tui.KeyEnter)) // empty: no run
		if v.running || r.count() != 0 {
			t.Fatalf("empty command ran (%d calls)", r.count())
		}
		v.input.SetValue("sleep")
		v.Handle(key(tui.KeyEnter))
		v.Handle(key(tui.KeyEnter)) // already running: ignored
		finishRun(t, c)
		if got := r.count(); got != 1 {
			t.Errorf("runs = %d, want 1", got)
		}
	})

	t.Run("enter after a result reruns", func(t *testing.T) {
		r := &fakeRunner{out: "ok"}
		c, v := newExecView(t, r)
		v.input.SetValue("uptime")
		v.Handle(key(tui.KeyEnter))
		finishRun(t, c)
		v.Handle(key(tui.KeyEnter))
		finishRun(t, c)
		if got := r.count(); got != 2 {
			t.Errorf("runs = %d, want 2", got)
		}
	})

	t.Run("scroll keys move the output window", func(t *testing.T) {
		_, v := newExecView(t, &fakeRunner{})
		v.pager.Rows = make([]string, 100)
		for _, ev := range []tui.Event{key(tui.KeyDown), key(tui.KeyPgDn), key(tui.KeyUp), key(tui.KeyPgUp)} {
			if act := v.Handle(ev); act.kind != actNone {
				t.Fatalf("Handle(%+v) = %v, want none", ev, act.kind)
			}
		}
		v.Handle(key(tui.KeyPgDn))
		if start, _ := v.pager.Window(10); start == 0 {
			t.Error("PgDn did not scroll")
		}
	})

	t.Run("esc pops, draw frames and clamps", func(t *testing.T) {
		_, v := newExecView(t, &fakeRunner{})
		if act := v.Handle(key(tui.KeyEsc)); act.kind != actPop {
			t.Errorf("Handle(Esc) = %v, want pop", act.kind)
		}
		joined := strings.Join(v.Draw(80, 24), "\n")
		for _, want := range []string{"$ ", "Output"} {
			if !strings.Contains(joined, want) {
				t.Errorf("Draw missing %q", want)
			}
		}
		for _, l := range v.Draw(200, 4) {
			if w := tui.VisibleWidth(l); w > 120 {
				t.Errorf("line wider than the 120-column clamp: %d", w)
			}
		}
		if got := v.Title(); got != "exec @ web" {
			t.Errorf("Title() = %q", got)
		}
		if !strings.Contains(v.Hints(), "Enter run") {
			t.Errorf("Hints() = %q", v.Hints())
		}
	})

	t.Run("tab focuses the output for vim scrolling", func(t *testing.T) {
		_, v := newExecView(t, &fakeRunner{})
		v.pager.Rows = make([]string, 100)
		v.Handle(key(tui.KeyTab))
		if !v.focusOut {
			t.Fatal("Tab did not focus the output")
		}
		if !strings.Contains(v.Hints(), "j/k scroll") {
			t.Errorf("Hints() = %q, want the scroll hints", v.Hints())
		}
		for _, ev := range []tui.Event{rn('j'), rn('j'), rn('k'), rn('G'), rn('g')} {
			if act := v.Handle(ev); act.kind != actNone {
				t.Fatalf("Handle(%+v) = %v, want none", ev, act.kind)
			}
		}
		v.Handle(rn('G'))
		if start, _ := v.pager.Window(10); start == 0 {
			t.Error("G did not scroll to the bottom")
		}
		// Letters that are not scroll keys fall through (and must not type).
		v.Handle(rn('z'))
		if v.input.Value() != "" {
			t.Errorf("input = %q, want typing suppressed while output focused", v.input.Value())
		}
		v.Handle(key(tui.KeyTab))
		v.Handle(rn('z'))
		if v.input.Value() != "z" {
			t.Errorf("input = %q, want typing back after Tab", v.input.Value())
		}
	})

	t.Run("draw at narrow width keeps one pager row", func(t *testing.T) {
		_, v := newExecView(t, &fakeRunner{})
		if got := v.Draw(40, 2); len(got) == 0 {
			t.Error("Draw returned nothing at tiny size")
		}
	})

	t.Run("demo mode masks the alias in the title", func(t *testing.T) {
		c := newTestConsole(t)
		c.fleetOpts.Anonymize = true
		addTestKey(t, c, "id")
		addTestServer(t, c, "web")
		v := c.execView(c.store.Servers[0], &fakeRunner{})
		if got := v.Title(); got != "exec @ server-1" {
			t.Errorf("Title() = %q, want the masked alias", got)
		}
		unknown := c.execView(config.Server{Alias: "ghost"}, &fakeRunner{})
		if got := unknown.Title(); got != "exec @ server" {
			t.Errorf("Title() for an unregistered alias = %q, want the generic mask", got)
		}
		if got := c.maskKey("ghost"); got != "key" {
			t.Errorf("maskKey(ghost) = %q, want the generic mask", got)
		}
	})

	t.Run("running state shows in the output title", func(t *testing.T) {
		_, v := newExecView(t, &fakeRunner{})
		v.running = true
		if joined := strings.Join(v.Draw(80, 24), "\n"); !strings.Contains(joined, "running…") {
			t.Errorf("Draw missing the running marker: %q", joined)
		}
	})
}
