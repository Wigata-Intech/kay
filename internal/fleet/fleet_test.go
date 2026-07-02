// White-box: exercises fleet.go directly — the pure helpers (rows, render,
// statCell, humanDurShort), the collection helpers, and the fleetView event loop
// (with an injected fake screen + channels), plus the unexported hostState type
// and collector seam.
package fleet

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wigata-Intech/kay/internal/config"
	"github.com/Wigata-Intech/kay/internal/metrics"
	"github.com/Wigata-Intech/kay/internal/sshx"
	"github.com/Wigata-Intech/kay/internal/tui"
)

// fakeConn is a scriptable collector: it reports a fixed connection state/error
// and returns canned output from Run, so the fleet can be exercised without a
// real SSH connection. ran, when set, is signalled on each Run.
type fakeConn struct {
	state  sshx.ConnState
	err    error
	out    string
	runErr error
	ran    chan struct{}

	mu   sync.Mutex
	runs int
}

func (f *fakeConn) Run(string) (string, error) {
	f.mu.Lock()
	f.runs++
	f.mu.Unlock()
	if f.ran != nil {
		select {
		case f.ran <- struct{}{}:
		default:
		}
	}
	return f.out, f.runErr
}
func (f *fakeConn) State() sshx.ConnState { return f.state }
func (f *fakeConn) Err() error            { return f.err }

// noColor disables tui coloring for the duration of a test so string
// assertions can match plain text, restoring the prior value afterwards.
func noColor(t *testing.T) {
	t.Helper()
	old := tui.ColorEnabled
	tui.ColorEnabled = false
	t.Cleanup(func() { tui.ColorEnabled = old })
}

// sampleHosts / sampleStates build a small fleet fixture: one online host, one
// errored host, and one not-yet-connected host.
func sampleHosts() []Host {
	return []Host{
		{Server: config.Server{Alias: "web", Host: "10.0.0.1"}},
		{Server: config.Server{Alias: "db", Host: "10.0.0.2"}},
		{Server: config.Server{Alias: "cache", Host: "10.0.0.3"}},
	}
}

func sampleStates() []hostState {
	snap := metrics.Snapshot{
		CPUPercent:     42,
		MemUsedPercent: 71,
		Load1:          1.25,
		UptimeSec:      3 * 24 * 3600, // 3 days
		Disks: []metrics.Disk{
			{Mount: "/", TotalBytes: 100, UsedBytes: 95},
		},
	}
	return []hostState{
		{snap: snap, ok: true},
		{err: errors.New("dial tcp: connection refused\nsecond line")},
		{ok: false},
	}
}

func TestHumanDurShort(t *testing.T) {
	tests := []struct {
		name string
		sec  float64
		want string
	}{
		{name: "days rounds down", sec: 3 * 24 * 3600, want: "3d"},
		{name: "just over a day", sec: 25 * 3600, want: "1d"},
		{name: "hours only", sec: 5 * 3600, want: "5h"},
		{name: "zero", sec: 0, want: "0h"},
		{name: "under an hour", sec: 600, want: "0h"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := humanDurShort(tt.sec); got != tt.want {
				t.Errorf("humanDurShort(%v) = %q, want %q", tt.sec, got, tt.want)
			}
		})
	}
}

func TestStatCell(t *testing.T) {
	noColor(t)
	tests := []struct {
		name  string
		label string
		pct   float64
		want  string
	}{
		{name: "cpu formatted", label: "cpu", pct: 42, want: "cpu  42%"},
		{name: "mem rounds", label: "mem", pct: 71.4, want: "mem  71%"},
		{name: "full width", label: "dsk", pct: 100, want: "dsk 100%"},
		{name: "zero", label: "cpu", pct: 0, want: "cpu   0%"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := statCell(tt.label, tt.pct); got != tt.want {
				t.Errorf("statCell(%q, %v) = %q, want %q", tt.label, tt.pct, got, tt.want)
			}
		})
	}
}

