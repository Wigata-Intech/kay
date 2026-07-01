// White-box: the pool's self-healing logic is exercised through the unexported
// conn seam and Managed internals, injecting a scriptable fake connection so no
// real SSH server is needed.
package sshx

import (
	"errors"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// fakeConn is a scriptable conn: Run/Ping return canned results and every call
// is counted (mutex-guarded, since the manager goroutine touches it).
type fakeConn struct {
	mu      sync.Mutex
	out     string
	runErr  error
	pingErr error
	runs    int
	pings   int
	closes  int
}

func (f *fakeConn) Run(string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runs++
	return f.out, f.runErr
}
func (f *fakeConn) Ping() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pings++
	return f.pingErr
}
func (f *fakeConn) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closes++
	return nil
}
func (f *fakeConn) closed() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closes
}

// newTestManaged wires a Managed to a dial function with fast timings, starts its
// manager goroutine, and registers cleanup.
func newTestManaged(t *testing.T, dial func() (conn, error)) *Managed {
	t.Helper()
	p := NewPool(4)
	m := &Managed{
		pool:      p,
		dialFn:    dial,
		reconnect: make(chan struct{}, 1),
		done:      make(chan struct{}),
		baseDelay: time.Millisecond,
		maxDelay:  2 * time.Millisecond,
		probe:     time.Millisecond,
	}
	go m.manage()
	t.Cleanup(m.Close)
	return m
}

// waitState polls until the connection reaches want or the deadline passes.
func waitState(t *testing.T, m *Managed, want ConnState) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if m.State() == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("state = %v, want %v (timed out)", m.State(), want)
}

func TestManagedReadyAndRun(t *testing.T) {
	fc := &fakeConn{out: "hello"}
	m := newTestManaged(t, func() (conn, error) { return fc, nil })

	waitState(t, m, StateReady)

	out, err := m.Run("cmd")
	if err != nil || out != "hello" {
		t.Fatalf("Run = %q, %v; want hello, nil", out, err)
	}
}

func TestManagedRunNotReady(t *testing.T) {
	block := make(chan struct{})
	m := newTestManaged(t, func() (conn, error) {
		<-block // never connects during the test
		return nil, errors.New("unreachable")
	})
	defer close(block)

	if _, err := m.Run("cmd"); !errors.Is(err, ErrNotReady) {
		t.Fatalf("Run before ready = %v, want ErrNotReady", err)
	}
	if c := m.Client(); c != nil {
		t.Errorf("Client() before ready = %v, want nil", c)
	}
}

func TestManagedBackoffThenRecover(t *testing.T) {
	var mu sync.Mutex
	attempts := 0
	release := make(chan struct{}) // holds the connecting dial open so Broken is observable
	fc := &fakeConn{}
	m := newTestManaged(t, func() (conn, error) {
		mu.Lock()
		a := attempts
		attempts++
		mu.Unlock()
		if a == 0 {
			return nil, errors.New("dial refused") // first attempt fails → Broken
		}
		<-release // stay broken until the test lets the retry succeed
		return fc, nil
	})

	// First failure surfaces as Broken with the error.
	waitState(t, m, StateBroken)
	if m.Err() == nil {
		t.Error("Err() should carry the dial failure while broken")
	}
	// Let the retry connect; it recovers to Ready.
	close(release)
	waitState(t, m, StateReady)
}

func TestManagedReconnectOnFatalRunError(t *testing.T) {
	var mu sync.Mutex
	dials := 0
	first := &fakeConn{runErr: errors.New("connection lost")} // not an *ssh.ExitError → fatal
	second := &fakeConn{out: "ok"}
	m := newTestManaged(t, func() (conn, error) {
		mu.Lock()
		defer mu.Unlock()
		dials++
		if dials == 1 {
			return first, nil
		}
		return second, nil
	})

	waitState(t, m, StateReady)
	// A transport-level error on Run schedules a reconnect and closes the dead conn.
	_, _ = m.Run("cmd")
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && first.closed() == 0 {
		time.Sleep(time.Millisecond)
	}
	if first.closed() == 0 {
		t.Error("a fatal Run error should tear down the dead connection")
	}
	waitState(t, m, StateReady) // reconnected onto the second conn
}

func TestManagedProbeFailureReconnects(t *testing.T) {
	var mu sync.Mutex
	dials := 0
	dead := &fakeConn{pingErr: errors.New("no pong")}
	live := &fakeConn{}
	m := newTestManaged(t, func() (conn, error) {
		mu.Lock()
		defer mu.Unlock()
		dials++
		if dials == 1 {
			return dead, nil
		}
		return live, nil
	})

	waitState(t, m, StateReady)
	// The health probe fails on the first conn, so the manager closes it and redials.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && dead.closed() == 0 {
		time.Sleep(time.Millisecond)
	}
	if dead.closed() == 0 {
		t.Error("a failed health probe should close the dead connection")
	}
}

func TestExitErrorIsNotFatal(t *testing.T) {
	// A remote command exiting non-zero leaves the transport usable.
	if fatalConnErr(&ssh.ExitError{}) {
		t.Error("an *ssh.ExitError should not be treated as a dead connection")
	}
	if fatalConnErr(nil) {
		t.Error("nil error is not fatal")
	}
	if !fatalConnErr(errors.New("EOF")) {
		t.Error("a generic transport error should be treated as fatal")
	}
}

func TestBackoffWithinCap(t *testing.T) {
	m := &Managed{baseDelay: 500 * time.Millisecond, maxDelay: 30 * time.Second}
	for attempt := 1; attempt <= 10; attempt++ {
		d := m.backoff(attempt)
		if d <= 0 || d > m.maxDelay {
			t.Errorf("backoff(%d) = %v, want (0, %v]", attempt, d, m.maxDelay)
		}
	}
}

func TestConnStateString(t *testing.T) {
	cases := map[ConnState]string{
		StateConnecting: "connecting",
		StateReady:      "ready",
		StateBroken:     "broken",
		ConnState(99):   "connecting", // unknown falls back to connecting
	}
	for s, want := range cases {
		if got := s.String(); got != want {
			t.Errorf("ConnState(%d).String() = %q, want %q", s, got, want)
		}
	}
}

func TestAddDialsThroughPool(t *testing.T) {
	p := NewPool(2)
	defer p.Close()
	dialed := make(chan struct{}, 1)
	m := p.Add(func() (*Client, error) {
		select {
		case dialed <- struct{}{}:
		default:
		}
		return nil, errors.New("refused")
	})
	select {
	case <-dialed: // the manager dialed via Add's adaptDial adapter
	case <-time.After(2 * time.Second):
		t.Fatal("Add did not start dialing")
	}
	waitState(t, m, StateBroken)
	if m.Err() == nil {
		t.Error("Err() should carry the dial failure")
	}
}

func TestPoolCloseStopsManagers(t *testing.T) {
	p := NewPool(2)
	m := p.Add(func() (*Client, error) { return nil, errors.New("nope") })
	p.Close()
	// After Close, the managed connection reports not-ready and Add returns an
	// already-closed Managed.
	if _, err := m.Run("x"); !errors.Is(err, ErrNotReady) {
		t.Errorf("Run after Close = %v, want ErrNotReady", err)
	}
	m2 := p.Add(func() (*Client, error) { return nil, nil })
	if _, err := m2.Run("x"); !errors.Is(err, ErrNotReady) {
		t.Errorf("Add after Close should yield a closed Managed, Run = %v", err)
	}
}
