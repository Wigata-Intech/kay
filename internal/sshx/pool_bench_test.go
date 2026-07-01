// White-box micro-benchmarks for the pool dispatch layer — the overhead the
// pool adds *on top of* the SSH transport. Real cold-vs-warm connect, fleet-scale
// load, and kill-recovery stress numbers need a live sshd and are driven by the
// harness in docs/technical-design/ssh-connection-pool.md, not by these.
package sshx

import (
	"testing"
	"time"
)

// benchConn is a zero-cost conn so the benchmark measures only Managed's own
// dispatch (state check + interface call), not any I/O.
type benchConn struct{}

func (benchConn) Run(string) (string, error) { return "", nil }
func (benchConn) Ping() error                { return nil }
func (benchConn) Close() error               { return nil }

func readyManaged() *Managed {
	m := &Managed{
		reconnect: make(chan struct{}, 1),
		done:      make(chan struct{}),
		baseDelay: time.Millisecond,
		maxDelay:  time.Millisecond,
		probe:     time.Hour,
	}
	m.set(StateReady, benchConn{}, nil)
	return m
}

// BenchmarkManagedRun measures the per-call cost of running a command over an
// already-established pooled connection — i.e. the reuse fast path that replaces
// a full dial+handshake every tick.
func BenchmarkManagedRun(b *testing.B) {
	m := readyManaged()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := m.Run("cmd"); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkManagedState measures the cost of reading connection state, done once
// per host per tick to decide whether to collect.
func BenchmarkManagedState(b *testing.B) {
	m := readyManaged()
	b.ResetTimer()
	for range b.N {
		_ = m.State()
	}
}

// BenchmarkBackoff measures the reconnect-delay computation (called only on the
// failure path, but cheap and jitter-bearing, so worth tracking).
func BenchmarkBackoff(b *testing.B) {
	m := &Managed{baseDelay: 500 * time.Millisecond, maxDelay: 30 * time.Second}
	b.ResetTimer()
	for i := range b.N {
		_ = m.backoff(i%10 + 1)
	}
}