func TestRows(t *testing.T) {
	noColor(t)
	hosts := sampleHosts()
	states := sampleStates()

	t.Run("plain online/error/connecting", func(t *testing.T) {
		got := rows(hosts, states, false)
		if len(got) != len(hosts) {
			t.Fatalf("rows len = %d, want %d", len(got), len(hosts))
		}
		// Online host: alias, host, stat cells, load, uptime.
		if !strings.Contains(got[0], "web") || !strings.Contains(got[0], "10.0.0.1") {
			t.Errorf("online row missing alias/host: %q", got[0])
		}
		for _, want := range []string{"cpu  42%", "mem  71%", "dsk  95%", "1.25", "3d"} {
			if !strings.Contains(got[0], want) {
				t.Errorf("online row missing %q: %q", want, got[0])
			}
		}
		// Errored host: offline + first line of error only.
		if !strings.Contains(got[1], "offline: dial tcp: connection refused") {
			t.Errorf("error row unexpected: %q", got[1])
		}
		if strings.Contains(got[1], "second line") {
			t.Errorf("error row leaked second line: %q", got[1])
		}
		// Not-yet-connected host.
		if !strings.Contains(got[2], "connecting") {
			t.Errorf("connecting row unexpected: %q", got[2])
		}
	})

	t.Run("anonymized masks alias and host", func(t *testing.T) {
		got := rows(hosts, states, true)
		if !strings.Contains(got[0], "server-1") || !strings.Contains(got[0], "demo.host") {
			t.Errorf("anon row not masked: %q", got[0])
		}
		if strings.Contains(got[0], "web") || strings.Contains(got[0], "10.0.0.1") {
			t.Errorf("anon row leaked real alias/host: %q", got[0])
		}
	})
}

func TestHandleFleetKey(t *testing.T) {
	tests := []struct {
		name         string
		ev           tui.Event
		startSel     int
		startRows    int
		startIval    time.Duration
		wantQuit     bool
		wantSel      int
		wantInterval time.Duration
		wantTrigger  bool
	}{
		{name: "q quits", ev: tui.Event{Rune: 'q'}, startIval: 5 * time.Second, wantQuit: true, wantInterval: 5 * time.Second},
		{name: "quit event quits", ev: tui.Event{Type: tui.EventQuit}, startIval: 5 * time.Second, wantQuit: true, wantInterval: 5 * time.Second},
		{name: "down moves selection", ev: tui.Event{Type: tui.EventKey, Key: tui.KeyDown}, startSel: 0, startRows: 3, startIval: 5 * time.Second, wantSel: 1, wantInterval: 5 * time.Second},
		{name: "j moves selection", ev: tui.Event{Rune: 'j'}, startSel: 0, startRows: 3, startIval: 5 * time.Second, wantSel: 1, wantInterval: 5 * time.Second},
		{name: "up moves selection", ev: tui.Event{Type: tui.EventKey, Key: tui.KeyUp}, startSel: 2, startRows: 3, startIval: 5 * time.Second, wantSel: 1, wantInterval: 5 * time.Second},
		{name: "G jumps to bottom", ev: tui.Event{Rune: 'G'}, startSel: 0, startRows: 3, startIval: 5 * time.Second, wantSel: 2, wantInterval: 5 * time.Second},
		{name: "g jumps to top", ev: tui.Event{Rune: 'g'}, startSel: 2, startRows: 3, startIval: 5 * time.Second, wantSel: 0, wantInterval: 5 * time.Second},
		{name: "r triggers refresh", ev: tui.Event{Rune: 'r'}, startIval: 5 * time.Second, wantInterval: 5 * time.Second, wantTrigger: true},
		{name: "plus grows interval", ev: tui.Event{Rune: '+'}, startIval: 5 * time.Second, wantInterval: 6 * time.Second},
		{name: "minus shrinks interval", ev: tui.Event{Rune: '-'}, startIval: 5 * time.Second, wantInterval: 4 * time.Second},
		{name: "minus clamped at one second", ev: tui.Event{Rune: '-'}, startIval: time.Second, wantInterval: time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			list := tui.List{Selected: tt.startSel}
			if tt.startRows > 0 {
				rowsFixture := make([]string, tt.startRows)
				list.SetRows(rowsFixture)
				list.Selected = tt.startSel
			}
			interval := tt.startIval
			ticker := time.NewTicker(tt.startIval)
			defer ticker.Stop()
			triggered := false
			trigger := func() { triggered = true }

			quit := handleFleetKey(tt.ev, &list, &interval, ticker, trigger)
			if quit != tt.wantQuit {
				t.Errorf("quit = %v, want %v", quit, tt.wantQuit)
			}
			if tt.startRows > 0 && list.Selected != tt.wantSel {
				t.Errorf("selected = %d, want %d", list.Selected, tt.wantSel)
			}
			if interval != tt.wantInterval {
				t.Errorf("interval = %v, want %v", interval, tt.wantInterval)
			}
			if triggered != tt.wantTrigger {
				t.Errorf("triggered = %v, want %v", triggered, tt.wantTrigger)
			}
		})
	}
}

