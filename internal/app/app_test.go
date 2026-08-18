// White-box (package app): these tests drive the unexported stack driver, the
// session/dashboard seams, and the view constructors directly.
package app

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wigata-Intech/kay/internal/config"
	"github.com/Wigata-Intech/kay/internal/dashboard"
	"github.com/Wigata-Intech/kay/internal/fleet"
	"github.com/Wigata-Intech/kay/internal/tui"
)

// waitForQueued blocks until a broker prompt sits in the queue, so a scripted
// Interrupted selection has something to surface.
func waitForQueued(t *testing.T, c *Console) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		n := len(c.queue)
		c.mu.Unlock()
		if n > 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("prompt never queued")
}

// fakeScreen records every frame drawn, at a fixed size.
type fakeScreen struct {
	mu     sync.Mutex
	w, h   int
	frames [][]string
}

func (f *fakeScreen) Size() (int, int) { return f.w, f.h }
func (f *fakeScreen) Draw(lines []string) {
	f.mu.Lock()
	f.frames = append(f.frames, append([]string(nil), lines...))
	f.mu.Unlock()
}

func (f *fakeScreen) contains(s string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, fr := range f.frames {
		for _, l := range fr {
			if strings.Contains(l, s) {
				return true
			}
		}
	}
	return false
}

func (f *fakeScreen) lastFrame() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.frames) == 0 {
		return nil
	}
	return f.frames[len(f.frames)-1]
}

func newScreen() *fakeScreen { return &fakeScreen{w: 80, h: 24} }

// newTestConsole builds a console over a fresh store in a temp dir, with no
// hosts unless the test adds servers.
func newTestConsole(t *testing.T) *Console {
	t.Helper()
	st, err := config.LoadFrom(t.TempDir())
	if err != nil {
		t.Fatalf("load store: %v", err)
	}
	return NewConsole(st, func() []fleet.Host {
		hosts := make([]fleet.Host, len(st.Servers))
		for i := range st.Servers {
			hosts[i] = fleet.Host{Server: st.Servers[i]}
		}
		return hosts
	}, fleet.Options{}, false)
}

// addTestKey registers a key entry (no files) so server forms validate.
func addTestKey(t *testing.T, c *Console, name string) {
	t.Helper()
	if err := c.store.AddKey(config.Key{Name: name}); err != nil {
		t.Fatalf("add key: %v", err)
	}
}

// addTestServer registers a server referencing key "id".
func addTestServer(t *testing.T, c *Console, alias string) {
	t.Helper()
	if err := c.store.AddServer(config.Server{Alias: alias, Host: "10.0.0.1", Port: 22, User: "u", KeyName: "id"}); err != nil {
		t.Fatalf("add server: %v", err)
	}
}

// fakeView is a scriptable View.
type fakeView struct {
	title  string
	lines  []string
	handle func(ev tui.Event) Action
}

func (v *fakeView) Title() string          { return v.title }
func (v *fakeView) Draw(_, _ int) []string { return v.lines }
func (v *fakeView) Handle(ev tui.Event) Action {
	return v.handle(ev)
}

// fakeSession scripts successive RunView outcomes for the base-loop seam.
type fakeSession struct {
	sels    []*fleet.Selection
	errs    []error
	calls   int
	closed  bool
	gotOpts fleet.Options
}

func (s *fakeSession) RunView(_ fleet.Screen, _ <-chan tui.Event, opts fleet.Options) (*fleet.Selection, error) {
	s.gotOpts = opts
	i := s.calls
	s.calls++
	return s.sels[i], s.errs[i]
}

func (s *fakeSession) Close() { s.closed = true }

func setSession(t *testing.T, sessions ...*fakeSession) {
	t.Helper()
	prev := newFleetSession
	n := 0
	newFleetSession = func([]fleet.Host) fleetSession {
		s := sessions[n]
		if n < len(sessions)-1 {
			n++
		}
		return s
	}
	t.Cleanup(func() { newFleetSession = prev })
}

func setRunDashboard(t *testing.T, fn func(dashboard.Screen, <-chan tui.Event, dashboard.Client, config.Server, dashboard.Options) (bool, error)) {
	t.Helper()
	prev := runDashboard
	runDashboard = fn
	t.Cleanup(func() { runDashboard = prev })
}

func send(evs ...tui.Event) chan tui.Event {
	ch := make(chan tui.Event, len(evs))
	for _, ev := range evs {
		ch <- ev
	}
	return ch
}

func key(k tui.Key) tui.Event { return tui.Event{Type: tui.EventKey, Key: k} }
func rn(r rune) tui.Event     { return tui.Event{Type: tui.EventKey, Key: tui.KeyRune, Rune: r} }

