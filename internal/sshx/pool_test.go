// Black-box: drives the pool through its exported surface (NewPool/Add/Close and
// the Managed accessors) using dial functions that fail or block, so no real SSH
// server is needed. The self-healing internals are covered white-box in
// pool_internal_test.go, which must inject a fake through the unexported conn seam.
package sshx_test

import (
	"errors"
	"testing"
	"time"

	"github.com/Wigata-Intech/kay/internal/sshx"
)

// waitState polls a Managed until it reaches want or the deadline passes.
func waitState(t *testing.T, m *sshx.Managed, want sshx.ConnState) {
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

func TestPoolFailingDialBecomesBroken(t *testing.T) {
	p := sshx.NewPool(2)
	defer p.Close()
	m := p.Add(func() (*sshx.Client, error) { return nil, errors.New("connection refused") })

	waitState(t, m, sshx.StateBroken)
	if m.Err() == nil {
		t.Error("Err() should carry the dial failure while broken")
	}
	if _, err := m.Run("uptime"); !errors.Is(err, sshx.ErrNotReady) {
		t.Errorf("Run on a broken connection = %v, want ErrNotReady", err)
	}
	if c := m.Client(); c != nil {
		t.Errorf("Client() while broken = %v, want nil", c)
	}
}

func TestManagedStartsConnecting(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	p := sshx.NewPool(1)
	defer p.Close()
	// Dial blocks, so the connection never leaves the initial Connecting state.
	m := p.Add(func() (*sshx.Client, error) {
		<-block
		return nil, errors.New("unreachable")
	})

	if got := m.State(); got != sshx.StateConnecting {
		t.Errorf("initial state = %v, want connecting", got)
	}
	if _, err := m.Run("x"); !errors.Is(err, sshx.ErrNotReady) {
		t.Errorf("Run while connecting = %v, want ErrNotReady", err)
	}
}

func TestPoolCloseYieldsClosedManaged(t *testing.T) {
	p := sshx.NewPool(1)
	p.Close()
	// Add after Close returns an already-closed Managed that never becomes ready.
	m := p.Add(func() (*sshx.Client, error) { return nil, nil })
	if _, err := m.Run("x"); !errors.Is(err, sshx.ErrNotReady) {
		t.Errorf("Run on a post-Close Managed = %v, want ErrNotReady", err)
	}
}

func TestPoolCloseIsIdempotent(t *testing.T) {
	p := sshx.NewPool(1)
	p.Add(func() (*sshx.Client, error) { return nil, errors.New("no") })
	p.Close()
	p.Close() // must not panic or block
}

func TestNewPoolNonNil(t *testing.T) {
	if sshx.NewPool(0) == nil { // non-positive cap must still yield a usable pool
		t.Fatal("NewPool(0) = nil, want a pool with the default cap")
	}
}
