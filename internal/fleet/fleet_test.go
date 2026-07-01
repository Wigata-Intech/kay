// White-box: exercises fleet.go directly — the pure helpers (rows, render,
// statCell, humanDurShort), the collection helpers, and the fleetView event loop
// (with an injected fake screen + channels), plus the unexported hostState type.
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

// failDial is a Dial that always errors, so collection returns immediately
// without a real connection.
func failDial() (*sshx.Client, error) { return nil, errors.New("dial refused") }

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

func TestCollectOne(t *testing.T) {
	st := collectOne(Host{Server: config.Server{Alias: "x"}, Dial: failDial})
	if st.ok || st.err == nil {
		t.Fatalf("collectOne with a failing dial = %+v, want an error state", st)
	}
	if !strings.Contains(st.err.Error(), "dial refused") {
		t.Errorf("err = %v, want the dial error", st.err)
	}
}

func TestCollectAll(t *testing.T) {
	hosts := []Host{
		{Server: config.Server{Alias: "a"}, Dial: failDial},
		{Server: config.Server{Alias: "b"}, Dial: failDial},
	}
	states := collectAll(hosts)
	if len(states) != len(hosts) {
		t.Fatalf("collectAll len = %d, want %d", len(states), len(hosts))
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

// newFleetView builds a one-host view whose Dial always fails, so a collection
// returns quickly without a real connection.
func newFleetView() *fleetView {
	return &fleetView{
		hosts: []Host{{
			Server: config.Server{Alias: "a", Host: "10.0.0.1"},
			Dial:   func() (*sshx.Client, error) { return nil, errors.New("nope") },
		}},
		states:   make([]hostState, 1),
		interval: time.Second,
		results:  make(chan hostUpdate, 1),
	}
}

func startFleetLoop(v *fleetView, scr screen) (chan tui.Event, chan time.Time, *time.Ticker, <-chan *Host) {
	ev := make(chan tui.Event)
	tick := make(chan time.Time)
	ticker := time.NewTicker(time.Hour) // never fires in tests; tick drives ticks
	done := make(chan *Host, 1)
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
			v := newFleetView()
			scr := &fakeScreen{w: 100, h: 30}
			ev, _, ticker, done := startFleetLoop(v, scr)
			defer ticker.Stop()

			ev <- tt.ev
			if host := <-done; host != nil {
				t.Errorf("loop returned %v, want nil (quit, not drill)", host)
			}
			if scr.draws() == 0 {
				t.Error("loop should draw at least once before quitting")
			}
		})
	}
}

func TestFleetLoopEnterDrillsIn(t *testing.T) {
	v := newFleetView()
	v.results = make(chan hostUpdate) // unbuffered: deterministic hand-off
	scr := &fakeScreen{w: 100, h: 30}
	ev, _, ticker, done := startFleetLoop(v, scr)
	defer ticker.Stop()

	// Host 0 reports ready, so Enter drills into it.
	v.results <- hostUpdate{index: 0, state: hostState{ok: true}}
	ev <- tui.Event{Key: tui.KeyEnter}
	host := <-done
	if host == nil {
		t.Fatal("Enter on a ready host should return it to drill into")
	}
	if host.Server.Alias != "a" {
		t.Errorf("drilled host alias = %q, want a", host.Server.Alias)
	}
}

func TestFleetLoopEnterNotReady(t *testing.T) {
	v := newFleetView()
	scr := &fakeScreen{w: 100, h: 30}
	ev, _, ticker, done := startFleetLoop(v, scr)
	defer ticker.Stop()

	// No result yet: the host is still connecting. Enter must not drill.
	ev <- tui.Event{Key: tui.KeyEnter}
	ev <- tui.Event{Rune: 'q'}
	if host := <-done; host != nil {
		t.Errorf("Enter on a not-ready host should not drill, got %v", host)
	}
}

func TestFleetLoopResultRedraws(t *testing.T) {
	v := newFleetView()
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
	v := newFleetView()
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
	v := &fleetView{
		hosts:  []Host{{Server: config.Server{Alias: "a"}}},
		states: make([]hostState, 1),
	}

	// Still connecting (zero state): no drill, "connecting" status.
	if h := v.enterHost(); h != nil {
		t.Error("connecting host should not drill in")
	}
	if !strings.Contains(v.status, "connecting") {
		t.Errorf("status = %q, want a connecting message", v.status)
	}

	// Offline (error): no drill, "offline" status.
	v.states[0] = hostState{err: errors.New("dial refused")}
	if h := v.enterHost(); h != nil {
		t.Error("offline host should not drill in")
	}
	if !strings.Contains(v.status, "offline") {
		t.Errorf("status = %q, want an offline message", v.status)
	}

	// Ready: drills in.
	v.states[0] = hostState{ok: true}
	if h := v.enterHost(); h == nil || h.Server.Alias != "a" {
		t.Errorf("ready host should drill in, got %v", h)
	}

	// Empty view: selection is out of range, so nothing happens.
	if h := (&fleetView{}).enterHost(); h != nil {
		t.Errorf("empty view should not drill in, got %v", h)
	}
}

func TestTrigger(t *testing.T) {
	v := &fleetView{
		hosts: []Host{
			{Server: config.Server{Alias: "a"}, Dial: func() (*sshx.Client, error) { return nil, errors.New("no") }},
			{Server: config.Server{Alias: "b"}, Dial: func() (*sshx.Client, error) { return nil, errors.New("no") }},
		},
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

func TestFleetLoopTickTriggersCollect(t *testing.T) {
	dialed := make(chan struct{}, 1)
	v := newFleetView()
	v.hosts[0].Dial = func() (*sshx.Client, error) {
		select {
		case dialed <- struct{}{}:
		default:
		}
		return nil, errors.New("nope")
	}
	scr := &fakeScreen{w: 100, h: 30}
	ev, tick, ticker, done := startFleetLoop(v, scr)
	defer ticker.Stop()

	tick <- time.Now()
	<-dialed // a tick must have started a collection (Dial was called)
	ev <- tui.Event{Rune: 'q'}
	<-done
}