// typeInto feeds a string as rune events onto ch.
func typeInto(ch chan tui.Event, s string) {
	for _, r := range s {
		ch <- rn(r)
	}
}

func popOnEsc(ev tui.Event) Action {
	if ev.Key == tui.KeyEsc {
		return Pop()
	}
	return None()
}

func TestDrive(t *testing.T) {
	t.Run("pop empties the stack and returns to the base", func(t *testing.T) {
		c := newTestConsole(t)
		c.Push(&fakeView{title: "a", lines: []string{"body"}, handle: popOnEsc})
		if quit := c.drive(newScreen(), send(key(tui.KeyEsc))); quit {
			t.Error("drive() = quit, want return to base")
		}
		if len(c.stack) != 0 {
			t.Errorf("stack size after pop = %d, want 0", len(c.stack))
		}
	})

	t.Run("push shows the new view and the breadcrumb", func(t *testing.T) {
		b := &fakeView{title: "b", lines: []string{"view b body"}, handle: popOnEsc}
		a := &fakeView{title: "a", lines: []string{"view a body"}, handle: func(ev tui.Event) Action {
			if ev.Rune == 'b' {
				return Push(b)
			}
			return Pop()
		}}
		c := newTestConsole(t)
		c.Push(a)
		scr := newScreen()
		if quit := c.drive(scr, send(rn('b'), key(tui.KeyEsc), rn('x'))); quit {
			t.Error("drive() = quit, want return to base")
		}
		for _, want := range []string{"view a body", "view b body", "kay › a › b"} {
			if !scr.contains(want) {
				t.Errorf("frames missing %q", want)
			}
		}
	})

	t.Run("quit action leaves the console", func(t *testing.T) {
		c := newTestConsole(t)
		c.Push(&fakeView{title: "a", handle: func(tui.Event) Action { return Quit() }})
		if quit := c.drive(newScreen(), send(rn('q'))); !quit {
			t.Error("drive() = base, want quit")
		}
	})

	t.Run("ctrl-c quits without consulting the view", func(t *testing.T) {
		c := newTestConsole(t)
		c.Push(&fakeView{title: "a", handle: func(tui.Event) Action {
			t.Error("view consulted for EventQuit")
			return None()
		}})
		if quit := c.drive(newScreen(), send(tui.Event{Type: tui.EventQuit})); !quit {
			t.Error("drive() = base, want quit")
		}
	})

	t.Run("none redraws and keeps going", func(t *testing.T) {
		c := newTestConsole(t)
		c.Push(&fakeView{title: "a", handle: popOnEsc})
		scr := newScreen()
		if quit := c.drive(scr, send(rn('x'), key(tui.KeyEsc))); quit {
			t.Error("drive() = quit, want return to base")
		}
		if len(scr.frames) != 2 {
			t.Errorf("frames drawn = %d, want 2", len(scr.frames))
		}
	})

	t.Run("closed input ends the drive", func(t *testing.T) {
		c := newTestConsole(t)
		c.Push(&fakeView{title: "a", handle: popOnEsc})
		ch := make(chan tui.Event)
		close(ch)
		if quit := c.drive(newScreen(), ch); quit {
			t.Error("drive() = quit, want plain return")
		}
	})

	t.Run("frame is padded to height with the statusbar last", func(t *testing.T) {
		c := newTestConsole(t)
		c.Push(&fakeView{title: "a", lines: []string{"only line"}, handle: popOnEsc})
		scr := newScreen()
		c.drive(scr, send(key(tui.KeyEsc)))
		frame := scr.lastFrame()
		if len(frame) != scr.h {
			t.Fatalf("frame lines = %d, want %d", len(frame), scr.h)
		}
		if bar := frame[len(frame)-1]; !strings.Contains(bar, "kay › a") || !strings.Contains(bar, "Ctrl-C quit") {
			t.Errorf("statusbar = %q, want breadcrumb and quit hint", bar)
		}
	})

	t.Run("a transient status replaces the breadcrumb until the next key", func(t *testing.T) {
		c := newTestConsole(t)
		c.status = "something failed"
		c.Push(&fakeView{title: "a", handle: popOnEsc})
		scr := newScreen()
		c.drive(scr, send(key(tui.KeyEsc)))
		first := scr.frames[0]
		if !strings.Contains(first[len(first)-1], "something failed") {
			t.Errorf("statusbar = %q, want the transient status", first[len(first)-1])
		}
	})

	t.Run("overlong frames are clipped above the statusbar", func(t *testing.T) {
		long := make([]string, 100)
		for i := range long {
			long[i] = "row"
		}
		c := newTestConsole(t)
		c.Push(&fakeView{title: "a", lines: long, handle: popOnEsc})
		scr := newScreen()
		c.drive(scr, send(key(tui.KeyEsc)))
		if got := len(scr.lastFrame()); got != scr.h {
			t.Errorf("frame lines = %d, want %d", got, scr.h)
		}
	})

	t.Run("tiny screens keep one content line", func(t *testing.T) {
		c := newTestConsole(t)
		c.Push(&fakeView{title: "a", lines: []string{"body"}, handle: popOnEsc})
		scr := &fakeScreen{w: 20, h: 1}
		c.drive(scr, send(key(tui.KeyEsc)))
		if got := len(scr.lastFrame()); got != 2 {
			t.Errorf("frame lines = %d, want 2", got)
		}
	})

	t.Run("modal views overlay the frame they interrupt", func(t *testing.T) {
		c := newTestConsole(t)
		scr := newScreen()
		rec := &frameRecorder{Screen: scr}
		c.rec = rec
		base := &fakeView{title: "base", lines: []string{"the fleet beneath"}, handle: func(ev tui.Event) Action {
			if ev.Rune == 'd' {
				return Push(&confirmView{title: "Delete server", text: []string{"sure?"}, respond: func(bool) {}})
			}
			return popOnEsc(ev)
		}}
		c.Push(base)
		c.drive(rec, send(rn('d'), rn('n'), key(tui.KeyEsc)))
		found := false
		for _, fr := range scr.frames {
			joined := strings.Join(fr, "\n")
			if strings.Contains(joined, "Delete server") && strings.Contains(joined, "the fleet beneath") {
				found = true
			}
		}
		if !found {
			t.Error("modal never drawn over the interrupted frame")
		}
	})

	t.Run("the bar shows the top view's hints", func(t *testing.T) {
		c := newTestConsole(t)
		c.Push(&welcomeView{c: c})
		scr := newScreen()
		c.drive(scr, send(rn('q')))
		frame := scr.lastFrame()
		bar := frame[len(frame)-1]
		if !strings.Contains(bar, "a add server") {
			t.Errorf("bar = %q, want the welcome hints", bar)
		}
	})

	t.Run("a host change clears the stack for a rebuild", func(t *testing.T) {
		c := newTestConsole(t)
		dirtying := &fakeView{title: "a", handle: func(tui.Event) Action {
			c.markHostsDirty()
			return Pop()
		}}
		c.Push(&fakeView{title: "base", handle: popOnEsc})
		c.Push(dirtying)
		if quit := c.drive(newScreen(), send(rn('x'))); quit {
			t.Error("drive() = quit, want rebuild return")
		}
		if len(c.stack) != 0 {
			t.Errorf("stack size = %d, want 0 (cleared for rebuild)", len(c.stack))
		}
		if !c.takeHostsDirty() {
			t.Error("hostsDirty not left for the caller")
		}
	})
}

