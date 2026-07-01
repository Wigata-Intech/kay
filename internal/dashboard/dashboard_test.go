// White-box (package dashboard): shared test fixtures plus tests for the model
// lifecycle in dashboard.go (event loop, collection, reconnect, snapshots).
package dashboard

import (
	"errors"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Wigata-Intech/kay/internal/config"
	"github.com/Wigata-Intech/kay/internal/metrics"
	"github.com/Wigata-Intech/kay/internal/tui"
)

// noColor disables ANSI colouring for the duration of a test so string
// assertions compare against plain text, restoring the previous state after.
func noColor(t *testing.T) {
	t.Helper()
	prev := tui.ColorEnabled
	tui.ColorEnabled = false
	t.Cleanup(func() { tui.ColorEnabled = prev })
}

func TestSortName(t *testing.T) {
	tests := []struct {
		name string
		mode int
		want string
	}{
		{"cpu", 0, "CPU"},
		{"mem", 1, "MEM"},
		{"pid", 2, "PID"},
		{"name", 3, "name"},
		{"default", 99, "CPU"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sortName(tt.mode); got != tt.want {
				t.Errorf("sortName(%d) = %q, want %q", tt.mode, got, tt.want)
			}
		})
	}
}

func TestAppendHist(t *testing.T) {
	t.Run("appends", func(t *testing.T) {
		h := appendHist(nil, 1, 3)
		h = appendHist(h, 2, 3)
		if len(h) != 2 || h[0] != 1 || h[1] != 2 {
			t.Errorf("appendHist = %v", h)
		}
	})
	t.Run("caps-at-max", func(t *testing.T) {
		var h []float64
		for i := 0; i < 5; i++ {
			h = appendHist(h, float64(i), 3)
		}
		if len(h) != 3 || h[0] != 2 || h[2] != 4 {
			t.Errorf("appendHist capped = %v, want tail [2 3 4]", h)
		}
	})
}

func TestNetRate(t *testing.T) {
	t.Run("no-prev-zero", func(t *testing.T) {
		m := newModel()
		m.prev = nil
		rx, tx := m.netRate(metrics.NetIface{Name: "eth0"})
		if rx != 0 || tx != 0 {
			t.Errorf("netRate without prev = (%v,%v), want (0,0)", rx, tx)
		}
	})
	t.Run("computes-rate", func(t *testing.T) {
		m := newModel()
		prev := metrics.Snapshot{Net: []metrics.NetIface{{Name: "eth0", RxBytes: 1000, TxBytes: 2000}}}
		m.prev = &prev
		m.prevAt = time.Now().Add(-2 * time.Second)
		m.snapAt = time.Now()
		rx, tx := m.netRate(metrics.NetIface{Name: "eth0", RxBytes: 3000, TxBytes: 6000})
		// ~2000 bytes / ~2s and ~4000 bytes / ~2s
		if rx < 900 || rx > 1100 || tx < 1900 || tx > 2100 {
			t.Errorf("netRate = (%v,%v), want ~ (1000,2000)", rx, tx)
		}
	})
	t.Run("counter-reset-clamps-zero", func(t *testing.T) {
		m := newModel()
		prev := metrics.Snapshot{Net: []metrics.NetIface{{Name: "eth0", RxBytes: 5000, TxBytes: 5000}}}
		m.prev = &prev
		m.prevAt = time.Now().Add(-time.Second)
		m.snapAt = time.Now()
		rx, tx := m.netRate(metrics.NetIface{Name: "eth0", RxBytes: 100, TxBytes: 100})
		if rx != 0 || tx != 0 {
			t.Errorf("netRate on counter reset = (%v,%v), want (0,0)", rx, tx)
		}
	})
	t.Run("unknown-iface-zero", func(t *testing.T) {
		m := newModel()
		prev := metrics.Snapshot{Net: []metrics.NetIface{{Name: "eth0"}}}
		m.prev = &prev
		m.prevAt = time.Now().Add(-time.Second)
		m.snapAt = time.Now()
		rx, tx := m.netRate(metrics.NetIface{Name: "wlan0", RxBytes: 100})
		if rx != 0 || tx != 0 {
			t.Errorf("netRate for unknown iface = (%v,%v), want (0,0)", rx, tx)
		}
	})
}

