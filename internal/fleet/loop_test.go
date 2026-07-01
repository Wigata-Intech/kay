// White-box: loop is unexported and drives fleetView; these tests inject a fake
// screen and channels to exercise every select arm without a real terminal.
package fleet

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Wigata-Intech/kay/internal/config"
	"github.com/Wigata-Intech/kay/internal/sshx"
	"github.com/Wigata-Intech/kay/internal/tui"
)

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

// newFleetView builds a one-host view whose Dial always fails, so collectAll
// returns quickly without a real connection.
func newFleetView() *fleetView {
	return &fleetView{
		hosts: []Host{{
			Server: config.Server{Alias: "a", Host: "10.0.0.1"},
			Dial:   func() (*sshx.Client, error) { return nil, errors.New("nope") },
		}},
		states:   make([]hostState, 1),
		interval: time.Second,
		results:  make(chan []hostState, 1),
	}
}

func startFleetLoop(v *fleetView, scr screen) (chan tui.Event, chan time.Time, *time.Ticker, <-chan error) {
	ev := make(chan tui.Event)
	tick := make(chan time.Time)
	ticker := time.NewTicker(time.Hour) // never fires in tests; tick drives ticks
	done := make(chan error, 1)
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
			if err := <-done; err != nil {
				t.Errorf("loop returned %v, want nil", err)
			}
			if scr.draws() == 0 {
				t.Error("loop should draw at least once before quitting")
			}
		})
	}
}

func TestFleetLoopResultRedraws(t *testing.T) {
	v := newFleetView()
	v.results = make(chan []hostState) // unbuffered: deterministic hand-off
	scr := &fakeScreen{w: 100, h: 30}
	ev, _, ticker, done := startFleetLoop(v, scr)
	defer ticker.Stop()

	updated := []hostState{{ok: true}}
	v.results <- updated
	ev <- tui.Event{Rune: 'q'}
	<-done
	if v.collecting {
		t.Error("collecting should be cleared after a result")
	}
	if !v.states[0].ok {
		t.Error("states should be replaced by the collection result")
	}
	if got := scr.draws(); got < 2 {
		t.Errorf("draws = %d, want >= 2 (initial + result)", got)
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