func TestConsoleRunWelcome(t *testing.T) {
	tests := []struct {
		name string
		ev   tui.Event
	}{
		{"q quits", rn('q')},
		{"esc quits", key(tui.KeyEsc)},
		{"ctrl-c quits", tui.Event{Type: tui.EventQuit}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newTestConsole(t)
			scr := newScreen()
			if err := c.Run(scr, send(tt.ev)); err != nil {
				t.Errorf("Run() error = %v", err)
			}
			if !scr.contains("No servers registered") {
				t.Error("empty console never drew its welcome view")
			}
		})
	}

	t.Run("other keys keep the view open", func(t *testing.T) {
		c := newTestConsole(t)
		if err := c.Run(newScreen(), send(rn('x'), rn('q'))); err != nil {
			t.Errorf("Run() error = %v", err)
		}
	})

	t.Run("a opens the add-server form", func(t *testing.T) {
		c := newTestConsole(t)
		scr := newScreen()
		if err := c.Run(scr, send(rn('a'), key(tui.KeyEsc), rn('q'))); err != nil {
			t.Errorf("Run() error = %v", err)
		}
		if !scr.contains("Add server") {
			t.Error("add-server form never drawn")
		}
	})

	t.Run("K opens the keys view", func(t *testing.T) {
		c := newTestConsole(t)
		scr := newScreen()
		if err := c.Run(scr, send(rn('K'), key(tui.KeyEsc), rn('q'))); err != nil {
			t.Errorf("Run() error = %v", err)
		}
		if !scr.contains("No keys yet") {
			t.Error("keys view never drawn")
		}
	})

	t.Run("wide terminals clamp the content width", func(t *testing.T) {
		for _, l := range (&welcomeView{}).Draw(200, 30) {
			if w := tui.VisibleWidth(l); w > 120 {
				t.Errorf("line wider than the 120-column clamp: %d", w)
			}
		}
	})

	t.Run("closed input ends the run cleanly", func(t *testing.T) {
		c := newTestConsole(t)
		ch := make(chan tui.Event)
		close(ch)
		if err := c.Run(newScreen(), ch); err != nil {
			t.Errorf("Run() error = %v", err)
		}
	})
}