// errClient returns a non-nil error from Run, for exercising the failure paths
// of runAction / actions that inspect errors.
type errClient struct{ out string }

func (e *errClient) Run(cmd string) (string, error) {
	return e.out, errBoom
}

var errBoom = boomError("boom")

type boomError string

func (b boomError) Error() string { return string(b) }

type fakeClient struct {
	last string
	out  string
}

func (f *fakeClient) Run(cmd string) (string, error) { f.last = cmd; return f.out, nil }

func sampleSnap() metrics.Snapshot {
	return metrics.Snapshot{
		Hostname: "web-1", UptimeSec: 90000, NumCPU: 4, CPUPercent: 31,
		MemTotalKB: 8000000, MemAvailableKB: 800000, MemUsedPercent: 90,
		Load1: 0.4, Load5: 0.5, Load15: 0.6,
		Net:   []metrics.NetIface{{Name: "lo"}, {Name: "eth0", RxBytes: 500000, TxBytes: 250000}},
		Disks: []metrics.Disk{{Mount: "/", TotalBytes: 4000000000, UsedBytes: 1600000000}},
		Procs: []metrics.Proc{
			{PID: 1432, Name: "postgres", CPU: 22, Mem: 18},
			{PID: 980, Name: "nginx", CPU: 6, Mem: 1},
			{PID: 5, Name: "a very long process name that overflows", CPU: 1, Mem: 1},
		},
		Docker:        []metrics.Container{{ID: "abc123", Name: "web", Image: "nginx:latest", Status: "Up 2h"}},
		DockerPresent: true,
	}
}

func newModel() *model {
	m := &model{
		srv:      config.Server{Alias: "prod-1", Host: "10.0.0.1", Port: 22, User: "ubuntu"},
		client:   &fakeClient{},
		interval: 3 * time.Second,
	}
	m.snap = sampleSnap()
	m.have = true
	m.snapAt = time.Now()
	m.rebuildLists()
	return m
}

// fakeScreen counts draws; the counter is mutex-guarded because Draw runs in the
// loop goroutine while the test reads the count. w/h are set once and never
// mutated, so Size needs no lock.
type fakeScreen struct {
	w, h int
	mu   sync.Mutex
	n    int
}

func (s *fakeScreen) Size() (int, int) { return s.w, s.h }

func (s *fakeScreen) Draw([]string) { s.mu.Lock(); s.n++; s.mu.Unlock() }

func (s *fakeScreen) draws() int { s.mu.Lock(); defer s.mu.Unlock(); return s.n }

// startLoop runs m.loop in a goroutine and returns its channels plus a done
// channel carrying the loop's exitApp result.
func startLoop(m *model, scr screen, reset func()) (chan tui.Event, chan os.Signal, chan time.Time, <-chan bool) {
	ev := make(chan tui.Event)
	sig := make(chan os.Signal)
	tick := make(chan time.Time)
	done := make(chan bool, 1)
	go func() { done <- m.loop(scr, ev, sig, tick, reset) }()
	return ev, sig, tick, done
}

func TestLoopQuitKey(t *testing.T) {
	tests := []struct {
		name     string
		ev       tui.Event
		wantExit bool // true = leave the whole app, false = leave this view
	}{
		{name: "q leaves the view", ev: tui.Event{Rune: 'q'}, wantExit: false},
		{name: "ctrl-c leaves the app", ev: tui.Event{Type: tui.EventQuit}, wantExit: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newModel()
			scr := &fakeScreen{w: 100, h: 30}
			ev, _, _, done := startLoop(m, scr, func() {})
			ev <- tt.ev
			if exit := <-done; exit != tt.wantExit {
				t.Errorf("loop exitApp = %v, want %v", exit, tt.wantExit)
			}
			if scr.draws() == 0 {
				t.Error("loop should draw at least once before quitting")
			}
		})
	}
}