func TestCollect(t *testing.T) {
	tests := []struct {
		name    string
		conn    *fakeConn
		wantOK  bool
		wantErr bool
	}{
		{name: "ready collects metrics", conn: &fakeConn{state: sshx.StateReady}, wantOK: true},
		{name: "ready but run fails", conn: &fakeConn{state: sshx.StateReady, runErr: errors.New("boom")}, wantErr: true},
		{name: "broken reports its error", conn: &fakeConn{state: sshx.StateBroken, err: errors.New("dial refused")}, wantErr: true},
		{name: "connecting has neither data nor error", conn: &fakeConn{state: sshx.StateConnecting}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := collect(tt.conn)
			if st.ok != tt.wantOK {
				t.Errorf("ok = %v, want %v (%+v)", st.ok, tt.wantOK, st)
			}
			if (st.err != nil) != tt.wantErr {
				t.Errorf("err = %v, want error=%v", st.err, tt.wantErr)
			}
		})
	}
}

func TestCollectAll(t *testing.T) {
	conns := []collector{
		&fakeConn{state: sshx.StateBroken, err: errors.New("no")},
		&fakeConn{state: sshx.StateBroken, err: errors.New("no")},
	}
	states := collectAll(conns)
	if len(states) != len(conns) {
		t.Fatalf("collectAll len = %d, want %d", len(states), len(conns))
	}
	for i, st := range states {
		if st.err == nil {
			t.Errorf("state %d = %+v, want an error", i, st)
		}
	}
}

func TestRender(t *testing.T) {
	noColor(t)
	hosts := sampleHosts()
	states := sampleStates()

	t.Run("fits within bounds and shows cells", func(t *testing.T) {
		var list tui.List
		const w, h = 100, 20
		out := render(hosts, states, &list, 5*time.Second, "", w, h, false)
		if len(out) > h {
			t.Fatalf("render produced %d lines, exceeds height %d", len(out), h)
		}
		for i, line := range out {
			if got := len([]rune(line)); got > w {
				t.Errorf("line %d width %d exceeds %d: %q", i, got, w, line)
			}
		}
		joined := strings.Join(out, "\n")
		for _, want := range []string{"kay fleet", "Fleet — 1/3 online", "ALIAS", "web", "connecting"} {
			if !strings.Contains(joined, want) {
				t.Errorf("render output missing %q", want)
			}
		}
	})

	t.Run("too small returns a hint", func(t *testing.T) {
		var list tui.List
		out := render(hosts, states, &list, time.Second, "", 20, 4, false)
		joined := strings.Join(out, "\n")
		if !strings.Contains(joined, "terminal too small") {
			t.Errorf("expected too-small hint, got %q", joined)
		}
	})

	t.Run("wide terminal clamps content width", func(t *testing.T) {
		var list tui.List
		const w, h = 200, 24
		out := render(hosts, states, &list, time.Second, "", w, h, false)
		if len(out) > h {
			t.Fatalf("render produced %d lines, exceeds height %d", len(out), h)
		}
		// Content is capped at 120 columns even on a 200-wide terminal.
		for i, line := range out {
			if got := len([]rune(line)); got > 120 {
				t.Errorf("line %d width %d exceeds clamp 120: %q", i, got, line)
			}
		}
	})

	t.Run("a status replaces the key hint", func(t *testing.T) {
		var list tui.List
		out := render(hosts, states, &list, time.Second, "host is still connecting", 100, 20, false)
		joined := strings.Join(out, "\n")
		if !strings.Contains(joined, "host is still connecting") {
			t.Errorf("status message not shown: %q", joined)
		}
		if strings.Contains(joined, "j/k select") {
			t.Errorf("key hint should be replaced by the status: %q", joined)
		}
	})
}

// fakeScreen counts draws; the counter is mutex-guarded because Draw runs in the
// loop goroutine while the test reads the count.
type fakeScreen struct {
	w, h int
	mu   sync.Mutex
	n    int
}

func (s *fakeScreen) Size() (int, int) { return s.w, s.h }
func (s *fakeScreen) Draw([]string)    { s.mu.Lock(); s.n++; s.mu.Unlock() }
func (s *fakeScreen) draws() int       { s.mu.Lock(); defer s.mu.Unlock(); return s.n }