// TestDefaultFleetSession pins the seam's default to the real constructor.
func TestDefaultFleetSession(t *testing.T) {
	s := newFleetSession(nil)
	if _, ok := s.(*fleet.Session); !ok {
		t.Fatalf("default session = %T, want *fleet.Session", s)
	}
	s.Close()
}

// TestConsoleRunAddFirstServer walks the real welcome → form → fleet rebuild
// path: type a server into the form, submit, and land in a fleet session.
func TestConsoleRunAddFirstServer(t *testing.T) {
	c := newTestConsole(t)
	addTestKey(t, c, "id")
	s := &fakeSession{sels: []*fleet.Selection{nil}, errs: []error{nil}}
	setSession(t, s)

	ch := make(chan tui.Event, 64)
	ch <- rn('a')
	typeInto(ch, "web")
	ch <- key(tui.KeyEnter) // -> Host
	typeInto(ch, "10.0.0.1")
	ch <- key(tui.KeyEnter) // -> Port (prefilled 22)
	ch <- key(tui.KeyEnter) // -> User
	typeInto(ch, "ubuntu")
	ch <- key(tui.KeyEnter) // -> Key (prefilled: only key)
	ch <- key(tui.KeyEnter) // submit

	scr := newScreen()
	if err := c.Run(scr, ch); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if s.calls != 1 {
		t.Fatalf("fleet sessions started = %d, want 1 (after the add)", s.calls)
	}
	srv, err := c.store.FindServer("web")
	if err != nil || srv.Host != "10.0.0.1" || srv.Port != 22 || srv.User != "ubuntu" || srv.KeyName != "id" {
		t.Errorf("stored server = %+v, %v; want the typed values", srv, err)
	}
	if got := c.store.Servers; len(got) != 1 {
		t.Errorf("servers stored = %d, want 1", len(got))
	}
	// The store round-tripped to disk too.
	reloaded, err := config.LoadFrom(c.store.Dir())
	if err != nil || len(reloaded.Servers) != 1 {
		t.Errorf("reloaded store servers = %v, %v; want 1", len(reloaded.Servers), err)
	}
}