func TestLoopSignalQuit(t *testing.T) {
	m := newModel()
	scr := &fakeScreen{w: 100, h: 30}
	_, sig, _, done := startLoop(m, scr, func() {})
	sig <- syscall.SIGTERM
	if exit := <-done; !exit {
		t.Error("SIGTERM should exit the whole app (exitApp = true)")
	}
}

func TestLoopResizeThenQuit(t *testing.T) {
	m := newModel()
	scr := &fakeScreen{w: 100, h: 30}
	ev, sig, _, done := startLoop(m, scr, func() {})

	sig <- os.Interrupt // non-quit signal: redraw and keep looping
	ev <- tui.Event{Rune: 'q'}
	<-done
	if got := scr.draws(); got < 2 {
		t.Errorf("draws = %d, want >= 2 (initial + resize)", got)
	}
}

func TestLoopCollectResult(t *testing.T) {
	m := newModel()
	m.results = make(chan collectResult) // unbuffered: deterministic hand-off
	scr := &fakeScreen{w: 100, h: 30}
	ev, _, _, done := startLoop(m, scr, func() {})

	m.results <- collectResult{snap: sampleSnap()}
	ev <- tui.Event{Rune: 'q'}
	<-done
	if m.collecting {
		t.Error("collecting should be cleared after a result")
	}
	if got := scr.draws(); got < 2 {
		t.Errorf("draws = %d, want >= 2 (initial + result)", got)
	}
}

// runFunc adapts a function to the Client interface for signaling in tests.
type runFunc func(string) (string, error)

func (f runFunc) Run(cmd string) (string, error) { return f(cmd) }

func TestLoopTickTriggersCollect(t *testing.T) {
	m := newModel()
	m.results = make(chan collectResult, 1) // buffered: absorb the async collect
	ran := make(chan struct{}, 1)
	m.client = runFunc(func(string) (string, error) {
		select {
		case ran <- struct{}{}:
		default:
		}
		return "", nil
	})
	scr := &fakeScreen{w: 100, h: 30}
	ev, _, tick, done := startLoop(m, scr, func() {})

	tick <- time.Now()
	<-ran // a tick must have started a collection (the client was queried)
	ev <- tui.Event{Rune: 'q'}
	<-done
}

func TestApplyKeyEventLoadingGuard(t *testing.T) {
	m := newModel()
	m.loading = true
	m.have = false
	m.tab = tabOverview

	// A tab-switch key is swallowed while loading.
	if quit, _ := m.applyKeyEvent(tui.Event{Rune: 'L'}, func() {}); quit {
		t.Error("non-quit key should not quit")
	}
	if m.tab != tabOverview {
		t.Errorf("tab changed to %d while loading, want %d (ignored)", m.tab, tabOverview)
	}
	// q quits (leaving the view, not the app).
	if quit, exit := m.applyKeyEvent(tui.Event{Rune: 'q'}, func() {}); !quit || exit {
		t.Errorf("q while loading = (quit %v, exit %v), want (true, false)", quit, exit)
	}
	// Ctrl-C quits the whole app.
	if quit, exit := m.applyKeyEvent(tui.Event{Type: tui.EventQuit}, func() {}); !quit || !exit {
		t.Errorf("ctrl-c while loading = (quit %v, exit %v), want (true, true)", quit, exit)
	}
}

func TestApplyCollectClearsLoading(t *testing.T) {
	t.Run("success clears loading", func(t *testing.T) {
		m := newModel()
		m.loading = true
		m.applyCollect(collectResult{snap: sampleSnap()})
		if m.loading {
			t.Error("applyCollect should clear loading on success")
		}
	})
	t.Run("error also clears loading", func(t *testing.T) {
		m := newModel()
		m.loading = true
		m.applyCollect(collectResult{err: errors.New("boom")})
		if m.loading {
			t.Error("applyCollect should clear loading even on error")
		}
	})
}

