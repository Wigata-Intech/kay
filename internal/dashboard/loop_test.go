// White-box: loop is unexported and drives the model's event handling; these
// tests inject a fake screen and channels to exercise every select arm without a
// real terminal.
package dashboard

import (
	"errors"
	"os"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Wigata-Intech/kay/internal/tui"
)

// fakeScreen counts draws; the counter is mutex-guarded because Draw runs in the
// loop goroutine while the test reads the count. w/h are set once and never
// mutated, so Size needs no lock.
type fakeScreen struct {
	w, h int
	mu   sync.Mutex
	n    int
}

func (s *fakeScreen) Size() (int, int) { return s.w, s.h }
func (s *fakeScreen) Draw([]string)    { s.mu.Lock(); s.n++; s.mu.Unlock() }
func (s *fakeScreen) draws() int       { s.mu.Lock(); defer s.mu.Unlock(); return s.n }

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