func TestConsoleRunBase(t *testing.T) {
	t.Run("fleet quit ends the console and closes the session", func(t *testing.T) {
		c := newTestConsole(t)
		addTestKey(t, c, "id")
		addTestServer(t, c, "web")
		s := &fakeSession{sels: []*fleet.Selection{nil}, errs: []error{nil}}
		setSession(t, s)
		if err := c.Run(newScreen(), make(chan tui.Event)); err != nil {
			t.Errorf("Run() error = %v", err)
		}
		if !s.closed {
			t.Error("session not closed")
		}
		if s.gotOpts.ConsoleKeys != consoleKeys || s.gotOpts.Interrupt == nil || s.gotOpts.FooterHints == "" || len(s.gotOpts.ExtraHelp) == 0 {
			t.Errorf("fleet opts = %+v, want console keys, interrupt, hints, and help wired", s.gotOpts)
		}
	})

	t.Run("fleet error is returned", func(t *testing.T) {
		wantErr := errors.New("screen too small")
		c := newTestConsole(t)
		addTestKey(t, c, "id")
		addTestServer(t, c, "web")
		s := &fakeSession{sels: []*fleet.Selection{nil}, errs: []error{wantErr}}
		setSession(t, s)
		if err := c.Run(newScreen(), make(chan tui.Event)); !errors.Is(err, wantErr) {
			t.Errorf("Run() error = %v, want %v", err, wantErr)
		}
	})

	t.Run("console keys open their views over the fleet", func(t *testing.T) {
		c := newTestConsole(t)
		addTestKey(t, c, "id")
		addTestServer(t, c, "web")
		srv := c.store.Servers[0]
		s := &fakeSession{
			sels: []*fleet.Selection{{Key: 'e', Host: fleet.Host{Server: srv}}, nil},
			errs: []error{nil, nil},
		}
		setSession(t, s)
		scr := newScreen()
		if err := c.Run(scr, send(key(tui.KeyEsc), rn('q'))); err != nil {
			t.Errorf("Run() error = %v", err)
		}
		if !scr.contains("Edit server web") {
			t.Error("edit form never drawn for the console key")
		}
		if s.calls != 2 {
			t.Errorf("fleet RunView calls = %d, want 2", s.calls)
		}
	})

	t.Run("a from the fleet opens the add form", func(t *testing.T) {
		c := newTestConsole(t)
		addTestKey(t, c, "id")
		addTestServer(t, c, "web")
		s := &fakeSession{
			sels: []*fleet.Selection{{Key: 'a'}, nil},
			errs: []error{nil, nil},
		}
		setSession(t, s)
		scr := newScreen()
		if err := c.Run(scr, send(key(tui.KeyEsc), rn('q'))); err != nil {
			t.Errorf("Run() error = %v", err)
		}
		if !scr.contains("Add server") {
			t.Error("add form never drawn for the console key")
		}
	})

	t.Run("an interrupt surfaces queued prompts over the fleet", func(t *testing.T) {
		c := newTestConsole(t)
		addTestKey(t, c, "id")
		addTestServer(t, c, "web")
		s := &fakeSession{
			sels: []*fleet.Selection{{Interrupted: true}, nil},
			errs: []error{nil, nil},
		}
		setSession(t, s)
		answered := make(chan bool, 1)
		go func() { answered <- c.AskYesNo("Unknown host", []string{"trust?"}) }()
		waitForQueued(t, c)
		scr := newScreen()
		if err := c.Run(scr, send(rn('y'), rn('q'))); err != nil {
			t.Errorf("Run() error = %v", err)
		}
		if ok := <-answered; !ok {
			t.Error("prompt answer lost")
		}
		if !scr.contains("Unknown host") {
			t.Error("prompt modal never drawn over the fleet")
		}
		if s.calls != 2 {
			t.Errorf("fleet RunView calls = %d, want 2", s.calls)
		}
	})

	t.Run("quit while a fleet prompt is open ends the run", func(t *testing.T) {
		c := newTestConsole(t)
		addTestKey(t, c, "id")
		addTestServer(t, c, "web")
		s := &fakeSession{sels: []*fleet.Selection{{Interrupted: true}}, errs: []error{nil}}
		setSession(t, s)
		answered := make(chan bool, 1)
		go func() { answered <- c.AskYesNo("Unknown host", nil) }()
		waitForQueued(t, c)
		if err := c.Run(newScreen(), send(tui.Event{Type: tui.EventQuit})); err != nil {
			t.Errorf("Run() error = %v", err)
		}
		select {
		case ok := <-answered:
			if ok {
				t.Error("stacked prompt answered true on exit, want fail-closed")
			}
		case <-time.After(2 * time.Second):
			t.Fatal("stacked prompt never released after Run exit")
		}
	})

	t.Run("quit from a console-key view ends the run", func(t *testing.T) {
		c := newTestConsole(t)
		addTestKey(t, c, "id")
		addTestServer(t, c, "web")
		s := &fakeSession{
			sels: []*fleet.Selection{{Key: 'K'}},
			errs: []error{nil},
		}
		setSession(t, s)
		if err := c.Run(newScreen(), send(tui.Event{Type: tui.EventQuit})); err != nil {
			t.Errorf("Run() error = %v", err)
		}
	})

	t.Run("i opens the install view", func(t *testing.T) {
		c := newTestConsole(t)
		addTestKey(t, c, "id")
		addTestServer(t, c, "web")
		s := &fakeSession{
			sels: []*fleet.Selection{{Key: 'i', Host: fleet.Host{Server: c.store.Servers[0]}}, nil},
			errs: []error{nil, nil},
		}
		setSession(t, s)
		scr := newScreen()
		if err := c.Run(scr, send(key(tui.KeyEsc), rn('q'))); err != nil {
			t.Errorf("Run() error = %v", err)
		}
		if !scr.contains("install @ web") {
			t.Error("install view never drawn")
		}
	})

	t.Run("x with a live connection opens the exec view", func(t *testing.T) {
		c := newTestConsole(t)
		addTestKey(t, c, "id")
		addTestServer(t, c, "web")
		s := &fakeSession{
			sels: []*fleet.Selection{{Key: 'x', Host: fleet.Host{Server: c.store.Servers[0]}, Client: &fakeRunner{}}, nil},
			errs: []error{nil, nil},
		}
		setSession(t, s)
		scr := newScreen()
		if err := c.Run(scr, send(key(tui.KeyEsc), rn('q'))); err != nil {
			t.Errorf("Run() error = %v", err)
		}
		if !scr.contains("exec @ web") {
			t.Error("exec view never drawn")
		}
	})

	t.Run("x without a connection explains itself", func(t *testing.T) {
		c := newTestConsole(t)
		addTestKey(t, c, "id")
		addTestServer(t, c, "web")
		s := &fakeSession{
			sels: []*fleet.Selection{{Key: 'x', Host: fleet.Host{Server: c.store.Servers[0]}}, nil},
			errs: []error{nil, nil},
		}
		setSession(t, s)
		scr := newScreen()
		if err := c.Run(scr, nil); err != nil {
			t.Errorf("Run() error = %v", err)
		}
		if scr.contains("exec @ web") {
			t.Error("exec view drawn without a connection")
		}
		if !strings.Contains(s.gotOpts.Status, "not connected") {
			t.Errorf("fleet reseeded with status %q, want the refusal where the user looks", s.gotOpts.Status)
		}
	})

	t.Run("a server change rebuilds the session", func(t *testing.T) {
		c := newTestConsole(t)
		addTestKey(t, c, "id")
		addTestServer(t, c, "web")
		srv := c.store.Servers[0]
		first := &fakeSession{
			sels: []*fleet.Selection{{Key: 'd', Host: fleet.Host{Server: srv}}},
			errs: []error{nil},
		}
		second := &fakeSession{sels: []*fleet.Selection{nil}, errs: []error{nil}}
		setSession(t, first, second)
		// Confirm the delete; the console must close the first session and
		// start a second one over the updated host list.
		if err := c.Run(newScreen(), send(rn('y'), rn('q'))); err != nil {
			t.Errorf("Run() error = %v", err)
		}
		if !first.closed {
			t.Error("first session not closed on rebuild")
		}
		if second.calls != 0 {
			t.Errorf("second session ran %d fleet loops, want 0 (no servers left -> welcome)", second.calls)
		}
		if len(c.store.Servers) != 0 {
			t.Errorf("servers left = %d, want 0", len(c.store.Servers))
		}
	})
}