func TestLoopIntervalChangeResetsTick(t *testing.T) {
	m := newModel()
	scr := &fakeScreen{w: 100, h: 30}
	resets := 0
	ev, _, _, done := startLoop(m, scr, func() { resets++ })

	ev <- tui.Event{Rune: '+'} // grow interval -> intervalChanged -> resetTick
	ev <- tui.Event{Rune: 'q'}
	<-done
	if resets != 1 {
		t.Errorf("resetTick calls = %d, want 1", resets)
	}
	if m.interval <= 3*time.Second {
		t.Errorf("interval = %v, want > 3s after '+'", m.interval)
	}
}

func TestApplyCollectError(t *testing.T) {
	t.Run("with redial, starts a reconnect", func(t *testing.T) {
		m := newModel()
		m.loading = true
		m.reconnected = make(chan reconnectResult, 1)
		fresh := &fakeClient{out: "ok"}
		m.redial = func() (Client, error) { return fresh, nil }

		m.applyCollect(collectResult{err: errors.New("connection lost")})

		if m.loading {
			t.Error("loading should clear once the first result (an error) arrives")
		}
		if m.err == nil {
			t.Error("err should be recorded")
		}
		if !m.reconnecting {
			t.Error("a redial should be in flight")
		}
		select {
		case rr := <-m.reconnected:
			if rr.err != nil || rr.client != fresh {
				t.Errorf("redial result = %+v, want the fresh client", rr)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("redial goroutine never reported")
		}
	})

	t.Run("without redial, just records the error", func(t *testing.T) {
		m := newModel()
		m.loading = true
		m.redial = nil

		m.applyCollect(collectResult{err: errors.New("boom")})

		if m.reconnecting {
			t.Error("no redial configured — should not attempt a reconnect")
		}
		if m.err == nil {
			t.Error("err should be recorded")
		}
	})
}

func TestApplyReconnect(t *testing.T) {
	t.Run("success swaps the client and refetches", func(t *testing.T) {
		m := newModel()
		m.results = make(chan collectResult, 1) // trigger() reports here
		m.reconnecting = true
		fresh := &fakeClient{out: "x"}

		m.applyReconnect(reconnectResult{client: fresh})

		if m.client != fresh {
			t.Error("client should be swapped to the reconnected one")
		}
		if !strings.Contains(m.status, "reconnected") {
			t.Errorf("status = %q, want a reconnected message", m.status)
		}
		if !m.collecting {
			t.Error("a fresh collection should be triggered after reconnecting")
		}
	})

	t.Run("failure keeps the old client and retries", func(t *testing.T) {
		m := newModel()
		old := m.client
		m.applyReconnect(reconnectResult{err: errors.New("still down")})

		if m.client != old {
			t.Error("a failed reconnect must not replace the client")
		}
		if !strings.Contains(m.status, "reconnect failed") {
			t.Errorf("status = %q, want a retry message", m.status)
		}
	})
}

func TestApplySnapRotatesPrev(t *testing.T) {
	m := newModel() // newModel already installed one snapshot (have == true)
	firstCPU := m.snap.CPUPercent

	m.applySnap(metrics.Snapshot{CPUPercent: 99})

	if m.prev == nil {
		t.Fatal("the previous snapshot should be retained for rate calculations")
	}
	if m.prev.CPUPercent != firstCPU {
		t.Errorf("prev CPU = %v, want the earlier snapshot's %v", m.prev.CPUPercent, firstCPU)
	}
	if m.snap.CPUPercent != 99 {
		t.Errorf("snap CPU = %v, want the new 99", m.snap.CPUPercent)
	}
	if m.err != nil {
		t.Errorf("applySnap should clear err, got %v", m.err)
	}
}