// newFleetView builds a one-host view backed by the given connection.
func newFleetView(c collector) *fleetView {
	return &fleetView{
		hosts:    []Host{{Server: config.Server{Alias: "a", Host: "10.0.0.1"}}},
		conns:    []collector{c},
		states:   make([]hostState, 1),
		interval: time.Second,
		results:  make(chan hostUpdate, 1),
	}
}

func startFleetLoop(v *fleetView, scr screen) (chan tui.Event, chan time.Time, *time.Ticker, <-chan *Selection) {
	ev := make(chan tui.Event)
	tick := make(chan time.Time)
	ticker := time.NewTicker(time.Hour) // never fires in tests; tick drives ticks
	done := make(chan *Selection, 1)
	go func() { done <- v.loop(scr, ev, tick, ticker) }()
	return ev, tick, ticker, done
}

func TestFleetLoopQuit(t *testing.T) {
	tests := []struct {
		name string
		ev   tui.Event
	}{
		{name: "q key quits", ev: tui.Event{Rune: 'q'}},
		{name: "quit event quits", ev: tui.Event{Type: tui.EventQuit}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := newFleetView(&fakeConn{state: sshx.StateConnecting})
			scr := &fakeScreen{w: 100, h: 30}
			ev, _, ticker, done := startFleetLoop(v, scr)
			defer ticker.Stop()

			ev <- tt.ev
			if sel := <-done; sel != nil {
				t.Errorf("loop returned %v, want nil (quit, not drill)", sel)
			}
			if scr.draws() == 0 {
				t.Error("loop should draw at least once before quitting")
			}
		})
	}
}

func TestFleetLoopEnterDrillsIn(t *testing.T) {
	// A ready connection is enterable, so Enter drills into it.
	v := newFleetView(&fakeConn{state: sshx.StateReady})
	scr := &fakeScreen{w: 100, h: 30}
	ev, _, ticker, done := startFleetLoop(v, scr)
	defer ticker.Stop()

	ev <- tui.Event{Key: tui.KeyEnter}
	sel := <-done
	if sel == nil {
		t.Fatal("Enter on a ready host should return a selection to drill into")
	}
	if sel.Host.Server.Alias != "a" {
		t.Errorf("drilled host alias = %q, want a", sel.Host.Server.Alias)
	}
	if sel.Client == nil {
		t.Error("selection should carry the live connection for reuse")
	}
}

func TestFleetLoopEnterNotReady(t *testing.T) {
	v := newFleetView(&fakeConn{state: sshx.StateConnecting})
	scr := &fakeScreen{w: 100, h: 30}
	ev, _, ticker, done := startFleetLoop(v, scr)
	defer ticker.Stop()

	// Still connecting: Enter must not drill.
	ev <- tui.Event{Key: tui.KeyEnter}
	ev <- tui.Event{Rune: 'q'}
	if sel := <-done; sel != nil {
		t.Errorf("Enter on a not-ready host should not drill, got %v", sel)
	}
}

func TestFleetLoopResultRedraws(t *testing.T) {
	v := newFleetView(&fakeConn{state: sshx.StateReady})
	v.results = make(chan hostUpdate) // unbuffered: deterministic hand-off
	scr := &fakeScreen{w: 100, h: 30}
	ev, _, ticker, done := startFleetLoop(v, scr)
	defer ticker.Stop()

	v.results <- hostUpdate{index: 0, state: hostState{ok: true}}
	ev <- tui.Event{Rune: 'q'}
	<-done
	if !v.states[0].ok {
		t.Error("state[0] should be replaced by the streamed result")
	}
	if got := scr.draws(); got < 2 {
		t.Errorf("draws = %d, want >= 2 (initial + result)", got)
	}
}

func TestFleetLoopIntervalChange(t *testing.T) {
	v := newFleetView(&fakeConn{state: sshx.StateConnecting})
	scr := &fakeScreen{w: 100, h: 30}
	ev, _, ticker, done := startFleetLoop(v, scr)
	defer ticker.Stop()

	start := v.interval
	ev <- tui.Event{Rune: '+'} // input is live immediately (no global gate)
	ev <- tui.Event{Rune: 'q'}
	<-done

	if v.interval != start+time.Second {
		t.Errorf("interval = %v, want %v", v.interval, start+time.Second)
	}
}