// suspendScreen is a fakeScreen with the optional suspend/resume extension.
type suspendScreen struct {
	fakeScreen
	onSuspend func()
	onResume  func()
	resumeErr error
}

func (s *suspendScreen) Suspend() {
	if s.onSuspend != nil {
		s.onSuspend()
	}
}

func (s *suspendScreen) Resume() error {
	if s.onResume != nil {
		s.onResume()
	}
	return s.resumeErr
}

func TestConsoleConnect(t *testing.T) {
	// newConnectConsole scripts one 'c' selection, optionally followed by a
	// fleet quit (quitAfter) so Run ends cleanly.
	newConnectConsole := func(t *testing.T, quitAfter bool) (*Console, *fakeSession) {
		t.Helper()
		c := newTestConsole(t)
		addTestKey(t, c, "id")
		addTestServer(t, c, "web")
		sels := []*fleet.Selection{{Key: 'c', Host: fleet.Host{Server: c.store.Servers[0]}}}
		if quitAfter {
			sels = append(sels, nil)
		}
		s := &fakeSession{sels: sels, errs: make([]error, len(sels))}
		setSession(t, s)
		return c, s
	}

	// okConnect returns a Connect whose dial and shell append to log under mu
	// (the dial runs off the UI goroutine).
	okConnect := func(mu *sync.Mutex, log *[]string) func(config.Server) (func() error, func(), error) {
		return func(srv config.Server) (func() error, func(), error) {
			mu.Lock()
			*log = append(*log, "dial:"+srv.Alias)
			mu.Unlock()
			shell := func() error {
				mu.Lock()
				*log = append(*log, "shell")
				mu.Unlock()
				return nil
			}
			return shell, func() {
				mu.Lock()
				*log = append(*log, "cleanup")
				mu.Unlock()
			}, nil
		}
	}

	t.Run("c dials, suspends, runs the shell, resumes, cleans up", func(t *testing.T) {
		c, _ := newConnectConsole(t, true)
		var mu sync.Mutex
		var log []string
		scr := &suspendScreen{fakeScreen: fakeScreen{w: 80, h: 24}}
		c.Connect = func(srv config.Server) (func() error, func(), error) {
			sh, cl, err := okConnect(&mu, &log)(srv)
			return sh, cl, err
		}
		suspendLog := func(s string) { mu.Lock(); log = append(log, s); mu.Unlock() }
		scr.onSuspend = func() { suspendLog("suspend") }
		scr.onResume = func() { suspendLog("resume") }
		if err := c.Run(scr, make(chan tui.Event)); err != nil {
			t.Errorf("Run() error = %v", err)
		}
		mu.Lock()
		got := strings.Join(log, ",")
		mu.Unlock()
		if got != "dial:web,suspend,shell,resume,cleanup" {
			t.Errorf("handoff order = %s, want dial,suspend,shell,resume,cleanup", got)
		}
	})

	t.Run("a dial that asks a question gets its answer", func(t *testing.T) {
		// The critical regression: the dial blocks on a broker prompt; the
		// console must keep pumping so the modal can be answered.
		c, _ := newConnectConsole(t, true)
		answered := make(chan bool, 1)
		c.Connect = func(config.Server) (func() error, func(), error) {
			ok := c.AskYesNo("Unknown host", []string{"trust?"})
			answered <- ok
			if !ok {
				return nil, nil, errors.New("declined")
			}
			return func() error { return nil }, func() {}, nil
		}
		scr := newScreen()
		ch := make(chan tui.Event, 8)
		go func() {
			// Answer only once the modal is on screen — earlier keys are
			// deliberately swallowed as typed-ahead.
			waitDrawn(scr, "Unknown host")
			ch <- rn('y')
		}()
		if err := c.Run(scr, ch); err != nil {
			t.Errorf("Run() error = %v", err)
		}
		select {
		case ok := <-answered:
			if !ok {
				t.Error("prompt answer lost")
			}
		case <-time.After(2 * time.Second):
			t.Fatal("dial prompt never answered — connect deadlock")
		}
		if !scr.contains("Unknown host") {
			t.Error("dial prompt never drawn")
		}
	})

	t.Run("typed-ahead keys during the dial are swallowed", func(t *testing.T) {
		c, _ := newConnectConsole(t, true)
		release := make(chan struct{})
		c.Connect = func(config.Server) (func() error, func(), error) {
			<-release
			return func() error { return nil }, func() {}, nil
		}
		scr := newScreen()
		ch := make(chan tui.Event, 8)
		ch <- rn('d') // would open the delete confirm if replayed
		go func() {
			time.Sleep(50 * time.Millisecond)
			close(release)
		}()
		if err := c.Run(scr, ch); err != nil {
			t.Errorf("Run() error = %v", err)
		}
		if scr.contains("Delete server") {
			t.Error("typed-ahead key replayed into the fleet")
		}
	})

	t.Run("dial errors land on the status", func(t *testing.T) {
		c, s := newConnectConsole(t, true)
		c.Connect = func(config.Server) (func() error, func(), error) {
			return nil, nil, errors.New("dial refused")
		}
		if err := c.Run(newScreen(), make(chan tui.Event)); err != nil {
			t.Errorf("Run() error = %v", err)
		}
		if !strings.Contains(s.gotOpts.Status, "dial refused") {
			t.Errorf("fleet reseeded with status %q, want the dial error rendered", s.gotOpts.Status)
		}
	})

	t.Run("shell errors land on the status", func(t *testing.T) {
		c, s := newConnectConsole(t, true)
		c.Connect = func(config.Server) (func() error, func(), error) {
			return func() error { return errors.New("session torn down") }, func() {}, nil
		}
		if err := c.Run(newScreen(), make(chan tui.Event)); err != nil {
			t.Errorf("Run() error = %v", err)
		}
		if !strings.Contains(s.gotOpts.Status, "session torn down") {
			t.Errorf("fleet reseeded with status %q, want the shell error rendered", s.gotOpts.Status)
		}
	})

	t.Run("resume failure is fatal", func(t *testing.T) {
		c, _ := newConnectConsole(t, false)
		c.Connect = func(config.Server) (func() error, func(), error) {
			return func() error { return nil }, func() {}, nil
		}
		scr := &suspendScreen{fakeScreen: fakeScreen{w: 80, h: 24}, resumeErr: errors.New("no tty")}
		if err := c.Run(scr, make(chan tui.Event)); err == nil {
			t.Error("Run() error = nil, want the resume failure")
		}
	})

	t.Run("quit while a dial prompt is open abandons the dial", func(t *testing.T) {
		c, _ := newConnectConsole(t, false)
		c.Connect = func(config.Server) (func() error, func(), error) {
			if !c.AskYesNo("Unknown host", nil) {
				return nil, nil, errors.New("declined")
			}
			return func() error { return nil }, func() {}, nil
		}
		scr := newScreen()
		ch := make(chan tui.Event, 4)
		go func() {
			waitDrawn(scr, "Unknown host")
			ch <- tui.Event{Type: tui.EventQuit}
		}()
		if err := c.Run(scr, ch); err != nil {
			t.Errorf("Run() error = %v", err)
		}
	})

	t.Run("quit during the dial abandons it and cleans up", func(t *testing.T) {
		c, _ := newConnectConsole(t, false)
		release := make(chan struct{})
		cleaned := make(chan struct{})
		c.Connect = func(config.Server) (func() error, func(), error) {
			<-release
			return func() error { return nil }, func() { close(cleaned) }, nil
		}
		if err := c.Run(newScreen(), send(tui.Event{Type: tui.EventQuit})); err != nil {
			t.Errorf("Run() error = %v", err)
		}
		close(release)
		select {
		case <-cleaned:
		case <-time.After(2 * time.Second):
			t.Error("abandoned dial's connection never cleaned up")
		}
	})

	t.Run("c without a callback is inert", func(t *testing.T) {
		c, _ := newConnectConsole(t, true)
		if err := c.Run(newScreen(), make(chan tui.Event)); err != nil {
			t.Errorf("Run() error = %v", err)
		}
	})
}

func TestConsoleRunMount(t *testing.T) {
	newFleetConsole := func(t *testing.T) *Console {
		t.Helper()
		c := newTestConsole(t)
		addTestKey(t, c, "id")
		addTestServer(t, c, "web")
		return c
	}
	sel := func(c *Console) *fleet.Selection {
		return &fleet.Selection{Host: fleet.Host{Server: c.store.Servers[0]}}
	}

	t.Run("dashboard back returns to the fleet, then quit", func(t *testing.T) {
		c := newFleetConsole(t)
		c.store.SetOverviewPanels([]config.PanelPref{{Name: "cpu"}})
		s := &fakeSession{sels: []*fleet.Selection{sel(c), nil}, errs: []error{nil, nil}}
		setSession(t, s)
		var gotOpts dashboard.Options
		var gotSrv config.Server
		setRunDashboard(t, func(_ dashboard.Screen, _ <-chan tui.Event, _ dashboard.Client, srv config.Server, opts dashboard.Options) (bool, error) {
			gotSrv, gotOpts = srv, opts
			return false, nil // q: back to the fleet
		})
		c.readOnly = true
		if err := c.Run(newScreen(), make(chan tui.Event)); err != nil {
			t.Errorf("Run() error = %v", err)
		}
		if s.calls != 2 {
			t.Errorf("fleet RunView calls = %d, want 2", s.calls)
		}
		if gotSrv.Alias != "web" || !gotOpts.ReadOnly {
			t.Errorf("dashboard got srv %q opts %+v, want the console's settings", gotSrv.Alias, gotOpts)
		}
		if len(gotOpts.Overview) != 1 || gotOpts.Overview[0].Name != "cpu" {
			t.Errorf("dashboard Overview = %+v, want the store's panels", gotOpts.Overview)
		}
		if err := gotOpts.SaveLayout([]config.PanelPref{{Name: "mem"}}); err != nil {
			t.Errorf("SaveLayout error = %v", err)
		}
		if got := c.store.OverviewPanels(); len(got) != 1 || got[0].Name != "mem" {
			t.Errorf("saved layout = %+v, want the update persisted", got)
		}
	})

	t.Run("exit from the dashboard ends the console", func(t *testing.T) {
		c := newFleetConsole(t)
		s := &fakeSession{sels: []*fleet.Selection{sel(c)}, errs: []error{nil}}
		setSession(t, s)
		setRunDashboard(t, func(dashboard.Screen, <-chan tui.Event, dashboard.Client, config.Server, dashboard.Options) (bool, error) {
			return true, nil // Ctrl-C inside the dashboard
		})
		if err := c.Run(newScreen(), make(chan tui.Event)); err != nil {
			t.Errorf("Run() error = %v", err)
		}
		if s.calls != 1 {
			t.Errorf("fleet RunView calls = %d, want 1", s.calls)
		}
	})

	t.Run("dashboard error is returned", func(t *testing.T) {
		c := newFleetConsole(t)
		wantErr := errors.New("draw failed")
		s := &fakeSession{sels: []*fleet.Selection{sel(c)}, errs: []error{nil}}
		setSession(t, s)
		setRunDashboard(t, func(dashboard.Screen, <-chan tui.Event, dashboard.Client, config.Server, dashboard.Options) (bool, error) {
			return false, wantErr
		})
		if err := c.Run(newScreen(), make(chan tui.Event)); !errors.Is(err, wantErr) {
			t.Errorf("Run() error = %v, want %v", err, wantErr)
		}
	})
}