func TestEnterHost(t *testing.T) {
	c := &fakeConn{state: sshx.StateConnecting}
	v := &fleetView{
		hosts: []Host{{Server: config.Server{Alias: "a"}}},
		conns: []collector{c},
	}

	// Still connecting: no drill, "connecting" status.
	if sel := v.enterHost(); sel != nil {
		t.Error("connecting host should not drill in")
	}
	if !strings.Contains(v.status, "connecting") {
		t.Errorf("status = %q, want a connecting message", v.status)
	}

	// Offline (error): no drill, "offline" status.
	c.state, c.err = sshx.StateBroken, errors.New("dial refused")
	if sel := v.enterHost(); sel != nil {
		t.Error("offline host should not drill in")
	}
	if !strings.Contains(v.status, "offline") {
		t.Errorf("status = %q, want an offline message", v.status)
	}

	// Ready: drills in and carries the live connection.
	c.state = sshx.StateReady
	sel := v.enterHost()
	if sel == nil || sel.Host.Server.Alias != "a" {
		t.Errorf("ready host should drill in, got %v", sel)
	}

	// Empty view: selection is out of range, so nothing happens.
	if sel := (&fleetView{}).enterHost(); sel != nil {
		t.Errorf("empty view should not drill in, got %v", sel)
	}
}

func TestTrigger(t *testing.T) {
	mk := func() collector { return &fakeConn{state: sshx.StateBroken, err: errors.New("no")} }
	v := &fleetView{
		hosts:   []Host{{Server: config.Server{Alias: "a"}}, {Server: config.Server{Alias: "b"}}},
		conns:   []collector{mk(), mk()},
		states:  make([]hostState, 2),
		results: make(chan hostUpdate, 2),
	}

	v.trigger()
	if v.inflight != 2 {
		t.Fatalf("inflight = %d, want 2 (one per host)", v.inflight)
	}
	v.trigger() // a second round while one is in flight must be skipped

	// Exactly one update arrives per host.
	seen := map[int]bool{}
	for range 2 {
		select {
		case u := <-v.results:
			seen[u.index] = true
			if u.state.err == nil {
				t.Errorf("host %d update = %+v, want an error state", u.index, u.state)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for a host update")
		}
	}
	if !seen[0] || !seen[1] {
		t.Errorf("missing per-host updates: %v", seen)
	}
	// No third update: the second trigger was skipped while a round was in flight.
	select {
	case u := <-v.results:
		t.Errorf("unexpected extra update for host %d (second trigger should skip)", u.index)
	default:
	}
}

func TestNewSessionWiresConns(t *testing.T) {
	hosts := []Host{
		{Server: config.Server{Alias: "a"}, Dial: func() (*sshx.Client, error) { return nil, errors.New("no") }},
		{Server: config.Server{Alias: "b"}, Dial: func() (*sshx.Client, error) { return nil, errors.New("no") }},
	}
	s := NewSession(hosts)
	defer s.Close()

	if len(s.conns) != len(hosts) {
		t.Fatalf("session conns = %d, want %d (one per host)", len(s.conns), len(hosts))
	}
	for i, c := range s.conns {
		if c == nil {
			t.Errorf("conn %d is nil, want a managed connection", i)
		}
	}
}

func TestRenderFleetHelp(t *testing.T) {
	noColor(t)
	out := strings.Join(renderFleetHelp(100, 30), "\n")
	for _, want := range []string{"Keybindings", "open host dashboard", "quit"} {
		if !strings.Contains(out, want) {
			t.Errorf("fleet help missing %q:\n%s", want, out)
		}
	}
}

func TestFleetHelpToggle(t *testing.T) {
	v := newFleetView(&fakeConn{state: sshx.StateConnecting})
	scr := &fakeScreen{w: 100, h: 30}
	ev, _, ticker, done := startFleetLoop(v, scr)
	defer ticker.Stop()

	ev <- tui.Event{Rune: '?'} // open help
	ev <- tui.Event{Rune: 'x'} // any key closes it
	ev <- tui.Event{Rune: 'q'} // quit
	<-done
	if v.help {
		t.Error("help should be closed after a keypress")
	}
}

func TestFleetLoopTickTriggersCollect(t *testing.T) {
	ran := make(chan struct{}, 1)
	v := newFleetView(&fakeConn{state: sshx.StateReady, ran: ran})
	scr := &fakeScreen{w: 100, h: 30}
	ev, tick, ticker, done := startFleetLoop(v, scr)
	defer ticker.Stop()

	tick <- time.Now()
	<-ran // a tick must have started a collection (Run was called)
	ev <- tui.Event{Rune: 'q'}
	<-done
}
